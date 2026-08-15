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
)

type Config struct {
	Addr              string
	DataDir           string
	DBPath            string
	ImageDir          string
	BaseURL           string
	SecretKey         []byte
	SecretIsEphemeral bool

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
	SidecarURL     string
	SidecarTimeout time.Duration

	LogLevel string
}

func Load() (*Config, error) {
	c := &Config{
		Addr:                   env("WISHD_ADDR", ":8080"),
		DataDir:                env("WISHD_DATA_DIR", "/data"),
		BaseURL:                strings.TrimRight(env("WISHD_BASE_URL", ""), "/"),
		SessionTTL:             envDuration("WISHD_SESSION_TTL", 30*24*time.Hour),
		InviteTTL:              envDuration("WISHD_INVITE_TTL", 7*24*time.Hour),
		SecureCookies:          envBool("WISHD_SECURE_COOKIES", true),
		BootstrapAdmin:         env("WISHD_BOOTSTRAP_ADMIN", ""),
		BootstrapAdminPassword: env("WISHD_BOOTSTRAP_ADMIN_PASSWORD", ""),
		FetchEnabled:           envBool("WISHD_FETCH_ENABLED", true),
		FetchUserAgent: env("WISHD_FETCH_USER_AGENT",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"),
		FetchLang:      env("WISHD_FETCH_ACCEPT_LANGUAGE", "en-US,en;q=0.9"),
		SidecarURL:     strings.TrimRight(env("EXTRACTOR_SIDECAR_URL", ""), "/"),
		SidecarTimeout: envDuration("EXTRACTOR_SIDECAR_TIMEOUT", 10*time.Second),
		LogLevel:       env("WISHD_LOG_LEVEL", "info"),
	}

	c.DBPath = env("WISHD_DB_PATH", filepath.Join(c.DataDir, "app.db"))
	c.ImageDir = env("WISHD_IMAGE_DIR", filepath.Join(c.DataDir, "images"))

	if raw := os.Getenv("WISHD_SECRET_KEY"); raw != "" {
		key, err := hex.DecodeString(strings.TrimSpace(raw))
		if err != nil || len(key) < 16 {
			return nil, fmt.Errorf("WISHD_SECRET_KEY must be at least 32 hex characters")
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
	for _, cidr := range splitList(env("WISHD_TRUSTED_PROXY_CIDRS", "")) {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("WISHD_TRUSTED_PROXY_CIDRS: %q: %w", cidr, err)
		}
		c.TrustedProxies = append(c.TrustedProxies, n)
	}

	if c.SidecarURL != "" {
		if _, err := url.Parse(c.SidecarURL); err != nil {
			return nil, fmt.Errorf("EXTRACTOR_SIDECAR_URL: %w", err)
		}
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
