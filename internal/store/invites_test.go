package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"wishbone/internal/model"
	"wishbone/internal/store"
)

func mustInvite(t *testing.T, st *store.Store, createdBy string) *model.Invite {
	t.Helper()
	now := model.Now()
	inv := &model.Invite{
		TokenHash: "hash-of-an-invite-token",
		CreatedBy: createdBy,
		CreatedAt: model.TimeString(now),
		ExpiresAt: model.TimeString(now.Add(time.Hour)),
	}
	if err := st.CreateInvite(context.Background(), inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	return inv
}

// TestRedeemInviteWritesAWholeUser is the store-level half of the registration
// regression. RedeemInvite duplicated the users INSERT, so a new user column
// (theme, migration 0003) landed in userCols and not in the copy, and every
// redemption failed on "8 values for 9 columns" while CreateUser was fine.
//
// The assertion that matters is the round trip: reading the account back
// through the normal path proves every column the app needs was written, not
// just that the statement ran.
func TestRedeemInviteWritesAWholeUser(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	admin := mustUser(t, st, "admin")
	inv := mustInvite(t, st, admin.ID)
	now := model.TimeString(model.Now())

	u := &model.User{Username: "invited", DisplayName: "Invited Person", PasswordHash: "hash"}
	if err := st.RedeemInvite(ctx, inv.TokenHash, u, now); err != nil {
		t.Fatalf("redeem invite: %v", err)
	}
	if u.ID == "" {
		t.Fatal("RedeemInvite left the user without an id, so no session can be started")
	}

	got, err := st.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("the redeemed account is not readable: %v", err)
	}
	if got.Username != "invited" || got.DisplayName != "Invited Person" {
		t.Errorf("stored account is %+v", got)
	}
	if got.Theme != model.ThemeForest {
		t.Errorf("theme is %q, want the default", got.Theme)
	}
	if got.CreatedAt != now {
		t.Errorf("created_at is %q, want %q", got.CreatedAt, now)
	}

	// The invite is burned, and burned to this account.
	if _, err := st.UsableInvite(ctx, inv.TokenHash, now); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("invite is still usable: %v", err)
	}
	list, err := st.ListInvites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d invites, want 1", len(list))
	}
	if list[0].UsedBy == nil || *list[0].UsedBy != u.ID {
		t.Error("the invite does not record who used it")
	}
}

// TestRedeemInviteRollsBackOnAConflict covers the transaction: a username
// collision must leave neither a half-made account nor a spent invite.
func TestRedeemInviteRollsBackOnAConflict(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	admin := mustUser(t, st, "admin")
	mustUser(t, st, "taken")
	inv := mustInvite(t, st, admin.ID)
	now := model.TimeString(model.Now())

	err := st.RedeemInvite(ctx, inv.TokenHash,
		&model.User{Username: "taken", DisplayName: "Someone Else", PasswordHash: "hash"}, now)
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("redeeming with a taken username returned %v, want ErrConflict", err)
	}

	if _, err := st.UsableInvite(ctx, inv.TokenHash, now); err != nil {
		t.Errorf("the invite was spent by a failed redemption: %v", err)
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Errorf("got %d users, want the 2 that existed before the failed redemption", len(users))
	}
}

// TestRedeemInviteCannotBeUsedTwice is the race the transaction exists for: the
// second redemption of one token must fail and leave nothing behind.
func TestRedeemInviteCannotBeUsedTwice(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	admin := mustUser(t, st, "admin")
	inv := mustInvite(t, st, admin.ID)
	now := model.TimeString(model.Now())

	first := &model.User{Username: "first", DisplayName: "First", PasswordHash: "hash"}
	if err := st.RedeemInvite(ctx, inv.TokenHash, first, now); err != nil {
		t.Fatalf("first redemption: %v", err)
	}

	second := &model.User{Username: "second", DisplayName: "Second", PasswordHash: "hash"}
	if err := st.RedeemInvite(ctx, inv.TokenHash, second, now); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("second redemption returned %v, want ErrNotFound", err)
	}
	if _, err := st.UserByUsername(ctx, "second"); !errors.Is(err, model.ErrNotFound) {
		t.Error("the losing registration left a user row behind")
	}
}
