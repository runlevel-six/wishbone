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

// Page is the metadata of a fetched document.
//
// Originally this was the parsed <head> alone (plan §5.2), on the reasoning
// that product pages are megabytes below the fold and none of it carries what
// we want. The second half of that is no longer true, and the reasoning had a
// hidden assumption: that a document's metadata is in its head.
//
// On a framework that streams metadata — Next.js App Router, and it is not
// alone — the head is a stub. React emits <title>, <meta> and <link rel=
// canonical> inline in the body as the response streams, and hoists them into
// the head when it hydrates. A department store chain's product page is the
// worked example: a 16KB head, and every piece of metadata at byte 1,083,000
// of 1,132,379. Parsing only the head there returns an empty Page from a
// perfectly good 200, and every tier downstream correctly reports nothing.
//
// So the whole document is scanned. The cost is bounded where it was always
// actually bounded — MaxPageBytes, enforced by the fetcher before a byte of
// this runs — so stopping at <body> was never saving a fetch, only skipping
// part of a buffer already in memory.
type Page struct {
	RequestedURL *url.URL
	FinalURL     *url.URL
	StatusCode   int

	Title     string
	Canonical string
	Metas     []Meta
	JSONLD    []string
	ItemTypes []string

	// FlightChunks holds the inline scripts of a React Server Components
	// payload, in document order. See nextflight.go for what is done with
	// them and why they are not JSON-LD yet.
	FlightChunks []string
}

// ParseDocument streams HTML and collects metadata from the whole document.
//
// Scanning past </head> means competing with the body for two fields that used
// to be unambiguous, so both take the first value found rather than the last:
// in a document that has a real head, the head still wins, and in one that
// does not, the streamed metadata beats anything further down.
func ParseDocument(r io.Reader) (*Page, error) {
	p := &Page{}
	z := html.NewTokenizer(r)
	// One token cannot exceed the document, and the document is already capped
	// by the fetcher. A single inline script — a flight payload chunk, most
	// likely — is the token that gets close.
	z.SetMaxBuf(MaxPageBytes)

	inTitle := false
	titleDone := false
	// <title> is not only a head tag: inline SVG icons carry one for
	// accessibility, and there are dozens in a storefront's body. Taking one
	// of those as the page title would be a new way to be confidently wrong,
	// so titles inside an <svg> are skipped entirely.
	svgDepth := 0
	var scriptBuf bytes.Buffer
	inScript := false
	inLDScript := false

	for {
		tt := z.Next()
		switch tt {
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
			if inScript {
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

			// A self-closing <svg/> or <script/> never gets an end tag, so
			// letting either open a state here would strand it open for the
			// rest of the document.
			selfClosing := tt == html.SelfClosingTagToken

			switch tag {
			case "svg":
				if !selfClosing {
					svgDepth++
				}
			case "title":
				if !selfClosing && svgDepth == 0 && !titleDone {
					inTitle = true
				}
			case "meta":
				p.Metas = append(p.Metas, Meta{
					Property: strings.ToLower(attrs["property"]),
					Name:     strings.ToLower(attrs["name"]),
					ItemProp: strings.ToLower(attrs["itemprop"]),
					Content:  attrs["content"],
				})
			case "link":
				if strings.EqualFold(attrs["rel"], "canonical") && attrs["href"] != "" &&
					p.Canonical == "" {
					p.Canonical = attrs["href"]
				}
			case "script":
				// Every inline script is buffered now, not only ld+json: which
				// ones matter cannot be told from the start tag, because a
				// flight payload chunk is an ordinary untyped <script>. What
				// it is gets decided at </script>, on its content.
				if !selfClosing {
					inScript = true
					inLDScript = strings.EqualFold(
						strings.TrimSpace(attrs["type"]), "application/ld+json")
					scriptBuf.Reset()
				}
			}
			if it := attrs["itemtype"]; it != "" {
				p.ItemTypes = append(p.ItemTypes, it)
			}

		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "svg":
				if svgDepth > 0 {
					svgDepth--
				}
			case "title":
				if inTitle {
					inTitle = false
					// Only the first real title counts; everything after it in
					// the body is someone else's heading.
					titleDone = strings.TrimSpace(p.Title) != ""
				}
			case "script":
				switch {
				case inLDScript:
					p.JSONLD = append(p.JSONLD, scriptBuf.String())
				case isFlightChunk(scriptBuf.Bytes()):
					p.FlightChunks = append(p.FlightChunks, scriptBuf.String())
				}
				inScript = false
				inLDScript = false
				scriptBuf.Reset()
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

// pageAddress is the address a page should be judged against: where the fetch
// ended up, or where it was aimed if that is all we have.
func pageAddress(p *Page) *url.URL {
	if p.FinalURL != nil {
		return p.FinalURL
	}
	return p.RequestedURL
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
