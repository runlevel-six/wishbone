package extract_test

import (
	"context"
	"strings"
	"testing"

	"wishbone/internal/extract"
	"wishbone/internal/model"
)

// TestProductGroupIsAProductPage is the shape a department store publishes for
// one item sold in several colors: a ProductGroup carrying the name, brand and
// group id, with each color as a Product under hasVariant.
//
// Requiring @type Product meant the tier found nothing here at all, so the only
// fields were OpenGraph ones — and the guard, seeing og:type "website" with no
// structured product behind it, warned that a perfectly real product page was
// not one.
func TestProductGroupIsAProductPage(t *testing.T) {
	const requested = "https://shop.example.com/p/quilted-cotton-sheet-set/ppr500123"
	page := loadPage(t, "product_group_variants.html", requested, requested)

	res := metadataChain().Run(context.Background(), page)
	extract.ApplySoft404Guard(res, page)

	if res.Suspect {
		t.Errorf("a ProductGroup page must not be flagged suspect: %v", res.SuspectReason)
	}
	if res.LinkStatus != model.LinkOK {
		t.Errorf("link status = %q, want ok", res.LinkStatus)
	}
	// The group's own name, not the OpenGraph title with the store name glued
	// on the end.
	if res.Title != "Quilted Cotton Sheet Set" {
		t.Errorf("title = %q, want the ProductGroup name", res.Title)
	}
	if res.Sources["title"] != extract.SourceJSONLD {
		t.Errorf("title source = %q, want jsonld", res.Sources["title"])
	}
	if res.SKU != "ppr500123" {
		t.Errorf("sku = %q, want the productGroupID", res.SKU)
	}
	if res.Brand != "Example Home" {
		t.Errorf("brand = %q, want Example Home", res.Brand)
	}
}

// TestProductGroupZeroPriceIsNotAPrice: every variant on that page is published
// as "0.00" because the real number is fetched by script after load. Nothing is
// free, and a $0.00 item on a wishlist is worse than a blank one.
func TestProductGroupZeroPriceIsNotAPrice(t *testing.T) {
	const requested = "https://shop.example.com/p/quilted-cotton-sheet-set/ppr500123"
	page := loadPage(t, "product_group_variants.html", requested, requested)

	res := metadataChain().Run(context.Background(), page)

	if res.PriceCents != nil {
		t.Errorf("price = %d cents, want none: 0.00 is a placeholder", *res.PriceCents)
	}
	// The currency is still a real statement about the page and costs nothing
	// to keep.
	if res.Currency != "USD" {
		t.Errorf("currency = %q, want USD", res.Currency)
	}
}

// TestProductGroupVariantAttributesNotGuessed: the group variesBy color and has
// two of them. Filling in one because it was listed first is how a list promises
// gray and gets navy.
func TestProductGroupVariantAttributesNotGuessed(t *testing.T) {
	const requested = "https://shop.example.com/p/quilted-cotton-sheet-set/ppr500123"
	page := loadPage(t, "product_group_variants.html", requested, requested)

	res := metadataChain().Run(context.Background(), page)

	if got, ok := res.Attributes["color"]; ok {
		t.Errorf("color = %q, want no color chosen for a multi-variant group", got)
	}
	if res.SKU == "72325920018" {
		t.Error("sku must be the group id, not the first variant's code")
	}
}

// TestProductGroupTakesCheapestRealVariant mirrors what AggregateOffer.lowPrice
// means: the "from" figure. The 0.00 variant must not win it by being smallest.
func TestProductGroupTakesCheapestRealVariant(t *testing.T) {
	const requested = "https://shop.example.com/p/ribbed-knit-throw/ppr500777"
	page := loadPage(t, "product_group_priced.html", requested, requested)

	res := metadataChain().Run(context.Background(), page)

	if res.PriceCents == nil {
		t.Fatal("a group with priced variants should yield a price")
	}
	if *res.PriceCents != 3450 {
		t.Errorf("price = %d cents, want 3450 (cheapest variant that has one)", *res.PriceCents)
	}
	if res.Currency != "USD" {
		t.Errorf("currency = %q, want USD", res.Currency)
	}
	if len(res.ImageURLs) == 0 || !strings.Contains(res.ImageURLs[0], "throw-") {
		t.Errorf("images = %v, want a variant image where the group has none", res.ImageURLs)
	}
}
