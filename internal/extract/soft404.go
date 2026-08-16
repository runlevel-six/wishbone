package extract

import (
	"net/url"
	"strings"

	"wishd/internal/model"
)

// ApplySoft404Guard runs after the chain, over whatever it produced (plan §5.4).
//
// Motivating case, verified against a real dead product link: the retailer
// returned HTTP 200 with valid OpenGraph tags describing the *parent
// collection page*. Every tier "succeeded". A naive extractor produces a
// confidently wrong item, and a confidently wrong item is a wrong gift.
func ApplySoft404Guard(res *Result, page *Page) {
	res.LinkStatus = model.LinkOK

	requested := page.RequestedURL
	final := page.FinalURL
	if final == nil {
		final = requested
	}

	if page.StatusCode >= 400 {
		res.Suspect = true
		res.SuspectReason = append(res.SuspectReason, "the page returned an error status")
		res.LinkStatus = model.LinkDead
		return
	}

	// 1. og:type present and not a product.
	//
	// On its own this is weak evidence. Plenty of storefronts — particularly
	// single-page ones — emit og:type "website" on every page including real
	// product pages, and firing on that alone produces a warning over a
	// perfectly good extraction. A guard that cries wolf gets clicked through,
	// which costs you the case it exists for.
	//
	// So it only counts when the page has not also published structured
	// product data at the address that was asked for. Structured data is much
	// stronger evidence than an OpenGraph type: a schema.org Product node with
	// a name and a SKU or price is a deliberate machine-readable claim, while
	// og:type is frequently boilerplate.
	if res.OGType != "" && res.OGType != "product" && !strings.HasPrefix(res.OGType, "product.") {
		if !hasStructuredProduct(res) {
			res.Suspect = true
			res.SuspectReason = append(res.SuspectReason,
				"the page describes itself as \""+res.OGType+"\", not a product")
		}
	}

	// 2. The final URL or canonical differs structurally from what was asked
	//    for — the /products/… to /collections/… slide.
	if requested != nil {
		reqID := identifyingSegment(requested.Path)
		if reqID != "" {
			if final != nil && !pathContains(final.Path, reqID) {
				res.Suspect = true
				res.SuspectReason = append(res.SuspectReason,
					"the link redirected to a different page ("+final.Path+")")
			} else if res.Canonical != "" {
				if cu, err := url.Parse(res.Canonical); err == nil && cu.Path != "" &&
					!pathContains(cu.Path, reqID) {
					res.Suspect = true
					res.SuspectReason = append(res.SuspectReason,
						"the page says its canonical address is "+cu.Path)
				}
			}
		}
		if isProductPath(requested.Path) && final != nil && !isProductPath(final.Path) {
			res.Suspect = true
			res.SuspectReason = append(res.SuspectReason,
				"a product link resolved to a non-product page")
		}
	}

	// 3. No price and no SKU on a host where the chain normally finds them.
	//    "Normally" means a tier that reliably yields both actually ran.
	if res.PriceCents == nil && res.SKU == "" && ranReliableTier(res) {
		res.Suspect = true
		res.SuspectReason = append(res.SuspectReason,
			"no price or product code was found where one was expected")
	}

	if res.Suspect {
		res.LinkStatus = model.LinkSuspect
	}
	res.SuspectReason = dedupeStrings(res.SuspectReason)
}

// hasStructuredProduct reports whether a structured-data tier — not
// OpenGraph — supplied the title, together with a SKU or a price. That
// combination is a page asserting "this is a specific purchasable product",
// which outweighs a sloppy og:type.
//
// The URL-shape signals below are deliberately not softened by this: if the
// address moved, structured data on the page you landed on describes the wrong
// product, and being confident about the wrong thing is the failure mode this
// whole guard exists to prevent.
func hasStructuredProduct(res *Result) bool {
	switch res.Sources["title"] {
	case SourceShopify, SourceJSONLD, SourceMicrodata:
	default:
		return false
	}
	return res.SKU != "" || res.PriceCents != nil
}

// ranReliableTier reports whether a tier ran that ordinarily produces a price
// or a SKU for a real product page.
func ranReliableTier(res *Result) bool {
	for _, t := range res.Tried {
		switch t {
		case SourceShopify, SourceJSONLD, SourceMicrodata:
			// Only counts if the tier produced *something*, otherwise a site
			// that simply has no structured data would be flagged forever.
			if res.Title != "" && res.Sources["title"] == t {
				return true
			}
		}
	}
	return false
}

// identifyingSegment returns the last meaningful path segment — the product
// handle, slug or ASIN. Amazon appends navigation state after the ASIN, so a
// "contains" comparison is used rather than equality.
func identifyingSegment(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		// Skip Amazon's trailing ref= crumbs.
		if strings.HasPrefix(p, "ref=") {
			continue
		}
		return strings.ToLower(p)
	}
	return ""
}

func pathContains(path, segment string) bool {
	for _, p := range strings.Split(strings.Trim(strings.ToLower(path), "/"), "/") {
		if p == segment {
			return true
		}
	}
	return false
}

func isProductPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "/products/") ||
		strings.Contains(lower, "/product/") ||
		strings.Contains(lower, "/dp/") ||
		strings.Contains(lower, "/gp/product/")
}
