package extract_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wishbone/internal/extract"
	"wishbone/internal/model"
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

	page, err := extract.ParseDocument(f)
	if err != nil {
		t.Fatalf("parse document: %v", err)
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

// TestHeadWinsOverBody pins the ordering rule that replaced the stop at
// </head>. The body is parsed now — it has to be, or streaming-metadata pages
// extract to nothing — so the head has to win on its own merits instead of by
// being the only thing read. First value found wins, and the head is first.
func TestHeadWinsOverBody(t *testing.T) {
	html := `<html><head><title>Real title</title>
	<meta property="og:title" content="Real title">
	<link rel="canonical" href="https://shop.example.com/p/real"></head>
	<body><meta property="og:title" content="Body injected title">
	<link rel="canonical" href="https://shop.example.com/p/carousel">
	<svg><title>Close icon</title></svg>
	<script type="application/ld+json">{"@type":"Product","name":"Body product"}</script>
	</body></html>`

	page, err := extract.ParseDocument(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := page.MetaProperty("og:title"); got != "Real title" {
		t.Errorf("og:title = %q, want the head one", got)
	}
	if got := strings.TrimSpace(page.Title); got != "Real title" {
		t.Errorf("title = %q, want the head one and not the SVG icon's", got)
	}
	if page.Canonical != "https://shop.example.com/p/real" {
		t.Errorf("canonical = %q, want the head one", page.Canonical)
	}
	// The body block is now read. That is the point of the change: on a
	// streaming-metadata page it is the only place the data exists.
	if len(page.JSONLD) != 1 {
		t.Errorf("body JSON-LD = %v, want it parsed", page.JSONLD)
	}
}

// TestStreamedMetadataPage is the Next.js App Router shape, reduced from a live
// department-store product page that returned a perfectly good 200 and
// extracted to absolutely nothing.
//
// Everything that matters is wrong in the same way: the head is a stub, and the
// title, the OpenGraph tags, the canonical link and the JSON-LD are all in the
// body — the JSON-LD not even as a script tag, but as a prop inside the React
// Server Components payload, split across two pushes so that a per-chunk scan
// would find neither half.
//
// The fixture also carries a neighbour product in the same payload, because a
// recommendation rail is the normal case and taking its price would be worse
// than extracting nothing at all.
func TestStreamedMetadataPage(t *testing.T) {
	const u = "https://shop.example.com/p/field-jacket/FJ-2201.html"
	page := loadPage(t, "product_streamed_metadata.html", u, u)

	// The premise: nothing here is reachable the old way.
	if len(page.FlightChunks) == 0 {
		t.Fatal("no flight chunks collected; the fixture is not exercising the payload")
	}

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if res.Title != "Ridgeline Waxed Cotton Field Jacket" {
		t.Errorf("title = %q", res.Title)
	}
	if res.SKU != "FJ-2201" {
		t.Errorf("sku = %q, want the page's own", res.SKU)
	}
	if res.Brand != "Ridgeline Supply" {
		t.Errorf("brand = %q", res.Brand)
	}
	if res.PriceCents == nil || *res.PriceCents != 14800 {
		t.Errorf("price = %v, want 14800 cents", res.PriceCents)
	}
	if res.Currency != "USD" {
		t.Errorf("currency = %q", res.Currency)
	}
	// The neighbour in the same payload must not have been picked up.
	if res.SKU == "WS-9100" || (res.PriceCents != nil && *res.PriceCents == 3900) {
		t.Error("extracted the recommendation rail's product instead of the page's")
	}
	// Streamed <link rel=canonical> is found even though it is in the body.
	if page.Canonical != u {
		t.Errorf("canonical = %q, want %q", page.Canonical, u)
	}
	// The SVG icon's <title> must not become the page title.
	if strings.Contains(page.Title, "Add to bag") {
		t.Errorf("page title picked up an SVG icon title: %q", page.Title)
	}
	// A complete extraction from a live page: no reason to doubt it.
	if res.Suspect {
		t.Errorf("streamed-metadata product flagged suspect: %v", res.SuspectReason)
	}
	if res.LinkStatus != model.LinkOK {
		t.Errorf("link status = %q, want ok", res.LinkStatus)
	}
	// Provenance: this came through the JSON-LD tier, which is what lets
	// hasStructuredProduct treat it as a real product claim.
	if got := res.Sources["title"]; got != extract.SourceJSONLD {
		t.Errorf("title source = %q, want jsonld", got)
	}
}

// TestCarouselProductLosesToThePage is what the old head-only stop was really
// buying: immunity from a recommendation block's JSON-LD. Parsing the body
// gives that up, so the address each Product claims has to earn it back.
//
// Both nodes are well-formed Products and the carousel's comes first in
// document order, so document order alone picks the wrong gift.
func TestCarouselProductLosesToThePage(t *testing.T) {
	const u = "https://shop.example.com/p/real-item"
	html := `<html><head></head><body>
	<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product",
	  "@id":"https://shop.example.com/p/you-may-also-like","name":"Neighbour item",
	  "sku":"NEIGHBOUR-1","offers":{"@type":"Offer","price":"99.00","priceCurrency":"USD"}}</script>
	<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product",
	  "@id":"https://shop.example.com/p/real-item","name":"The real item",
	  "sku":"REAL-1","offers":{"@type":"Offer","price":"24.00","priceCurrency":"USD"}}</script>
	</body></html>`

	page, err := extract.ParseDocument(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	page.RequestedURL = mustURL(t, u)
	page.FinalURL = page.RequestedURL

	res := metadataChain().Run(context.Background(), page)
	if res.Title != "The real item" || res.SKU != "REAL-1" {
		t.Errorf("took the carousel's product: title=%q sku=%q", res.Title, res.SKU)
	}
	if res.PriceCents == nil || *res.PriceCents != 2400 {
		t.Errorf("price = %v, want the page's own 2400", res.PriceCents)
	}
}

// TestOGTypeWebsiteWithStructuredProduct is a regression test from a live
// false positive: a real product page on a single-page storefront that emits
// og:type "website" everywhere. The guard flagged it as suspect on that alone,
// suppressing a complete extraction and training the person to click through
// warnings — which is how the genuine dead-link case gets ignored.
//
// The URL did not move and the page published a schema.org Product with a
// name and SKU. That outweighs a boilerplate og:type.
func TestOGTypeWebsiteWithStructuredProduct(t *testing.T) {
	const u = "https://store.example.com/us/en/category/internet/collections/routing/products/router-ultra"
	page := loadPage(t, "product_ogtype_website.html", u, u)

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if res.Suspect {
		t.Errorf("a product page with structured data was flagged suspect: %v", res.SuspectReason)
	}
	if res.LinkStatus != model.LinkOK {
		t.Errorf("link status = %q, want ok", res.LinkStatus)
	}
	if res.Title != "Mobile Router Ultra" || res.SKU != "RTR-Ultra-US" {
		t.Errorf("extraction incomplete: title=%q sku=%q", res.Title, res.SKU)
	}
}

// TestOGTypeWebsiteWithoutStructuredProduct is the other half: the same weak
// signal, but with nothing corroborating it, must still be treated as suspect.
func TestOGTypeWebsiteWithoutStructuredProduct(t *testing.T) {
	const u = "https://shop.example/products/mystery-thing"
	page := loadPage(t, "product_dead.html", u, u)

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if !res.Suspect {
		t.Fatal("og:type website with no structured product data must stay suspect")
	}
	joined := strings.Join(res.SuspectReason, " | ")
	if !strings.Contains(joined, "website") {
		t.Errorf("reasons %q should cite the og:type", joined)
	}
}
