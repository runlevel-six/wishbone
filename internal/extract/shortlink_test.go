package extract_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A phone's share sheet does not hand over a product URL. It hands over a
// shortened one, whose whole purpose is to resolve somewhere else — and the
// soft-404 guard used to compare the short code against the resolved path, which
// can never match. Every link added the most common way was flagged suspect with
// its title, price and image read and then discarded.
//
// Measured on the first real corpus: three of three short links suspect, and the
// share target is the flow they arrive through.
const shortLinkTargetPage = `<!doctype html><html><head>
<link rel="canonical" href="https://shop.example.com/dp/B0D92TV4SQ"/>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Product",
 "url":"https://shop.example.com/dp/B0D92TV4SQ",
 "name":"Nonstick meatloaf pan","sku":"B0D92TV4SQ",
 "offers":{"@type":"Offer","price":"19.99","priceCurrency":"USD"}}
</script></head><body><h1>Nonstick meatloaf pan</h1></body></html>`

// TestShortLinkIsNotSuspect: the requested path is an opaque code on another
// host, so there is nothing to compare it against and no conclusion to draw.
func TestShortLinkIsNotSuspect(t *testing.T) {
	var product *httptest.Server
	product = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.ReplaceAll(shortLinkTargetPage,
			"https://shop.example.com", product.URL)))
	}))
	defer product.Close()

	// The short-link host: a different host, an opaque path, one redirect.
	shortener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, product.URL+"/dp/B0D92TV4SQ", http.StatusFound)
	}))
	defer shortener.Close()

	svc := loopbackService(t)
	prev, err := svc.Fetch(context.Background(), shortener.URL+"/d/08r00ya6")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if prev.Suspect() {
		t.Errorf("a resolved short link was flagged suspect: %v", prev.Result.SuspectReason)
	}
	if prev.Result.Title == "" || prev.Result.PriceCents == nil {
		t.Error("the fields were read but not kept")
	}
	if *prev.Result.PriceCents != 1999 {
		t.Errorf("price = %d cents, want 1999", *prev.Result.PriceCents)
	}
	// The address recorded is where the product actually lives, not the alias.
	if !strings.Contains(prev.URL, "/dp/B0D92TV4SQ") {
		t.Errorf("stored URL = %q, want the resolved address", prev.URL)
	}
}

// TestSameHostRedirectStillSuspect is the case the rule exists for, and the one
// the fix must not soften: a product URL that quietly lands on a collection page,
// on the same host, is still a dead link wearing a 200.
func TestSameHostRedirectStillSuspect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		if strings.HasPrefix(r.URL.Path, "/products/") {
			http.Redirect(w, r, "/collections/best-sellers", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><html><head>
<meta property="og:type" content="website"/>
<meta property="og:title" content="Best sellers"/>
</head><body></body></html>`))
	}))
	defer srv.Close()

	svc := loopbackService(t)
	prev, err := svc.Fetch(context.Background(), srv.URL+"/products/narnia-set")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !prev.Suspect() {
		t.Error("a product link that landed on a collection page was not flagged")
	}
}

// TestCrossHostRedirectToNothingUsefulStillSuspect: dropping the path comparison
// for cross-host redirects does not mean cross-host redirects are trusted. The
// other signals still apply — here, a page that says it is not a product and
// carries no product data.
func TestCrossHostRedirectToNothingUsefulStillSuspect(t *testing.T) {
	landing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head>
<meta property="og:type" content="website"/>
<meta property="og:title" content="Domain for sale"/>
</head><body></body></html>`))
	}))
	defer landing.Close()

	shortener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, landing.URL+"/parked", http.StatusFound)
	}))
	defer shortener.Close()

	svc := loopbackService(t)
	prev, err := svc.Fetch(context.Background(), shortener.URL+"/d/whatever")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !prev.Suspect() {
		t.Error("a short link that resolved to a parked page should still be flagged")
	}
}
