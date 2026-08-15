package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"wishd/internal/fetch"
)

// Shopify is tier 1: one adapter that covers a large share of independent
// retailers with no per-site selectors, because every Shopify storefront
// serves {product-url}.json (plan §5.3).
type Shopify struct {
	Client PageGetter
}

// PageGetter is the slice of the fetch client this package needs. Keeping it
// an interface lets tests supply a loopback-permitted client.
type PageGetter interface {
	Get(ctx context.Context, rawURL, accept, wantPrefix string, maxBytes int64) (*fetch.Response, error)
}

func (Shopify) Name() string { return SourceShopify }

// Applies matches the /products/{handle} path shape Shopify always uses.
func (Shopify) Applies(u *url.URL) bool {
	if u == nil {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "products" && i+1 < len(parts) && parts[i+1] != "" {
			return true
		}
	}
	return false
}

type shopifyProduct struct {
	Product struct {
		Title    string `json:"title"`
		BodyHTML string `json:"body_html"`
		Vendor   string `json:"vendor"`
		Handle   string `json:"handle"`
		Options  []struct {
			Name   string   `json:"name"`
			Values []string `json:"values"`
		} `json:"options"`
		Variants []struct {
			Title          string `json:"title"`
			SKU            string `json:"sku"`
			Price          string `json:"price"`
			CompareAtPrice string `json:"compare_at_price"`
			Option1        string `json:"option1"`
			Option2        string `json:"option2"`
			Option3        string `json:"option3"`
			Available      bool   `json:"available"`
		} `json:"variants"`
		Images []struct {
			Src string `json:"src"`
		} `json:"images"`
	} `json:"product"`
}

func (s Shopify) Extract(ctx context.Context, p *Page) (Fields, error) {
	f := Fields{Attributes: map[string]string{}}
	if s.Client == nil {
		return f, nil
	}
	target := p.FinalURL
	if target == nil {
		target = p.RequestedURL
	}
	if target == nil {
		return f, nil
	}

	jsonURL := *target
	jsonURL.RawQuery = ""
	jsonURL.Fragment = ""
	jsonURL.Path = strings.TrimSuffix(jsonURL.Path, "/") + ".json"

	resp, err := s.Client.Get(ctx, jsonURL.String(), "application/json", "", 1<<20)
	if err != nil {
		return f, err
	}
	if resp.StatusCode != 200 {
		return f, fmt.Errorf("shopify: status %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(resp.ContentType), "json") {
		// A storefront that isn't Shopify commonly answers .json with HTML.
		return f, fmt.Errorf("shopify: not a product JSON endpoint")
	}

	var doc shopifyProduct
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return f, fmt.Errorf("shopify: %w", err)
	}
	pr := doc.Product
	if pr.Title == "" {
		return f, fmt.Errorf("shopify: no product in response")
	}

	f.Title = pr.Title
	f.Description = htmlToText(pr.BodyHTML)
	f.Brand = pr.Vendor
	for _, img := range pr.Images {
		if u := p.ResolveURL(img.Src); u != "" {
			f.ImageURLs = append(f.ImageURLs, u)
		}
	}
	f.ImageURLs = dedupeStrings(f.ImageURLs)

	// Price: the cheapest available variant is the honest headline number.
	for _, v := range pr.Variants {
		cents, _ := ParsePriceCents(v.Price)
		if cents == nil {
			continue
		}
		if f.PriceCents == nil || *cents < *f.PriceCents {
			f.PriceCents = cents
		}
	}
	// Shopify's product JSON carries no currency; a later tier supplies it.

	if len(pr.Variants) == 1 {
		v := pr.Variants[0]
		f.SKU = v.SKU
		// With exactly one variant the option values describe the product
		// itself. With several, guessing which one the recipient wants is
		// precisely the mistake that produces a wrong gift.
		for i, opt := range pr.Options {
			var val string
			switch i {
			case 0:
				val = v.Option1
			case 1:
				val = v.Option2
			case 2:
				val = v.Option3
			}
			if key := attrKeyFor(opt.Name); key != "" && val != "" {
				f.Attributes[key] = val
			}
		}
	}
	return f, nil
}

// attrKeyFor maps a Shopify option name onto one of our category field keys.
func attrKeyFor(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "size", "sizes":
		return "size"
	case "color", "colour":
		return "color"
	case "material":
		return "material"
	case "style", "fit":
		return "fit"
	case "capacity":
		return "capacity"
	case "format":
		return "format"
	default:
		return ""
	}
}

// htmlToText flattens a body_html blob to readable text.
func htmlToText(in string) string {
	if strings.TrimSpace(in) == "" {
		return ""
	}
	z := html.NewTokenizer(strings.NewReader(in))
	var b strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			return collapseSpace(b.String())
		case html.TextToken:
			b.Write(z.Text())
		case html.StartTagToken, html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "p", "br", "li", "div", "tr":
				b.WriteString("\n")
			}
		}
		if b.Len() > 8000 {
			return collapseSpace(b.String())
		}
	}
}

func collapseSpace(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
