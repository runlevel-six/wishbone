package extract

import (
	"context"
	"net/url"
	"strings"
)

// Microdata is tier 3: itemtype="…schema.org/Product".
//
// Scope note: only the head is fetched and parsed (plan §5.2), so this tier
// sees head-level <meta itemprop="…"> tags, which is the form most retailers
// that still ship microdata use. Body-level itemprop spans are out of reach by
// design — the alternative is parsing megabytes of page for a tier that JSON-LD
// has largely replaced.
type Microdata struct{}

func (Microdata) Name() string          { return SourceMicrodata }
func (Microdata) Applies(*url.URL) bool { return true }

func (Microdata) Extract(_ context.Context, p *Page) (Fields, error) {
	f := Fields{Attributes: map[string]string{}}
	if !p.hasProductItemType() && p.MetaItemProp("price", "name") == "" {
		return f, nil
	}

	f.Title = p.MetaItemProp("name")
	f.Description = p.MetaItemProp("description")
	f.SKU = firstNonEmpty(p.MetaItemProp("sku"), p.MetaItemProp("mpn"))
	f.Brand = p.MetaItemProp("brand")
	if c := p.MetaItemProp("color"); c != "" {
		f.Attributes["color"] = c
	}
	if s := p.MetaItemProp("size"); s != "" {
		f.Attributes["size"] = s
	}
	if img := p.MetaItemProp("image"); img != "" {
		if u := p.ResolveURL(img); u != "" {
			f.ImageURLs = append(f.ImageURLs, u)
		}
	}
	if price := p.MetaItemProp("price", "lowprice"); price != "" {
		f.PriceCents, f.Currency = ParsePriceCents(price)
		if cur := NormalizeCurrency(p.MetaItemProp("pricecurrency")); cur != "" {
			f.Currency = cur
		}
	}
	return f, nil
}

func (p *Page) hasProductItemType() bool {
	for _, t := range p.ItemTypes {
		if strings.Contains(strings.ToLower(t), "schema.org/product") {
			return true
		}
	}
	return false
}
