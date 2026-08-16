package extract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wishd/internal/extract"
	"wishd/internal/fetch"
)

// TestNormalizeURL covers plan §5.1. The cases mirror the shapes in the
// extraction corpus — retired-alias leftovers, marketplace slug/ref crumbs, locale
// query parameters — with placeholder identifiers.
func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"the retired smile. alias lives on in the legacy data",
			"https://smile.amazon.com/gp/product/B0EXAMPLE1/",
			"https://www.amazon.com/dp/B0EXAMPLE1",
		},
		{
			"marketplace slug and ref crumbs are navigation, not identity",
			"https://www.amazon.com/Some-Long-Product-Slug/dp/B0EXAMPLE2/ref=sr_1_3?crid=X&qid=1",
			"https://www.amazon.com/dp/B0EXAMPLE2",
		},
		{
			"locale and tracking query params are dropped",
			"https://store.example.com/us/en/pro/category/routers/products/model-x?s=us&l=en",
			"https://store.example.com/us/en/pro/category/routers/products/model-x",
		},
		{
			"utm parameters are dropped, real ones are kept",
			"https://shop.example/products/thing?utm_source=news&utm_medium=email&variant=42",
			"https://shop.example/products/thing?variant=42",
		},
		{
			"host and scheme are lowercased, fragments dropped",
			"HTTPS://Shop.Example/Products/Thing#reviews",
			"https://shop.example/Products/Thing",
		},
		{
			"a bare host gets https and keeps its root path",
			"example.com",
			"https://example.com/",
		},
		{
			"trailing slashes do not create duplicates",
			"https://shop.example/products/thing/",
			"https://shop.example/products/thing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extract.NormalizeURL(tc.in)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if got != tc.want {
				t.Errorf("NormalizeURL(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeRejectsNonHTTP(t *testing.T) {
	for _, in := range []string{"javascript:alert(1)", "file:///etc/passwd"} {
		if _, err := extract.NormalizeURL(in); err == nil {
			t.Errorf("NormalizeURL(%q) should fail", in)
		}
	}
}

func TestParsePriceCents(t *testing.T) {
	cases := []struct {
		in       string
		want     int64
		currency string
	}{
		{"279.99", 27999, ""},
		{"$279.99", 27999, "USD"},
		{"1,299.00", 129900, ""},
		{"USD 49.5", 4950, "USD"},
		{"€12,99", 1299, "EUR"},
		{"1.234,56 EUR", 123456, "EUR"},
		{"39", 3900, ""},
	}
	for _, tc := range cases {
		got, cur := extract.ParsePriceCents(tc.in)
		if got == nil {
			t.Errorf("ParsePriceCents(%q) = nil", tc.in)
			continue
		}
		if *got != tc.want {
			t.Errorf("ParsePriceCents(%q) = %d, want %d", tc.in, *got, tc.want)
		}
		if tc.currency != "" && cur != tc.currency {
			t.Errorf("ParsePriceCents(%q) currency = %q, want %q", tc.in, cur, tc.currency)
		}
	}
	if got, _ := extract.ParsePriceCents("call for pricing"); got != nil {
		t.Errorf("non-numeric price returned %v", *got)
	}
}

// TestShopifyTier exercises tier 1 against a local stand-in storefront. The
// point of the tier is that one adapter covers every Shopify store with no
// per-site selectors.
func TestShopifyTier(t *testing.T) {
	product := map[string]any{
		"product": map[string]any{
			"title":     "Ridgeline Wool Socks",
			"body_html": "<p>Merino wool.</p><p>Made in Vermont.</p>",
			"vendor":    "Ridgeline",
			"handle":    "ridgeline-wool-socks",
			"options":   []any{map[string]any{"name": "Size", "values": []string{"L"}}},
			"variants": []any{map[string]any{
				"title": "L", "sku": "RWS-L", "price": "24.00", "option1": "L", "available": true,
			}},
			"images": []any{map[string]any{"src": "https://cdn.example/socks.jpg"}},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/products/ridgeline-wool-socks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(product)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := fetch.New(fetch.Options{AllowLoopback: true})
	page, err := extract.ParseDocument(strings.NewReader(
		`<html><head><meta property="og:type" content="product">
		 <meta property="product:price:currency" content="USD"></head><body></body></html>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	page.RequestedURL = mustURL(t, srv.URL+"/products/ridgeline-wool-socks")
	page.FinalURL = page.RequestedURL
	page.StatusCode = 200

	chain := extract.NewChain(extract.Shopify{Client: client}, extract.OpenGraph{})
	res := chain.Run(context.Background(), page)

	if res.Title != "Ridgeline Wool Socks" {
		t.Errorf("title = %q", res.Title)
	}
	if res.SKU != "RWS-L" {
		t.Errorf("sku = %q", res.SKU)
	}
	if res.PriceCents == nil || *res.PriceCents != 2400 {
		t.Errorf("price = %v, want 2400", res.PriceCents)
	}
	// Shopify's product JSON has no currency; a later tier fills it in. That
	// is what per-field merging buys.
	if res.Currency != "USD" {
		t.Errorf("currency = %q, want USD from the OG tier", res.Currency)
	}
	if res.Sources["title"] != extract.SourceShopify || res.Sources["currency"] != extract.SourceOG {
		t.Errorf("field sources = %v, want title from shopify and currency from og", res.Sources)
	}
	if res.Attributes["size"] != "L" {
		t.Errorf("size attribute = %q, want L from the single variant", res.Attributes["size"])
	}
	if !strings.Contains(res.Description, "Merino wool") {
		t.Errorf("description = %q", res.Description)
	}
}

// TestShopifyTierIgnoresNonShopify makes sure a store that answers .json with
// HTML does not poison the chain.
func TestShopifyTierIgnoresNonShopify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>404</body></html>"))
	}))
	defer srv.Close()

	client := fetch.New(fetch.Options{AllowLoopback: true})
	page, _ := extract.ParseDocument(strings.NewReader(
		`<html><head><meta property="og:title" content="A Thing"></head></html>`))
	page.RequestedURL = mustURL(t, srv.URL+"/products/a-thing")
	page.FinalURL = page.RequestedURL
	page.StatusCode = 200

	chain := extract.NewChain(extract.Shopify{Client: client}, extract.OpenGraph{})
	res := chain.Run(context.Background(), page)

	if res.Title != "A Thing" {
		t.Errorf("title = %q, want the OG tier's value", res.Title)
	}
	if res.Errors[extract.SourceShopify] == "" {
		t.Error("the shopify tier should have recorded why it declined")
	}
}
