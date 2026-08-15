package extract

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

var errUnsupportedScheme = errors.New("extract: unsupported URL scheme")

// Meta is one <meta> tag from the document head.
type Meta struct {
	Property string // property="og:title"
	Name     string // name="description"
	ItemProp string // itemprop="price"
	Content  string
}

// Page is the parsed head of a fetched document.
//
// Only the head is parsed (plan §5.2): product pages are routinely megabytes
// below the fold and none of it carries the metadata we want.
type Page struct {
	RequestedURL *url.URL
	FinalURL     *url.URL
	StatusCode   int

	Title     string
	Canonical string
	Metas     []Meta
	JSONLD    []string
	ItemTypes []string
}

// ParseHead streams HTML and stops at </head>.
func ParseHead(r io.Reader) (*Page, error) {
	p := &Page{}
	z := html.NewTokenizer(r)
	// A head that never closes is a malformed page; cap the work either way.
	z.SetMaxBuf(1 << 20)

	inTitle := false
	var scriptType string
	var scriptBuf bytes.Buffer
	inLDScript := false

	for {
		switch z.Next() {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return p, nil
			}
			// Truncated or malformed input still yields whatever we collected.
			return p, nil

		case html.TextToken:
			if inTitle {
				p.Title += string(z.Text())
			}
			if inLDScript {
				scriptBuf.Write(z.Text())
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[strings.ToLower(string(k))] = string(v)
			}

			switch tag {
			case "title":
				inTitle = true
			case "meta":
				p.Metas = append(p.Metas, Meta{
					Property: strings.ToLower(attrs["property"]),
					Name:     strings.ToLower(attrs["name"]),
					ItemProp: strings.ToLower(attrs["itemprop"]),
					Content:  attrs["content"],
				})
			case "link":
				if strings.EqualFold(attrs["rel"], "canonical") && attrs["href"] != "" {
					p.Canonical = attrs["href"]
				}
			case "script":
				scriptType = strings.ToLower(strings.TrimSpace(attrs["type"]))
				if scriptType == "application/ld+json" {
					inLDScript = true
					scriptBuf.Reset()
				}
			case "body":
				// Some pages omit </head> entirely; <body> ends it just as well.
				return p, nil
			}
			if it := attrs["itemtype"]; it != "" {
				p.ItemTypes = append(p.ItemTypes, it)
			}

		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "script":
				if inLDScript {
					p.JSONLD = append(p.JSONLD, scriptBuf.String())
					inLDScript = false
					scriptBuf.Reset()
				}
			case "head":
				return p, nil
			}
		}
	}
}

// Meta lookups.

func (p *Page) MetaProperty(names ...string) string {
	for _, want := range names {
		for _, m := range p.Metas {
			if m.Property == want && strings.TrimSpace(m.Content) != "" {
				return strings.TrimSpace(m.Content)
			}
		}
	}
	return ""
}

func (p *Page) MetaName(names ...string) string {
	for _, want := range names {
		for _, m := range p.Metas {
			if m.Name == want && strings.TrimSpace(m.Content) != "" {
				return strings.TrimSpace(m.Content)
			}
		}
	}
	return ""
}

func (p *Page) MetaItemProp(names ...string) string {
	for _, want := range names {
		for _, m := range p.Metas {
			if m.ItemProp == want && strings.TrimSpace(m.Content) != "" {
				return strings.TrimSpace(m.Content)
			}
		}
	}
	return ""
}

// ResolveURL makes a possibly-relative URL from the page absolute.
func (p *Page) ResolveURL(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	base := p.FinalURL
	if base == nil {
		base = p.RequestedURL
	}
	if base == nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}
