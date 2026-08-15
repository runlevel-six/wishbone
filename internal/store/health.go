package store

import (
	"context"
	"fmt"
)

// ClaimDrift is one item whose denormalized claimed_qty disagrees with the sum
// of its claims (plan §2.1). This should always be empty; the admin health
// endpoint and the test suite both assert that.
type ClaimDrift struct {
	ItemID     string
	ClaimedQty int
	SumClaims  int
}

// CheckClaimInvariant returns every item where items.claimed_qty !=
// SUM(claims.qty), plus any item whose claimed_qty exceeds its quantity.
func (s *Store) CheckClaimInvariant(ctx context.Context) ([]ClaimDrift, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.claimed_qty, COALESCE((SELECT SUM(c.qty) FROM claims c WHERE c.item_id = i.id), 0) AS s
		  FROM items i
		 WHERE i.claimed_qty != COALESCE((SELECT SUM(c.qty) FROM claims c WHERE c.item_id = i.id), 0)
		    OR i.claimed_qty > i.quantity`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClaimDrift
	for rows.Next() {
		var d ClaimDrift
		if err := rows.Scan(&d.ItemID, &d.ClaimedQty, &d.SumClaims); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Writable verifies the database accepts writes, which is what readiness means
// for this app (plan §10).
func (s *Store) Writable(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS _readyz (k INTEGER PRIMARY KEY, at TEXT NOT NULL) STRICT`); err != nil {
		return fmt.Errorf("readyz create: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO _readyz (k, at) VALUES (1, datetime('now'))
		 ON CONFLICT(k) DO UPDATE SET at = excluded.at`); err != nil {
		return fmt.Errorf("readyz write: %w", err)
	}
	return nil
}

// Stats are the counters shown on the admin page. Deliberately no claim counts
// broken down by list — see plan §3.2; the aggregate below is global and does
// not let an owner infer anything about their own items.
type Stats struct {
	Users  int
	Lists  int
	Items  int
	Images int
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM users),
		       (SELECT COUNT(*) FROM lists),
		       (SELECT COUNT(*) FROM items WHERE deleted_at IS NULL),
		       (SELECT COUNT(*) FROM item_images)`).
		Scan(&st.Users, &st.Lists, &st.Items, &st.Images)
	return st, err
}
