package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"wishbone/internal/auth"
	"wishbone/internal/model"
)

// mkInvite creates a usable invite the way the admin page does and returns the
// clear token that goes in the link.
func (h *harness) mkInvite() string {
	h.t.Helper()
	token := auth.NewToken()
	now := model.Now()
	inv := &model.Invite{
		TokenHash: auth.HashToken(token),
		CreatedBy: h.owner.ID,
		CreatedAt: model.TimeString(now),
		ExpiresAt: model.TimeString(now.Add(h.cfg.InviteTTL)),
	}
	if err := h.st.CreateInvite(context.Background(), inv); err != nil {
		h.t.Fatalf("create invite: %v", err)
	}
	return token
}

// postAnonymous submits a form with no session, carrying the anonymous CSRF
// cookie the GET handed out. The register and sign-in forms are the only posts
// that work this way, so the shared harness helpers cannot express them.
func (h *harness) postAnonymous(target string, csrfKey string, form url.Values) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrfKey})
	req.Header.Set(csrfHeader, auth.CSRFToken(h.cfg.SecretKey, csrfKey))
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

// openInviteLink follows the register link and returns the CSRF key its
// response set, so the follow-up POST is the same exchange a browser makes.
func (h *harness) openInviteLink(token string) string {
	h.t.Helper()
	rec := h.get("/register/"+token, "")
	if rec.Code != http.StatusOK {
		h.t.Fatalf("GET the invite link: status %d, want 200", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookie {
			return c.Value
		}
	}
	h.t.Fatal("the register form set no csrf cookie, so no anonymous post can succeed")
	return ""
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestRegisteringThroughAnInviteCreatesTheAccount is an end-to-end regression
// test for a bug that reached the family: every invite registration answered
// 500 while everything else worked.
//
// RedeemInvite kept its own copy of the users INSERT, and the theme column was
// added to userCols without it, so the statement bound eight values to nine
// columns. Nothing exercised the invite path, so the only symptom was the
// generic error page on save. The store now inserts users in one place; this
// test is what notices if that stops being true.
func TestRegisteringThroughAnInviteCreatesTheAccount(t *testing.T) {
	h := newHarness(t)
	token := h.mkInvite()
	csrfKey := h.openInviteLink(token)

	rec := h.postAnonymous("/register/"+token, csrfKey, url.Values{
		"username":         {"newcomer"},
		"display_name":     {"A New Person"},
		"password":         {"a-long-enough-password"},
		"password_confirm": {"a-long-enough-password"},
	})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("saving the registration answered %d, want 303; body: %s",
			rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("redirected to %q, want the dashboard", got)
	}
	if c := cookieNamed(rec, sessionCookie); c == nil || c.Value == "" {
		t.Error("registration set no session cookie; the new account is not signed in")
	}

	ctx := context.Background()
	u, err := h.st.UserByUsername(ctx, "newcomer")
	if err != nil {
		t.Fatalf("the account is not readable after registering: %v", err)
	}
	if u.DisplayName != "A New Person" {
		t.Errorf("display name is %q", u.DisplayName)
	}
	if u.Theme != model.ThemeForest {
		t.Errorf("theme is %q, want the default; a registered account must render", u.Theme)
	}
	if u.IsAdmin {
		t.Error("an invited account came out as an admin")
	}

	// The invite is spent, so the link cannot make a second account.
	if _, err := h.st.UsableInvite(ctx, auth.HashToken(token), model.TimeString(model.Now())); err == nil {
		t.Error("the invite is still usable after being redeemed")
	}
	again := h.postAnonymous("/register/"+token, csrfKey, url.Values{
		"username":         {"second"},
		"display_name":     {"Second Try"},
		"password":         {"a-long-enough-password"},
		"password_confirm": {"a-long-enough-password"},
	})
	if again.Code != http.StatusNotFound {
		t.Errorf("reusing a spent invite answered %d, want 404", again.Code)
	}
}

// TestRegisteringWithATakenUsernameIsToldSo keeps the conflict path distinct
// from the failure above: both used to look like the same generic error, and
// only one of them is the person's fault.
func TestRegisteringWithATakenUsernameIsToldSo(t *testing.T) {
	h := newHarness(t)
	token := h.mkInvite()
	csrfKey := h.openInviteLink(token)

	rec := h.postAnonymous("/register/"+token, csrfKey, url.Values{
		"username":         {"owner"}, // seeded by the harness
		"display_name":     {"Impostor"},
		"password":         {"a-long-enough-password"},
		"password_confirm": {"a-long-enough-password"},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a taken username answered %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "taken") {
		t.Error("the page does not say the username is taken")
	}
	// The invite survives a rejected attempt, or one typo would burn the link.
	if _, err := h.st.UsableInvite(context.Background(), auth.HashToken(token),
		model.TimeString(model.Now())); err != nil {
		t.Errorf("the invite was spent by a failed attempt: %v", err)
	}
}
