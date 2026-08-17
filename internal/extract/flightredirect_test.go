package extract_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The shape a department store actually serves for an alias of a product path:
// a 200, a shell with no metadata of any kind, an unresolved Suspense boundary
// where the product should be, and the redirect delivered as an error digest
// inside the payload because the status line was already gone. Trimmed from a
// megabyte to the parts that decide the outcome.
const aliasShellPage = `<!doctype html><html lang="en"><head>
<meta charSet="utf-8"/>
<link rel="stylesheet" href="/_next/static/chunks/14dd1cc182be3fc6.css" data-precedence="next"/>
</head><body><div hidden=""><!--$?--><template id="B:0"></template><!--/$--></div>
<script>self.__next_f.push([1,"0:{\"P\":null,\"b\":\"kQAlRVkByCJtCg3h\",\"c\":[\"\",\"p\",\"womens-3\",\"4-sleeve-henley-solid-top\",\"180496814627FAM000039.html\"]}\n"])</script>
<script>self.__next_f.push([1,"20:E{\"digest\":\"NEXT_REDIRECT;replace;https://shop.example.com/p/example-brand-womens-3-4-sleeve-henley-solid-top/180496814627FAM000039.html;307;\"}\n24:null\n"])</script>
</body></html>`

// The address the digest names, serving what the shell never did.
const redirectTargetPage = `<!doctype html><html><head>
<title>Women's 3/4 Sleeve Henley Solid Top</title>
<link rel="canonical" href="https://shop.example.com/p/example-brand-womens-3-4-sleeve-henley-solid-top/180496814627FAM000039.html"/>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Product",
 "url":"https://shop.example.com/p/example-brand-womens-3-4-sleeve-henley-solid-top/180496814627FAM000039.html",
 "name":"Women's 3/4 Sleeve Henley Solid Top","sku":"180496814627FAM000039",
 "brand":{"@type":"Brand","name":"Example Brand"},
 "image":"https://images.example.com/henley-solid-top.jpg",
 "offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"}}
</script></head><body><h1>Women's 3/4 Sleeve Henley Solid Top</h1></body></html>`

// aliasStore serves the shell at any path that is not the address the digest
// names, and the real page at that one. The absolute host inside both is
// rewritten to the test server's, so the same-site rule is exercised rather
// than bypassed.
func aliasStore(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	const realPath = "/p/example-brand-womens-3-4-sleeve-henley-solid-top/180496814627FAM000039.html"
	var hits int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body := aliasShellPage
		if r.URL.Path == realPath {
			body = redirectTargetPage
		}
		body = strings.ReplaceAll(body, "https://shop.example.com", srv.URL)
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestStreamedRedirectFollowed is the reported failure: pasting an alias of a
// product path produced a blank form and no explanation, because the redirect
// out of it was never an HTTP redirect.
func TestStreamedRedirectFollowed(t *testing.T) {
	srv, hits := aliasStore(t)
	svc := loopbackService(t)

	alias := srv.URL + "/s/Store/p/women's-3/4-sleeve-henley-solid-top/180496814627FAM000039.html"
	prev, err := svc.Fetch(context.Background(), alias)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if prev.Result.PriceCents == nil {
		t.Fatal("the address the payload redirected to has a price and should have been used")
	}
	if *prev.Result.PriceCents != 999 {
		t.Errorf("price = %d cents, want 999", *prev.Result.PriceCents)
	}
	if prev.Result.Title != "Women's 3/4 Sleeve Henley Solid Top" {
		t.Errorf("title = %q, want the product's", prev.Result.Title)
	}
	if prev.Result.SKU != "180496814627FAM000039" {
		t.Errorf("sku = %q, want the redirect target's", prev.Result.SKU)
	}
	if prev.Suspect() {
		t.Errorf("suspect after following a redirect to a complete page: %v",
			prev.Result.SuspectReason)
	}
	if n := atomic.LoadInt32(hits); n != 2 {
		t.Errorf("%d requests, want exactly 2 — one hop, never a chain", n)
	}
	// The fields came from the address the shop named, so that is the address
	// recorded; what the person pasted stays on the record as URLRaw.
	if !strings.Contains(prev.URL, "/p/example-brand-womens-3-4-sleeve-henley-solid-top/") {
		t.Errorf("stored URL = %q, want the address the fields came from", prev.URL)
	}
	if prev.URLRaw != alias {
		t.Errorf("URLRaw = %q, want the pasted address", prev.URLRaw)
	}
}

// TestStreamedRedirectNotFollowedWhenPageHasFields keeps the follow-up to the
// case it was written for. A payload carries the errors of every segment that
// threw, so a page that described a product is not overruled by one of them.
func TestStreamedRedirectNotFollowedWhenPageHasFields(t *testing.T) {
	var hits int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body := `<!doctype html><html><head>
<meta property="og:title" content="Cotton Tee"/>
<meta property="og:type" content="product"/>
</head><body>
<script>self.__next_f.push([1,"20:E{\"digest\":\"NEXT_REDIRECT;replace;` + srv.URL + `/p/somewhere-else/OTHER222.html;307;\"}\n"])</script>
</body></html>`
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
		t.Errorf("%d requests, want 1: the page described a product", n)
	}
	if prev.Result.Title != "Cotton Tee" {
		t.Errorf("title = %q, want the page's own", prev.Result.Title)
	}
}

// TestStreamedRedirectAmbiguousRefused: one payload, two destinations, no way to
// tell which segment threw for the product. Refusing leaves a blank form, which
// is recoverable; landing on the wrong product is not.
func TestStreamedRedirectAmbiguousRefused(t *testing.T) {
	var hits int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body := `<!doctype html><html><head></head><body>
<script>self.__next_f.push([1,"20:E{\"digest\":\"NEXT_REDIRECT;replace;` + srv.URL + `/p/first/AAA111.html;307;\"}\n"])</script>
<script>self.__next_f.push([1,"21:E{\"digest\":\"NEXT_REDIRECT;replace;` + srv.URL + `/p/second/BBB222.html;307;\"}\n"])</script>
</body></html>`
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	svc := loopbackService(t)
	if _, err := svc.Fetch(context.Background(), srv.URL+"/p/alias/AAA111.html"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("%d requests, want 1: two destinations is a guess, not a redirect", n)
	}
}

// TestStreamedRedirectOffSiteRefused: the digest is content written by the page
// being examined, and no honest one sends the fetcher to another site.
func TestStreamedRedirectOffSiteRefused(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body := `<!doctype html><html><head></head><body>
<script>self.__next_f.push([1,"20:E{\"digest\":\"NEXT_REDIRECT;replace;https://elsewhere.example.net/p/thing/ZZZ999.html;307;\"}\n"])</script>
</body></html>`
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	svc := loopbackService(t)
	if _, err := svc.Fetch(context.Background(), srv.URL+"/p/alias/ZZZ999.html"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("%d requests, want 1: a digest naming another site is not followed", n)
	}
}

// TestStreamedRedirectRelativeAndChunked covers the two shapes the parse has to
// survive: a root-relative destination, and a digest split across two pushes at
// an arbitrary offset — the same boundary problem that loses JSON-LD blobs.
func TestStreamedRedirectRelativeAndChunked(t *testing.T) {
	const realPath = "/p/example-brand-tee/CCC333.html"
	var mu sync.Mutex
	var paths []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		body := `<!doctype html><html><head></head><body>
<script>self.__next_f.push([1,"20:E{\"digest\":\"NEXT_RED"])</script>
<script>self.__next_f.push([1,"IRECT;replace;` + realPath + `;307;\"}\n"])</script>
</body></html>`
		if r.URL.Path == realPath {
			body = strings.ReplaceAll(redirectTargetPage, "https://shop.example.com", srv.URL)
		}
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	svc := loopbackService(t)
	prev, err := svc.Fetch(context.Background(), srv.URL+"/s/Store/p/tee/CCC333.html")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if prev.Result.PriceCents == nil {
		t.Fatalf("no price; requests were %v", paths)
	}
	if len(paths) != 2 || paths[1] != realPath {
		t.Errorf("requests = %v, want the alias then %s", paths, realPath)
	}
}
