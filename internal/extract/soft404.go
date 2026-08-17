package extract

import (
	"net/url"
	"strconv"
	"strings"

	"wishbone/internal/model"
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

	// An error status splits two ways, and conflating them tells people the
	// wrong thing about a link that is fine.
	//
	// 404 and 410 are the retailer saying the thing is not there. That is
	// evidence about the link, and the strongest kind.
	//
	// 403, 429 and the rest are the retailer declining to talk to *us*. Some
	// answer 403 to the Go fetcher and to the sidecar alike, from the same
	// address whose browser opens the page fine. Calling that link dead is a
	// lie about the person's link, and it sends them off to check something
	// that was never wrong.
	if page.StatusCode >= 400 {
		switch page.StatusCode {
		case 404, 410:
			res.Suspect = true
			res.SuspectReason = append(res.SuspectReason, "the page is gone (the shop returned "+
				strconv.Itoa(page.StatusCode)+")")
			res.LinkStatus = model.LinkDead
		default:
			// Nothing was learned about the link, so nothing is claimed about
			// it. Whatever the chain scraped off a block page is discarded:
			// those pages carry meta tags too, and an item titled "Access
			// Denied" is the confidently-wrong-item failure with a new cause.
			res.Blocked = true
			res.BlockedStatus = page.StatusCode
			res.Fields = Fields{}
			res.Sources = map[string]string{}
			res.LinkStatus = model.LinkUnknown
		}
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

// CanonicalAlternative returns the address a page claims for itself, resolved
// against the address actually fetched and normalized, when that claim is a
// plausible thing to look up instead. It returns "" when there is nothing
// worth offering: no canonical, one that normalizes back to the address
// already fetched, or one on another site.
//
// It exists for the case the guard fires on most often. A live product page
// names a different product as canonical — a large marketplace collapsing
// every size and color of one garment onto whichever listing it indexes,
// which is the usual reason. Sometimes that sibling
// is what the person actually wants; sometimes it is a different size and
// buying it would be the wrong gift. Nothing here decides which. The owner is
// offered the address and picks.
//
// Same host only. The re-lookup runs through the ordinary fetch path with its
// address guard, but a canonical tag is input written by the page being
// examined, and no honest one points at another site.
func CanonicalAlternative(canonical, fetched string) string {
	return sameSiteAlternative(canonical, fetched)
}

// sameSiteAlternative resolves an address a page named for itself against the
// address actually fetched, and returns it normalized — or "" when it is not a
// different address on the same site.
//
// Shared by the two things a page can say about where it really lives: the
// canonical tag above, and the streamed redirect in flightredirect.go. Both are
// input written by the page being examined, and the same-host rule is what
// keeps either from sending the fetcher somewhere else entirely.
func sameSiteAlternative(ref, fetched string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || fetched == "" {
		return ""
	}
	base, err := url.Parse(fetched)
	if err != nil {
		return ""
	}
	// Resolved against the base so a relative reference — common enough —
	// works, and so a scheme-relative one cannot change host silently.
	cu, err := base.Parse(ref)
	if err != nil || !strings.EqualFold(cu.Host, base.Host) {
		return ""
	}
	normalized, err := NormalizeURL(cu.String())
	if err != nil || normalized == "" || normalized == fetched {
		return ""
	}
	return normalized
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
// handle, slug or listing id. Marketplaces append navigation state after that
// id, so a "contains" comparison is used rather than equality.
func identifyingSegment(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		// Skip trailing ref= navigation crumbs.
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
