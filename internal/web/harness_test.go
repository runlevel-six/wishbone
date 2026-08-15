package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wishd/internal/auth"
	"wishd/internal/config"
	"wishd/internal/db"
	"wishd/internal/extract"
	"wishd/internal/fetch"
	"wishd/internal/imgstore"
	"wishd/internal/model"
	"wishd/internal/store"
)

// The strings below are canaries. None of them may ever appear in a response
// rendered for the owner of the list they belong to.
const (
	canaryNote      = "CANARY-NOTE-QUUX"
	canaryClaimer   = "Zaphod Beeblebrox"
	canaryItemTitle = "Enamel dutch oven"
)

type harness struct {
	t   *testing.T
	srv *Server
	st  *store.Store
	cfg *config.Config

	owner, claimer, stranger *model.User
	ownerSession             string
	claimerSession           string
	strangerSession          string

	list  *model.List
	item  *model.Item
	item2 *model.Item
	claim *model.Claim
	sha   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	sqldb, err := db.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqldb.Close() })

	cfg := &config.Config{
		Addr:          "127.0.0.1:0",
		DataDir:       dir,
		DBPath:        filepath.Join(dir, "test.db"),
		ImageDir:      filepath.Join(dir, "images"),
		SecretKey:     []byte("0123456789abcdef0123456789abcdef"),
		SessionTTL:    24 * time.Hour,
		InviteTTL:     time.Hour,
		SecureCookies: false,
		// Fetching is off in tests: no test may touch the network (plan §8).
		FetchEnabled: false,
	}

	st := store.New(sqldb)
	client := fetch.New(fetch.Options{})
	images, err := imgstore.New(cfg.ImageDir, client)
	if err != nil {
		t.Fatalf("imgstore: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, st, extract.NewService(client, nil, false), images, log)

	h := &harness{t: t, srv: srv, st: st, cfg: cfg}
	h.seed()
	return h
}

func (h *harness) seed() {
	ctx := context.Background()
	t := h.t

	h.owner = h.mkUser("owner", "List Owner", true) // owner is also admin: admin is not exempt
	h.claimer = h.mkUser("claimer", canaryClaimer, false)
	h.stranger = h.mkUser("stranger", "Nobody", false)

	h.ownerSession = h.mkSession(h.owner)
	h.claimerSession = h.mkSession(h.claimer)
	h.strangerSession = h.mkSession(h.stranger)

	h.list = &model.List{OwnerID: h.owner.ID, Name: "Christmas", Visibility: model.VisibilityAllUsers}
	if err := h.st.CreateList(ctx, h.list); err != nil {
		t.Fatalf("create list: %v", err)
	}

	h.item = &model.Item{ListID: h.list.ID, Title: canaryItemTitle, Quantity: 3}
	if err := h.st.CreateItem(ctx, h.item); err != nil {
		t.Fatalf("create item: %v", err)
	}
	h.item2 = &model.Item{ListID: h.list.ID, Title: "Second thing", Quantity: 1}
	if err := h.st.CreateItem(ctx, h.item2); err != nil {
		t.Fatalf("create item 2: %v", err)
	}

	h.sha = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	if err := h.st.AddItemImage(ctx, &model.ItemImage{
		ItemID: h.item.ID, SHA256: h.sha, Mime: "image/jpeg", IsPrimary: true,
	}); err != nil {
		t.Fatalf("add image: %v", err)
	}
}

// addClaims puts claim state in the database. Kept separate from seed() so a
// test can compare owner-facing responses before and after.
func (h *harness) addClaims() {
	ctx := context.Background()
	c, err := h.st.CreateClaim(ctx, h.item.ID, h.claimer.ID, 2, model.Ptr(canaryNote))
	if err != nil {
		h.t.Fatalf("create claim: %v", err)
	}
	h.claim = c
	if _, err := h.st.CreateClaim(ctx, h.item2.ID, h.claimer.ID, 1, nil); err != nil {
		h.t.Fatalf("create claim 2: %v", err)
	}
	if _, err := h.st.SetClaimState(ctx, c.ID, h.claimer.ID, model.ClaimStatePurchased); err != nil {
		h.t.Fatalf("mark purchased: %v", err)
	}
}

func (h *harness) mkUser(username, display string, admin bool) *model.User {
	h.t.Helper()
	hash, err := auth.HashPassword("correcthorsebattery")
	if err != nil {
		h.t.Fatalf("hash: %v", err)
	}
	u := &model.User{Username: username, DisplayName: display, PasswordHash: hash, IsAdmin: admin}
	if err := h.st.CreateUser(context.Background(), u); err != nil {
		h.t.Fatalf("create user: %v", err)
	}
	return u
}

func (h *harness) mkSession(u *model.User) string {
	h.t.Helper()
	token := auth.NewToken()
	now := model.Now()
	err := h.st.CreateSession(context.Background(), &model.Session{
		TokenHash: auth.HashToken(token),
		UserID:    u.ID,
		CreatedAt: model.TimeString(now),
		ExpiresAt: model.TimeString(now.Add(24 * time.Hour)),
	})
	if err != nil {
		h.t.Fatalf("create session: %v", err)
	}
	return token
}

// request issues a request as the holder of the given session token.
func (h *harness) request(method, target, session string, form url.Values) *httptest.ResponseRecorder {
	h.t.Helper()

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		req.Header.Set(csrfHeader, auth.CSRFToken(h.cfg.SecretKey, auth.HashToken(session)))
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

func (h *harness) get(target, session string) *httptest.ResponseRecorder {
	return h.request(http.MethodGet, target, session, nil)
}

func (h *harness) post(target, session string, form url.Values) *httptest.ResponseRecorder {
	if form == nil {
		form = url.Values{}
	}
	return h.request(http.MethodPost, target, session, form)
}
