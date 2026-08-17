package store

import (
	"context"
	"database/sql"
	"errors"

	"wishbone/internal/model"
)

func (s *Store) AddItemImage(ctx context.Context, img *model.ItemImage) error {
	if img.ID == "" {
		img.ID = model.NewID()
	}
	if img.CreatedAt == "" {
		img.CreatedAt = model.TimeString(model.Now())
	}
	return s.write(ctx, func(q Querier) error {
		if img.IsPrimary {
			if _, err := q.ExecContext(ctx,
				`UPDATE item_images SET is_primary = 0 WHERE item_id = ?`, img.ItemID); err != nil {
				return err
			}
		}
		_, err := q.ExecContext(ctx,
			`INSERT INTO item_images (id, item_id, sha256, mime, width, height, is_primary, created_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			img.ID, img.ItemID, img.SHA256, img.Mime, img.Width, img.Height,
			boolInt(img.IsPrimary), img.CreatedAt)
		return err
	})
}

func (s *Store) ImagesForItem(ctx context.Context, itemID string) ([]*model.ItemImage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, item_id, sha256, mime, width, height, is_primary, created_at
		   FROM item_images WHERE item_id = ? ORDER BY is_primary DESC, created_at`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ItemImage
	for rows.Next() {
		var im model.ItemImage
		if err := rows.Scan(&im.ID, &im.ItemID, &im.SHA256, &im.Mime, &im.Width, &im.Height,
			&im.IsPrimary, &im.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &im)
	}
	return out, rows.Err()
}

// PrimaryImages returns the display image per item for a whole list, so the
// list page does not issue one query per item.
func (s *Store) PrimaryImages(ctx context.Context, listID string) (map[string]*model.ItemImage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT im.id, im.item_id, im.sha256, im.mime, im.width, im.height, im.is_primary, im.created_at
		  FROM item_images im
		  JOIN items i ON i.id = im.item_id
		 WHERE i.list_id = ?
		 ORDER BY im.is_primary DESC, im.created_at`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*model.ItemImage{}
	for rows.Next() {
		var im model.ItemImage
		if err := rows.Scan(&im.ID, &im.ItemID, &im.SHA256, &im.Mime, &im.Width, &im.Height,
			&im.IsPrimary, &im.CreatedAt); err != nil {
			return nil, err
		}
		if _, seen := out[im.ItemID]; !seen {
			out[im.ItemID] = &im
		}
	}
	return out, rows.Err()
}

func (s *Store) DeleteItemImage(ctx context.Context, imageID, itemID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM item_images WHERE id = ? AND item_id = ?`, imageID, itemID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// ImageAccessible reports whether a stored blob is referenced by an item the
// viewer may see. Images are served through an authenticated handler
// (plan §6); this is the authorization behind it.
func (s *Store) ImageAccessible(ctx context.Context, sha256, viewerID string) (*model.ItemImage, error) {
	var im model.ItemImage
	err := s.db.QueryRowContext(ctx, `
		SELECT im.id, im.item_id, im.sha256, im.mime, im.width, im.height, im.is_primary, im.created_at
		  FROM item_images im
		  JOIN items i ON i.id = im.item_id
		  JOIN lists l ON l.id = i.list_id
		 WHERE im.sha256 = ?
		   AND (l.owner_id = ?
		        OR l.visibility = 'all_users'
		        OR (l.visibility = 'selected'
		            AND EXISTS (SELECT 1 FROM list_shares sh
		                         WHERE sh.list_id = l.id AND sh.user_id = ?)))
		 LIMIT 1`, sha256, viewerID, viewerID).
		Scan(&im.ID, &im.ItemID, &im.SHA256, &im.Mime, &im.Width, &im.Height, &im.IsPrimary, &im.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &im, nil
}

// ImageRefCount reports how many item_images rows point at a blob, so deleting
// one item does not unlink a blob another item deduped onto.
func (s *Store) ImageRefCount(ctx context.Context, sha256 string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_images WHERE sha256 = ?`, sha256).Scan(&n)
	return n, err
}

// ImageSHAsForList returns every blob referenced by any item in a list,
// including soft-deleted items. Used to prune files after a deletion.
func (s *Store) ImageSHAsForList(ctx context.Context, listID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT im.sha256
		  FROM item_images im JOIN items i ON i.id = im.item_id
		 WHERE i.list_id = ?`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out = append(out, sha)
	}
	return out, rows.Err()
}
