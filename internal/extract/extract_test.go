package extract_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wishd/internal/extract"
	"wishd/internal/model"
)

// Golden-file tests over saved HTML. No test in this package touches the
// network (plan §8).
func loadPage(t *testing.T, name, requested, final string) *extract.Page {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	page, err := extract.ParseHead(f)
	if err != nil {
		t.Fatalf("parse head: %v", err)
	}
	page.RequestedURL = mustURL(t, requested)
	page.FinalURL = mustURL(t, final)
	page.StatusCode = 200
	return page
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse url %q: %v", s, err)
	}
	return u
}

// metadataChain is tiers 2-4; the Shopify tier needs a client and is tested
// separately.
func metadataChain() *extract.Chain {
	return extract.NewChain(extract.JSONLD{}, extract.Microdata{}, extract.OpenGraph{})
}

func TestExtractLiveProductPage(t *testing.T) {
	const u = "https://cedarpress.example/products/the-complete-ring-saga-box-set"
	page := loadPage(t, "product_live.html", u, u)

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if res.Title != "The Complete Ring Saga Box Set" {
		t.Errorf("title = %q", res.Title)
	}
	if res.PriceCents == nil || *res.PriceCents != 27999 {
		t.Errorf("price = %v, want 27999 cents", res.PriceCents)
	}
	if res.Currency != "USD" {
		t.Errorf("currency = %q, want USD", res.Currency)
	}
	if res.SKU != "CP-LOTR-4V" {
		t.Errorf("sku = %q", res.SKU)
	}
	if res.Brand != "Cedar Press" {
		t.Errorf("brand = %q", res.Brand)
	}
	if res.OGType != "product" {
		t.Errorf("og:type = %q", res.OGType)
	}
	if len(res.ImageURLs) == 0 || !strings.HasPrefix(res.ImageURLs[0], "https://") {
		t.Errorf("images = %v, want an https URL first", res.ImageURLs)
	}
	if res.Suspect {
		t.Errorf("live product flagged suspect: %v", res.SuspectReason)
	}
	if res.LinkStatus != model.LinkOK {
		t.Errorf("link status = %q, want ok", res.LinkStatus)
	}

	// Field-level provenance: JSON-LD wins the description over the SEO meta
	// description, which is what per-field merging is for (plan §5.3).
	if got := res.Sources["description"]; got != extract.SourceJSONLD {
		t.Errorf("description source = %q, want jsonld", got)
	}
	if !strings.HasPrefix(res.Description, "A four-volume set of a classic fantasy sequence") {
		t.Errorf("description = %q, want the JSON-LD one", res.Description)
	}
}

// TestSoft404DeadProductLink is the fixture pair plan §8 requires: the same
// site, a live product and a dead one.
func TestSoft404DeadProductLink(t *testing.T) {
	const requested = "https://cedarpress.example/collections/best-sellers/products/the-childrens-classics-set"
	const final = "https://cedarpress.example/collections/best-sellers"
	page := loadPage(t, "product_dead.html", requested, final)

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if !res.Suspect {
		t.Fatal("a dead product link that 200s with collection metadata must be flagged suspect")
	}
	if res.LinkStatus != model.LinkSuspect {
		t.Errorf("link status = %q, want suspect", res.LinkStatus)
	}
	joined := strings.Join(res.SuspectReason, " | ")
	for _, want := range []string{"website", "redirected"} {
		if !strings.Contains(joined, want) {
			t.Errorf("suspect reasons %q should mention %q", joined, want)
		}
	}
	// The extractor still reports what it saw — the caller decides — but it
	// must not present the collection page as the product.
	if res.PriceCents != nil {
		t.Errorf("dead link produced a price: %v", *res.PriceCents)
	}
}

func TestMicrodataTier(t *testing.T) {
	const u = "https://tools.example/product/impact-driver"
	page := loadPage(t, "microdata_only.html", u, u)

	res := metadataChain().Run(context.Background(), page)

	if res.Title != "18V Brushless Impact Driver" {
		t.Errorf("title = %q", res.Title)
	}
	if res.PriceCents == nil || *res.PriceCents != 124950 {
		t.Errorf("price = %v, want 124950 cents", res.PriceCents)
	}
	if res.Currency != "USD" {
		t.Errorf("currency = %q", res.Currency)
	}
	if res.Attributes["color"] != "Graphite" {
		t.Errorf("color attribute = %q", res.Attributes["color"])
	}
	if len(res.ImageURLs) != 1 || res.ImageURLs[0] != "https://tools.example/img/impact-driver.png" {
		t.Errorf("images = %v, want the relative URL resolved", res.ImageURLs)
	}
}

// TestNoTitleTagFallback pins plan §5.3's refusal to fall back to <title>.
func TestNoTitleTagFallback(t *testing.T) {
	const u = "https://sockworld.example/products/wool-socks"
	page := loadPage(t, "title_only.html", u, u)

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if res.Title != "" {
		t.Errorf("title = %q; SEO page titles must not be used as product names", res.Title)
	}
	if page.Title == "" {
		t.Error("the fixture's <title> should still be parsed, just not used")
	}
	// Nothing was found, but nothing is claimed either: this is the manual
	// path, not a suspect link.
	if res.Suspect {
		t.Errorf("a page with no structured data should not be suspect: %v", res.SuspectReason)
	}
}

// TestHeadOnlyParsing pins the streaming stop at </head> (plan §5.2).
func TestHeadOnlyParsing(t *testing.T) {
	html := `<html><head><meta property="og:title" content="Real title"></head>
	<body><meta property="og:title" content="Body injected title">
	<script type="application/ld+json">{"@type":"Product","name":"Body product"}</script>
	</body></html>`

	page, err := extract.ParseHead(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := page.MetaProperty("og:title"); got != "Real title" {
		t.Errorf("og:title = %q, want the head one", got)
	}
	if len(page.JSONLD) != 0 {
		t.Errorf("body JSON-LD was parsed: %v", page.JSONLD)
	}
}
