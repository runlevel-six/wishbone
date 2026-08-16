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
func (s *Service) Fetch(ctx context.Context, rawURL string) (*Preview, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	normalized, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
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

	return &Preview{
		URLRaw:     rawURL,
		URL:        AfterFetch(normalized, resp.FinalURL),
		Result:     res,
		FetchedAt:  model.Now(),
		LinkStatus: res.LinkStatus,
		StatusCode: resp.StatusCode,
		Bytes:      len(resp.Body),
	}, nil
}
