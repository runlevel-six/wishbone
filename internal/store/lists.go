package store

import (
	"context"
	"database/sql"
	"errors"

	"wishbone/internal/model"
)

const listCols = `id, owner_id, name, visibility, created_at, updated_at`

func scanList(sc interface{ Scan(...any) error }) (*model.List, error) {
	var l model.List
	err := sc.Scan(&l.ID, &l.OwnerID, &l.Name, &l.Visibility, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *Store) CreateList(ctx context.Context, l *model.List) error {
	if l.ID == "" {
		l.ID = model.NewID()
	}
	now := model.TimeString(model.Now())
	if l.CreatedAt == "" {
		l.CreatedAt = now
	}
	l.UpdatedAt = now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO lists (`+listCols+`) VALUES (?,?,?,?,?,?)`,
		l.ID, l.OwnerID, l.Name, l.Visibility, l.CreatedAt, l.UpdatedAt)
	return err
}

// ListByID returns the raw row without any authorization check. Callers must
// pass the result through CanView / ownership checks; handlers should prefer
// VisibleList.
func (s *Store) ListByID(ctx context.Context, id string) (*model.List, error) {
	return scanList(s.db.QueryRowContext(ctx, `SELECT `+listCols+` FROM lists WHERE id = ?`, id))
}

// VisibleList fetches a list only if the viewer may see it (plan §3.1),
// returning ErrNotFound otherwise — an unauthorized viewer must not be able to
// distinguish "exists but hidden" from "does not exist".
func (s *Store) VisibleList(ctx context.Context, listID, viewerID string) (*model.List, error) {
	l, err := s.ListByID(ctx, listID)
	if err != nil {
		return nil, err
	}
	ok, err := s.CanView(ctx, l, viewerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, model.ErrNotFound
	}
	return l, nil
}

// CanView implements the predicate in plan §3.1 verbatim. Admin is not a
// bypass: admins see what everyone else sees.
func (s *Store) CanView(ctx context.Context, l *model.List, viewerID string) (bool, error) {
	switch {
	case l.OwnerID == viewerID:
		return true, nil
	case l.Visibility == model.VisibilityAllUsers:
		return true, nil
	case l.Visibility == model.VisibilitySelected:
		var n int
		err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM list_shares WHERE list_id = ? AND user_id = ?`,
			l.ID, viewerID).Scan(&n)
		return n > 0, err
	default:
		return false, nil
	}
}

// ListsOwnedBy returns the viewer's own lists.
func (s *Store) ListsOwnedBy(ctx context.Context, ownerID string) ([]*model.List, error) {
	return s.queryLists(ctx,
		`SELECT `+listCols+` FROM lists WHERE owner_id = ? ORDER BY name COLLATE NOCASE`, ownerID)
}

// ListsVisibleTo returns lists the viewer can see but does not own.
func (s *Store) ListsVisibleTo(ctx context.Context, viewerID string) ([]*model.List, error) {
	return s.queryLists(ctx, `
		SELECT `+listCols+` FROM lists
		 WHERE owner_id != ?
		   AND (visibility = 'all_users'
		        OR (visibility = 'selected'
		            AND EXISTS (SELECT 1 FROM list_shares sh
		                         WHERE sh.list_id = lists.id AND sh.user_id = ?)))
		 ORDER BY name COLLATE NOCASE`, viewerID, viewerID)
}

func (s *Store) queryLists(ctx context.Context, q string, args ...any) ([]*model.List, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) UpdateList(ctx context.Context, l *model.List) error {
	l.UpdatedAt = model.TimeString(model.Now())
	_, err := s.db.ExecContext(ctx,
		`UPDATE lists SET name = ?, visibility = ?, updated_at = ? WHERE id = ?`,
		l.Name, l.Visibility, l.UpdatedAt, l.ID)
	return err
}

func (s *Store) DeleteList(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM lists WHERE id = ?`, id)
	return err
}

func (s *Store) ShareUserIDs(ctx context.Context, listID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM list_shares WHERE list_id = ?`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ReplaceShares sets the exact share set for a list.
func (s *Store) ReplaceShares(ctx context.Context, listID string, userIDs []string) error {
	return s.write(ctx, func(q Querier) error {
		if _, err := q.ExecContext(ctx, `DELETE FROM list_shares WHERE list_id = ?`, listID); err != nil {
			return err
		}
		for _, uid := range userIDs {
			if _, err := q.ExecContext(ctx,
				`INSERT OR IGNORE INTO list_shares (list_id, user_id) VALUES (?,?)`, listID, uid); err != nil {
				return err
			}
		}
		return nil
	})
}
