// Package store is the only package that talks SQL. Claim reads and writes go
// through the guarded methods in claims.go, which enforce the owner-blindness
// invariant (plan §3.2) at the data layer.
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Querier is satisfied by *sql.DB, *sql.Conn and *sql.Tx, so query helpers can
// run either standalone or inside a transaction.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// DB exposes the handle for health checks and tests. Application code should
// use the repository methods.
func (s *Store) DB() *sql.DB { return s.db }

// write runs fn inside a BEGIN IMMEDIATE transaction.
//
// Immediate (not deferred) is required by plan §3.3: the claim path reads and
// then writes, and a deferred transaction that upgrades to a writer mid-flight
// is how you get SQLITE_BUSY deadlocks under concurrency. Taking the write
// lock up front turns the race into a serialized queue.
func (s *Store) write(ctx context.Context, fn func(Querier) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Best effort; the connection is closed right after either way.
			conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()

	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}
