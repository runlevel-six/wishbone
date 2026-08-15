package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"wishd/internal/model"
)

const itemCols = `id, list_id, category_id, title, url, url_raw, description, notes,
	price_cents, currency, price_seen_at, quantity, claimed_qty, attributes, field_sources,
	link_status, link_checked_at, sort_order, created_at, updated_at, edited_at, deleted_at, legacy_id`

func scanItem(sc interface{ Scan(...any) error }) (*model.Item, error) {
	var it model.Item
	err := sc.Scan(&it.ID, &it.ListID, &it.CategoryID, &it.Title, &it.URL, &it.URLRaw,
		&it.Description, &it.Notes, &it.PriceCents, &it.Currency, &it.PriceSeenAt,
		&it.Quantity, &it.ClaimedQty, &it.Attributes, &it.FieldSources,
		&it.LinkStatus, &it.LinkCheckedAt, &it.SortOrder, &it.CreatedAt, &it.UpdatedAt,
		&it.EditedAt, &it.DeletedAt, &it.LegacyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func (s *Store) CreateItem(ctx context.Context, it *model.Item) error {
	if it.ID == "" {
		it.ID = model.NewID()
	}
	now := model.TimeString(model.Now())
	if it.CreatedAt == "" {
		it.CreatedAt = now
	}
	it.UpdatedAt = now
	if it.Attributes == "" {
		it.Attributes = "{}"
	}
	if it.FieldSources == "" {
		it.FieldSources = "{}"
	}
	if it.LinkStatus == "" {
		it.LinkStatus = model.LinkUnknown
	}
	if it.Quantity < 1 {
		it.Quantity = 1
	}
	return s.write(ctx, func(q Querier) error {
		if it.SortOrder == 0 {
			var next sql.NullInt64
			if err := q.QueryRowContext(ctx,
				`SELECT MAX(sort_order) FROM items WHERE list_id = ?`, it.ListID).Scan(&next); err != nil {
				return err
			}
			it.SortOrder = int(next.Int64) + 1
		}
		_, err := q.ExecContext(ctx,
			`INSERT INTO items (`+itemCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			it.ID, it.ListID, it.CategoryID, it.Title, it.URL, it.URLRaw, it.Description, it.Notes,
			it.PriceCents, it.Currency, it.PriceSeenAt, it.Quantity, it.ClaimedQty, it.Attributes,
			it.FieldSources, it.LinkStatus, it.LinkCheckedAt, it.SortOrder, it.CreatedAt,
			it.UpdatedAt, it.EditedAt, it.DeletedAt, it.LegacyID)
		return err
	})
}

// ItemByID returns any item, deleted or not, without authorization checks.
func (s *Store) ItemByID(ctx context.Context, id string) (*model.Item, error) {
	return scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items WHERE id = ?`, id))
}

func (s *Store) ItemByLegacyID(ctx context.Context, legacyID string) (*model.Item, error) {
	return scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items WHERE legacy_id = ?`, legacyID))
}

// LiveItemsForList returns the visible (not soft-deleted) items in display
// order. Ordering is by sort_order then created_at only — never by anything
// claim-derived, which would leak (plan §3.2).
func (s *Store) LiveItemsForList(ctx context.Context, listID string) ([]*model.Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items
		  WHERE list_id = ? AND deleted_at IS NULL
		  ORDER BY sort_order, created_at, id`, listID)
}

// RemovedClaimedItems returns soft-deleted items in the list that the viewer
// holds a claim on, so a claimer sees "removed by owner" instead of an item
// silently vanishing (plan §3.4). The owner never calls this.
func (s *Store) RemovedClaimedItems(ctx context.Context, listID, viewerID string) ([]*model.Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items i
		  WHERE i.list_id = ? AND i.deleted_at IS NOT NULL
		    AND EXISTS (SELECT 1 FROM claims c WHERE c.item_id = i.id AND c.claimer_id = ?)
		  ORDER BY i.deleted_at DESC`, listID, viewerID)
}

func (s *Store) queryItems(ctx context.Context, q string, args ...any) ([]*model.Item, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemUpdate carries the owner-editable fields. Quantity is handled separately
// because it interacts with claims.
type ItemUpdate struct {
	Title        string
	URL          *string
	URLRaw       *string
	Description  *string
	Notes        *string
	PriceCents   *int64
	Currency     *string
	PriceSeenAt  *string
	CategoryID   *string
	Attributes   string
	FieldSources string
	LinkStatus   string
	Quantity     int
}

// UpdateItem applies an owner edit.
//
// Reducing quantity below claimed_qty is rejected by the WHERE clause rather
// than by reading claimed_qty first, so the handler can produce the count-free
// message required by plan §3.4 without ever holding the count.
func (s *Store) UpdateItem(ctx context.Context, id string, u ItemUpdate) error {
	now := model.TimeString(model.Now())
	return s.write(ctx, func(q Querier) error {
		res, err := q.ExecContext(ctx,
			`UPDATE items
			    SET title = ?, url = ?, url_raw = ?, description = ?, notes = ?,
			        price_cents = ?, currency = ?, price_seen_at = ?, category_id = ?,
			        attributes = ?, field_sources = ?, link_status = ?, quantity = ?,
			        updated_at = ?, edited_at = ?
			  WHERE id = ? AND deleted_at IS NULL AND ? >= claimed_qty`,
			u.Title, u.URL, u.URLRaw, u.Description, u.Notes, u.PriceCents, u.Currency,
			u.PriceSeenAt, u.CategoryID, u.Attributes, u.FieldSources, u.LinkStatus,
			u.Quantity, now, now, id, u.Quantity)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			// Either the item is gone or the quantity reduction is blocked.
			// The caller must not learn which without re-checking existence.
			return model.ErrConflict
		}
		return nil
	})
}

// DeleteItem removes an item. Items with claims are soft-deleted and kept
// (plan §3.4); unclaimed items are removed outright. Both branches run
// unconditionally in one transaction so the owner cannot tell which happened.
func (s *Store) DeleteItem(ctx context.Context, id string) error {
	now := model.TimeString(model.Now())
	return s.write(ctx, func(q Querier) error {
		if _, err := q.ExecContext(ctx,
			`UPDATE items SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			now, now, id); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `DELETE FROM items WHERE id = ? AND claimed_qty = 0`, id)
		return err
	})
}

// ReorderItems applies an explicit ordering of item IDs within a list.
func (s *Store) ReorderItems(ctx context.Context, listID string, ids []string) error {
	now := model.TimeString(model.Now())
	return s.write(ctx, func(q Querier) error {
		for i, id := range ids {
			if _, err := q.ExecContext(ctx,
				`UPDATE items SET sort_order = ?, updated_at = ?
				  WHERE id = ? AND list_id = ? AND deleted_at IS NULL`,
				i+1, now, id, listID); err != nil {
				return err
			}
		}
		return nil
	})
}

// MoveItem shifts one item one position up or down within its list. This is
// the htmx-friendly form of reordering; it never depends on claim state.
func (s *Store) MoveItem(ctx context.Context, listID, itemID string, up bool) error {
	items, err := s.LiveItemsForList(ctx, listID)
	if err != nil {
		return err
	}
	idx := -1
	for i, it := range items {
		if it.ID == itemID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return model.ErrNotFound
	}
	swap := idx + 1
	if up {
		swap = idx - 1
	}
	if swap < 0 || swap >= len(items) {
		return nil
	}
	items[idx], items[swap] = items[swap], items[idx]
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return s.ReorderItems(ctx, listID, ids)
}

// SetLinkStatus is used by the extractor and the link-health job.
func (s *Store) SetLinkStatus(ctx context.Context, itemID, status, checkedAt string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE items SET link_status = ?, link_checked_at = ? WHERE id = ?`,
		status, checkedAt, itemID)
	return err
}

// DuplicateItems finds items with the same normalized URL that the viewer can
// already see, for the non-blocking duplicate warning in plan §5.1.
func (s *Store) DuplicateItems(ctx context.Context, normalizedURL, viewerID, excludeItemID string) ([]*model.Item, error) {
	if strings.TrimSpace(normalizedURL) == "" {
		return nil, nil
	}
	return s.queryItems(ctx, `
		SELECT `+itemCols+` FROM items i
		  JOIN lists l ON l.id = i.list_id
		 WHERE i.url = ? AND i.deleted_at IS NULL AND i.id != ?
		   AND (l.owner_id = ?
		        OR l.visibility = 'all_users'
		        OR (l.visibility = 'selected'
		            AND EXISTS (SELECT 1 FROM list_shares sh
		                         WHERE sh.list_id = l.id AND sh.user_id = ?)))
		 LIMIT 5`, normalizedURL, excludeItemID, viewerID, viewerID)
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
