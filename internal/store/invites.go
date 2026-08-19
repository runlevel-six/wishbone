package store

import (
	"context"
	"database/sql"
	"errors"

	"wishbone/internal/model"
)

func (s *Store) CreateInvite(ctx context.Context, inv *model.Invite) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (token_hash, created_by, created_at, expires_at) VALUES (?,?,?,?)`,
		inv.TokenHash, inv.CreatedBy, inv.CreatedAt, inv.ExpiresAt)
	return err
}

// UsableInvite returns an invite that is neither used nor expired.
func (s *Store) UsableInvite(ctx context.Context, tokenHash, now string) (*model.Invite, error) {
	var inv model.Invite
	err := s.db.QueryRowContext(ctx,
		`SELECT token_hash, created_by, created_at, expires_at, used_at, used_by
		   FROM invites WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, now).
		Scan(&inv.TokenHash, &inv.CreatedBy, &inv.CreatedAt, &inv.ExpiresAt, &inv.UsedAt, &inv.UsedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// RedeemInvite creates the user and burns the invite in one transaction, so a
// token cannot be used twice by two concurrent registrations.
func (s *Store) RedeemInvite(ctx context.Context, tokenHash string, u *model.User, now string) error {
	if u.ID == "" {
		u.ID = model.NewID()
	}
	if u.CreatedAt == "" {
		u.CreatedAt = now
	}
	return s.write(ctx, func(q Querier) error {
		// The user row goes in first: invites.used_by references users(id), so
		// burning the invite before the account exists trips the foreign key.
		if err := insertUser(ctx, q, u); err != nil {
			return err
		}

		res, err := q.ExecContext(ctx,
			`UPDATE invites SET used_at = ?, used_by = ?
			  WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
			now, u.ID, tokenHash, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			// Rolls back the user insert too, so a raced invite leaves nothing
			// behind.
			return model.ErrNotFound
		}
		return nil
	})
}

func (s *Store) ListInvites(ctx context.Context) ([]*model.Invite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT token_hash, created_by, created_at, expires_at, used_at, used_by
		   FROM invites ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Invite
	for rows.Next() {
		var inv model.Invite
		if err := rows.Scan(&inv.TokenHash, &inv.CreatedBy, &inv.CreatedAt,
			&inv.ExpiresAt, &inv.UsedAt, &inv.UsedBy); err != nil {
			return nil, err
		}
		out = append(out, &inv)
	}
	return out, rows.Err()
}

func (s *Store) DeleteInvite(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE token_hash = ? AND used_at IS NULL`, tokenHash)
	return err
}
