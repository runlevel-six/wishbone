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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
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

	// Impersonate, when set to "chrome", performs the TLS handshake with
	// Chrome's ClientHello instead of Go's. Off by default and opt-in from
	// configuration; see applyBrowserHeaders and the package doc for what it
	// is for and what it costs.
	Impersonate string

	// AllowLoopback disables the loopback part of the guard. It exists only so
	// tests can point the client at httptest servers; it is never set from
	// configuration.
	AllowLoopback bool

	// RootCAs overrides the system trust store. Nil means the system store.
	// Tests use it to trust an httptest server's certificate; nothing in
	// production sets it, and it is not a way to skip verification.
	RootCAs *x509.CertPool
}

// Impersonation modes accepted in Options.Impersonate.
const (
	ImpersonateOff    = ""
	ImpersonateChrome = "chrome"
)

// DefaultAcceptLanguage is sent when Options leaves it unset, and is the
// default the configuration documents.
//
// It is a default rather than an optional field because absent is not neutral.
// A department store's bot filter answers 403 to every request that omits this
// header or sends it empty, and 200 to the identical request — same address,
// same Chrome ClientHello, same everything else — that sends this value:
// deterministically, 6 for 6 against 15 for 15. Browsers always send one, so a
// request claiming to be Chrome without it is a contradiction of exactly the
// kind applyBrowserHeaders exists to avoid. Leaving the field empty cost an
// hour of chasing an intermittent block that was never intermittent.
const DefaultAcceptLanguage = "en-US,en;q=0.9"

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
		opts.UserAgent = "wishbone/1.0"
	}
	if opts.AcceptLanguage == "" {
		opts.AcceptLanguage = DefaultAcceptLanguage
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
		TLSClientConfig:        &tls.Config{RootCAs: opts.RootCAs, MinVersion: tls.VersionTLS12},
		// No proxy: a proxy would move the connection off the guarded dialer.
		Proxy: nil,
	}
	var rt http.RoundTripper = transport
	if opts.Impersonate == ImpersonateChrome {
		// The handshake changes; the dialer does not. Every connection still
		// goes out through dialer.DialContext, so the address guard sees the
		// resolved IP exactly as before — which is why the SSRF table in the
		// tests covers this mode too rather than only the default one.
		transport.DialTLSContext = chromeTLSDialer(dialer, opts, "http/1.1")
		rt = &impersonatingTransport{
			h1: transport,
			h2: &http2.Transport{
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return chromeTLSDialer(dialer, opts, "h2", "http/1.1")(ctx, network, addr)
				},
			},
		}
	}

	c := &Client{opts: opts}
	c.http = &http.Client{
		Transport: rt,
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
	applyBrowserHeaders(req.Header, c.opts.UserAgent, accept)

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

// chromeTLSDialer returns a DialTLSContext that connects through the guarded
// dialer and then completes the handshake with Chrome's ClientHello.
//
// Why this exists: some retailers inspect the TLS handshake itself, and Go's
// cipher and extension ordering is nothing like a browser's. One department
// store chain answers 403 to every request this app can otherwise make — full
// browser header set, browser Accept, brotli, a retry carrying the cookies
// from its own 403 — and 200 to the identical request behind a Chrome
// ClientHello, from the same address. Headers were not the whole story there.
//
// Why it is opt-in: it is an arms race, and losing it quietly is worse than
// not entering it. Fingerprints drift with Chrome's releases, so this needs
// occasional attention that the rest of the fetcher does not, and a family
// wishlist that types in one item by hand is not obviously worse off. The flag
// means the code is here, exercised by tests, and off until someone decides a
// specific shop is worth it.
//
// What is deliberately not done: the HTTP/2 layer is Go's. Matching Chrome's
// SETTINGS frames and header ordering as well would mean routing this through
// a fork of net/http, and this is the one package where the SSRF guard lives.
//
// A fingerprint that is half right turns out to be enough, at least for the
// retailer that prompted this. check-url against it answers 403 with
// -impersonate off and 200 with -impersonate chrome, returning a response
// byte-identical to curl-impersonate's — which matches Chrome's HTTP/2
// fingerprint as well and gains nothing for it. The handshake was the whole
// check there.
//
// "At least for" is doing real work in that sentence. That retailer fronts
// with a commercial bot-detection service that scores behavior over time
// rather than only inspecting the hello, so a cold request succeeding says
// nothing about what a link-health job hammering one egress IP would see.
// Treat this as measured for the case it was measured on, not as a general
// result.
func chromeTLSDialer(dialer *net.Dialer, opts Options, nextProtos ...string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			raw.Close()
			return nil, err
		}

		conn := utls.UClient(raw, &utls.Config{
			ServerName: host,
			RootCAs:    opts.RootCAs,
			NextProtos: nextProtos,
		}, utls.HelloChrome_Auto)

		// DialTLSContext bypasses Transport.TLSHandshakeTimeout, so the
		// handshake gets its deadline here or it gets none at all.
		deadline := time.Now().Add(opts.DialTimeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, err
		}
		if err := conn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		if err := conn.SetDeadline(time.Time{}); err != nil {
			conn.Close()
			return nil, err
		}

		// The HTTP/2 transport must not be handed a connection the server
		// intends to speak HTTP/1.1 over. Checking here rather than reading
		// http2's error text keeps the fallback from depending on a string.
		if len(nextProtos) > 0 && nextProtos[0] == "h2" &&
			conn.ConnectionState().NegotiatedProtocol != "h2" {
			conn.Close()
			return nil, errNoHTTP2
		}
		return conn, nil
	}
}

// errNoHTTP2 reports a server that did not negotiate h2, so the request can be
// replayed over HTTP/1.1.
var errNoHTTP2 = errors.New("fetch: server did not negotiate HTTP/2")

// impersonatingTransport sends a request over HTTP/2 when the server
// negotiates it and HTTP/1.1 when it does not.
//
// net/http will not do this for us. Its automatic HTTP/2 upgrade type-asserts
// the connection to *tls.Conn, and a utls connection is not one, so the
// transport would speak HTTP/1.1 down a connection the server had already
// switched to h2. The alternative is a fork of net/http, which is not
// something to put underneath the address guard. Two transports and one retry
// is the smaller price.
//
// Replaying is safe because every request this package makes is a GET with no
// body. If that ever stops being true, this has to stop being a retry.
type impersonatingTransport struct {
	h1 *http.Transport
	h2 *http2.Transport
}

func (t *impersonatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.Body != nil {
		return t.h1.RoundTrip(req)
	}
	resp, err := t.h2.RoundTrip(req)
	if !errors.Is(err, errNoHTTP2) {
		return resp, err
	}
	return t.h1.RoundTrip(req)
}

// chromeVersion matches the major version out of a Chrome-shaped User-Agent.
var chromeVersion = regexp.MustCompile(`Chrome/(\d+)`)

// applyBrowserHeaders makes the request look like what the User-Agent says it
// is. Three headers where a browser sends fifteen is itself a signal, and a
// cheap one to check for.
//
// Verified against a workwear retailer, which answers 403 to User-Agent +
// Accept + Accept-Language and 200 to the full set, from the same address,
// over curl — whose TLS and HTTP/2 fingerprints are no more browser-like than
// Go's. So for that retailer the check was on headers alone and nothing
// deeper.
//
// A department store chain refuses all of it: full header set, browser Accept,
// brotli, and a second request carrying the cookies from the first. Whatever
// it inspects is below HTTP, and no amount of header work reaches it. That is
// the line this stops at — see docs/explanation/extraction-trade-offs.md.
//
// Nothing here is emitted for an honest bot User-Agent. The shape of the
// request follows the claim the User-Agent makes: a request that says wishbone
// and behaves like Chrome is a contradiction, which is precisely the thing
// being avoided.
func applyBrowserHeaders(h http.Header, userAgent, accept string) {
	if !strings.HasPrefix(userAgent, "Mozilla/") {
		return
	}

	// Client hints are Chromium's. Sending them under a Firefox or Safari
	// User-Agent would introduce a fresh contradiction in place of the old one.
	if m := chromeVersion.FindStringSubmatch(userAgent); m != nil {
		v := m[1]
		h.Set("sec-ch-ua", `"Chromium";v="`+v+`", "Not;A=Brand";v="24", "Google Chrome";v="`+v+`"`)
		h.Set("sec-ch-ua-mobile", boolHint(strings.Contains(userAgent, "Mobile")))
		h.Set("sec-ch-ua-platform", `"`+platformHint(userAgent)+`"`)
	}

	// Fetch metadata describes what the request is for. A page is a top-level
	// navigation; an image is a subresource on another origin.
	//
	// Accept is rewritten here too. The callers pass what they mean —
	// "text/html", "image/*" — which is honest and terse, and terse is the
	// tell: a browser sends a long q-valued list, and a request claiming to be
	// Chrome with a two-item Accept does not look like one. What the response
	// must actually be is enforced separately, against the response's own
	// Content-Type, so widening what is asked for concedes nothing.
	switch {
	case strings.HasPrefix(accept, "text/html"):
		h.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,"+
			"image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
		h.Set("Sec-Fetch-Dest", "document")
		h.Set("Sec-Fetch-Mode", "navigate")
		h.Set("Sec-Fetch-Site", "none")
		h.Set("Sec-Fetch-User", "?1")
		h.Set("Upgrade-Insecure-Requests", "1")
	case strings.HasPrefix(accept, "image/"):
		h.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,*/*;q=0.8")
		h.Set("Sec-Fetch-Dest", "image")
		h.Set("Sec-Fetch-Mode", "no-cors")
		h.Set("Sec-Fetch-Site", "cross-site")
	default:
		h.Set("Sec-Fetch-Dest", "empty")
		h.Set("Sec-Fetch-Mode", "cors")
		h.Set("Sec-Fetch-Site", "same-origin")
	}

	// Accept-Encoding is deliberately left to net/http, which sends "gzip" and
	// transparently decompresses it. Setting it by hand to Chrome's full list
	// turns that off — the response would arrive as brotli nobody decodes.
	// Adding a brotli decoder to match one header is not worth it unless a
	// retailer turns out to check.
}

func boolHint(b bool) string {
	if b {
		return "?1"
	}
	return "?0"
}

func platformHint(userAgent string) string {
	switch {
	case strings.Contains(userAgent, "Windows"):
		return "Windows"
	case strings.Contains(userAgent, "Android"):
		return "Android"
	case strings.Contains(userAgent, "Macintosh"), strings.Contains(userAgent, "Mac OS X"):
		return "macOS"
	case strings.Contains(userAgent, "iPhone"), strings.Contains(userAgent, "iPad"):
		return "iOS"
	default:
		return "Linux"
	}
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
