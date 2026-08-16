package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Sidecar is tier 5: the get-product-data service running as a second
// container in the same pod, reachable on localhost only (plan §5.5).
//
// Metadata-only extraction is a regression on large marketplaces relative to
// the app this replaces, and those are a large share of real usage — so this
// tier exists to hold that quality line without linking a Node runtime into
// the binary.
//
// It is treated as untrusted: short timeout, no retries, and any failure
// degrades to the manual path rather than surfacing an error.
type Sidecar struct {
	BaseURL string
	Client  *http.Client
}

// NewSidecar returns nil when no sidecar is configured, which is a supported
// deployment: the app is fully functional without it.
func NewSidecar(baseURL string, timeout time.Duration) *Sidecar {
	if baseURL == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Sidecar{
		BaseURL: baseURL,
		// A plain client: this is a localhost peer, not a user-supplied
		// address, so the SSRF dialer guard does not apply and would in fact
		// block it.
		Client: &http.Client{Timeout: timeout},
	}
}

func (*Sidecar) Name() string          { return SourceSidecar }
func (*Sidecar) Applies(*url.URL) bool { return true }

// Fallback marks this tier as one to skip when the metadata tiers already
// produced a usable result.
func (*Sidecar) Fallback() bool { return true }

// sidecarResponse is the contract we expect from the sidecar wrapper. It is
// documented in deploy/sidecar/README.md; the wrapper is a thin HTTP shim over
// get-product-data.
type sidecarResponse struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Price       string            `json:"price"`
	Currency    string            `json:"currency"`
	Images      []string          `json:"images"`
	Image       string            `json:"image"`
	SKU         string            `json:"sku"`
	Brand       string            `json:"brand"`
	Attributes  map[string]string `json:"attributes"`
	Error       string            `json:"error"`
}

func (s *Sidecar) Extract(ctx context.Context, p *Page) (Fields, error) {
	f := Fields{Attributes: map[string]string{}}
	target := p.FinalURL
	if target == nil {
		target = p.RequestedURL
	}
	if target == nil {
		return f, nil
	}

	endpoint := s.BaseURL + "/extract?url=" + url.QueryEscape(target.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return f, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return f, fmt.Errorf("sidecar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// No retry on 5xx by design.
		return f, fmt.Errorf("sidecar: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return f, fmt.Errorf("sidecar: %w", err)
	}
	var sr sidecarResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return f, fmt.Errorf("sidecar: %w", err)
	}
	if sr.Error != "" {
		return f, fmt.Errorf("sidecar: %s", sr.Error)
	}

	f.Title = sr.Title
	f.Description = sr.Description
	f.SKU = sr.SKU
	f.Brand = sr.Brand
	if sr.Price != "" {
		cents, cur := ParsePriceCents(sr.Price)
		f.PriceCents = cents
		f.Currency = firstNonEmpty(NormalizeCurrency(sr.Currency), cur)
	}
	images := sr.Images
	if sr.Image != "" {
		images = append(images, sr.Image)
	}
	for _, img := range images {
		if u := p.ResolveURL(img); u != "" {
			f.ImageURLs = append(f.ImageURLs, u)
		}
	}
	f.ImageURLs = dedupeStrings(f.ImageURLs)
	for k, v := range sr.Attributes {
		if key := attrKeyFor(k); key != "" {
			f.Attributes[key] = v
		}
	}
	return f, nil
}
