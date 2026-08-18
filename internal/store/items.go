package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"wishbone/internal/model"
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

// LiveItemsForList returns the visible (not soft-deleted) items in the owner's
// own order.
func (s *Store) LiveItemsForList(ctx context.Context, listID string) ([]*model.Item, error) {
	return s.LiveItemsForListSorted(ctx, listID, model.SortManual)
}

// LiveItemsForListSorted is the same set in one of the orders a reader can ask
// for. Every available key is owner-authored — position, price, when the owner
// added it, the category they picked — and never anything claim-derived, which
// would leak (plan §3.2).
func (s *Store) LiveItemsForListSorted(ctx context.Context, listID string, sort model.ItemSort) ([]*model.Item, error) {
	return s.queryItems(ctx,
		`SELECT `+prefixed(itemCols, "i")+` FROM items i
		  LEFT JOIN categories c ON c.id = i.category_id
		  WHERE i.list_id = ? AND i.deleted_at IS NULL
		  ORDER BY `+itemOrderBy(sort), listID)
}

// itemOrderBy maps a sort onto SQL. It is a closed switch over constants with a
// default, so nothing a reader supplies reaches the statement — ParseItemSort
// clamps unknown values, and anything that slipped past it lands on the default.
func itemOrderBy(sort model.ItemSort) string {
	switch sort {
	// Unpriced items sort last in both directions: an empty price field is the
	// owner not having said anything, which is not the same as cheap. Cents are
	// compared across currencies as plain integers, which is wrong in principle
	// and irrelevant for one family buying in one currency.
	case model.SortPriceAsc:
		return `i.price_cents IS NULL, i.price_cents, i.sort_order, i.id`
	case model.SortPriceDesc:
		return `i.price_cents IS NULL, i.price_cents DESC, i.sort_order, i.id`
	// created_at is stored to the second, so a batch of items added together
	// ties. UUIDv7 ids break the tie in the same direction time runs.
	case model.SortNewest:
		return `i.created_at DESC, i.id DESC`
	case model.SortOldest:
		return `i.created_at, i.id`
	// Uncategorized last, categories in the order the category table defines,
	// and the owner's own order kept inside each group.
	case model.SortCategory:
		return `i.category_id IS NULL, c.sort_order, c.label, i.sort_order, i.id`
	default:
		return `i.sort_order, i.created_at, i.id`
	}
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

// ItemsDueForLinkCheck returns items whose link has not been looked at since
// before, oldest first, at most limit of them (plan §5.4).
//
// Never-checked items come first: link_status is written when an item is created
// from a URL and then never revisited, so on the first sweep of an existing
// instance everything is equally stale and the oldest thing on the oldest list
// is the best guess at what has rotted.
//
// Soft-deleted items are skipped. Nobody is going to buy them, and spending
// requests on a retailer's patience for a row that is only kept so a claimer can
// be told it went away is the wrong trade.
func (s *Store) ItemsDueForLinkCheck(ctx context.Context, before string, limit int) ([]*model.Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items
		  WHERE deleted_at IS NULL
		    AND url IS NOT NULL AND url <> ''
		    AND (link_checked_at IS NULL OR link_checked_at < ?)
		  ORDER BY link_checked_at IS NOT NULL, link_checked_at, created_at
		  LIMIT ?`, before, limit)
}

// AuditItemsForList returns every item in a list, soft-deleted ones included.
//
// For the admin reconciliation report and nothing else (plan §13). "My claim
// disappeared" is usually the owner having removed the item, so a report that
// hides removed items cannot answer the question it exists for. Deliberately
// separate from LiveItemsForList rather than a flag on it: a caller has to ask
// for this by name.
func (s *Store) AuditItemsForList(ctx context.Context, listID string) ([]*model.Item, error) {
	return s.queryItems(ctx,
		`SELECT `+itemCols+` FROM items
		  WHERE list_id = ?
		  ORDER BY deleted_at IS NOT NULL, sort_order, created_at, id`, listID)
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

// MoveItemToList moves an item to another list with the same owner. It is the
// cross-list counterpart of MoveItem, which only shifts a position.
//
// Claims are not read, not counted and not touched. They hang off the item, so
// they travel with it, and the outcome of this call is identical whether the
// item is claimed or not — the owner learns nothing from having made the move
// (plan §3.2). What can change for a claimer is whether they can still see the
// item at all, because the destination carries its own visibility, so the move
// is recorded as an owner edit: that is the signal an edit already gives them
// (plan §12).
//
// The item lands at the end of the destination list. A destination that is not
// the same owner's list is reported as ErrNotFound, like everything else that is
// not yours to touch (plan §3.1).
func (s *Store) MoveItemToList(ctx context.Context, itemID, destListID string) error {
	now := model.TimeString(model.Now())
	return s.write(ctx, func(q Querier) error {
		var srcListID, ownerID string
		err := q.QueryRowContext(ctx,
			`SELECT l.id, l.owner_id FROM items i JOIN lists l ON l.id = i.list_id
			  WHERE i.id = ? AND i.deleted_at IS NULL`, itemID).Scan(&srcListID, &ownerID)
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrNotFound
		}
		if err != nil {
			return err
		}

		var destOwnerID string
		err = q.QueryRowContext(ctx,
			`SELECT owner_id FROM lists WHERE id = ?`, destListID).Scan(&destOwnerID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return model.ErrNotFound
		case err != nil:
			return err
		case destOwnerID != ownerID:
			return model.ErrNotFound
		}
		if destListID == srcListID {
			return nil
		}

		var last sql.NullInt64
		if err := q.QueryRowContext(ctx,
			`SELECT MAX(sort_order) FROM items WHERE list_id = ?`, destListID).Scan(&last); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx,
			`UPDATE items
			    SET list_id = ?, sort_order = ?, updated_at = ?, edited_at = ?
			  WHERE id = ? AND deleted_at IS NULL`,
			destListID, int(last.Int64)+1, now, now, itemID)
		return err
	})
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
