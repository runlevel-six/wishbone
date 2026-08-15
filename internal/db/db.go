// Package db opens the SQLite database, applies the required pragmas and runs
// embedded migrations in-process at startup (plan §1, §2).
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // pure-Go driver: keeps CGO_ENABLED=0
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Pragmas are not optional (plan §2). They are applied as connection
// parameters so every pooled connection gets them, not just the first.
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=synchronous(NORMAL)"

// Open opens (creating if needed) the database at path and migrates it.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := "file:" + path + pragmas
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// A single writer avoids SQLITE_BUSY churn entirely; reads are cheap at
	// this scale and the deployment is single-replica anyway (plan §10).
	sqldb.SetMaxOpenConns(1)
	sqldb.SetMaxIdleConns(1)

	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	if err := Migrate(ctx, sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}
	return sqldb, nil
}

// Migrate applies any embedded migrations not yet recorded in
// schema_migrations. Each migration runs in its own transaction.
func Migrate(ctx context.Context, sqldb *sql.DB) error {
	if _, err := sqldb.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	) STRICT`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := sqldb.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		applied[n] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, e := range entries {
		name := strings.TrimPrefix(e, "migrations/")
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile(e)
		if err != nil {
			return err
		}
		tx, err := sqldb.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, datetime('now'))`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
