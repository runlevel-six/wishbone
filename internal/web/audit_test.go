package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The admin reconciliation report is a deliberate hole in plan §3.2, so these
// tests are about the shape of the hole. The harness makes the list owner an
// admin on purpose — "admin is not exempt" is the rule this feature bends, and
// bending it exactly as far as intended is the whole design.

// ownOn performs the second deliberate action and returns the cookie jar entry
// that carries it, the way a browser would.
func ownOn(t *testing.T, h *harness) *http.Cookie {
	t.Helper()
	rec := h.post("/admin/lists/mine", h.ownerSession, url.Values{"include": {"on"}})
	for _, c := range rec.Result().Cookies() {
		if c.Name == ownReportCookie && c.Value == "on" {
			return c
		}
	}
	t.Fatalf("the toggle did not set %s (status %d)", ownReportCookie, rec.Code)
	return nil
}

// withOwn repeats a GET carrying the toggle cookie.
func withOwn(t *testing.T, h *harness, target string, c *http.Cookie) string {
	t.Helper()
	req := newAuthedRequest(h, http.MethodGet, target, h.ownerSession)
	req.AddCookie(c)
	rec := serve(h, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s with the toggle on = %d, want 200", target, rec.Code)
	}
	return rec.Body.String()
}

// TestReportDefaultsToHidingYourOwnLists is the first of the two actions doing
// its job: arriving at the report is not enough.
func TestReportDefaultsToHidingYourOwnLists(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	// The owner of the seeded list is also the admin.
	person := h.get("/admin/people/"+h.owner.ID, h.ownerSession)
	if person.Code != http.StatusOK {
		t.Fatalf("own person page = %d, want 200 with an explanation", person.Code)
	}
	body := person.Body.String()
	if !strings.Contains(body, "These are your own lists") {
		t.Error("no explanation of why the lists are not shown")
	}
	if strings.Contains(body, h.list.Name) {
		t.Error("the list is named on a page that is supposed to be withholding it")
	}

	// A stale link straight to the report gets the ordinary
	// does-not-exist-or-not-yours answer instead of an explanation.
	if code := h.get("/admin/lists/"+h.list.ID, h.ownerSession).Code; code != http.StatusNotFound {
		t.Errorf("own list report = %d, want 404 so a stale link cannot spoil anything", code)
	}
}

// TestReportShowsOwnListOnceIncluded: the guard is only half the feature. With
// the switch on, the report has to actually answer the question.
func TestReportShowsOwnListOnceIncluded(t *testing.T) {
	h := newHarness(t)
	h.addClaims()
	c := ownOn(t, h)

	person := withOwn(t, h, "/admin/people/"+h.owner.ID, c)
	if !strings.Contains(person, h.list.Name) {
		t.Error("own lists still hidden after switching inclusion on")
	}

	report := withOwn(t, h, "/admin/lists/"+h.list.ID, c)
	if !strings.Contains(report, canaryClaimer) {
		t.Error("the report does not name who holds the claim, which is the question it exists for")
	}
	if !strings.Contains(report, canaryItemTitle) {
		t.Error("the report does not list the item")
	}
	if !strings.Contains(report, "bought") {
		t.Error("the report does not distinguish a purchased claim")
	}
	// Soft-deleted items are included: "my claim disappeared" is usually the
	// owner having removed the item.
	if !strings.Contains(report, "your own list") {
		t.Error("the report does not say this is the admin's own list")
	}
}

// TestReportNeverShowsClaimNoteText: the note is claimer-to-claimer coordination
// and the most personal field in the schema. The report says one exists.
func TestReportNeverShowsClaimNoteText(t *testing.T) {
	h := newHarness(t)
	h.addClaims()
	c := ownOn(t, h)

	report := withOwn(t, h, "/admin/lists/"+h.list.ID, c)
	if strings.Contains(report, canaryNote) {
		t.Error("the claim note text is in the report")
	}
	if !strings.Contains(report, "has a note") {
		t.Error("the report should say a note exists without quoting it")
	}
}

// TestToggleDoesNotFollowThemOut is the leak this feature could most easily
// become: an admin with inclusion on must still be blind on the ordinary list
// page, which is built from OwnerItemView and has no claim fields at all.
func TestToggleDoesNotFollowThemOut(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	before := h.get("/lists/"+h.list.ID, h.ownerSession).Body.String()

	c := ownOn(t, h)
	req := newAuthedRequest(h, http.MethodGet, "/lists/"+h.list.ID, h.ownerSession)
	req.AddCookie(c)
	after := serve(h, req).Body.String()

	if before != after {
		t.Error("the owner's own list page changed once the admin toggle was on")
	}
	// The same canaries the route walk uses, rather than a second opinion about
	// what counts as claim data. Note that a claimer's display *name* is not one
	// of them: the share picker legitimately lists every family member by name,
	// which is why that set is defined once and reused.
	for canary, what := range claimCanaries(h) {
		if strings.Contains(after, canary) {
			t.Errorf("%s (%q) leaked onto the ordinary owner page", what, canary)
		}
	}
}

// TestToggleIsNotAURL: a query parameter would be bookmarked, autocompleted or
// re-entered from history, and then the second deliberate action is free
// forever. Only the POST may turn it on.
func TestToggleIsNotAURL(t *testing.T) {
	h := newHarness(t)

	for _, target := range []string{
		"/admin/lists/" + h.list.ID + "?mine=1",
		"/admin/lists/" + h.list.ID + "?include=on",
		"/admin/lists/" + h.list.ID + "?v=on&mine=on",
	} {
		if code := h.get(target, h.ownerSession).Code; code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: a URL must not switch inclusion on", target, code)
		}
	}
}

// TestReportOnSomeoneElsesListNeedsNoToggle: the ordinary support case is
// somebody else's list, and it works on the first action alone.
func TestReportOnSomeoneElsesListNeedsNoToggle(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	// A list owned by the claimer, viewed by the admin.
	other := h.mkList(h.claimer.ID, "Claimer's list")
	rec := h.get("/admin/lists/"+other.ID, h.ownerSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("someone else's list report = %d, want 200", rec.Code)
	}
	if code := h.get("/admin/people/"+h.claimer.ID, h.ownerSession).Code; code != http.StatusOK {
		t.Error("another person's list index should not need the toggle")
	}
}

// TestReportIsAdminOnly: 404 rather than 403, like every other admin route, and
// the toggle refuses a non-admin too.
func TestReportIsAdminOnly(t *testing.T) {
	h := newHarness(t)

	for _, target := range []string{
		"/admin/people/" + h.owner.ID,
		"/admin/lists/" + h.list.ID,
	} {
		if code := h.get(target, h.claimerSession).Code; code != http.StatusNotFound {
			t.Errorf("GET %s as a non-admin = %d, want 404", target, code)
		}
	}
	rec := h.post("/admin/lists/mine", h.claimerSession, url.Values{"include": {"on"}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("the toggle as a non-admin = %d, want 404", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == ownReportCookie && c.Value == "on" {
			t.Error("a non-admin managed to set the inclusion cookie")
		}
	}
}

// TestGrantingYourselfAdminDoesNotOpenYourOwnList: admin is grantable, which is
// why the default has to be off rather than "admins are trusted".
func TestGrantingYourselfAdminDoesNotOpenYourOwnList(t *testing.T) {
	h := newHarness(t)
	h.addClaims()

	// The claimer owns a list of their own and is made an admin.
	own := h.mkList(h.claimer.ID, "Newly minted admin's list")
	h.post("/admin/users/"+h.claimer.ID+"/admin", h.ownerSession, url.Values{"admin": {"1"}})

	if code := h.get("/admin/lists/"+own.ID, h.claimerSession).Code; code != http.StatusNotFound {
		t.Errorf("a fresh admin's own list = %d, want 404 until they opt in", code)
	}
}
