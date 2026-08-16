package fetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wishd/internal/fetch"
)

const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// captureHeaders serves one request and hands back what it received.
func captureHeaders(t *testing.T, ua, accept, wantPrefix string) http.Header {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	client := fetch.New(fetch.Options{AllowLoopback: true, UserAgent: ua})
	if _, err := client.Get(context.Background(), srv.URL, accept, wantPrefix, 1<<20); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return got
}

// TestPageRequestLooksLikeANavigation is the fix for the retailers that answer
// 403 to a three-header request and 200 to a browser-shaped one. Verified from
// the same address with curl, so it is the header set being checked and not
// the TLS fingerprint.
func TestPageRequestLooksLikeANavigation(t *testing.T) {
	h := captureHeaders(t, chromeUA, "text/html,application/xhtml+xml", "text/html")

	want := map[string]string{
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
		"Sec-Ch-Ua-Mobile":          "?0",
		"Sec-Ch-Ua-Platform":        `"macOS"`,
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	// The brand list has to agree with the version in the User-Agent, or the
	// pair is a contradiction of exactly the kind this is fixing.
	if got := h.Get("sec-ch-ua"); got != `"Chromium";v="124", "Not;A=Brand";v="24", "Google Chrome";v="124"` {
		t.Errorf("sec-ch-ua = %q", got)
	}
	// The caller's terse "text/html,application/xhtml+xml" is widened to what
	// a browser actually asks for; a two-item Accept is its own tell.
	if got := h.Get("Accept"); !strings.Contains(got, "image/avif") || !strings.Contains(got, "q=0.8") {
		t.Errorf("Accept = %q, want a browser's list", got)
	}
}

// TestImageRequestLooksLikeASubresource: an image is not a navigation, and
// claiming it is would be its own inconsistency.
func TestImageRequestLooksLikeASubresource(t *testing.T) {
	h := captureHeaders(t, chromeUA, "image/*", "")

	if got := h.Get("Sec-Fetch-Dest"); got != "image" {
		t.Errorf("Sec-Fetch-Dest = %q, want image", got)
	}
	if got := h.Get("Sec-Fetch-Mode"); got != "no-cors" {
		t.Errorf("Sec-Fetch-Mode = %q, want no-cors", got)
	}
	if h.Get("Upgrade-Insecure-Requests") != "" {
		t.Error("an image request claimed to be a top-level navigation")
	}
}

// TestHonestUserAgentSendsHonestHeaders: the request's shape follows the claim
// its User-Agent makes. Configure a bot and you get a bot's request, not a
// browser costume over a name that gives it away anyway.
func TestHonestUserAgentSendsHonestHeaders(t *testing.T) {
	h := captureHeaders(t, "wishd/1.0", "text/html", "text/html")

	for _, k := range []string{"sec-ch-ua", "Sec-Fetch-Dest", "Sec-Fetch-Mode", "Upgrade-Insecure-Requests"} {
		if v := h.Get(k); v != "" {
			t.Errorf("%s = %q; a bot User-Agent should not carry browser headers", k, v)
		}
	}
	if h.Get("User-Agent") != "wishd/1.0" {
		t.Errorf("User-Agent = %q", h.Get("User-Agent"))
	}
	// Including the Accept it was asked for: an honest client says what it
	// wants and nothing more.
	if got := h.Get("Accept"); got != "text/html" {
		t.Errorf("Accept = %q, want the caller's own value", got)
	}
}

// TestNonChromeUserAgentOmitsClientHints: sec-ch-ua is Chromium's. Sending it
// under a Firefox User-Agent swaps one contradiction for another.
func TestNonChromeUserAgentOmitsClientHints(t *testing.T) {
	const firefoxUA = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"
	h := captureHeaders(t, firefoxUA, "text/html", "text/html")

	if v := h.Get("sec-ch-ua"); v != "" {
		t.Errorf("sec-ch-ua = %q under a Firefox User-Agent", v)
	}
	// Fetch metadata is not Chromium-specific and should still be sent.
	if h.Get("Sec-Fetch-Mode") != "navigate" {
		t.Error("a Firefox navigation lost its fetch metadata")
	}
}
