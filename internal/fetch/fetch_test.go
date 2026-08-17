package fetch_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wishbone/internal/fetch"
)

// TestHostileURLsRefused is the SSRF table from plan §8.
//
// Note what is being tested: the refusal happens in the dialer's Control hook,
// at connect time, against the *resolved* address. That is why a hostname is
// in the table alongside the literals — a URL-string check would wave
// "http://localhost/" straight through.
func TestHostileURLsRefused(t *testing.T) {
	client := fetch.New(fetch.Options{Timeout: 2 * time.Second})

	hostile := []struct {
		name string
		url  string
	}{
		{"loopback v4", "http://127.0.0.1:1/"},
		{"loopback v6", "http://[::1]:1/"},
		{"loopback by name", "http://localhost:1/"},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/"},
		{"rfc1918 10/8", "http://10.0.0.1/"},
		{"rfc1918 192.168/16", "http://192.168.1.1/"},
		{"rfc1918 172.16/12", "http://172.16.0.1/"},
		{"cgnat", "http://100.64.0.1/"},
		{"ipv6 ULA", "http://[fc00::1]/"},
		{"ipv6 link-local", "http://[fe80::1]/"},
		{"ipv4-mapped ipv6", "http://[::ffff:127.0.0.1]/"},
		{"unspecified", "http://0.0.0.0/"},
		{"multicast", "http://224.0.0.1/"},
		{"6to4 wrapping a private v4", "http://[2002:a00:1::1]/"},
	}

	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.Get(ctx, tc.url, "", "", 1<<20)
			if err == nil {
				t.Fatalf("%s was fetched; it must be refused", tc.url)
			}
			if !errors.Is(err, fetch.ErrBlockedAddress) {
				// A DNS failure would also "pass" without telling us the guard
				// worked, so insist on the guard's own error.
				t.Fatalf("%s failed with %v, want ErrBlockedAddress", tc.url, err)
			}
		})
	}
}

// TestRedirectToPrivateRefused covers the nastier case: a perfectly ordinary
// public URL that redirects into the private network. Each hop dials through
// the same guarded transport, so the guard sees the redirect target too.
func TestRedirectToPrivateRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/secrets", http.StatusFound)
	}))
	defer srv.Close()

	// AllowLoopback lets the test reach its own server; it does not weaken the
	// checks that matter here, since 10.0.0.1 is private either way.
	client := fetch.New(fetch.Options{AllowLoopback: true, Timeout: 2 * time.Second})
	_, err := client.Get(context.Background(), srv.URL, "", "", 1<<20)
	if err == nil {
		t.Fatal("redirect into RFC1918 space was followed")
	}
	if !errors.Is(err, fetch.ErrBlockedAddress) {
		t.Fatalf("got %v, want ErrBlockedAddress", err)
	}
}

func TestNonHTTPSchemesRefused(t *testing.T) {
	client := fetch.New(fetch.Options{})
	for _, u := range []string{
		"file:///etc/passwd",
		"gopher://example.com/",
		"ftp://example.com/x",
		"data:text/html,<h1>hi",
	} {
		if _, err := client.Get(context.Background(), u, "", "", 1<<20); !errors.Is(err, fetch.ErrScheme) {
			t.Errorf("%s: got %v, want ErrScheme", u, err)
		}
	}
}

func TestRedirectLimit(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	client := fetch.New(fetch.Options{AllowLoopback: true, MaxRedirects: 5, Timeout: 3 * time.Second})
	if _, err := client.Get(context.Background(), srv.URL, "", "", 1<<20); err == nil {
		t.Fatal("infinite redirect chain was followed to completion")
	}
}

func TestBodyCapAndContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		big := make([]byte, 4096)
		for i := range big {
			big[i] = 'a'
		}
		for i := 0; i < 8; i++ {
			w.Write(big)
		}
	}))
	defer srv.Close()

	client := fetch.New(fetch.Options{AllowLoopback: true})
	resp, err := client.Get(context.Background(), srv.URL, "", "text/html", 1024)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(resp.Body) != 1024 || !resp.Truncated {
		t.Errorf("body was not capped: %d bytes, truncated=%v", len(resp.Body), resp.Truncated)
	}

	if _, err := client.Get(context.Background(), srv.URL, "", "image/", 1024); !errors.Is(err, fetch.ErrContentType) {
		t.Errorf("wrong content type accepted: %v", err)
	}
}

// TestCheckIPAllowsPublicAddresses guards against a guard so strict it blocks
// the actual internet.
func TestCheckIPAllowsPublicAddresses(t *testing.T) {
	for _, ip := range []string{"93.184.216.34", "1.1.1.1", "2606:4700:4700::1111"} {
		if err := fetch.CheckIP(net.ParseIP(ip), false); err != nil {
			t.Errorf("public address %s was blocked: %v", ip, err)
		}
	}
}
