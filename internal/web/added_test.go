package web

import (
	"strings"
	"testing"
)

// TestAddedDateShownToBothAudiences: plan §14 puts the date on the card for
// everyone who can see the list, the owner included. Two view types render these
// cards — OwnerItemView and ViewerItemView — and a field added to only one of
// them is the ordinary way this half-ships.
func TestAddedDateShownToBothAudiences(t *testing.T) {
	h := newHarness(t)

	for _, who := range []struct {
		name    string
		session string
	}{
		{"owner", h.ownerSession},
		{"claimer", h.claimerSession},
	} {
		t.Run(who.name, func(t *testing.T) {
			body := h.get("/lists/"+h.list.ID, who.session).Body.String()
			if !strings.Contains(body, "Added today") {
				t.Errorf("no added date on the %s's view of the list", who.name)
			}
			// The relative label is the readable half; the exact moment is
			// behind it for anyone who wants to be sure.
			if !strings.Contains(body, " at ") {
				t.Errorf("no exact timestamp title on the %s's view", who.name)
			}
		})
	}
}

// TestAddedDateIndependentOfClaims is the §3.2 question asked of a new field.
// created_at is owner-authored and does not move when anything is claimed, so
// the owner's page must be identical before and after a claim — the same
// property OwnerResponsesUnchangedByClaims asserts for the page as a whole,
// pinned here against the field that was just added to it.
func TestAddedDateIndependentOfClaims(t *testing.T) {
	h := newHarness(t)

	before := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String()
	h.addClaims()
	after := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String()

	if !strings.Contains(before, "Added today") {
		t.Fatal("no added date before claims; the rest of this test proves nothing")
	}
	if before != after {
		t.Error("the owner's list changed once an item was claimed")
	}
}
