// statushub-migrate applies new schema migrations with checksums and a shared lock.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/migrations"
	"github.com/jackc/pgx/v5"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, os.Getenv("DATABASE_URL"), migrations.Files); err != nil {
		// Connection errors can contain connection strings; do not print them.
		fmt.Fprintln(os.Stderr, "migration failed; check database access and schema compatibility:", err)
		os.Exit(1)
	}
	fmt.Println("schema is up to date")
}

func run(ctx context.Context, url string, files fs.FS) error {
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("unable to connect to database")
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(734901256)"); err != nil {
		return fmt.Errorf("unable to acquire migration lock")
	}
	// Session closure releases the lock even on errors.
	var tracked bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.statushub_schema_migrations') IS NOT NULL").Scan(&tracked); err != nil {
		return err
	}
	if !tracked {
		var occupied bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public')").Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return fmt.Errorf("existing database has no migration ledger; refuse automatic adoption; use a new database or audited migration procedure")
		}
		if _, err := conn.Exec(ctx, "CREATE TABLE public.statushub_schema_migrations (name text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
			return err
		}
	}
	names, err := fs.Glob(files, "*.up.sql")
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no migrations found")
	}
	sort.Strings(names)
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	rows, err := conn.Query(ctx, "SELECT name, checksum FROM public.statushub_schema_migrations")
	if err != nil {
		return err
	}
	applied := map[string]string{}
	for rows.Next() {
		var n, c string
		if err := rows.Scan(&n, &c); err != nil {
			rows.Close()
			return err
		}
		applied[n] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for n := range applied {
		if !known[n] {
			return fmt.Errorf("database contains migration unavailable in this version: %s; downgrade refused", n)
		}
	}
	// Validate all checksums and history before making any schema change.
	missing := false
	for _, n := range names {
		data, err := fs.ReadFile(files, n)
		if err != nil {
			return err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(data))
		if old, ok := applied[n]; ok {
			if old != checksum {
				return fmt.Errorf("migration checksum mismatch: %s", n)
			}
			if missing {
				return fmt.Errorf("migration ledger has a gap before %s", n)
			}
		} else {
			missing = true
		}
	}
	for _, n := range names {
		if _, ok := applied[n]; ok {
			continue
		}
		data, err := fs.ReadFile(files, n)
		if err != nil {
			return err
		}
		sql := strings.TrimSpace(string(data))
		if !strings.HasPrefix(sql, "BEGIN;") || !strings.HasSuffix(sql, "COMMIT;") {
			return fmt.Errorf("migration must have outer BEGIN/COMMIT: %s", n)
		}
		sql = strings.TrimSuffix(strings.TrimPrefix(sql, "BEGIN;"), "COMMIT;")
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, sql); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO public.statushub_schema_migrations (name,checksum) VALUES ($1,$2)", n, fmt.Sprintf("%x", sha256.Sum256(data)))
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s failed (transaction rolled back)", n)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit for %s was not confirmed; rerun to check ledger", n)
		}
		fmt.Println("applied", n)
	}
	return nil
}
