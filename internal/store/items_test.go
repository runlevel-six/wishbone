package store_test

import (
	"context"
	"errors"
	"testing"

	"wishbone/internal/model"
	"wishbone/internal/store"
)

// mkItem creates an item with the fields the ordering tests care about. Titles
// are the identity here: the assertions read as the order a person would see.
func mkItem(t *testing.T, st *store.Store, list *model.List, title string, cents *int64,
	createdAt string, categorySlug string) *model.Item {
	t.Helper()
	ctx := context.Background()
	it := &model.Item{
		ListID:     list.ID,
		Title:      title,
		Quantity:   1,
		PriceCents: cents,
		CreatedAt:  createdAt,
	}
	if categorySlug != "" {
		cats, err := st.Categories(ctx)
		if err != nil {
			t.Fatalf("categories: %v", err)
		}
		for _, c := range cats {
			if c.Slug == categorySlug {
				it.CategoryID = model.Ptr(c.ID)
			}
		}
		if it.CategoryID == nil {
			t.Fatalf("no seeded category %q", categorySlug)
		}
	}
	if err := st.CreateItem(ctx, it); err != nil {
		t.Fatalf("create item %s: %v", title, err)
	}
	return it
}

func titles(items []*model.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Title)
	}
	return out
}

func sameOrder(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestLiveItemsForListSorted covers every order the list page offers, including
// what happens to the rows that have nothing to sort by.
func TestLiveItemsForListSorted(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	owner := mustUser(t, st, "owner")
	list := mustList(t, st, owner, model.VisibilityAllUsers)

	// Created in an order that matches none of the sorts, so a passing
	// assertion cannot be insertion order in disguise.
	mkItem(t, st, list, "kettle", model.Ptr(int64(4500)), "2026-03-01T00:00:00Z", "kitchen")
	mkItem(t, st, list, "socks", model.Ptr(int64(900)), "2026-01-01T00:00:00Z", "clothing")
	mkItem(t, st, list, "surprise", nil, "2026-02-01T00:00:00Z", "")
	mkItem(t, st, list, "novel", model.Ptr(int64(1800)), "2026-04-01T00:00:00Z", "books")

	cases := []struct {
		sort model.ItemSort
		want []string
	}{
		{model.SortManual, []string{"kettle", "socks", "surprise", "novel"}},
		// The unpriced item goes last in both directions: no price is not cheap.
		{model.SortPriceAsc, []string{"socks", "novel", "kettle", "surprise"}},
		{model.SortPriceDesc, []string{"kettle", "novel", "socks", "surprise"}},
		{model.SortNewest, []string{"novel", "kettle", "surprise", "socks"}},
		{model.SortOldest, []string{"socks", "surprise", "kettle", "novel"}},
		// Category order comes from the categories table (clothing 20, books 40,
		// kitchen 80), and the uncategorized item sorts last.
		{model.SortCategory, []string{"socks", "novel", "kettle", "surprise"}},
	}
	for _, tc := range cases {
		items, err := st.LiveItemsForListSorted(ctx, list.ID, tc.sort)
		if err != nil {
			t.Fatalf("sorted %s: %v", tc.sort, err)
		}
		if got := titles(items); !sameOrder(got, tc.want...) {
			t.Errorf("sort %s: got %v, want %v", tc.sort, got, tc.want)
		}
	}

	// The plain call and the default sort are the same query, and an unknown
	// sort has to land there too rather than failing.
	plain, err := st.LiveItemsForList(ctx, list.ID)
	if err != nil {
		t.Fatalf("unsorted: %v", err)
	}
	if got := titles(plain); !sameOrder(got, "kettle", "socks", "surprise", "novel") {
		t.Errorf("default order: got %v", got)
	}
	junk, err := st.LiveItemsForListSorted(ctx, list.ID, model.ParseItemSort("'; DROP TABLE items --"))
	if err != nil {
		t.Fatalf("nonsense sort: %v", err)
	}
	if got := titles(junk); !sameOrder(got, titles(plain)...) {
		t.Errorf("nonsense sort: got %v, want the default order", got)
	}
}

// TestSortedListExcludesRemovedItems keeps the sorts honest about the one filter
// they must not lose.
func TestSortedListExcludesRemovedItems(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	owner := mustUser(t, st, "owner")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	keep := mkItem(t, st, list, "keep", model.Ptr(int64(100)), "2026-01-01T00:00:00Z", "")
	gone := mkItem(t, st, list, "gone", model.Ptr(int64(200)), "2026-01-02T00:00:00Z", "")

	claimer := mustUser(t, st, "claimer")
	if _, err := st.CreateClaim(ctx, gone.ID, claimer.ID, 1, nil); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := st.DeleteItem(ctx, gone.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	for _, sort := range []model.ItemSort{model.SortManual, model.SortPriceAsc, model.SortNewest, model.SortCategory} {
		items, err := st.LiveItemsForListSorted(ctx, list.ID, sort)
		if err != nil {
			t.Fatalf("sorted %s: %v", sort, err)
		}
		if got := titles(items); !sameOrder(got, keep.Title) {
			t.Errorf("sort %s returned %v; the removed item should be absent", sort, got)
		}
	}
}

// TestMoveItemToList is the happy path: the item changes list, lands at the end,
// and is marked as edited so the people who claimed it are told something moved.
func TestMoveItemToList(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	owner := mustUser(t, st, "owner")
	src := mustList(t, st, owner, model.VisibilityAllUsers)
	dest := mustList(t, st, owner, model.VisibilityPrivate)

	moving := mustItem(t, st, src, 1)
	staying := mustItem(t, st, src, 1)
	sitting := mustItem(t, st, dest, 1)

	if err := st.MoveItemToList(ctx, moving.ID, dest.ID); err != nil {
		t.Fatalf("move: %v", err)
	}

	got, err := st.ItemByID(ctx, moving.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ListID != dest.ID {
		t.Errorf("list_id = %s, want the destination", got.ListID)
	}
	if got.EditedAt == nil {
		t.Error("edited_at was not set; claimers get no signal that the item moved")
	}

	after, err := st.LiveItemsForList(ctx, dest.ID)
	if err != nil {
		t.Fatalf("destination: %v", err)
	}
	if len(after) != 2 || after[0].ID != sitting.ID || after[1].ID != moving.ID {
		t.Errorf("destination order = %v, want the moved item last", titles(after))
	}
	remaining, err := st.LiveItemsForList(ctx, src.ID)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != staying.ID {
		t.Errorf("source still holds %d items, want only the one that stayed", len(remaining))
	}
}

// TestMoveItemToListKeepsClaims is the invariant that matters: a claim belongs to
// the item, so it survives the move untouched and the counter still agrees.
func TestMoveItemToListKeepsClaims(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	owner := mustUser(t, st, "owner")
	claimer := mustUser(t, st, "claimer")
	src := mustList(t, st, owner, model.VisibilityAllUsers)
	dest := mustList(t, st, owner, model.VisibilityAllUsers)

	it := mustItem(t, st, src, 3)
	if _, err := st.CreateClaim(ctx, it.ID, claimer.ID, 2, model.Ptr("shh")); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := st.MoveItemToList(ctx, it.ID, dest.ID); err != nil {
		t.Fatalf("move: %v", err)
	}

	moved, err := st.ItemByID(ctx, it.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if moved.ClaimedQty != 2 {
		t.Errorf("claimed_qty = %d, want 2", moved.ClaimedQty)
	}
	claims, err := st.ClaimsForItem(ctx, it.ID, claimer.ID)
	if err != nil {
		t.Fatalf("claims: %v", err)
	}
	if len(claims.Claims) != 1 || claims.Claims[0].Qty != 2 {
		t.Errorf("claims after the move: %+v", claims.Claims)
	}
	// The claim moved with the item, so it is still on the claimer's own page —
	// under the destination list's name.
	mine, err := st.ClaimsByUser(ctx, claimer.ID)
	if err != nil {
		t.Fatalf("claims by user: %v", err)
	}
	if len(mine) != 1 || mine[0].Item.ListID != dest.ID {
		t.Errorf("claimer's page lost track of the item: %+v", mine)
	}
	// Owner-blindness is unaffected by any of this.
	if _, err := st.ClaimsForItem(ctx, it.ID, owner.ID); !errors.Is(err, model.ErrOwnerBlind) {
		t.Errorf("owner reading claims after a move: %v, want ErrOwnerBlind", err)
	}
	assertInvariant(t, st)
}

// TestMoveItemToListRefusesForeignDestinations covers everywhere an item must not
// be able to go. Each case answers ErrNotFound, like everything else that is not
// yours to touch, and leaves the item where it was.
func TestMoveItemToListRefusesForeignDestinations(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	owner := mustUser(t, st, "owner")
	other := mustUser(t, st, "other")
	src := mustList(t, st, owner, model.VisibilityAllUsers)
	theirs := mustList(t, st, other, model.VisibilityAllUsers)
	it := mustItem(t, st, src, 1)

	for name, dest := range map[string]string{
		"someone else's list": theirs.ID,
		"no such list":        "not-a-list",
		"empty":               "",
	} {
		if err := st.MoveItemToList(ctx, it.ID, dest); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("move to %s: %v, want ErrNotFound", name, err)
		}
		got, err := st.ItemByID(ctx, it.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got.ListID != src.ID {
			t.Fatalf("move to %s went through anyway", name)
		}
	}

	// A missing item is refused the same way, whoever asks.
	if err := st.MoveItemToList(ctx, "not-an-item", src.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("moving a missing item: %v, want ErrNotFound", err)
	}
}

// TestMoveItemToItsOwnListDoesNothing keeps a redundant submission from
// reshuffling the list it was already on.
func TestMoveItemToItsOwnListDoesNothing(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	owner := mustUser(t, st, "owner")
	list := mustList(t, st, owner, model.VisibilityAllUsers)
	first := mustItem(t, st, list, 1)
	second := mustItem(t, st, list, 1)

	if err := st.MoveItemToList(ctx, first.ID, list.ID); err != nil {
		t.Fatalf("move onto its own list: %v", err)
	}
	items, err := st.LiveItemsForList(ctx, list.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(items) != 2 || items[0].ID != first.ID || items[1].ID != second.ID {
		t.Error("a no-op move changed the order")
	}
	if items[0].EditedAt != nil {
		t.Error("a no-op move marked the item edited")
	}
}

// TestMoveItemToListLeavesRemovedItemsAlone: a soft-deleted item is kept only so
// its claimer can be told it went away, and moving it would move that notice onto
// a list it was never on.
func TestMoveItemToListLeavesRemovedItemsAlone(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	owner := mustUser(t, st, "owner")
	claimer := mustUser(t, st, "claimer")
	src := mustList(t, st, owner, model.VisibilityAllUsers)
	dest := mustList(t, st, owner, model.VisibilityAllUsers)

	it := mustItem(t, st, src, 1)
	if _, err := st.CreateClaim(ctx, it.ID, claimer.ID, 1, nil); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := st.DeleteItem(ctx, it.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.MoveItemToList(ctx, it.ID, dest.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("moving a removed item: %v, want ErrNotFound", err)
	}
	got, err := st.ItemByID(ctx, it.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ListID != src.ID {
		t.Error("a removed item was moved")
	}
}
