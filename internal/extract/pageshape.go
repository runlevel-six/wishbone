package extract

import (
	"net/url"
	"strings"
)

// Not every address someone pastes names a product.
//
// A search results page, a brand or department listing, a cart — these are
// pages about many products, or about none. No extractor tier can turn one into
// an item, and the tiers report that in the least useful way available: they
// find no title and no price, and the form comes back blank. That reads exactly
// like a lookup that failed on a good link, so the natural response is to try
// again, which cannot work either.
//
// The production log is what prompted this: a brand listing pasted three times
// in seven minutes, and a search address whose own path still held the terms
// somebody had typed into the shop. Nothing was ever going to come back from
// either, and nothing told the person why — so they tried again.
//
// Detection is deliberately host-keyed rather than clever. A generic rule for a
// one-letter segment like "s" or "b" would fire on real product addresses
// elsewhere, and a wrong "that is not a product" is worse than the blank form it
// replaces: it tells somebody their good link is bad, about the one part of this
// that was never wrong. Hosts whose scheme is known are listed; everything else
// falls through to an ordinary lookup.
type PageShape string

const (
	// ShapeSearch is a results page for terms somebody typed.
	ShapeSearch PageShape = "search"
	// ShapeCategory is a brand, department or category listing.
	ShapeCategory PageShape = "category"
	// ShapeCart is a cart or checkout page, which is also nobody's wish.
	ShapeCart PageShape = "cart"
)

// Label names the shape the way the person reading it would name it.
func (s PageShape) Label() string {
	switch s {
	case ShapeSearch:
		return "a search results page"
	case ShapeCategory:
		return "a category or brand listing"
	case ShapeCart:
		return "a shopping cart"
	default:
		return ""
	}
}

// universalSegments are first path segments that no shop uses for a product.
// Spelled-out English words, so they carry their meaning on any host and are
// safe to apply to hosts this file has never heard of.
var universalSegments = map[string]PageShape{
	"search":   ShapeSearch,
	"cart":     ShapeCart,
	"checkout": ShapeCart,
	"basket":   ShapeCart,
}

// siteSegments maps a base host to the abbreviated path segments that host uses
// for listings. These are short enough to mean something else elsewhere, which
// is exactly why they are scoped to the host that defines them.
//
// A product segment is never listed, and that is the point of the table. Each
// host below serves its products from some other path, so a product address
// finds no entry here and falls straight through to an ordinary lookup.
var siteSegments = map[string]map[string]PageShape{
	"homedepot.com": {
		"s": ShapeSearch,   // /s/<terms>
		"b": ShapeCategory, // /b/<brand>/N-<node>
	},
	"amazon.com": {
		"s":         ShapeSearch,   // /s?k=<terms>
		"b":         ShapeCategory, // /b?node=<id>
		"gp/browse": ShapeCategory,
		"gp/search": ShapeSearch,
	},
	"target.com": {
		"s": ShapeSearch,
		"c": ShapeCategory,
	},
	"walmart.com": {
		"browse": ShapeCategory,
		"cp":     ShapeCategory,
	},
	"lowes.com": {
		"pl": ShapeCategory,
	},
	"ebay.com": {
		"sch": ShapeSearch,
		"b":   ShapeCategory,
	},
	"etsy.com": {
		"c": ShapeCategory,
	},
}

// ClassifyNonProduct names what an address obviously is when it obviously is
// not a product page. It returns "" when the address might name a product,
// which includes every host not in the table — silence means "go and look",
// never "this is fine".
func ClassifyNonProduct(raw string) PageShape {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	segs := pathSegments(u.Path)
	if len(segs) == 0 {
		// A bare host is not a product either, but saying so is not useful and
		// the fetch is harmless: a shop's front page answers, and the guard that
		// already exists calls the result suspect rather than filling anything in.
		return ""
	}

	if shape, ok := universalSegments[segs[0]]; ok {
		return shape
	}

	rules, ok := siteSegments[baseHost(u.Host)]
	if !ok {
		return ""
	}
	// Two segments before one: "gp/browse" must win over any rule for "gp".
	if len(segs) >= 2 {
		if shape, ok := rules[segs[0]+"/"+segs[1]]; ok {
			return shape
		}
	}
	return rules[segs[0]]
}

// pathSegments splits a URL path into its non-empty, lowercased segments, for
// matching against the tables above.
func pathSegments(p string) []string {
	out := rawPathSegments(p)
	for i, s := range out {
		out[i] = strings.ToLower(s)
	}
	return out
}

// rawPathSegments is pathSegments with the case left alone, for callers reading
// a segment as words rather than matching it against a table — a shop writes
// "SOMEBRAND-ATOMIC-20V" and lowercasing that loses information no rule can
// restore.
func rawPathSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// baseHost drops a leading "www." and any country suffix the Amazon hosts wear,
// so one table entry covers the whole family of a shop's addresses.
func baseHost(host string) string {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimPrefix(host, "www.")
	if isAmazonHost(host) {
		return "amazon.com"
	}
	return host
}
