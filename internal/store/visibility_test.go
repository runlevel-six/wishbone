package store_test

import (
	"context"
	"errors"
	"testing"

	"wishd/internal/model"
)

// TestVisibilityMatrix is the matrix plan §8 asks for:
// owner / shared / unshared / admin × private / all_users / selected.
//
// Admin appears in the matrix precisely to pin down that admin is not a
// bypass (plan §3.2).
func TestVisibilityMatrix(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	shared := mustUser(t, st, "shared")
	unshared := mustUser(t, st, "unshared")

	admin := &model.User{Username: "admin", DisplayName: "admin", PasswordHash: "x", IsAdmin: true}
	if err := st.CreateUser(ctx, admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	lists := map[string]*model.List{}
	for _, vis := range []string{model.VisibilityPrivate, model.VisibilityAllUsers, model.VisibilitySelected} {
		l := mustList(t, st, owner, vis)
		lists[vis] = l
	}
	if err := st.ReplaceShares(ctx, lists[model.VisibilitySelected].ID, []string{shared.ID}); err != nil {
		t.Fatalf("share: %v", err)
	}

	viewers := map[string]*model.User{
		"owner": owner, "shared": shared, "unshared": unshared, "admin": admin,
	}
	want := map[string]map[string]bool{
		model.VisibilityPrivate:  {"owner": true, "shared": false, "unshared": false, "admin": false},
		model.VisibilityAllUsers: {"owner": true, "shared": true, "unshared": true, "admin": true},
		model.VisibilitySelected: {"owner": true, "shared": true, "unshared": false, "admin": false},
	}

	for vis, expectations := range want {
		for who, expected := range expectations {
			viewer := viewers[who]
			ok, err := st.CanView(ctx, lists[vis], viewer.ID)
			if err != nil {
				t.Fatalf("CanView(%s, %s): %v", vis, who, err)
			}
			if ok != expected {
				t.Errorf("CanView(%s, %s) = %v, want %v", vis, who, ok, expected)
			}

			// VisibleList must agree, and must report ErrNotFound rather than a
			// distinguishable "forbidden".
			_, err = st.VisibleList(ctx, lists[vis].ID, viewer.ID)
			switch {
			case expected && err != nil:
				t.Errorf("VisibleList(%s, %s): unexpected error %v", vis, who, err)
			case !expected && !errors.Is(err, model.ErrNotFound):
				t.Errorf("VisibleList(%s, %s) = %v, want ErrNotFound", vis, who, err)
			}
		}
	}
}

// TestImageAuthorizationFollowsList covers plan §6: images are only readable
// by someone who can see an item that references them.
func TestImageAuthorizationFollowsList(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	owner := mustUser(t, st, "owner")
	shared := mustUser(t, st, "shared")
	stranger := mustUser(t, st, "stranger")

	list := mustList(t, st, owner, model.VisibilitySelected)
	if err := st.ReplaceShares(ctx, list.ID, []string{shared.ID}); err != nil {
		t.Fatalf("share: %v", err)
	}
	item := mustItem(t, st, list, 1)

	const sha = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	if err := st.AddItemImage(ctx, &model.ItemImage{
		ItemID: item.ID, SHA256: sha, Mime: "image/jpeg", IsPrimary: true,
	}); err != nil {
		t.Fatalf("add image: %v", err)
	}

	for _, tc := range []struct {
		who   string
		user  *model.User
		allow bool
	}{
		{"owner", owner, true},
		{"shared", shared, true},
		{"stranger", stranger, false},
	} {
		_, err := st.ImageAccessible(ctx, sha, tc.user.ID)
		if tc.allow && err != nil {
			t.Errorf("%s should be able to load the image: %v", tc.who, err)
		}
		if !tc.allow && err == nil {
			t.Errorf("%s should not be able to load the image", tc.who)
		}
	}
}
