package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// profileForm is the account form as the browser sends it: both names every
// time, because the two fields live in one form.
func profileForm(display, username string) url.Values {
	return url.Values{"display_name": {display}, "username": {username}}
}

func TestRenameChangesTheSignInName(t *testing.T) {
	h := newHarness(t)

	rec := h.post("/account/profile", h.ownerSession, profileForm(h.owner.DisplayName, "jason"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("rename: status %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}

	got, err := h.st.UserByID(t.Context(), h.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "jason" {
		t.Errorf("username = %q, want %q", got.Username, "jason")
	}

	// The old name is gone rather than aliased, so nothing can sign in with it.
	if _, err := h.st.UserByUsername(t.Context(), h.owner.Username); err == nil {
		t.Errorf("the previous name %q still resolves to an account", h.owner.Username)
	}
	if found, err := h.st.UserByUsername(t.Context(), "jason"); err != nil {
		t.Errorf("the new name does not resolve: %v", err)
	} else if found.ID != h.owner.ID {
		t.Error("the new name resolves to somebody else")
	}
}

// A rename must not sign anybody out. Sessions key on the user's ID, and this is
// the test that says so out loud — it is the first thing that would break if a
// session ever keyed on the name instead.
func TestRenameKeepsYouSignedIn(t *testing.T) {
	h := newHarness(t)

	if rec := h.post("/account/profile", h.ownerSession, profileForm("Jason", "jason")); rec.Code != http.StatusSeeOther {
		t.Fatalf("rename: status %d", rec.Code)
	}

	if rec := h.get("/", h.ownerSession); rec.Code != http.StatusOK {
		t.Fatalf("dashboard after rename: status %d, want 200 — the session did not survive", rec.Code)
	}

	// The account page is where both names are rendered back, so it is the one
	// that shows the session still resolves to this person and to the new names.
	rec := h.get("/account", h.ownerSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("account page after rename: status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`value="Jason"`, `value="jason"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the account form is missing %s after the rename", want)
		}
	}
}

func TestRenameRefusesANameSomebodyElseHas(t *testing.T) {
	h := newHarness(t)

	rec := h.post("/account/profile", h.ownerSession,
		profileForm("Renamed Owner", h.claimer.Username))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("taken name: status %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already signs in with that name") {
		t.Error("the page does not say the name is taken")
	}

	// Nothing at all was saved. The sign-in name is written before the display
	// name for exactly this reason: a refused rename must not leave half of the
	// form applied.
	got, err := h.st.UserByID(t.Context(), h.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != h.owner.Username {
		t.Errorf("username = %q, want it unchanged at %q", got.Username, h.owner.Username)
	}
	if got.DisplayName != h.owner.DisplayName {
		t.Errorf("display name = %q, want it unchanged at %q; the rename was refused "+
			"but the other half of the form was saved anyway", got.DisplayName, h.owner.DisplayName)
	}
}

// The name is unique without regard to case, so taking the same name in
// different capitals is the same collision.
func TestRenameRefusesADifferentlyCasedDuplicate(t *testing.T) {
	h := newHarness(t)

	rec := h.post("/account/profile", h.ownerSession,
		profileForm(h.owner.DisplayName, strings.ToUpper(h.claimer.Username)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("taken name in other case: status %d, want 400", rec.Code)
	}
}

// Recasing your own name is not a collision with yourself. The handler compares
// exactly rather than case-insensitively so that this is reachable at all.
func TestRenameCanChangeOnlyTheCapitalization(t *testing.T) {
	h := newHarness(t)

	want := strings.ToUpper(h.owner.Username)
	if rec := h.post("/account/profile", h.ownerSession,
		profileForm(h.owner.DisplayName, want)); rec.Code != http.StatusSeeOther {
		t.Fatalf("recasing own name: status %d, want 303", rec.Code)
	}
	got, err := h.st.UserByID(t.Context(), h.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != want {
		t.Errorf("username = %q, want %q", got.Username, want)
	}
}

// The sign-up form and the rename form must refuse the same names, or a name the
// one rejects becomes reachable through the other.
func TestRenameAppliesTheSignUpRules(t *testing.T) {
	h := newHarness(t)

	for _, bad := range []struct{ name, why string }{
		{"a", "too short"},
		{strings.Repeat("x", 41), "too long"},
		{"two words", "contains a space"},
		{"has/slash", "contains a slash"},
		{"why?", "contains a question mark"},
		{"", "empty"},
	} {
		t.Run(bad.why, func(t *testing.T) {
			rec := h.post("/account/profile", h.ownerSession,
				profileForm(h.owner.DisplayName, bad.name))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("username %q (%s): status %d, want 400", bad.name, bad.why, rec.Code)
			}
			got, err := h.st.UserByID(t.Context(), h.owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Username != h.owner.Username {
				t.Errorf("username became %q after a refused rename", got.Username)
			}
		})
	}
}

// The display name still saves on its own, which is what this form did before it
// grew a second field.
func TestProfileStillSavesTheDisplayName(t *testing.T) {
	h := newHarness(t)

	if rec := h.post("/account/profile", h.ownerSession,
		profileForm("New Display Name", h.owner.Username)); rec.Code != http.StatusSeeOther {
		t.Fatalf("save display name: status %d", rec.Code)
	}
	got, err := h.st.UserByID(t.Context(), h.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "New Display Name" {
		t.Errorf("display name = %q, want %q", got.DisplayName, "New Display Name")
	}
}
