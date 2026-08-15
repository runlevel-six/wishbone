package extract

import (
	"context"
	"net/url"
	"strings"
)

// OpenGraph is tier 4. It also supplies og:type, which the soft-404 guard
// depends on.
type OpenGraph struct{}

func (OpenGraph) Name() string          { return SourceOG }
func (OpenGraph) Applies(*url.URL) bool { return true }

func (OpenGraph) Extract(_ context.Context, p *Page) (Fields, error) {
	f := Fields{Attributes: map[string]string{}}

	// Deliberately no <title> fallback (plan §5.3): page titles are SEO copy
	// far more often than they are product names.
	f.Title = p.MetaProperty("og:title")
	f.Description = firstNonEmpty(p.MetaProperty("og:description"), p.MetaName("description"))
	f.OGType = strings.ToLower(p.MetaProperty("og:type"))
	f.Canonical = p.Canonical

	// og:image:secure_url before og:image: the plain form is frequently http,
	// and we refuse to hotlink or downgrade.
	var images []string
	for _, m := range p.Metas {
		switch m.Property {
		case "og:image:secure_url", "og:image:url", "og:image":
			if u := p.ResolveURL(m.Content); u != "" {
				images = append(images, u)
			}
		}
	}
	// Stable preference order rather than document order.
	f.ImageURLs = dedupeStrings(append(
		imagesWithProperty(p, "og:image:secure_url"),
		images...))

	// Currency is read independently of the amount: a storefront whose price
	// comes from another tier (Shopify's product JSON carries none) may still
	// declare its currency here, and merging is per field.
	f.Currency = NormalizeCurrency(p.MetaProperty("product:price:currency", "og:price:currency"))
	if amt := p.MetaProperty("product:price:amount", "og:price:amount"); amt != "" {
		cents, cur := ParsePriceCents(amt)
		f.PriceCents = cents
		f.Currency = firstNonEmpty(f.Currency, cur)
	}
	if sku := p.MetaProperty("product:retailer_item_id", "product:sku"); sku != "" {
		f.SKU = sku
	}
	if brand := p.MetaProperty("product:brand", "og:brand"); brand != "" {
		f.Brand = brand
	}
	if color := p.MetaProperty("product:color"); color != "" {
		f.Attributes["color"] = color
	}
	if size := p.MetaProperty("product:size"); size != "" {
		f.Attributes["size"] = size
	}
	return f, nil
}

func imagesWithProperty(p *Page, prop string) []string {
	var out []string
	for _, m := range p.Metas {
		if m.Property == prop {
			if u := p.ResolveURL(m.Content); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
