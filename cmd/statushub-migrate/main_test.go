package main

import (
	"context"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Seeridia/StatusHub/migrations"
	"github.com/jackc/pgx/v5"
)

func testDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL required; creates and drops a separate test database")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "statushub_deploy_test_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		admin.Close(ctx)
	})
	config, err := pgx.ParseConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	// A keyword connection string retains the local test server and credentials.
	quote := func(s string) string {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "\\'") + "'"
	}
	return "host=" + quote(config.Host) + " port=" + quote(strconv.Itoa(int(config.Port))) + " user=" + quote(config.User) + " password=" + quote(config.Password) + " dbname=" + quote(name) + " sslmode=disable"
}

func TestMigrationLifecycle(t *testing.T) {
	url := testDatabase(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- run(ctx, url, migrations.Files) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM public.statushub_schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 14 {
		t.Fatalf("want 14 migrations, got %d", count)
	}
	data := fstest.MapFS{}
	names, _ := fs.Glob(migrations.Files, "*.up.sql")
	for _, name := range names {
		body, _ := fs.ReadFile(migrations.Files, name)
		data[name] = &fstest.MapFile{Data: body}
	}
	data[names[0]] = &fstest.MapFile{Data: []byte("BEGIN; SELECT 1; COMMIT;")}
	if err = run(ctx, url, data); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("modified migration accepted: %v", err)
	}
	if _, err = conn.Exec(ctx, "INSERT INTO statushub_schema_migrations(name,checksum) VALUES('999999_future.up.sql','x')"); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, url, migrations.Files); err == nil || !strings.Contains(err.Error(), "downgrade refused") {
		t.Fatalf("downgrade accepted: %v", err)
	}
}

func TestMigrationFailureIsAtomic(t *testing.T) {
	url := testDatabase(t)
	ctx := context.Background()
	files := fstest.MapFS{"000001_test.up.sql": &fstest.MapFile{Data: []byte("BEGIN; CREATE TABLE should_rollback(id int); SELECT * FROM missing_table; COMMIT;")}}
	if err := run(ctx, url, files); err == nil {
		t.Fatal("invalid migration succeeded")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var exists bool
	if err = conn.QueryRow(ctx, "SELECT to_regclass('public.should_rollback') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("DDL not rolled back: %v", err)
	}
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM statushub_schema_migrations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration recorded: %v", err)
	}
	files["000001_test.up.sql"] = &fstest.MapFile{Data: []byte("BEGIN; CREATE TABLE should_rollback(id int); COMMIT;")}
	if err = run(ctx, url, files); err != nil {
		t.Fatal(err)
	}
}

func TestUntrackedDatabaseRejected(t *testing.T) {
	url := testDatabase(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, "CREATE TABLE existing_business_data(id int)"); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, url, migrations.Files); err == nil || !strings.Contains(err.Error(), "refuse automatic adoption") {
		t.Fatalf("untracked database accepted: %v", err)
	}
}
