// Package postgres persists scheduler state and the canonical event ledger in
// PostgreSQL. Work is claimed in short transactions and completed with lease
// token compare-and-swap updates so network I/O never holds database locks.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrLeaseLost means that the work item does not exist, is already complete,
	// or is now owned by a different lease token. Callers must stop processing
	// the item and must not retry completion with the stale token.
	ErrLeaseLost = errors.New("postgres store: lease lost")

	// ErrInvalidArgument identifies input rejected before a database call.
	ErrInvalidArgument = errors.New("postgres store: invalid argument")
)

// database is deliberately small. It is implemented by pgxpool.Pool and keeps
// CAS behavior unit-testable without emulating the entire pgx pool.
type database interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Ping(context.Context) error
	Close()
}

// Store is safe for concurrent use. Close it only after all users have
// stopped; the underlying pgx pool owns its connections.
type Store struct {
	db database
}

// Open creates and verifies a pgx connection pool from a PostgreSQL URL.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, invalid("database URL is required")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres store: parse database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("postgres store: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres store: ping: %w", err)
	}
	return NewWithPool(pool), nil
}

// NewWithPool wraps a caller-configured pool. The caller must not close the
// pool separately while Store is in use.
func NewWithPool(pool *pgxpool.Pool) *Store {
	if pool == nil {
		return &Store{}
	}
	return &Store{db: pool}
}

// Ping verifies that the pool can reach PostgreSQL.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.db.Ping(ctx); err != nil {
		return fmt.Errorf("postgres store: ping: %w", err)
	}
	return nil
}

// Close releases the underlying connection pool. It is safe on a nil Store.
func (s *Store) Close() {
	if s != nil && s.db != nil {
		s.db.Close()
	}
}

func (s *Store) ready() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("%w: nil store or pool", ErrInvalidArgument)
	}
	return nil
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, message)
}

func required(value, name string) error {
	if strings.TrimSpace(value) == "" {
		return invalid(name + " is required")
	}
	return nil
}
