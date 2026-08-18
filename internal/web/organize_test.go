package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wishbone/internal/model"
)

// mkPricedItem adds an item to a list with a price and an explicit added-date, so
// the sort assertions have something to sort by.
func (h *harness) mkPricedItem(list *model.List, title string, cents int64, createdAt string) *model.Item {
	h.t.Helper()
	it := &model.Item{
		ListID:     list.ID,
		Title:      title,
		Quantity:   1,
		PriceCents: model.Ptr(cents),
		CreatedAt:  createdAt,
	}
	if err := h.st.CreateItem(context.Background(), it); err != nil {
		h.t.Fatalf("create item %s: %v", title, err)
	}
	return it
}

// order returns the positions of each needle in the body, so a test can assert
// what comes before what without pinning the markup around it.
func order(t *testing.T, body string, needles ...string) []int {
	t.Helper()
	out := make([]int, 0, len(needles))
	for _, n := range needles {
		i := strings.Index(body, n)
		if i < 0 {
			t.Fatalf("%q is not on the page at all", n)
		}
		out = append(out, i)
	}
	return out
}

func ascending(positions []int) bool {
	for i := 1; i < len(positions); i++ {
		if positions[i] < positions[i-1] {
			return false
		}
	}
	return true
}

// TestSortAListAsOwnerAndAsViewer is the same list read both ways: the sort keys
// are all owner-authored, so both audiences get the same control.
func TestSortAListAsOwnerAndAsViewer(t *testing.T) {
	h := newHarness(t)
	h.mkPricedItem(h.list, "Cheap thing", 500, "2026-01-01T00:00:00Z")
	h.mkPricedItem(h.list, "Dear thing", 90000, "2026-05-01T00:00:00Z")

	for _, who := range []struct {
		name    string
		session string
	}{
		{"owner", h.ownerSession},
		{"viewer", h.claimerSession},
	} {
		body := h.get("/lists/"+h.list.ID+"?sort=price-asc", who.session).Body.String()
		if !ascending(order(t, body, "Cheap thing", "Dear thing")) {
			t.Errorf("%s: price-asc did not put the cheap item first", who.name)
		}
		body = h.get("/lists/"+h.list.ID+"?sort=price-desc", who.session).Body.String()
		if !ascending(order(t, body, "Dear thing", "Cheap thing")) {
			t.Errorf("%s: price-desc did not put the expensive item first", who.name)
		}
		// The seeded items carry today's date, so the two dated ones are compared
		// against each other rather than against the fixtures around them.
		body = h.get("/lists/"+h.list.ID+"?sort=added-new", who.session).Body.String()
		if !ascending(order(t, body, "Dear thing", "Cheap thing")) {
			t.Errorf("%s: added-new did not put the newer item first", who.name)
		}
		body = h.get("/lists/"+h.list.ID+"?sort=added-old", who.session).Body.String()
		if !ascending(order(t, body, "Cheap thing", "Dear thing")) {
			t.Errorf("%s: added-old did not put the older item first", who.name)
		}

		// The control says which order is showing, and nonsense falls back to the
		// list's own order rather than failing.
		body = h.get("/lists/"+h.list.ID+"?sort=category", who.session).Body.String()
		if !strings.Contains(body, `value="category" selected`) {
			t.Errorf("%s: the sort control does not show the active order", who.name)
		}
		rec := h.get("/lists/"+h.list.ID+"?sort=nonsense", who.session)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: a nonsense sort answered %d", who.name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `value="manual" selected`) {
			t.Errorf("%s: a nonsense sort did not fall back to list order", who.name)
		}
	}
}

// TestReorderArrowsOnlyInListOrder: the arrows move an item within the stored
// order, which is not the order on screen once a sort is applied.
func TestReorderArrowsOnlyInListOrder(t *testing.T) {
	h := newHarness(t)

	body := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String()
	if !strings.Contains(body, `title="Move up"`) {
		t.Fatal("the reorder arrows are missing from the owner's list order view")
	}
	body = h.get("/lists/"+h.list.ID+"?sort=price-asc", h.ownerSession).Body.String()
	if strings.Contains(body, `title="Move up"`) {
		t.Error("the reorder arrows are offered under a sort they cannot honor")
	}
	if !strings.Contains(body, "Switch back to list order") {
		t.Error("nothing explains where the arrows went")
	}
}

// TestMoveItemToAnotherList walks the whole path through HTTP: the item lands on
// the destination, leaves the source, and the owner comes back to the view they
// were using.
func TestMoveItemToAnotherList(t *testing.T) {
	h := newHarness(t)
	dest := h.mkList(h.owner.ID, "Birthday")

	rec := h.post("/items/"+h.item.ID+"/move-to-list", h.ownerSession, url.Values{
		"list_id": {dest.ID},
		"sort":    {"price-asc"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("move: status %d, want 303", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), "/lists/"+h.list.ID+"?sort=price-asc"; got != want {
		t.Errorf("redirected to %q, want %q — the sort was dropped", got, want)
	}

	if body := h.get("/lists/"+dest.ID, h.ownerSession).Body.String(); !strings.Contains(body, canaryItemTitle) {
		t.Error("the item is not on the destination list")
	}
	if body := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String(); strings.Contains(body, canaryItemTitle) {
		t.Error("the item is still on the list it came from")
	}
}

// TestMoveItemRefusedForOtherPeoplesLists covers both directions of "not yours":
// somebody else's item, and somebody else's list as a destination.
func TestMoveItemRefusedForOtherPeoplesLists(t *testing.T) {
	h := newHarness(t)
	theirs := h.mkList(h.claimer.ID, "Not yours")

	rec := h.post("/items/"+h.item.ID+"/move-to-list", h.ownerSession,
		url.Values{"list_id": {theirs.ID}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("moving onto someone else's list: status %d, want 404", rec.Code)
	}

	// The claimer can see this list, which is not the same as being able to
	// rearrange it.
	own := h.mkList(h.claimer.ID, "Mine")
	rec = h.post("/items/"+h.item.ID+"/move-to-list", h.claimerSession,
		url.Values{"list_id": {own.ID}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("a viewer moving the owner's item: status %d, want 404", rec.Code)
	}

	rec = h.post("/items/"+h.item.ID+"/move-to-list", h.ownerSession, url.Values{})
	if rec.Code != http.StatusNotFound {
		t.Errorf("move with no destination: status %d, want 404", rec.Code)
	}

	it, err := h.st.ItemByID(t.Context(), h.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.ListID != h.list.ID {
		t.Error("one of the refused moves went through")
	}
}

// TestMoveItemCarriesClaimsAndTellsNobody is the owner-blindness side of moving:
// the claim travels with the item, the claimer keeps it, and the owner's request
// looks the same either way.
func TestMoveItemCarriesClaimsAndTellsNobody(t *testing.T) {
	unclaimed := newHarness(t)
	claimed := newHarness(t)
	claimed.addClaims()

	// Compared as "same status, same destination" rather than byte for byte: the
	// two harnesses have their own identifiers, and those are all that may differ.
	var answer [2]string
	for i, h := range []*harness{unclaimed, claimed} {
		dest := h.mkList(h.owner.ID, "Birthday")
		rec := h.post("/items/"+h.item.ID+"/move-to-list", h.ownerSession,
			url.Values{"list_id": {dest.ID}})
		answer[i] = rec.Result().Status + " " +
			strings.Replace(rec.Header().Get("Location"), h.list.ID, "{source}", 1)

		body := h.get("/lists/"+dest.ID, h.ownerSession).Body.String()
		for canary, what := range claimCanaries(h) {
			if strings.Contains(body, canary) {
				t.Errorf("the destination list leaks %s after a move (%q)", what, canary)
			}
		}
	}
	if answer[0] != answer[1] {
		t.Errorf("moving a claimed item answered %q, an unclaimed one %q", answer[1], answer[0])
	}

	// The claimer still holds the claim, and can still find it.
	it, err := claimed.st.ItemByID(t.Context(), claimed.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.ClaimedQty != 2 {
		t.Errorf("claimed_qty = %d after the move, want 2", it.ClaimedQty)
	}
	body := claimed.get("/claims", claimed.claimerSession).Body.String()
	if !strings.Contains(body, canaryItemTitle) || !strings.Contains(body, "Birthday") {
		t.Error("the claimer's page lost the item, or still names the old list")
	}
}

// TestMoveControlAbsentWithNowhereToGo: an owner with one list is offered no
// destination rather than a control that cannot do anything.
func TestMoveControlAbsentWithNowhereToGo(t *testing.T) {
	h := newHarness(t)

	if body := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String(); strings.Contains(body, "move-to-list") {
		t.Error("the move control is offered with only one list to move between")
	}
	h.mkList(h.owner.ID, "Birthday")
	if body := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String(); !strings.Contains(body, "move-to-list") {
		t.Error("the move control is missing once there is a second list")
	}
	// Never for someone reading somebody else's list.
	if body := h.get("/lists/"+h.list.ID, h.claimerSession).Body.String(); strings.Contains(body, "move-to-list") {
		t.Error("a viewer is offered a move control on a list they do not own")
	}
}

// TestMoveToPrivateListSaysSo. The warning is about the destination's
// visibility, which is the owner's own setting — it is not, and must never
// become, conditional on whether anyone had claimed the item.
func TestMoveToPrivateListSaysSo(t *testing.T) {
	h := newHarness(t)
	private := &model.List{OwnerID: h.owner.ID, Name: "Secrets", Visibility: model.VisibilityPrivate}
	if err := h.st.CreateList(t.Context(), private); err != nil {
		t.Fatal(err)
	}

	rec := h.post("/items/"+h.item.ID+"/move-to-list", h.ownerSession,
		url.Values{"list_id": {private.ID}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("move: status %d, want 303", rec.Code)
	}
	flash := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == flashCookie {
			flash, _ = url.QueryUnescape(c.Value)
		}
	}
	if !strings.Contains(flash, "Secrets") || !strings.Contains(flash, "private") {
		t.Errorf("flash after moving to a private list: %q", flash)
	}
}
