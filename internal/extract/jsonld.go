package extract

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// JSONLD is tier 2: application/ld+json blocks with @type Product or
// ProductGroup.
type JSONLD struct{}

func (JSONLD) Name() string          { return SourceJSONLD }
func (JSONLD) Applies(*url.URL) bool { return true }

func (JSONLD) Extract(_ context.Context, p *Page) (Fields, error) {
	f := Fields{Attributes: map[string]string{}}
	for _, node := range jsonldProducts(p) {
		applyProduct(p, node, &f)
		if f.Title != "" {
			return f, nil
		}
	}
	return f, nil
}

// jsonldProducts returns every Product node in the document's JSON-LD, in the
// order they should be trusted.
//
// Document order is the default and is usually right: a page's own data is
// published before the carousel of things you might also like. It stopped
// being sufficient once the body was parsed too, because a recommendation
// block is JSON-LD as legitimate-looking as the page's own, and taking the
// wrong one produces a complete, confident, wrong item — the failure the
// soft-404 guard exists to prevent, arriving from inside the page.
//
// So a node that claims this page's address is promoted ahead of the rest.
// Nothing is discarded on that basis: a page whose Product carries no @id at
// all is ordinary, and demoting it to nothing would lose more than it saves.
func jsonldProducts(p *Page) []map[string]any {
	want := pageAddress(p)
	var owned, others []map[string]any

	// Script tags first, then whatever was recoverable from a framework's
	// hydration payload. A real tag is the page saying this plainly and
	// outranks anything reconstructed; the recovery happens here rather than
	// at the call site so that running the chain is all anyone has to remember
	// to do.
	docs := append(append([]string(nil), p.JSONLD...),
		recoverFlightJSONLD(p.FlightChunks, want)...)

	for _, raw := range docs {
		var doc any
		if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &doc); err != nil {
			continue // one malformed block must not fail the tier
		}
		for _, node := range flattenLD(doc) {
			if !isProductNode(node) {
				continue
			}
			claimed := nodeURL(node)
			if want != nil && claimed != "" && sameProductAddress(claimed, want) {
				owned = append(owned, node)
				continue
			}
			others = append(others, node)
		}
	}
	return append(owned, others...)
}

// flattenLD walks @graph arrays and nested lists into a flat list of objects.
func flattenLD(doc any) []map[string]any {
	var out []map[string]any
	switch v := doc.(type) {
	case map[string]any:
		out = append(out, v)
		if g, ok := v["@graph"]; ok {
			out = append(out, flattenLD(g)...)
		}
		// Product sometimes hangs off mainEntity on a WebPage node.
		if me, ok := v["mainEntity"]; ok {
			out = append(out, flattenLD(me)...)
		}
	case []any:
		for _, e := range v {
			out = append(out, flattenLD(e)...)
		}
	}
	return out
}

func isType(node map[string]any, want string) bool {
	t, ok := node["@type"]
	if !ok {
		return false
	}
	for _, s := range stringsOf(t) {
		if strings.EqualFold(strings.TrimPrefix(s, "schema:"), want) {
			return true
		}
	}
	return false
}

// isProductNode reports whether a JSON-LD node describes the thing on sale.
//
// ProductGroup is the type a retailer publishes when one page sells one garment
// in ten colors: the group carries the name, brand and group id, and each
// color hangs off hasVariant as a Product of its own. Requiring @type Product
// skipped those pages entirely — no title, no brand, no code — and an empty
// structured tier then reads downstream as "no structured product data here",
// which is how a real product page ends up warned about as not being one.
func isProductNode(node map[string]any) bool {
	return isType(node, "Product") || isType(node, "ProductGroup")
}

func applyProduct(p *Page, node map[string]any, f *Fields) {
	f.Title = firstNonEmpty(f.Title, str(node["name"]))
	f.Description = firstNonEmpty(f.Description, str(node["description"]))
	// productGroupID identifies the group the way sku identifies a variant. It
	// is preferred over any one variant's sku, which would name a color nobody
	// chose.
	f.SKU = firstNonEmpty(f.SKU, str(node["sku"]), str(node["mpn"]), str(node["productGroupID"]))
	f.Brand = firstNonEmpty(f.Brand, brandName(node["brand"]))
	if c := str(node["color"]); c != "" {
		f.Attributes["color"] = c
	}
	if s := str(node["size"]); s != "" {
		f.Attributes["size"] = s
	}
	if m := str(node["material"]); m != "" {
		f.Attributes["material"] = m
	}

	for _, img := range stringsOf(node["image"]) {
		if u := p.ResolveURL(img); u != "" {
			f.ImageURLs = append(f.ImageURLs, u)
		}
	}
	// image can also be an ImageObject (or a list of them).
	for _, obj := range objectsOf(node["image"]) {
		if u := p.ResolveURL(str(obj["url"])); u != "" {
			f.ImageURLs = append(f.ImageURLs, u)
		}
	}
	f.ImageURLs = dedupeStrings(f.ImageURLs)

	applyOffers(node, f)
	applyVariants(p, node, f)
}

func applyOffers(node map[string]any, f *Fields) {
	for _, offer := range append(objectsOf(node["offers"]), objectsOf(node["Offers"])...) {
		if isType(offer, "AggregateOffer") {
			setPrice(f, firstNonEmpty(str(offer["lowPrice"]), str(offer["price"])))
		} else {
			setPrice(f, str(offer["price"]))
		}
		if cur := NormalizeCurrency(str(offer["priceCurrency"])); cur != "" && f.Currency == "" {
			f.Currency = cur
		}
		if f.PriceCents != nil && f.Currency != "" {
			break
		}
	}
}

// applyVariants reads what a ProductGroup keeps in hasVariant rather than on
// itself: an image, and the cheapest price actually being asked for one of the
// variants — the same "from" figure AggregateOffer.lowPrice carries, and the
// only honest single number for a page selling ten things at possibly ten
// prices.
//
// Variant attributes are deliberately not merged. A group that variesBy color
// has no color, and picking one because it was listed first is how a wishlist
// promises gray and gets navy. The exception is a group with exactly one
// variant, where there is nothing to guess between.
func applyVariants(p *Page, node map[string]any, f *Fields) {
	variants := objectsOf(node["hasVariant"])
	if len(variants) == 0 {
		return
	}
	if len(variants) == 1 {
		applyProduct(p, variants[0], f)
		return
	}
	for _, v := range variants {
		var cheapest Fields
		applyOffers(v, &cheapest)
		if cheapest.PriceCents != nil && (f.PriceCents == nil || *cheapest.PriceCents < *f.PriceCents) {
			f.PriceCents = cheapest.PriceCents
		}
		if f.Currency == "" {
			f.Currency = cheapest.Currency
		}
		if len(f.ImageURLs) == 0 {
			for _, img := range stringsOf(v["image"]) {
				if u := p.ResolveURL(img); u != "" {
					f.ImageURLs = append(f.ImageURLs, u)
				}
			}
		}
	}
}

// setPrice records a price only if it is a real one, and never overwrites a
// price already found.
//
// A published "0.00" is not a free product, it is the field a retailer fills in
// when the real number is fetched by script after the page loads. Taking it
// literally puts a $0.00 item on a wishlist, which reads as a bug to everyone
// who sees it and is worse than the honest blank the guard would otherwise
// prompt about.
func setPrice(f *Fields, raw string) {
	if f.PriceCents != nil || strings.TrimSpace(raw) == "" {
		return
	}
	cents, currency := ParsePriceCents(raw)
	if cents == nil || *cents <= 0 {
		return
	}
	f.PriceCents = cents
	// A symbol in the amount ("$24.99") names the currency as well as any
	// priceCurrency field does, and some offers carry only the one.
	if f.Currency == "" {
		f.Currency = NormalizeCurrency(currency)
	}
}

func brandName(v any) string {
	switch b := v.(type) {
	case string:
		return b
	case map[string]any:
		return str(b["name"])
	case []any:
		for _, e := range b {
			if n := brandName(e); n != "" {
				return n
			}
		}
	}
	return ""
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s)
	case float64:
		return trimFloat(s)
	case json.Number:
		return s.String()
	case []any:
		if len(s) > 0 {
			return str(s[0])
		}
	}
	return ""
}

func trimFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func stringsOf(v any) []string {
	switch s := v.(type) {
	case string:
		return []string{s}
	case []any:
		var out []string
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func objectsOf(v any) []map[string]any {
	switch o := v.(type) {
	case map[string]any:
		return []map[string]any{o}
	case []any:
		var out []map[string]any
		for _, e := range o {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}
