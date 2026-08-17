package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"wishbone/internal/model"
)

// This file is the single chokepoint for claim data (plan §3.2).
//
// Every read takes a viewerID and returns ErrOwnerBlind when that viewer owns
// the list the claim hangs off. Every write refuses a claim whose claimer owns
// the list. Nothing else in the codebase may SELECT from claims — the
// owner-blindness test walks the router, but this is the layer that makes the
// property true rather than merely tested.

// ItemClaims is what a non-owning viewer is allowed to know about one item.
type ItemClaims struct {
	ItemID     string
	ClaimedQty int
	Claims     []*model.Claim
}

// MineQty returns how many units the given user has claimed on this item.
func (ic *ItemClaims) MineQty(userID string) int {
	n := 0
	for _, c := range ic.Claims {
		if c.ClaimerID == userID {
			n += c.Qty
		}
	}
	return n
}

// MyClaims returns the viewer's own claims on this item.
func (ic *ItemClaims) MyClaims(userID string) []*model.Claim {
	var out []*model.Claim
	for _, c := range ic.Claims {
		if c.ClaimerID == userID {
			out = append(out, c)
		}
	}
	return out
}

const claimCols = `c.id, c.item_id, c.claimer_id, u.display_name, c.qty, c.state, c.note, c.created_at, c.updated_at`

func scanClaim(sc interface{ Scan(...any) error }) (*model.Claim, error) {
	var c model.Claim
	err := sc.Scan(&c.ID, &c.ItemID, &c.ClaimerID, &c.ClaimerName, &c.Qty, &c.State,
		&c.Note, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ownerOfList returns the owner of the list an item belongs to.
func ownerOfItem(ctx context.Context, q Querier, itemID string) (listID, ownerID string, err error) {
	err = q.QueryRowContext(ctx,
		`SELECT l.id, l.owner_id FROM items i JOIN lists l ON l.id = i.list_id WHERE i.id = ?`,
		itemID).Scan(&listID, &ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", model.ErrNotFound
	}
	return listID, ownerID, err
}

// ClaimsForList returns claim state for every item in a list, keyed by item ID.
// Returns ErrOwnerBlind if the viewer owns the list — fail closed.
func (s *Store) ClaimsForList(ctx context.Context, listID, viewerID string) (map[string]*ItemClaims, error) {
	var ownerID string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM lists WHERE id = ?`, listID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ownerID == viewerID {
		return nil, model.ErrOwnerBlind
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+claimCols+`
		   FROM claims c
		   JOIN users u ON u.id = c.claimer_id
		   JOIN items i ON i.id = c.item_id
		  WHERE i.list_id = ?
		  ORDER BY c.created_at`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]*ItemClaims{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		ic := out[c.ItemID]
		if ic == nil {
			ic = &ItemClaims{ItemID: c.ItemID}
			out[c.ItemID] = ic
		}
		ic.Claims = append(ic.Claims, c)
		ic.ClaimedQty += c.Qty
	}
	return out, rows.Err()
}

// ClaimsForItem is the single-item form of ClaimsForList.
func (s *Store) ClaimsForItem(ctx context.Context, itemID, viewerID string) (*ItemClaims, error) {
	_, ownerID, err := ownerOfItem(ctx, s.db, itemID)
	if err != nil {
		return nil, err
	}
	if ownerID == viewerID {
		return nil, model.ErrOwnerBlind
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+claimCols+`
		   FROM claims c JOIN users u ON u.id = c.claimer_id
		  WHERE c.item_id = ? ORDER BY c.created_at`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ic := &ItemClaims{ItemID: itemID}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		ic.Claims = append(ic.Claims, c)
		ic.ClaimedQty += c.Qty
	}
	return ic, rows.Err()
}

// ClaimedItem pairs a claim with the item and list it refers to, for the
// viewer's own "things I've claimed" page.
type ClaimedItem struct {
	Claim     *model.Claim
	Item      *model.Item
	ListName  string
	OwnerName string
}

// ClaimsByUser lists everything the viewer has claimed, across lists. The
// viewer is by definition not the owner of any of these (claims on your own
// list cannot be created), but the query excludes them anyway.
func (s *Store) ClaimsByUser(ctx context.Context, viewerID string) ([]*ClaimedItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+claimCols+`, `+prefixed(itemCols, "i")+`, l.name, u2.display_name
		   FROM claims c
		   JOIN users u  ON u.id  = c.claimer_id
		   JOIN items i  ON i.id  = c.item_id
		   JOIN lists l  ON l.id  = i.list_id
		   JOIN users u2 ON u2.id = l.owner_id
		  WHERE c.claimer_id = ? AND l.owner_id != ?
		  ORDER BY l.name, i.sort_order`, viewerID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ClaimedItem
	for rows.Next() {
		var c model.Claim
		var it model.Item
		var listName, ownerName string
		if err := rows.Scan(
			&c.ID, &c.ItemID, &c.ClaimerID, &c.ClaimerName, &c.Qty, &c.State, &c.Note, &c.CreatedAt, &c.UpdatedAt,
			&it.ID, &it.ListID, &it.CategoryID, &it.Title, &it.URL, &it.URLRaw, &it.Description, &it.Notes,
			&it.PriceCents, &it.Currency, &it.PriceSeenAt, &it.Quantity, &it.ClaimedQty, &it.Attributes,
			&it.FieldSources, &it.LinkStatus, &it.LinkCheckedAt, &it.SortOrder, &it.CreatedAt, &it.UpdatedAt,
			&it.EditedAt, &it.DeletedAt, &it.LegacyID,
			&listName, &ownerName); err != nil {
			return nil, err
		}
		out = append(out, &ClaimedItem{Claim: &c, Item: &it, ListName: listName, OwnerName: ownerName})
	}
	return out, rows.Err()
}

// CreateClaim performs the atomic claim of plan §3.3.
//
// The conditional UPDATE is the concurrency control: if it affects no rows the
// claim lost the race (or the item was removed), and no claims row is written.
func (s *Store) CreateClaim(ctx context.Context, itemID, claimerID string, qty int, note *string) (*model.Claim, error) {
	if qty < 1 {
		return nil, model.ErrConflict
	}
	now := model.TimeString(model.Now())
	claim := &model.Claim{
		ID:        model.NewID(),
		ItemID:    itemID,
		ClaimerID: claimerID,
		Qty:       qty,
		State:     model.ClaimStateClaimed,
		Note:      note,
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := s.write(ctx, func(q Querier) error {
		// Fail closed: re-check both visibility and owner-blindness here, not
		// only in the handler.
		var ownerID, visibility string
		err := q.QueryRowContext(ctx,
			`SELECT l.owner_id, l.visibility
			   FROM items i JOIN lists l ON l.id = i.list_id
			  WHERE i.id = ? AND i.deleted_at IS NULL`, itemID).Scan(&ownerID, &visibility)
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if ownerID == claimerID {
			return model.ErrOwnerBlind
		}
		if visibility == model.VisibilitySelected {
			var n int
			if err := q.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM list_shares sh
				  JOIN items i ON i.list_id = sh.list_id
				 WHERE i.id = ? AND sh.user_id = ?`, itemID, claimerID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return model.ErrNotFound
			}
		} else if visibility == model.VisibilityPrivate {
			return model.ErrNotFound
		}

		res, err := q.ExecContext(ctx,
			`UPDATE items
			    SET claimed_qty = claimed_qty + ?,
			        updated_at  = ?
			  WHERE id = ?
			    AND deleted_at IS NULL
			    AND claimed_qty + ? <= quantity`, qty, now, itemID, qty)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return model.ErrConflict
		}
		_, err = q.ExecContext(ctx,
			`INSERT INTO claims (id, item_id, claimer_id, qty, state, note, created_at, updated_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			claim.ID, claim.ItemID, claim.ClaimerID, claim.Qty, claim.State, claim.Note,
			claim.CreatedAt, claim.UpdatedAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

// ReleaseClaim deletes a claim and decrements the denormalized counter in the
// same transaction (plan §2.1). Only the claimer may release.
func (s *Store) ReleaseClaim(ctx context.Context, claimID, claimerID string) (itemID string, err error) {
	now := model.TimeString(model.Now())
	err = s.write(ctx, func(q Querier) error {
		var qty int
		err := q.QueryRowContext(ctx,
			`SELECT item_id, qty FROM claims WHERE id = ? AND claimer_id = ?`,
			claimID, claimerID).Scan(&itemID, &qty)
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM claims WHERE id = ?`, claimID); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx,
			`UPDATE items SET claimed_qty = claimed_qty - ?, updated_at = ? WHERE id = ?`,
			qty, now, itemID)
		return err
	})
	return itemID, err
}

// SetClaimState marks a claim purchased or back to claimed. Claimer only.
func (s *Store) SetClaimState(ctx context.Context, claimID, claimerID, state string) (itemID string, err error) {
	if state != model.ClaimStateClaimed && state != model.ClaimStatePurchased {
		return "", model.ErrConflict
	}
	now := model.TimeString(model.Now())
	err = s.write(ctx, func(q Querier) error {
		res, err := q.ExecContext(ctx,
			`UPDATE claims SET state = ?, updated_at = ? WHERE id = ? AND claimer_id = ?`,
			state, now, claimID, claimerID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return model.ErrNotFound
		}
		return q.QueryRowContext(ctx, `SELECT item_id FROM claims WHERE id = ?`, claimID).Scan(&itemID)
	})
	return itemID, err
}

// SetClaimNote updates the claimer-side note, which the list owner never sees.
func (s *Store) SetClaimNote(ctx context.Context, claimID, claimerID string, note *string) error {
	now := model.TimeString(model.Now())
	res, err := s.db.ExecContext(ctx,
		`UPDATE claims SET note = ?, updated_at = ? WHERE id = ? AND claimer_id = ?`,
		note, now, claimID, claimerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// AuditClaimsForList returns claim state for every item in a list without asking
// who is looking.
//
// This is the deliberate hole in §3.2, and it is a separate method rather than a
// parameter on ClaimsForList for exactly one reason: a bypass flag on the
// chokepoint would put "skip the invariant" inside the function every other
// caller goes through, one typo away from a leak. A caller has to name this
// function, and the name says what it does.
//
// The only permitted caller is the admin reconciliation report (plan §13), whose
// own rules — admin only, own lists behind a second deliberate opt-in, both
// actions logged — are enforced in the handler, not here. Nothing else may call
// it. It never returns ErrOwnerBlind, so there is no safety net underneath.
func (s *Store) AuditClaimsForList(ctx context.Context, listID string) (map[string]*ItemClaims, error) {
	if _, err := s.ListByID(ctx, listID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+claimCols+`
		   FROM claims c
		   JOIN users u ON u.id = c.claimer_id
		   JOIN items i ON i.id = c.item_id
		  WHERE i.list_id = ?
		  ORDER BY c.created_at`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]*ItemClaims{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		ic := out[c.ItemID]
		if ic == nil {
			ic = &ItemClaims{ItemID: c.ItemID}
			out[c.ItemID] = ic
		}
		ic.Claims = append(ic.Claims, c)
		ic.ClaimedQty += c.Qty
	}
	return out, rows.Err()
}

// ClaimUpdateCount counts the items a person has claimed that the owner has
// edited or removed since that person last looked at their claims (plan §12).
//
// This is a claim read that takes no viewerID and cannot return ErrOwnerBlind,
// which needs saying out loud in this file. It is not an exception to the
// chokepoint: the row set is "claims whose claimer is you", and a list owner
// cannot hold a claim on their own list because CreateClaim refuses one. So the
// viewer and the claimer are the same person by construction, and there is no
// owner whose surprises this could spoil. What it must never do is travel the
// other way — the count belongs to the claimer's own chrome, and telling an
// owner that somebody noticed their edit would leak the interest §3.2 protects.
//
// "Since they last looked" is the later of the watermark and the claim's own
// creation, so an edit that predates the claim is not news, and an instance
// upgrading to this column does not hand everyone a badge for history.
//
// String comparison is chronological here because every timestamp in the schema
// is written by model.TimeString: UTC, RFC3339, second precision, fixed width.
//
// The comparison is strictly greater-than, so an edit landing in the same second
// as the claim or the visit is not counted. That direction is deliberate: with
// >=, an item edited during the same second as the visit would satisfy the test
// on every later page load too, and a badge that cannot be cleared is a worse
// bug than a one-second window where a notification is missed.
func (s *Store) ClaimUpdateCount(ctx context.Context, claimerID string) (int, error) {
	const q = `
	SELECT COUNT(DISTINCT c.item_id)
	  FROM claims c
	  JOIN items i ON i.id = c.item_id
	 WHERE c.claimer_id = ?
	   AND ( COALESCE(i.edited_at, '')  > MAX(COALESCE(?, ''), c.created_at)
	      OR COALESCE(i.deleted_at, '') > MAX(COALESCE(?, ''), c.created_at) )`

	var seen *string
	if err := s.db.QueryRowContext(ctx,
		`SELECT claims_seen_at FROM users WHERE id = ?`, claimerID).Scan(&seen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}

	var n int
	if err := s.db.QueryRowContext(ctx, q, claimerID, seen, seen).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// MarkClaimsSeen moves the watermark to now and returns where it was, so the
// page that clears the badge can still say which rows the badge was about. A
// count that points at a page with nothing marked on it is a nag rather than a
// notification.
func (s *Store) MarkClaimsSeen(ctx context.Context, claimerID string) (string, error) {
	var previous *string
	err := s.db.QueryRowContext(ctx,
		`SELECT claims_seen_at FROM users WHERE id = ?`, claimerID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET claims_seen_at = ? WHERE id = ?`,
		model.TimeString(model.Now()), claimerID); err != nil {
		return "", err
	}
	return model.Deref(previous), nil
}

// ChangedSince reports whether an owner touched this item after the later of the
// viewer's watermark and the claim itself — the same rule ClaimUpdateCount
// counts by, applied to one row so the page can mark it.
func ChangedSince(item *model.Item, claim *model.Claim, watermark string) bool {
	after := watermark
	if claim != nil && claim.CreatedAt > after {
		after = claim.CreatedAt
	}
	if item == nil {
		return false
	}
	return model.Deref(item.EditedAt) > after || model.Deref(item.DeletedAt) > after
}

// prefixed rewrites a bare column list into a table-qualified one.
func prefixed(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
