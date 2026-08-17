package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"wishd/internal/store"
)

// tick waits for the stored-timestamp clock to advance.
//
// Every timestamp in the schema is truncated to the second, and the unread count
// compares strictly greater-than (see store.ClaimUpdateCount for why). A test
// that claims and edits inside one second is therefore testing the tie, not the
// feature, so these tests step over the boundary rather than pretend it is not
// there.
func tick() {
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 50*time.Millisecond)))
}

// ownerEdit applies a minimal valid owner edit. The point of these tests is the
// edited_at stamp, not the fields, but ItemUpdate writes the whole row, so the
// constrained columns still have to be given legal values.
func ownerEdit(t *testing.T, h *harness, itemID, title string) {
	t.Helper()
	err := h.st.UpdateItem(context.Background(), itemID, store.ItemUpdate{
		Title:        title,
		Quantity:     h.item.Quantity,
		Attributes:   "{}",
		FieldSources: "{}",
		LinkStatus:   "unknown",
	})
	if err != nil {
		t.Fatalf("owner edit: %v", err)
	}
}

// badge reads the unread count out of the chrome of an ordinary page. Any page
// will do, which is the point of putting it in the nav.
func badge(t *testing.T, h *harness, session string) string {
	t.Helper()
	body := h.get("/", session).Body.String()
	i := strings.Index(body, `class="badge"`)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	start := strings.Index(rest, ">")
	end := strings.Index(rest, "</span>")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("unreadable badge markup: %q", rest[:min(len(rest), 120)])
	}
	return strings.TrimSpace(rest[start+1 : end])
}

// TestClaimBadgeAfterOwnerEdit is the plan §12 case: the owner changes something
// you claimed, and until now nothing told you unless you happened to re-read the
// card.
func TestClaimBadgeAfterOwnerEdit(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	if got := badge(t, h, h.claimerSession); got != "" {
		t.Fatalf("badge %q before anything changed", got)
	}

	tick()
	ownerEdit(t, h, h.item.ID, "Enamel dutch oven, 6 quart")

	if got := badge(t, h, h.claimerSession); got != "1" {
		t.Errorf("badge = %q after an owner edit, want 1", got)
	}
	// The owner holds no claims, so nothing about their own edit comes back to
	// them through this count.
	if got := badge(t, h, h.ownerSession); got != "" {
		t.Errorf("owner badge = %q, want none", got)
	}
	// A person with no claim on the list learns nothing either.
	if got := badge(t, h, h.strangerSession); got != "" {
		t.Errorf("stranger badge = %q, want none", got)
	}
}

// TestClaimBadgeAfterOwnerRemoval: a removal is the case where being told late
// costs money, since the claimer may already have bought the thing.
func TestClaimBadgeAfterOwnerRemoval(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	tick()
	if err := h.st.DeleteItem(context.Background(), h.item.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}

	if got := badge(t, h, h.claimerSession); got != "1" {
		t.Errorf("badge = %q after the owner removed a claimed item, want 1", got)
	}
}

// TestClaimBadgeClearsOnceSeenAndRowIsMarked covers both halves of "seen":
// opening the page clears the count, and the visit that clears it still says
// which row it was about. A badge pointing at a page with nothing marked on it
// is a nag, not a notification.
func TestClaimBadgeClearsOnceSeenAndRowIsMarked(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	tick()
	ownerEdit(t, h, h.item.ID, canaryItemTitle)
	if got := badge(t, h, h.claimerSession); got != "1" {
		t.Fatalf("badge = %q before the claims page was opened, want 1", got)
	}

	first := h.get("/claims", h.claimerSession).Body.String()
	if !strings.Contains(first, "changed since you last looked") {
		t.Error("the visit that cleared the badge did not mark the changed row")
	}
	if got := badge(t, h, h.claimerSession); got != "" {
		t.Errorf("badge = %q after opening the claims page, want none", got)
	}

	second := h.get("/claims", h.claimerSession).Body.String()
	if strings.Contains(second, "changed since you last looked") {
		t.Error("the row is still marked on a second visit; the watermark did not move")
	}
	// The edit itself stays visible, the way it does on the list card.
	if !strings.Contains(second, "edited by owner") {
		t.Error("the edit marker vanished along with the unread state")
	}
}

// TestClaimBadgeIgnoresEditsBeforeTheClaim: news is what happened since you got
// involved. An item edited last year is not an update to a claim made today,
// and an instance upgrading to this column must not hand everybody a badge for
// history.
func TestClaimBadgeIgnoresEditsBeforeTheClaim(t *testing.T) {
	h := newHarness(t)

	ownerEdit(t, h, h.item.ID, canaryItemTitle)

	tick()
	h.addClaims()

	if got := badge(t, h, h.claimerSession); got != "" {
		t.Errorf("badge = %q for an edit that predates the claim, want none", got)
	}
}

// TestClaimBadgeCountsItemsNotEvents: two edits to one item are one thing to go
// and look at.
func TestClaimBadgeCountsItemsNotEvents(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	tick()
	for _, title := range []string{"First rename", "Second rename"} {
		ownerEdit(t, h, h.item.ID, title)
	}

	if got := badge(t, h, h.claimerSession); got != "1" {
		t.Errorf("badge = %q after two edits to one item, want 1", got)
	}
}
