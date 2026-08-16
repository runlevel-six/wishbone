package extract_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wishd/internal/extract"
)

// sidecarStub serves the contract documented in deploy/sidecar/README.md.
func sidecarStub(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extract" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("url") == "" {
			t.Errorf("sidecar called without a url parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sidecarPage(t *testing.T, head string) *extract.Page {
	t.Helper()
	page, err := extract.ParseDocument(strings.NewReader("<html><head>" + head + "</head></html>"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	page.RequestedURL = mustURL(t, "https://marketplace.example/dp/B0EXAMPLE1")
	page.FinalURL = page.RequestedURL
	page.StatusCode = 200
	return page
}

// TestSidecarTier covers the shape the real library produces: a name, a
// free-text price, and one image. Everything else is absent, which is the
// normal case rather than an error.
func TestSidecarTier(t *testing.T) {
	srv := sidecarStub(t, 200, `{
		"title": "Hummingbird Feeder Camera",
		"price": "129.99 USD",
		"images": ["https://cdn.example/feeder.jpg"]
	}`)

	sidecar := extract.NewSidecar(srv.URL, 5*time.Second)
	res := extract.NewChain(extract.OpenGraph{}, sidecar).Run(context.Background(), sidecarPage(t, ""))

	if res.Title != "Hummingbird Feeder Camera" {
		t.Errorf("title = %q", res.Title)
	}
	if res.PriceCents == nil || *res.PriceCents != 12999 {
		t.Errorf("price = %v, want 12999 cents", res.PriceCents)
	}
	if res.Currency != "USD" {
		t.Errorf("currency = %q, want USD parsed out of the free-text price", res.Currency)
	}
	if len(res.ImageURLs) != 1 {
		t.Errorf("images = %v", res.ImageURLs)
	}
	if res.Sources["title"] != extract.SourceSidecar {
		t.Errorf("title source = %q, want sidecar", res.Sources["title"])
	}
}

// TestSidecarUnparseablePrice: the library returns things like "Currently
// unavailable." Nothing should be invented from that.
func TestSidecarUnparseablePrice(t *testing.T) {
	srv := sidecarStub(t, 200, `{"title":"Out of stock thing","price":"Currently unavailable."}`)

	sidecar := extract.NewSidecar(srv.URL, 5*time.Second)
	res := extract.NewChain(sidecar).Run(context.Background(), sidecarPage(t, ""))

	if res.Title != "Out of stock thing" {
		t.Errorf("title = %q", res.Title)
	}
	if res.PriceCents != nil {
		t.Errorf("price = %v, want none", *res.PriceCents)
	}
}

// TestSidecarFailuresDegrade: the sidecar is untrusted, so every failure mode
// leaves the rest of the chain intact rather than failing the extraction.
func TestSidecarFailuresDegrade(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"server error":   {500, `{"error":"boom"}`},
		"error field":    {200, `{"error":"unsupported site"}`},
		"malformed json": {200, `{"title": `},
		"empty response": {200, ``},
		"html not json":  {200, `<html>nope</html>`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := sidecarStub(t, tc.status, tc.body)
			sidecar := extract.NewSidecar(srv.URL, 5*time.Second)

			page := sidecarPage(t, `<meta property="og:title" content="From the page">`)
			res := extract.NewChain(extract.OpenGraph{}, sidecar).Run(context.Background(), page)

			if res.Title != "From the page" {
				t.Errorf("title = %q, want the OpenGraph value to survive", res.Title)
			}
			if res.Errors[extract.SourceSidecar] == "" {
				t.Error("the failure should have been recorded against the sidecar tier")
			}
		})
	}
}

// TestSidecarUnreachableDegrades covers the sidecar container being down.
func TestSidecarUnreachableDegrades(t *testing.T) {
	srv := sidecarStub(t, 200, `{}`)
	base := srv.URL
	srv.Close() // nothing is listening now

	sidecar := extract.NewSidecar(base, time.Second)
	page := sidecarPage(t, `<meta property="og:title" content="From the page">`)
	res := extract.NewChain(extract.OpenGraph{}, sidecar).Run(context.Background(), page)

	if res.Title != "From the page" {
		t.Errorf("title = %q, want extraction to carry on without the sidecar", res.Title)
	}
	if res.Errors[extract.SourceSidecar] == "" {
		t.Error("an unreachable sidecar should be recorded, not silently ignored")
	}
}

// TestSidecarSkippedWhenMetadataSuffices: tier 5 costs a round trip through
// another process, so it must not run when the cheap tiers already produced a
// usable result.
func TestSidecarSkippedWhenMetadataSuffices(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"title":"should not be used"}`))
	}))
	defer srv.Close()

	page := sidecarPage(t, `<meta property="og:title" content="Complete">`+
		`<meta property="product:price:amount" content="10.00">`+
		`<meta property="product:price:currency" content="USD">`)

	sidecar := extract.NewSidecar(srv.URL, 5*time.Second)
	res := extract.NewChain(extract.OpenGraph{}, sidecar).Run(context.Background(), page)

	if called {
		t.Error("the sidecar was called even though the page already had a title and a price")
	}
	if res.Title != "Complete" {
		t.Errorf("title = %q", res.Title)
	}
}

// TestNewSidecarDisabledWhenUnset pins that an unconfigured sidecar is a
// supported deployment, not a broken one.
func TestNewSidecarDisabledWhenUnset(t *testing.T) {
	if s := extract.NewSidecar("", time.Second); s != nil {
		t.Error("an empty EXTRACTOR_SIDECAR_URL should produce no tier at all")
	}
}
