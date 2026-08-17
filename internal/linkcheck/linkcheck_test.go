package linkcheck

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"wishbone/internal/db"
	"wishbone/internal/extract"
	"wishbone/internal/fetch"
	"wishbone/internal/model"
	"wishbone/internal/store"
)

// A live product page and a dead one, the pair the soft-404 guard was built
// against: the dead link answers 200 with metadata describing the collection it
// redirected to, which is why a status-code check is not enough.
const (
	livePage = `<!doctype html><html><head>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Product","name":"Enamel dutch oven",
 "sku":"DO-6QT","offers":{"@type":"Offer","price":"89.00","priceCurrency":"USD"}}
</script></head><body></body></html>`

	collectionPage = `<!doctype html><html><head>
<meta property="og:type" content="website"/>
<meta property="og:title" content="Best sellers"/>
</head><body></body></html>`
)

type fixture struct {
	t   *testing.T
	st  *store.Store
	c   *Checker
	srv *httptest.Server

	slept []time.Duration
	list  *model.List
}

// newFixture wires a real store and a real extraction service against a test
// server, because the point of this job is what the pipeline concludes — a fake
// extractor would test nothing.
func newFixture(t *testing.T, handler http.HandlerFunc) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	sqldb, err := db.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqldb.Close() })
	st := store.New(sqldb)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := fetch.New(fetch.Options{AllowLoopback: true})
	ex := extract.NewService(client, nil, true)

	f := &fixture{t: t, st: st, srv: srv}
	f.c = New(st, ex, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Interval: time.Hour,
		Batch:    10,
		Age:      7 * 24 * time.Hour,
		Spacing:  30 * time.Second,
	})
	f.c.sleep = func(d time.Duration) { f.slept = append(f.slept, d) }

	u := &model.User{Username: "owner", DisplayName: "Owner", PasswordHash: "x"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	f.list = &model.List{OwnerID: u.ID, Name: "Christmas", Visibility: model.VisibilityAllUsers}
	if err := st.CreateList(ctx, f.list); err != nil {
		t.Fatalf("create list: %v", err)
	}
	return f
}

// addItem stores an item whose link has never been checked.
func (f *fixture) addItem(path, status string) *model.Item {
	f.t.Helper()
	url := f.srv.URL + path
	it := &model.Item{
		ListID:     f.list.ID,
		Title:      "Item " + path,
		URL:        model.Ptr(url),
		Quantity:   1,
		LinkStatus: status,
	}
	if err := f.st.CreateItem(context.Background(), it); err != nil {
		f.t.Fatalf("create item: %v", err)
	}
	return it
}

func (f *fixture) reload(id string) *model.Item {
	f.t.Helper()
	it, err := f.st.ItemByID(context.Background(), id)
	if err != nil {
		f.t.Fatalf("reload item: %v", err)
	}
	return it
}

// TestGoneLinksAreMarkedDead: 404 and 410 are the shop saying the thing is not
// there, which is the strongest evidence a link can carry.
func TestGoneLinksAreMarkedDead(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			w.WriteHeader(http.StatusNotFound)
		case "/retired":
			w.WriteHeader(http.StatusGone)
		}
	})
	gone := f.addItem("/gone", model.LinkOK)
	retired := f.addItem("/retired", model.LinkOK)

	res := f.c.Sweep(context.Background())
	if res.Dead != 2 {
		t.Errorf("dead = %d, want 2 (%+v)", res.Dead, res)
	}
	for _, it := range []*model.Item{gone, retired} {
		got := f.reload(it.ID)
		if got.LinkStatus != model.LinkDead {
			t.Errorf("%s: link_status = %q, want dead", model.Deref(it.URL), got.LinkStatus)
		}
		if got.LinkCheckedAt == nil {
			t.Errorf("%s: link_checked_at not recorded", model.Deref(it.URL))
		}
	}
}

// TestRefusalLeavesTheStatusAlone is the distinction this job would be most
// damaging without: 403 and 429 are the shop declining to talk to *us*, and
// writing that into link_status tells people their good links are broken.
func TestRefusalLeavesTheStatusAlone(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/refused":
			w.WriteHeader(http.StatusForbidden)
		case "/ratelimited":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	items := []*model.Item{
		f.addItem("/refused", model.LinkOK),
		f.addItem("/ratelimited", model.LinkOK),
		f.addItem("/broken", model.LinkOK),
	}

	res := f.c.Sweep(context.Background())
	if res.Dead != 0 {
		t.Errorf("dead = %d, want 0: a refusal is not evidence about the link", res.Dead)
	}
	if res.Inconclusive != 3 {
		t.Errorf("inconclusive = %d, want 3 (%+v)", res.Inconclusive, res)
	}
	for _, it := range items {
		got := f.reload(it.ID)
		if got.LinkStatus != model.LinkOK {
			t.Errorf("%s: link_status became %q; it should have been left alone",
				model.Deref(it.URL), got.LinkStatus)
		}
		// The attempt is still recorded, so the same item is not retried on
		// every sweep for the sake of a shop that will keep refusing.
		if got.LinkCheckedAt == nil {
			t.Errorf("%s: link_checked_at not recorded for an inconclusive check",
				model.Deref(it.URL))
		}
	}
}

// TestUnreachableHostIsInconclusive: DNS and connection failures say nothing
// about the link either.
func TestUnreachableHostIsInconclusive(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	it := f.addItem("/whatever", model.LinkOK)
	f.srv.Close() // nothing is listening now

	res := f.c.Sweep(context.Background())
	if res.Inconclusive != 1 {
		t.Errorf("inconclusive = %d, want 1 (%+v)", res.Inconclusive, res)
	}
	if got := f.reload(it.ID); got.LinkStatus != model.LinkOK {
		t.Errorf("link_status became %q after a connection failure", got.LinkStatus)
	}
}

// TestSoftFourOhFourIsCaught is why this job runs the extraction pipeline rather
// than asking for a status code. The link answers 200; it is still dead.
func TestSoftFourOhFourIsCaught(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		if r.URL.Path == "/products/narnia-set" {
			// Redirects to a collection and answers 200 describing it.
			http.Redirect(w, r, "/collections/best-sellers", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(collectionPage))
	})
	it := f.addItem("/products/narnia-set", model.LinkOK)

	f.c.Sweep(context.Background())
	if got := f.reload(it.ID); got.LinkStatus == model.LinkOK {
		t.Error("a product link that now lands on a collection page is still marked ok")
	}
}

// TestLiveLinkStaysOK: the common case has to be quiet, or the warnings mean
// nothing.
func TestLiveLinkStaysOK(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(livePage))
	})
	it := f.addItem("/products/dutch-oven", model.LinkUnknown)

	res := f.c.Sweep(context.Background())
	if res.OK != 1 {
		t.Errorf("ok = %d, want 1 (%+v)", res.OK, res)
	}
	if got := f.reload(it.ID); got.LinkStatus != model.LinkOK {
		t.Errorf("link_status = %q, want ok", got.LinkStatus)
	}
}

// TestSweepPacesItself: the pause between items is the part that keeps this from
// looking like a crawl, so it is asserted rather than assumed.
func TestSweepPacesItself(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(livePage))
	})
	for i := 0; i < 4; i++ {
		f.addItem("/products/thing-"+string(rune('a'+i)), model.LinkUnknown)
	}

	res := f.c.Sweep(context.Background())
	if res.Checked != 4 {
		t.Fatalf("checked = %d, want 4", res.Checked)
	}
	if len(f.slept) != 3 {
		t.Errorf("slept %d times for 4 items, want 3 — between items, not before the first", len(f.slept))
	}
	for _, d := range f.slept {
		if d != 30*time.Second {
			t.Errorf("paused %s, want the configured 30s", d)
		}
	}
}

// TestSweepRespectsBatchAndAge: the job is deliberately slow, and both limits are
// what make it so.
func TestSweepRespectsBatchAndAge(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(livePage))
	})
	f.c.opts.Batch = 2
	for i := 0; i < 5; i++ {
		f.addItem("/products/thing-"+string(rune('a'+i)), model.LinkUnknown)
	}

	if res := f.c.Sweep(context.Background()); res.Checked != 2 {
		t.Errorf("checked = %d, want the batch size of 2", res.Checked)
	}

	// The two just checked are now recent, so a second sweep moves on to others
	// rather than re-checking them.
	second := f.c.Sweep(context.Background())
	if second.Checked != 2 {
		t.Errorf("second sweep checked = %d, want 2 fresh items", second.Checked)
	}
}

// TestSoftDeletedItemsAreNotChecked: nobody is going to buy them, and a
// retailer's patience is the scarce resource.
func TestSoftDeletedItemsAreNotChecked(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(livePage))
	})
	it := f.addItem("/products/dutch-oven", model.LinkUnknown)
	if err := f.st.DeleteItem(context.Background(), it.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if res := f.c.Sweep(context.Background()); res.Checked != 0 {
		t.Errorf("checked = %d, want 0 for a removed item", res.Checked)
	}
}

// TestItemsWithoutLinksAreNotChecked: a hand-typed item has nothing to check.
func TestItemsWithoutLinksAreNotChecked(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	it := &model.Item{ListID: f.list.ID, Title: "A jumper, any jumper", Quantity: 1,
		LinkStatus: model.LinkUnknown}
	if err := f.st.CreateItem(context.Background(), it); err != nil {
		t.Fatalf("create: %v", err)
	}

	if res := f.c.Sweep(context.Background()); res.Checked != 0 {
		t.Errorf("checked = %d, want 0 for an item with no URL", res.Checked)
	}
}

// TestNeverCheckedItemsComeFirst: on the first sweep of an existing instance
// everything is equally stale, and the oldest thing is the best guess at what
// has rotted.
func TestNeverCheckedItemsComeFirst(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(livePage))
	})
	ctx := context.Background()
	recent := f.addItem("/products/recent", model.LinkOK)
	never := f.addItem("/products/never", model.LinkUnknown)

	// Mark one as checked a fortnight ago — still due, but less stale than never.
	twoWeeks := model.TimeString(model.Now().Add(-14 * 24 * time.Hour))
	if err := f.st.SetLinkStatus(ctx, recent.ID, model.LinkOK, twoWeeks); err != nil {
		t.Fatalf("set: %v", err)
	}

	f.c.opts.Batch = 1
	f.c.Sweep(ctx)

	if got := f.reload(never.ID); got.LinkCheckedAt == nil {
		t.Error("the never-checked item was not the one picked first")
	}
}
