package fetch_test

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wishd/internal/fetch"
)

// rootsFor trusts one httptest server's certificate and nothing else.
func rootsFor(srv *httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}

// TestImpersonationKeepsTheAddressGuard is the test this feature exists to
// pass. Impersonation changes the handshake and nothing else: the connection
// is still made by the guarded dialer, so the SSRF protection still sees the
// resolved address at connect time.
//
// If a future change moves impersonation onto a library that brings its own
// dialer, this is what fails.
func TestImpersonationKeepsTheAddressGuard(t *testing.T) {
	client := fetch.New(fetch.Options{
		Timeout:     2 * time.Second,
		Impersonate: fetch.ImpersonateChrome,
	})

	hostile := []string{
		"https://127.0.0.1:1/",
		"https://[::1]:1/",
		"https://localhost:1/",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/",
		"https://192.168.1.1/",
		"https://172.16.0.1/",
		"https://[fc00::1]/",
		"https://0.0.0.0/",
	}
	for _, u := range hostile {
		_, err := client.Get(context.Background(), u, "", "", 1<<20)
		if !errors.Is(err, fetch.ErrBlockedAddress) {
			t.Errorf("%s: err = %v, want ErrBlockedAddress", u, err)
		}
	}
}

// TestImpersonatedHandshakeWorks: a Chrome ClientHello still has to complete a
// handshake and speak HTTP to a server that is not pretending to be anything.
// HTTP/2 matters — a custom DialTLSContext silently drops Go back to HTTP/1.1
// unless it is asked not to, and half a browser's fingerprint is the h2 layer.
func TestImpersonatedHandshakeWorks(t *testing.T) {
	var proto string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto = r.Proto
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>ok</title></head></html>"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	client := fetch.New(fetch.Options{
		AllowLoopback: true,
		Impersonate:   fetch.ImpersonateChrome,
		RootCAs:       rootsFor(srv),
		UserAgent:     chromeUA,
	})

	resp, err := client.Get(context.Background(), srv.URL, "text/html", "text/html", 1<<20)
	if err != nil {
		t.Fatalf("impersonated fetch: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if proto != "HTTP/2.0" {
		t.Errorf("negotiated %s, want HTTP/2.0 — ALPN or ForceAttemptHTTP2 regressed", proto)
	}
}

// TestImpersonationVerifiesCertificates: the point is to look like a browser,
// not to stop being a client that checks who it is talking to. A server whose
// certificate is not trusted must still be refused.
func TestImpersonationVerifiesCertificates(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	client := fetch.New(fetch.Options{
		AllowLoopback: true,
		Impersonate:   fetch.ImpersonateChrome,
		// No RootCAs: the system store, which has never heard of this cert.
	})
	if _, err := client.Get(context.Background(), srv.URL, "", "", 1<<20); err == nil {
		t.Fatal("an untrusted certificate was accepted")
	}
}

// TestImpersonationOffIsTheDefault: the flag has to be opt-in, and the way to
// be sure is that the zero Options do not impersonate. A default that quietly
// changed would be exactly the sort of thing nobody notices for a year.
func TestImpersonationOffIsTheDefault(t *testing.T) {
	if fetch.ImpersonateOff != "" {
		t.Fatal("the off mode is not the zero value, so the zero Options impersonate")
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html></html>"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	// The plain path still has to work over TLS, which is what the default
	// TLSClientConfig now carries.
	client := fetch.New(fetch.Options{AllowLoopback: true, RootCAs: rootsFor(srv)})
	if _, err := client.Get(context.Background(), srv.URL, "text/html", "text/html", 1<<20); err != nil {
		t.Fatalf("default client over TLS: %v", err)
	}
}
