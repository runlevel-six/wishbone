package extract

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"time"

	"wishd/internal/fetch"
	"wishd/internal/model"
)

// MaxPageBytes caps a fetched HTML document (plan §5.2).
const MaxPageBytes = 2 << 20 // 2 MiB

// ErrDisabled is returned when URL fetching is switched off by configuration.
var ErrDisabled = errors.New("extract: URL fetching is disabled")

// Service is the whole pipeline: normalize, fetch, parse the head, run the
// chain, apply the soft-404 guard.
type Service struct {
	client  *fetch.Client
	chain   *Chain
	enabled bool
}

// NewService wires the tiers in the order given by plan §5.3. sidecar may be
// nil, in which case tier 5 simply is not present.
func NewService(client *fetch.Client, sidecar *Sidecar, enabled bool) *Service {
	tiers := []Extractor{
		Shopify{Client: client},
		JSONLD{},
		Microdata{},
		OpenGraph{},
	}
	if sidecar != nil {
		tiers = append(tiers, sidecar)
	}
	return &Service{client: client, chain: NewChain(tiers...), enabled: enabled}
}

func (s *Service) Enabled() bool { return s != nil && s.enabled && s.client != nil }

// Preview is what the add-item form shows before anything is saved.
type Preview struct {
	URLRaw     string
	URL        string // normalized, after redirects
	Result     *Result
	FetchedAt  time.Time
	LinkStatus string
	// StatusCode and Bytes describe the response itself. They are diagnostics:
	// when a lookup comes back empty, "what did the shop actually answer" is
	// the first question, and it used to take a second tool to ask.
	StatusCode int
	Bytes      int
}

// Suspect reports whether the user must confirm before the fields are applied
// (plan §5.4).
func (p *Preview) Suspect() bool { return p.Result != nil && p.Result.Suspect }

// Blocked reports whether the retailer refused to serve the page. Nothing was
// learned about the link, so nothing should be said about it.
func (p *Preview) Blocked() bool { return p.Result != nil && p.Result.Blocked }

// Fetch runs the pipeline for one URL. A fetch failure is returned as an error;
// the caller falls back to the manual path, which is never a degraded path in
// the UI (plan §5.3 tier 6).
//
// One retailer address can have two renderings. A department store serves the
// same garment at a legacy campaign path and at the slug it declares canonical;
// the legacy one came back as a complete 1 MB document with an OpenGraph title,
// no JSON-LD anywhere in it and the string "price" not present once, while the
// canonical one carried a full Product node with the price, the SKU and the
// brand. Nothing was blocked and nothing was wrong with the link, so no amount
// of work on the tiers reaches a price that was never sent. Asking the page
// where it lives, and asking that address instead, is what gets it.
func (s *Service) Fetch(ctx context.Context, rawURL string) (*Preview, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	normalized, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	got, err := s.fetchOnce(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if alt := canonicalFollowUp(got); alt != "" {
		// One extra hop, never a chain of them: the second fetch's own canonical
		// is not followed, so a pair of pages naming each other cannot loop.
		if second, err := s.fetchOnce(ctx, alt); err == nil && improvesOn(second.res, got.res) {
			got = second
		}
	}

	return &Preview{
		URLRaw:     rawURL,
		URL:        got.url,
		Result:     got.res,
		FetchedAt:  model.Now(),
		LinkStatus: got.res.LinkStatus,
		StatusCode: got.status,
		Bytes:      got.bytes,
	}, nil
}

// fetched is one completed pass of the pipeline over one address.
type fetched struct {
	requested *url.URL // what this pass asked for, normalized
	url       string   // where it landed, normalized after redirects
	res       *Result
	status    int
	bytes     int
}

func (s *Service) fetchOnce(ctx context.Context, normalized string) (*fetched, error) {
	requested, err := url.Parse(normalized)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Get(ctx, normalized, "text/html,application/xhtml+xml", "text/html", MaxPageBytes)
	if err != nil {
		return nil, err
	}

	page, err := ParseDocument(bytes.NewReader(resp.Body))
	if err != nil {
		return nil, err
	}
	page.RequestedURL = requested
	page.FinalURL = resp.FinalURL
	page.StatusCode = resp.StatusCode

	res := s.chain.Run(ctx, page)
	ApplySoft404Guard(res, page)

	return &fetched{
		requested: requested,
		url:       AfterFetch(normalized, resp.FinalURL),
		res:       res,
		status:    resp.StatusCode,
		bytes:     len(resp.Body),
	}, nil
}

// canonicalFollowUp returns the address to re-ask, or "" to keep what the first
// pass produced.
//
// The gate is deliberately narrow, because a canonical tag is not always a
// prettier spelling of the same thing. CanonicalAlternative documents the case
// this must not swallow: a marketplace that collapses every size and color of
// a garment onto whichever listing it indexes, where following the tag silently
// buys the wrong size. So the tag is only followed when it still carries the
// identifying segment of the address that was asked for — the product id or
// listing code. Same id, different slug, is one product with two spellings.
// A different id is a different product, and that stays a suggestion the owner
// accepts by hand rather than something done to them.
func canonicalFollowUp(got *fetched) string {
	if got == nil || got.res == nil || got.res.Blocked || got.status >= 400 {
		return ""
	}
	// A price is the one field worth a second round trip. Everything else the
	// first pass either found or is not going to find at another spelling.
	if got.res.PriceCents != nil {
		return ""
	}
	alt := CanonicalAlternative(got.res.Canonical, got.url)
	if alt == "" {
		return ""
	}
	if got.requested == nil {
		return ""
	}
	reqID := identifyingSegment(got.requested.Path)
	if reqID == "" {
		return ""
	}
	au, err := url.Parse(alt)
	if err != nil || !pathContains(au.Path, reqID) {
		return ""
	}
	return alt
}

// improvesOn reports whether the canonical page's result should replace the
// first one. It has to actually answer the question that prompted the second
// fetch, and it must not be a worse page in any other respect: a canonical that
// refuses us, or that the guard distrusts, leaves the original standing.
func improvesOn(next, first *Result) bool {
	if next == nil || first == nil {
		return false
	}
	if next.Blocked || next.Suspect || next.PriceCents == nil || next.Title == "" {
		return false
	}
	return true
}
