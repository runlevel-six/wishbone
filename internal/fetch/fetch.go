// Package fetch is the outbound HTTP client for user-supplied URLs.
//
// This is the highest-risk component in the app (plan §5.2): it makes requests
// to addresses a user typed, from inside a home cluster. The guard lives in
// net.Dialer.Control, which sees the resolved IP at connect time. That is what
// defeats DNS rebinding — a URL-string check cannot, because the name can
// resolve to a public address when validated and a private one when dialed.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a connection is refused by the guard.
var ErrBlockedAddress = errors.New("fetch: destination address is not permitted")

// Errors surfaced to callers.
var (
	ErrScheme      = errors.New("fetch: only http and https URLs are supported")
	ErrTooManyHops = errors.New("fetch: too many redirects")
	ErrContentType = errors.New("fetch: unexpected content type")
	ErrTooLarge    = errors.New("fetch: response exceeded size limit")
)

type Options struct {
	UserAgent      string
	AcceptLanguage string
	Timeout        time.Duration
	DialTimeout    time.Duration
	MaxRedirects   int

	// AllowLoopback disables the loopback part of the guard. It exists only so
	// tests can point the client at httptest servers; it is never set from
	// configuration.
	AllowLoopback bool
}

type Client struct {
	http *http.Client
	opts Options
}

func New(opts Options) *Client {
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.DialTimeout == 0 {
		opts.DialTimeout = 2 * time.Second
	}
	if opts.MaxRedirects == 0 {
		opts.MaxRedirects = 5
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "wishd/1.0"
	}

	dialer := &net.Dialer{
		Timeout:   opts.DialTimeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			return guard(network, address, opts.AllowLoopback)
		},
	}
	transport := &http.Transport{
		DialContext:            dialer.DialContext,
		TLSHandshakeTimeout:    opts.DialTimeout,
		ResponseHeaderTimeout:  opts.Timeout,
		MaxResponseHeaderBytes: 64 << 10,
		MaxIdleConns:           8,
		IdleConnTimeout:        30 * time.Second,
		DisableCompression:     false,
		// No proxy: a proxy would move the connection off the guarded dialer.
		Proxy: nil,
	}

	c := &Client{opts: opts}
	c.http = &http.Client{
		Transport: transport,
		Timeout:   opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= opts.MaxRedirects {
				return ErrTooManyHops
			}
			// Every hop is dialed through the same transport, so the guard
			// applies to redirect targets too; the scheme still needs checking
			// because a redirect can name any scheme at all.
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return ErrScheme
			}
			return nil
		},
	}
	return c
}

// Response is a fetched document, already truncated to the caller's limit.
type Response struct {
	// FinalURL is the URL after redirects — the soft-404 guard compares it
	// against what was requested (plan §5.4).
	FinalURL    *url.URL
	StatusCode  int
	ContentType string
	Body        []byte
	Truncated   bool
}

// Get performs a guarded GET. wantPrefix, if non-empty, is required as a
// Content-Type prefix ("text/html", "image/"). maxBytes caps the body.
func (c *Client) Get(ctx context.Context, rawURL, accept, wantPrefix string, maxBytes int64) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrScheme
	}
	if u.Host == "" {
		return nil, ErrScheme
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept-Language", c.opts.AcceptLanguage)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Unwrap so callers can test for the guard specifically.
		if errors.Is(err, ErrBlockedAddress) {
			return nil, ErrBlockedAddress
		}
		return nil, err
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if wantPrefix != "" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), wantPrefix) {
		return nil, fmt.Errorf("%w: %s", ErrContentType, ct)
	}

	body, truncated, err := readLimited(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}
	return &Response{
		FinalURL:    resp.Request.URL,
		StatusCode:  resp.StatusCode,
		ContentType: ct,
		Body:        body,
		Truncated:   truncated,
	}, nil
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, bool, error) {
	// Read one extra byte so an exactly-at-limit body is distinguishable from
	// a truncated one.
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > maxBytes {
		return b[:maxBytes], true, nil
	}
	return b, false, nil
}

// guard is the connect-time address check.
func guard(network, address string, allowLoopback bool) error {
	if network != "tcp4" && network != "tcp6" && network != "tcp" {
		return fmt.Errorf("%w: network %s", ErrBlockedAddress, network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, address)
	}
	// An IPv4-mapped IPv6 literal (::ffff:127.0.0.1) is a classic way to slip a
	// private v4 target past a v6-unaware check. The value-level checks below
	// unwrap it correctly, but the form has no legitimate use here, so refuse
	// it outright on the text.
	if strings.Contains(strings.ToLower(host), "::ffff:") {
		return fmt.Errorf("%w: IPv4-mapped IPv6 %s", ErrBlockedAddress, host)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: unparseable address %s", ErrBlockedAddress, host)
	}
	return CheckIP(ip, allowLoopback)
}

// Networks denied in addition to the stdlib predicates.
var deniedNets = []*net.IPNet{
	mustCIDR("0.0.0.0/8"),          // "this network"
	mustCIDR("100.64.0.0/10"),      // CGNAT
	mustCIDR("192.0.0.0/24"),       // IETF protocol assignments
	mustCIDR("192.0.2.0/24"),       // TEST-NET-1
	mustCIDR("198.18.0.0/15"),      // benchmarking
	mustCIDR("198.51.100.0/24"),    // TEST-NET-2
	mustCIDR("203.0.113.0/24"),     // TEST-NET-3
	mustCIDR("240.0.0.0/4"),        // reserved
	mustCIDR("255.255.255.255/32"), // broadcast
	mustCIDR("::/128"),             // unspecified
	mustCIDR("2001::/32"),          // Teredo
	mustCIDR("2002::/16"),          // 6to4 — can encapsulate a private v4 target
	mustCIDR("64:ff9b::/96"),       // NAT64
}

// CheckIP reports whether an address may be dialed. Exported so tests can
// exercise the predicate directly, and so the image fetcher shares exactly one
// implementation.
func CheckIP(ip net.IP, allowLoopback bool) error {
	// net.IP compares by value, so a mapped address like ::ffff:10.0.0.1 is
	// evaluated as 10.0.0.1 by every predicate below. The textual form is
	// rejected separately in guard.
	if ip.IsLoopback() {
		if allowLoopback {
			return nil
		}
		return fmt.Errorf("%w: loopback %s", ErrBlockedAddress, ip)
	}
	switch {
	case ip.IsUnspecified():
		return fmt.Errorf("%w: unspecified %s", ErrBlockedAddress, ip)
	case ip.IsPrivate(): // RFC1918 and IPv6 ULA fc00::/7
		return fmt.Errorf("%w: private %s", ErrBlockedAddress, ip)
	case ip.IsLinkLocalUnicast(): // includes 169.254.169.254
		return fmt.Errorf("%w: link-local %s", ErrBlockedAddress, ip)
	case ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return fmt.Errorf("%w: multicast %s", ErrBlockedAddress, ip)
	case !ip.IsGlobalUnicast():
		return fmt.Errorf("%w: not global unicast %s", ErrBlockedAddress, ip)
	}
	for _, n := range deniedNets {
		if n.Contains(ip) {
			return fmt.Errorf("%w: reserved range %s", ErrBlockedAddress, n)
		}
	}
	return nil
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("fetch: bad CIDR " + s)
	}
	return n
}
