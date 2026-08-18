package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateUpgradesAPopulatedDatabase is the case a fresh-database test cannot
// reach: an instance that has been running on an older schema, with rows in it.
//
// Every other test in the tree calls Open on an empty file, which applies all
// migrations in one pass and proves nothing about upgrading. 0002 was the first
// migration this project ever added on top of another, so the ALTER path had
// never actually run anywhere except a real deployment.
func TestMigrateUpgradesAPopulatedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	sqldb, err := sql.Open("sqlite", "file:"+path+pragmas)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqldb.Close()

	// Stand up an instance as it existed before 0002: apply the first migration
	// only, and record it the way Migrate would have.
	body, err := migrationFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read 0001: %v", err)
	}
	if _, err := sqldb.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	) STRICT`); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if _, err := sqldb.ExecContext(ctx, string(body)); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	if _, err := sqldb.ExecContext(ctx,
		`INSERT INTO schema_migrations (name, applied_at) VALUES ('0001_init.sql', '2026-08-15T00:00:00Z')`); err != nil {
		t.Fatalf("record 0001: %v", err)
	}
	if _, err := sqldb.ExecContext(ctx,
		`INSERT INTO users (id, username, display_name, password_hash, is_admin, must_reset, created_at)
		 VALUES ('u1', 'existing', 'Existing Person', 'argon2id$fake', 0, 0, '2026-08-15T00:00:00Z')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Now upgrade.
	if err := Migrate(ctx, sqldb); err != nil {
		t.Fatalf("migrate a populated database: %v", err)
	}

	// The new column exists and is NULL for the person who predates it, which is
	// what the unread count reads as "never looked".
	var seen *string
	if err := sqldb.QueryRowContext(ctx,
		`SELECT claims_seen_at FROM users WHERE id = 'u1'`).Scan(&seen); err != nil {
		t.Fatalf("select the new column: %v", err)
	}
	if seen != nil {
		t.Errorf("claims_seen_at = %q for an existing row, want NULL", *seen)
	}

	// 0003 is the other shape of ALTER: NOT NULL with a default, which SQLite
	// backfills into every existing row. An account that predates themes is on
	// the brand green rather than on an empty string nothing in the stylesheet
	// matches.
	var theme string
	if err := sqldb.QueryRowContext(ctx,
		`SELECT theme FROM users WHERE id = 'u1'`).Scan(&theme); err != nil {
		t.Fatalf("select the theme column: %v", err)
	}
	if theme != "forest" {
		t.Errorf("theme = %q for an existing row, want the default", theme)
	}

	// The row survived, and STRICT is still in force on the altered table.
	var name string
	if err := sqldb.QueryRowContext(ctx,
		`SELECT display_name FROM users WHERE id = 'u1'`).Scan(&name); err != nil {
		t.Fatalf("existing row: %v", err)
	}
	if name != "Existing Person" {
		t.Errorf("display_name = %q after upgrade", name)
	}
	// STRICT still applies to the column ALTER added. A BLOB is the check worth
	// making: STRICT permits lossless conversions, so an integer would be stored
	// as its own digits and prove nothing, while a BLOB in a TEXT column is
	// refused outright.
	if _, err := sqldb.ExecContext(ctx,
		`UPDATE users SET claims_seen_at = x'00ff' WHERE id = 'u1'`); err == nil {
		t.Error("a BLOB was accepted in a TEXT column; the altered table is not STRICT")
	}

	// Idempotent: running again applies nothing and fails nothing.
	if err := Migrate(ctx, sqldb); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
