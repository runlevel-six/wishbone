package extract_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"wishbone/internal/extract"
	"wishbone/internal/fetch"
)

// The two renderings a real department store serves for one garment. The legacy
// campaign path answers 200 with a complete document that has an OpenGraph
// title and no structured data at all — the string "price" does not appear in
// it — while the slug it declares canonical carries the full Product node.
const (
	strippedPage = `<!doctype html><html><head>
<link rel="canonical" href="https://shop.example.com/p/example-brand-long-sleeve-henley-top/180496814627FAM000039.html"/>
<meta property="og:title" content="Long Sleeve Henley Top"/>
<meta property="og:url" content="https://shop.example.com/p/example-brand-long-sleeve-henley-top/180496814627FAM000039.html"/>
<meta property="og:image" content="https://images.example.com/henley.jpg"/>
</head><body><h1>Long Sleeve Henley Top</h1></body></html>`

	canonicalPage = `<!doctype html><html><head>
<link rel="canonical" href="https://shop.example.com/p/example-brand-long-sleeve-henley-top/180496814627FAM000039.html"/>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Product",
 "url":"https://shop.example.com/p/example-brand-long-sleeve-henley-top/180496814627FAM000039.html",
 "name":"Long Sleeve Henley Top","sku":"180496814627FAM000039",
 "brand":{"@type":"Brand","name":"Example Brand"},
 "offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"}}
</script></head><body><h1>Long Sleeve Henley Top</h1></body></html>`
)

// twoRenderingStore serves the stripped page at the legacy path and the full one
// at the canonical path, rewriting the absolute canonical host to the test
// server's so the same-host rule is exercised honestly rather than bypassed.
func twoRenderingStore(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body := strippedPage
		if strings.HasPrefix(r.URL.Path, "/p/") {
			body = canonicalPage
		}
		body = strings.ReplaceAll(body, "https://shop.example.com", srv.URL)
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func loopbackService(t *testing.T) *extract.Service {
	t.Helper()
	client := fetch.New(fetch.Options{AllowLoopback: true})
	return extract.NewService(client, nil, true)
}

// TestCanonicalFollowedForPrice: the page that was asked for is fine, is not
// blocked and is not a soft 404. It simply does not carry a price, and says
// where it lives. Asking that address is the only thing that reaches one.
func TestCanonicalFollowedForPrice(t *testing.T) {
	srv, hits := twoRenderingStore(t)
	svc := loopbackService(t)

	legacy := srv.URL + "/s/Store/p/womens-long-sleeve-henley-top/180496814627FAM000039.html"
	prev, err := svc.Fetch(context.Background(), legacy)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if prev.Result.PriceCents == nil {
		t.Fatal("the canonical rendering has a price and should have been used")
	}
	if *prev.Result.PriceCents != 999 {
		t.Errorf("price = %d cents, want 999", *prev.Result.PriceCents)
	}
	if prev.Result.SKU != "180496814627FAM000039" {
		t.Errorf("sku = %q, want the canonical page's", prev.Result.SKU)
	}
	if prev.Result.Brand != "Example Brand" {
		t.Errorf("brand = %q, want Example Brand", prev.Result.Brand)
	}
	if n := atomic.LoadInt32(hits); n != 2 {
		t.Errorf("%d requests, want exactly 2 — one follow-up hop, never a chain", n)
	}
	// The fields came from the canonical address, so that is the address
	// recorded. Storing one page's price under another page's URL is the
	// provenance mistake the Sources map exists to prevent.
	if !strings.Contains(prev.URL, "/p/example-brand-long-sleeve-henley-top/") {
		t.Errorf("stored URL = %q, want the canonical address the fields came from", prev.URL)
	}
	// What the person actually pasted is still on the record.
	if prev.URLRaw != legacy {
		t.Errorf("URLRaw = %q, want the pasted address", prev.URLRaw)
	}
}

// TestCanonicalNotFollowedToAnotherProduct is the case the narrow gate exists
// for: a canonical that names a different listing is a different product, and
// following it silently buys the wrong size. That one stays a suggestion.
func TestCanonicalNotFollowedToAnotherProduct(t *testing.T) {
	var hits int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body := `<!doctype html><html><head>
<meta property="og:title" content="Cotton Tee"/>
<link rel="canonical" href="` + srv.URL + `/p/cotton-tee/DIFFERENT999.html"/>
</head><body></body></html>`
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	svc := loopbackService(t)
	prev, err := svc.Fetch(context.Background(), srv.URL+"/p/cotton-tee/ORIGINAL111.html")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("%d requests, want 1: a canonical naming another product is not followed", n)
	}
	if !prev.Suspect() {
		t.Error("a canonical naming a different product should still be flagged suspect")
	}
	alt := extract.CanonicalAlternative(prev.Result.Canonical, prev.URL)
	if !strings.Contains(alt, "DIFFERENT999") {
		t.Errorf("alternative = %q, want the sibling listing offered to the owner", alt)
	}
}

// TestCanonicalNotFollowedWhenPriceAlreadyFound keeps the common case at one
// request: the follow-up exists to find a missing price, not to second-guess a
// page that already gave one.
func TestCanonicalNotFollowedWhenPriceAlreadyFound(t *testing.T) {
	var hits int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body := strings.ReplaceAll(canonicalPage, "https://shop.example.com", srv.URL)
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	svc := loopbackService(t)
	// Asked for at a different spelling than the canonical, but complete.
	_, err := svc.Fetch(context.Background(),
		srv.URL+"/s/Store/p/other-slug/180496814627FAM000039.html")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("%d requests, want 1: nothing was missing", n)
	}
}
