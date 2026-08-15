package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"wishd/internal/model"
)

// TestConcurrentClaimSingleUnit is the first of the two tests that gate P1
// (plan §8): N goroutines race for an item with quantity 1.
func TestConcurrentClaimSingleUnit(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	item := mustItem(t, st, list, 1)

	const racers = 24
	claimers := make([]*model.User, racers)
	for i := range claimers {
		claimers[i] = mustUser(t, st, fmt.Sprintf("claimer%02d", i))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var succeeded, conflicted int

	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := st.CreateClaim(ctx, item.ID, claimers[i].ID, 1, nil)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, model.ErrConflict):
				conflicted++
			default:
				t.Errorf("unexpected claim error: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("exactly one claim should win, got %d (and %d conflicts)", succeeded, conflicted)
	}

	got, err := st.ItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if got.ClaimedQty != 1 {
		t.Errorf("claimed_qty = %d, want 1", got.ClaimedQty)
	}

	var sum int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(qty),0) FROM claims WHERE item_id = ?`, item.ID).Scan(&sum); err != nil {
		t.Fatalf("sum claims: %v", err)
	}
	if sum != 1 {
		t.Errorf("SUM(claims.qty) = %d, want 1", sum)
	}
	assertInvariant(t, st)
}

// TestConcurrentClaimMixedSizes repeats the race with quantity 3 and mixed
// claim sizes, per plan §8.
func TestConcurrentClaimMixedSizes(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	item := mustItem(t, st, list, 3)

	sizes := []int{1, 2, 3, 1, 2, 3, 1, 1, 2, 3, 1, 2}
	claimers := make([]*model.User, len(sizes))
	for i := range sizes {
		claimers[i] = mustUser(t, st, fmt.Sprintf("c%02d", i))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0

	start := make(chan struct{})
	for i, size := range sizes {
		wg.Add(1)
		go func(i, size int) {
			defer wg.Done()
			<-start
			_, err := st.CreateClaim(ctx, item.ID, claimers[i].ID, size, nil)
			if err == nil {
				mu.Lock()
				granted += size
				mu.Unlock()
				return
			}
			if !errors.Is(err, model.ErrConflict) {
				t.Errorf("unexpected claim error: %v", err)
			}
		}(i, size)
	}
	close(start)
	wg.Wait()

	got, err := st.ItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if got.ClaimedQty > got.Quantity {
		t.Errorf("claimed_qty %d exceeds quantity %d", got.ClaimedQty, got.Quantity)
	}
	if got.ClaimedQty != granted {
		t.Errorf("claimed_qty %d disagrees with granted total %d", got.ClaimedQty, granted)
	}
	assertInvariant(t, st)
}

// TestReleaseRestoresCapacity covers the other half of the invariant: the
// counter must come back down.
func TestReleaseRestoresCapacity(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	other := mustUser(t, st, "other")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	item := mustItem(t, st, list, 2)

	c1, err := st.CreateClaim(ctx, item.ID, other.ID, 2, nil)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := st.CreateClaim(ctx, item.ID, other.ID, 1, nil); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("overclaim should conflict, got %v", err)
	}
	if _, err := st.ReleaseClaim(ctx, c1.ID, other.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := st.CreateClaim(ctx, item.ID, other.ID, 2, nil); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	assertInvariant(t, st)
}

// TestOwnerCannotClaimOwnItem covers plan §3.3.
func TestOwnerCannotClaimOwnItem(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	item := mustItem(t, st, list, 1)

	if _, err := st.CreateClaim(ctx, item.ID, owner.ID, 1, nil); !errors.Is(err, model.ErrOwnerBlind) {
		t.Fatalf("owner claim should be refused with ErrOwnerBlind, got %v", err)
	}
	assertInvariant(t, st)
}

// TestClaimReadsFailClosedForOwner covers plan §3.2 point 2 at the repository
// layer: every claim read refuses the owner.
func TestClaimReadsFailClosedForOwner(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	other := mustUser(t, st, "other")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	item := mustItem(t, st, list, 2)

	if _, err := st.CreateClaim(ctx, item.ID, other.ID, 1, model.Ptr("shh")); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := st.ClaimsForList(ctx, list.ID, owner.ID); !errors.Is(err, model.ErrOwnerBlind) {
		t.Errorf("ClaimsForList as owner: got %v, want ErrOwnerBlind", err)
	}
	if _, err := st.ClaimsForItem(ctx, item.ID, owner.ID); !errors.Is(err, model.ErrOwnerBlind) {
		t.Errorf("ClaimsForItem as owner: got %v, want ErrOwnerBlind", err)
	}
	// The owner's own claims page must not surface claims on their own lists.
	rows, err := st.ClaimsByUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ClaimsByUser: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("owner sees %d claim rows on their own list, want 0", len(rows))
	}
}

// TestQuantityReductionBlockedByClaims covers plan §3.4.
func TestQuantityReductionBlockedByClaims(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	other := mustUser(t, st, "other")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	item := mustItem(t, st, list, 3)

	if _, err := st.CreateClaim(ctx, item.ID, other.ID, 2, nil); err != nil {
		t.Fatalf("claim: %v", err)
	}

	err := st.UpdateItem(ctx, item.ID, itemUpdateFor(item, 1))
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("reducing below claimed_qty should conflict, got %v", err)
	}
	// Reducing to exactly the claimed amount is allowed.
	if err := st.UpdateItem(ctx, item.ID, itemUpdateFor(item, 2)); err != nil {
		t.Fatalf("reducing to claimed_qty should be allowed, got %v", err)
	}
	assertInvariant(t, st)
}

// TestDeleteWithClaimsSoftDeletes covers plan §3.4: claimed items are never
// hard-deleted, unclaimed ones are.
func TestDeleteWithClaimsSoftDeletes(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	other := mustUser(t, st, "other")
	list := mustList(t, st, owner, model.VisibilityAllUsers)

	claimed := mustItem(t, st, list, 1)
	unclaimed := mustItem(t, st, list, 1)
	if _, err := st.CreateClaim(ctx, claimed.ID, other.ID, 1, nil); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := st.DeleteItem(ctx, claimed.ID); err != nil {
		t.Fatalf("delete claimed: %v", err)
	}
	if err := st.DeleteItem(ctx, unclaimed.ID); err != nil {
		t.Fatalf("delete unclaimed: %v", err)
	}

	got, err := st.ItemByID(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("claimed item should still exist: %v", err)
	}
	if got.DeletedAt == nil {
		t.Error("claimed item should be soft-deleted")
	}
	if _, err := st.ItemByID(ctx, unclaimed.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("unclaimed item should be gone, got %v", err)
	}

	// The claimer still sees it, marked as removed.
	removed, err := st.RemovedClaimedItems(ctx, list.ID, other.ID)
	if err != nil {
		t.Fatalf("removed items: %v", err)
	}
	if len(removed) != 1 {
		t.Errorf("claimer should see 1 removed item, got %d", len(removed))
	}
	assertInvariant(t, st)
}
