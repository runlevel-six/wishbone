package web

import (
	"strings"
	"testing"
)

// The claimed-vs-total bar is the widget plan §3.2 names in its leak-vector
// table: "any 'N remaining' / 'N of M claimed' counter". It ships for buyers
// only, so it needs the pair of tests every vector in that table needs — one
// proving the owner never sees it, one proving a buyer does, because a bar that
// rendered nowhere would satisfy the first test on its own.
//
// The owner half is largely covered already: claimCanaries carries "claim-bar",
// so TestOwnerBlindnessAcrossAllRoutes checks every owner-rendered route for it,
// and TestOwnerResponsesUnchangedByClaims compares the owner's dashboard byte
// for byte across a claim landing. What those two cannot show is that the thing
// they are guarding against is a thing that exists.

func TestClaimBarIsShownToABuyer(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	list := h.get("/lists/"+h.list.ID, h.claimerSession).Body.String()
	if !strings.Contains(list, "claim-bar") {
		t.Error("a buyer's list page has no claim bar; the owner-blindness canary for it is vacuous")
	}
	if !strings.Contains(list, "still available") {
		t.Error("a buyer's list page has no claimed-vs-total count")
	}

	// The dashboard card for somebody else's list carries the same summary, which
	// is the whole point of putting it there: it answers "is there anything left
	// to buy here" without opening the list.
	dash := h.get("/", h.claimerSession).Body.String()
	if !strings.Contains(dash, "claim-bar") {
		t.Error("a buyer's dashboard has no claim bar on the list shared with them")
	}
}

// The owner's own dashboard card must carry no bar even though the same page
// draws one for everybody else's list. This is the case the type split exists
// for: ListSummary has no claim fields, VisibleListSummary does, and the owner's
// loop is handed the former.
func TestClaimBarIsAbsentFromTheOwnersOwnCard(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	body := h.get("/", h.ownerSession).Body.String()
	if strings.Contains(body, "claim-bar") {
		t.Error("the owner's dashboard drew a claim bar; their own list's claim state is visible")
	}
	if strings.Contains(body, "still available") {
		t.Error("the owner's dashboard carries a claimed-vs-total count")
	}

	// And the list page itself, which is the same question one level down.
	list := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String()
	if strings.Contains(list, "claim-bar") {
		t.Error("the owner's own list page drew a claim bar")
	}
}
