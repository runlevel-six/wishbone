package store_test

import (
	"context"
	"testing"
	"time"

	"wishbone/internal/model"
)

// TestThemeDefaultsAndRoundTrips: a new account is on the brand green, and a
// choice made once comes back on every later read.
func TestThemeDefaultsAndRoundTrips(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	u := mustUser(t, st, "sam")

	if u.Theme != model.ThemeForest {
		t.Errorf("CreateUser left the theme as %q", u.Theme)
	}
	got, err := st.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != model.ThemeForest {
		t.Errorf("a new account reads back as %q, want the default", got.Theme)
	}

	if err := st.SetTheme(ctx, u.ID, model.ThemeNavy); err != nil {
		t.Fatalf("set theme: %v", err)
	}
	got, err = st.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != model.ThemeNavy {
		t.Errorf("theme reads back as %q, want navy", got.Theme)
	}
	// And through the other way a user is loaded.
	if users, err := st.ListUsers(ctx); err != nil {
		t.Fatal(err)
	} else if users[0].Theme != model.ThemeNavy {
		t.Errorf("ListUsers reports %q", users[0].Theme)
	}
}

// TestThemeIsClampedOnWrite: nothing but a known palette reaches the column, so
// a row can never point at a palette the stylesheet has no block for.
func TestThemeIsClampedOnWrite(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	u := mustUser(t, st, "sam")

	if err := st.SetTheme(ctx, u.ID, model.Theme("tangerine")); err != nil {
		t.Fatalf("set theme: %v", err)
	}
	var stored string
	if err := st.DB().QueryRowContext(ctx, `SELECT theme FROM users WHERE id = ?`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != string(model.ThemeForest) {
		t.Errorf("stored theme is %q; an unknown palette was written through", stored)
	}
}

// TestSessionUserCarriesTheTheme is a regression test with a specific cause.
//
// SessionUser writes its own column list rather than reusing userCols, because
// of the join, and it is the user object every authenticated request is built
// from. When the theme column was added it was added to userCols and missed
// here, so the preference was stored correctly, read correctly by anything that
// looked a user up by id — and absent from every actual page.
func TestSessionUserCarriesTheTheme(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	u := mustUser(t, st, "sam")
	if err := st.SetTheme(ctx, u.ID, model.ThemeCranberry); err != nil {
		t.Fatal(err)
	}

	now := model.Now()
	sess := &model.Session{
		TokenHash: "hash-of-a-token",
		UserID:    u.ID,
		CreatedAt: model.TimeString(now),
		ExpiresAt: model.TimeString(now.Add(time.Hour)),
	}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, _, err := st.SessionUser(ctx, sess.TokenHash, model.TimeString(now))
	if err != nil {
		t.Fatalf("session user: %v", err)
	}
	if got.Theme != model.ThemeCranberry {
		t.Errorf("the user behind a session has theme %q, want cranberry", got.Theme)
	}
}
