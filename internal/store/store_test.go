package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"wishbone/internal/db"
	"wishbone/internal/model"
	"wishbone/internal/store"
)

// newStore opens a real SQLite database in a temp dir. The tests exercise the
// actual pragmas, constraints and locking behavior, which is the whole point:
// the claim invariant is enforced by SQL, not by Go.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	sqldb, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqldb.Close() })
	return store.New(sqldb)
}

func mustUser(t *testing.T, st *store.Store, username string) *model.User {
	t.Helper()
	u := &model.User{Username: username, DisplayName: username, PasswordHash: "x"}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return u
}

func mustList(t *testing.T, st *store.Store, owner *model.User, visibility string) *model.List {
	t.Helper()
	l := &model.List{OwnerID: owner.ID, Name: "list", Visibility: visibility}
	if err := st.CreateList(context.Background(), l); err != nil {
		t.Fatalf("create list: %v", err)
	}
	return l
}

func mustItem(t *testing.T, st *store.Store, list *model.List, qty int) *model.Item {
	t.Helper()
	it := &model.Item{ListID: list.ID, Title: "thing", Quantity: qty}
	if err := st.CreateItem(context.Background(), it); err != nil {
		t.Fatalf("create item: %v", err)
	}
	return it
}

// assertInvariant is the check plan §2.1 requires in the test suite.
func assertInvariant(t *testing.T, st *store.Store) {
	t.Helper()
	drift, err := st.CheckClaimInvariant(context.Background())
	if err != nil {
		t.Fatalf("invariant check: %v", err)
	}
	if len(drift) > 0 {
		t.Fatalf("claimed_qty drifted from SUM(claims.qty): %+v", drift)
	}
}

// itemUpdateFor builds an update that changes only the quantity.
func itemUpdateFor(it *model.Item, quantity int) store.ItemUpdate {
	return store.ItemUpdate{
		Title:        it.Title,
		URL:          it.URL,
		URLRaw:       it.URLRaw,
		Description:  it.Description,
		Notes:        it.Notes,
		PriceCents:   it.PriceCents,
		Currency:     it.Currency,
		PriceSeenAt:  it.PriceSeenAt,
		CategoryID:   it.CategoryID,
		Attributes:   it.Attributes,
		FieldSources: it.FieldSources,
		LinkStatus:   it.LinkStatus,
		Quantity:     quantity,
	}
}
