package store

import (
	"context"
	"database/sql"
	"errors"

	"wishd/internal/model"
)

func (s *Store) CreateSession(ctx context.Context, sess *model.Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, user_agent)
		 VALUES (?,?,?,?,?)`,
		sess.TokenHash, sess.UserID, sess.CreatedAt, sess.ExpiresAt, sess.UserAgent)
	return err
}

// SessionUser resolves a session token hash to its user, rejecting expired
// sessions. now must be an RFC3339 UTC string.
func (s *Store) SessionUser(ctx context.Context, tokenHash, now string) (*model.User, *model.Session, error) {
	var sess model.Session
	var u model.User
	err := s.db.QueryRowContext(ctx,
		`SELECT s.token_hash, s.user_id, s.created_at, s.expires_at, s.user_agent,
		        u.id, u.username, u.display_name, u.password_hash, u.is_admin, u.must_reset, u.created_at, u.legacy_id
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now).
		Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt, &sess.UserAgent,
			&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.IsAdmin, &u.MustReset, &u.CreatedAt, &u.LegacyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, model.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return &u, &sess, nil
}

// TouchSession slides the expiry (plan §4: 30-day sliding renewal).
func (s *Store) TouchSession(ctx context.Context, tokenHash, expiresAt string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, expiresAt, tokenHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions logs a user out everywhere; used after a password change.
func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
