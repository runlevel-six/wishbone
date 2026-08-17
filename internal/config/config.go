// Package config loads runtime configuration from the environment. Every
// variable here is documented in the README (plan §10).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wishbone/internal/fetch"
)

type Config struct {
	Addr              string
	DataDir           string
	DBPath            string
	ImageDir          string
	BaseURL           string
	SecretKey         []byte
	SecretIsEphemeral bool

	// Version is the build stamp, set by main rather than read from the
	// environment. It is not configuration in the usual sense; it is here
	// because every asset URL carries it as a cache-busting token, and a
	// released build needs exactly one thing to change for browsers, phones and
	// the service worker to let go of the previous release's files.
	Version string

	SessionTTL     time.Duration
	InviteTTL      time.Duration
	SecureCookies  bool
	TrustedProxies []*net.IPNet

	// Bootstrap admin, applied only when the users table is empty.
	BootstrapAdmin         string
	BootstrapAdminPassword string

	// Extraction (plan §5).
	FetchEnabled   bool
	FetchUserAgent string
	FetchLang      string
	// FetchImpersonate is "" or "chrome". See internal/fetch: it changes the
	// TLS handshake, not just the headers, and is off unless someone decided a
	// specific shop was worth it.
	FetchImpersonate string
	SidecarURL       string
	SidecarTimeout   time.Duration

	// Link health (plan §5.4). LinkCheckSpacing is the pause between items:
	// the point of the job is to notice a dead link before Christmas, not to
	// finish quickly, and one request every half minute from one address is a
	// shape nothing objects to.
	LinkCheckEnabled  bool
	LinkCheckInterval time.Duration
	LinkCheckBatch    int
	LinkCheckAge      time.Duration
	LinkCheckSpacing  time.Duration

	LogLevel string
}

func Load() (*Config, error) {
	c := &Config{
		Addr:                   env("WISHBONE_ADDR", ":8080"),
		DataDir:                env("WISHBONE_DATA_DIR", "/data"),
		BaseURL:                strings.TrimRight(env("WISHBONE_BASE_URL", ""), "/"),
		SessionTTL:             envDuration("WISHBONE_SESSION_TTL", 30*24*time.Hour),
		InviteTTL:              envDuration("WISHBONE_INVITE_TTL", 7*24*time.Hour),
		SecureCookies:          envBool("WISHBONE_SECURE_COOKIES", true),
		BootstrapAdmin:         env("WISHBONE_BOOTSTRAP_ADMIN", ""),
		BootstrapAdminPassword: env("WISHBONE_BOOTSTRAP_ADMIN_PASSWORD", ""),
		FetchEnabled:           envBool("WISHBONE_FETCH_ENABLED", true),
		FetchUserAgent: env("WISHBONE_FETCH_USER_AGENT",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"),
		FetchLang: env("WISHBONE_FETCH_ACCEPT_LANGUAGE", fetch.DefaultAcceptLanguage),
		FetchImpersonate: strings.ToLower(strings.TrimSpace(
			env("WISHBONE_FETCH_IMPERSONATE", ""))),
		SidecarURL:     strings.TrimRight(env("EXTRACTOR_SIDECAR_URL", ""), "/"),
		SidecarTimeout: envDuration("EXTRACTOR_SIDECAR_TIMEOUT", 10*time.Second),
		// The link-health job (plan §5.4). Off by default, like every other
		// outbound-traffic feature here: a job that walks every item on a
		// schedule is the traffic shape bot detection scores hardest, and losing
		// that quietly is worse than not entering it. See internal/linkcheck.
		LinkCheckEnabled:  envBool("WISHBONE_LINK_CHECK_ENABLED", false),
		LinkCheckInterval: envDuration("WISHBONE_LINK_CHECK_INTERVAL", 24*time.Hour),
		LinkCheckBatch:    envInt("WISHBONE_LINK_CHECK_BATCH", 20),
		LinkCheckAge:      envDuration("WISHBONE_LINK_CHECK_AGE", 7*24*time.Hour),
		LinkCheckSpacing:  envDuration("WISHBONE_LINK_CHECK_SPACING", 30*time.Second),
		LogLevel:          env("WISHBONE_LOG_LEVEL", "info"),
	}

	c.DBPath = env("WISHBONE_DB_PATH", filepath.Join(c.DataDir, "app.db"))
	c.ImageDir = env("WISHBONE_IMAGE_DIR", filepath.Join(c.DataDir, "images"))

	if raw := os.Getenv("WISHBONE_SECRET_KEY"); raw != "" {
		key, err := hex.DecodeString(strings.TrimSpace(raw))
		if err != nil || len(key) < 16 {
			return nil, fmt.Errorf("WISHBONE_SECRET_KEY must be at least 32 hex characters")
		}
		c.SecretKey = key
	} else {
		// Usable out of the box; the cost is that CSRF tokens issued before a
		// restart stop validating, which surfaces as one retried form post.
		c.SecretKey = make([]byte, 32)
		if _, err := rand.Read(c.SecretKey); err != nil {
			return nil, err
		}
		c.SecretIsEphemeral = true
	}

	// Only these sources may set X-Forwarded-For (plan §4).
	for _, cidr := range splitList(env("WISHBONE_TRUSTED_PROXY_CIDRS", "")) {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("WISHBONE_TRUSTED_PROXY_CIDRS: %q: %w", cidr, err)
		}
		c.TrustedProxies = append(c.TrustedProxies, n)
	}

	if c.SidecarURL != "" {
		if _, err := url.Parse(c.SidecarURL); err != nil {
			return nil, fmt.Errorf("EXTRACTOR_SIDECAR_URL: %w", err)
		}
	}
	// Refused rather than ignored: a typo here would silently leave the app
	// making the requests the operator set this to stop making.
	switch c.FetchImpersonate {
	case fetch.ImpersonateOff, fetch.ImpersonateChrome:
	default:
		return nil, fmt.Errorf("WISHBONE_FETCH_IMPERSONATE: %q is not a known mode (want %q)",
			c.FetchImpersonate, fetch.ImpersonateChrome)
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
