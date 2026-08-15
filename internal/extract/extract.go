// Package extract turns a product URL into item fields (plan §5).
package extract

import (
	"context"
	"net/url"
	"strings"
)

// Fields is the extractor output shape from plan §5.3.
type Fields struct {
	Title, Description, Currency string
	PriceCents                   *int64
	ImageURLs                    []string
	SKU, Brand                   string
	Attributes                   map[string]string // color, size, ... when available
	OGType                       string
	Canonical                    string
}

// Extractor is one tier of the chain.
type Extractor interface {
	Name() string
	Applies(*url.URL) bool
	Extract(context.Context, *Page) (Fields, error)
}

// Source names recorded in items.field_sources.
const (
	SourceUser      = "user"
	SourceShopify   = "shopify"
	SourceJSONLD    = "jsonld"
	SourceMicrodata = "microdata"
	SourceOG        = "og"
	SourceSidecar   = "sidecar"
)

// Result is the merged output of the whole chain.
type Result struct {
	Fields
	// Sources maps a field name to the extractor that supplied it. This is
	// what lets a later re-scrape leave human-corrected values alone.
	Sources map[string]string
	// Tried lists the extractors that ran, for diagnostics in the UI.
	Tried []string
	// Errors keyed by extractor name; a failing tier is not fatal.
	Errors map[string]string

	Suspect       bool
	SuspectReason []string
	LinkStatus    string
}

// Chain runs extractors in order, merging field by field.
type Chain struct {
	extractors []Extractor
}

func NewChain(extractors ...Extractor) *Chain {
	return &Chain{extractors: extractors}
}

// Extractors exposes the configured tiers, in order.
func (c *Chain) Extractors() []Extractor { return c.extractors }

// Run executes every applicable extractor against the page.
//
// Merging is per field, not per extractor (plan §5.3): the first tier to
// produce a non-empty title wins the title even if a later tier is the one
// that produced the price.
func (c *Chain) Run(ctx context.Context, page *Page) *Result {
	res := &Result{
		Sources: map[string]string{},
		Errors:  map[string]string{},
	}
	res.Attributes = map[string]string{}

	target := page.FinalURL
	if target == nil {
		target = page.RequestedURL
	}

	for _, e := range c.extractors {
		if target != nil && !e.Applies(target) {
			continue
		}
		// A fallback tier (the sidecar) is skipped when the cheap metadata
		// tiers already produced a usable result — it costs a second network
		// round trip through another process.
		if fb, ok := e.(Fallback); ok && fb.Fallback() && res.usable() {
			continue
		}
		res.Tried = append(res.Tried, e.Name())
		f, err := e.Extract(ctx, page)
		if err != nil {
			res.Errors[e.Name()] = err.Error()
			continue
		}
		res.merge(f, e.Name())
	}
	return res
}

// Fallback is implemented by tiers that should only run when the earlier ones
// came up short.
type Fallback interface {
	Fallback() bool
}

// usable reports whether the result is already good enough to skip fallback
// tiers: a title and a price is what an item needs to be worth showing.
func (r *Result) usable() bool {
	return r.Title != "" && r.PriceCents != nil
}

func (r *Result) merge(f Fields, source string) {
	set := func(field string, dst *string, val string) {
		val = strings.TrimSpace(val)
		if *dst == "" && val != "" {
			*dst = val
			r.Sources[field] = source
		}
	}
	set("title", &r.Title, f.Title)
	set("description", &r.Description, f.Description)
	set("sku", &r.SKU, f.SKU)
	set("brand", &r.Brand, f.Brand)
	set("og_type", &r.OGType, f.OGType)
	set("canonical", &r.Canonical, f.Canonical)

	if r.PriceCents == nil && f.PriceCents != nil {
		r.PriceCents = f.PriceCents
		r.Sources["price_cents"] = source
		if r.Currency == "" && f.Currency != "" {
			r.Currency = strings.ToUpper(f.Currency)
			r.Sources["currency"] = source
		}
	}
	if r.Currency == "" && f.Currency != "" {
		r.Currency = strings.ToUpper(f.Currency)
		r.Sources["currency"] = source
	}
	if len(r.ImageURLs) == 0 && len(f.ImageURLs) > 0 {
		r.ImageURLs = f.ImageURLs
		r.Sources["images"] = source
	}
	for k, v := range f.Attributes {
		if v = strings.TrimSpace(v); v != "" {
			if _, exists := r.Attributes[k]; !exists {
				r.Attributes[k] = v
				r.Sources["attr:"+k] = source
			}
		}
	}
}

// dedupeStrings keeps order and drops blanks and repeats.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
