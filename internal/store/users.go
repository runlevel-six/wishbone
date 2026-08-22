package store

import (
	"context"
	"database/sql"
	"errors"

	"wishbone/internal/model"
)

const userCols = `id, username, display_name, password_hash, is_admin, must_reset, theme, created_at, legacy_id`

func scanUser(sc interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	var theme string
	err := sc.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&u.IsAdmin, &u.MustReset, &theme, &u.CreatedAt, &u.LegacyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Clamped on the way out as well as on the way in: a row holding a palette
	// this build has never heard of renders as the default rather than as a page
	// with no colors at all.
	u.Theme = model.ParseTheme(theme)
	return &u, nil
}

// insertUser writes one user row, filling in the defaults every caller wants.
//
// Both account-creation paths go through it — CreateUser for an admin-made
// account, RedeemInvite inside its transaction — so the column list and its
// placeholders live in exactly one place. RedeemInvite used to keep its own
// copy, and adding the theme column to userCols left that copy one value short:
// admin-created accounts kept working while every invite registration failed
// with "8 values for 9 columns".
func insertUser(ctx context.Context, q Querier, u *model.User) error {
	if u.ID == "" {
		u.ID = model.NewID()
	}
	if u.CreatedAt == "" {
		u.CreatedAt = model.TimeString(model.Now())
	}
	if u.Theme == "" {
		u.Theme = model.ThemeForest
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash,
		boolInt(u.IsAdmin), boolInt(u.MustReset), string(u.Theme), u.CreatedAt, u.LegacyID)
	if isUniqueViolation(err) {
		return model.ErrConflict
	}
	return err
}

func (s *Store) CreateUser(ctx context.Context, u *model.User) error {
	return insertUser(ctx, s.db, u)
}

func (s *Store) UserByID(ctx context.Context, id string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, username))
}

func (s *Store) UserByLegacyID(ctx context.Context, legacyID string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE legacy_id = ?`, legacyID))
}

func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userCols+` FROM users ORDER BY display_name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) SetPassword(ctx context.Context, userID, hash string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_reset = 0 WHERE id = ?`, hash, userID)
	return err
}

func (s *Store) UpdateProfile(ctx context.Context, userID, displayName string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET display_name = ? WHERE id = ?`, displayName, userID)
	return err
}

// SetUsername changes the name someone signs in with.
//
// The column is UNIQUE COLLATE NOCASE, so the database is what decides whether
// a name is free — not a SELECT beforehand, which would be a race with any
// other registration or rename in flight. A collision comes back as
// ErrConflict, the same answer registration gives.
//
// Nothing else has to move. Sessions key on the user's ID, so a rename does not
// sign anybody out; invites are spent and unrelated; and the audit lines that
// record a username recorded the one in use at the time, which is what a log is
// for.
func (s *Store) SetUsername(ctx context.Context, userID, username string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET username = ? WHERE id = ?`, username, userID)
	if isUniqueViolation(err) {
		return model.ErrConflict
	}
	return err
}

// SetTheme records which palette this person wants. The value is clamped here
// too, so nothing but a known palette is ever written.
func (s *Store) SetTheme(ctx context.Context, userID string, theme model.Theme) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET theme = ? WHERE id = ?`, string(model.ParseTheme(string(theme))), userID)
	return err
}

func (s *Store) SetAdmin(ctx context.Context, userID string, admin bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_admin = ? WHERE id = ?`, boolInt(admin), userID)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
