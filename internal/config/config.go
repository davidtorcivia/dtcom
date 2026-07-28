// Package config holds runtime configuration derived from environment variables.
package config

import (
	"encoding/base32"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	BaseURL           string
	ListenAddr        string
	AdminPasswordHash string
	TOTPSecret        string
	SessionKey        string
	APIToken          string
	RSSInterval       time.Duration
	ContentDir        string
	StaticDir         string
	PublicDir         string
	DataDir           string
	TemplatesDir      string
	SiteYAMLPath      string
	DBPath            string
	ImagesDir         string

	// CookieSecure marks the admin session cookie Secure. Derived from the
	// BaseURL scheme (https → true) so a local http:// dev run can still log
	// in; override with DTCOM_COOKIE_SECURE=true|false.
	CookieSecure bool

	// TrustProxyHeaders makes the server read the client IP from
	// X-Forwarded-For / CF-Connecting-IP instead of the TCP peer address. Only
	// enable when a reverse proxy (Cloudflare Tunnel, nginx, Caddy) sits in
	// front — otherwise any client can spoof its own address, which would let
	// it inflate view counts and evade the login rate limiter.
	TrustProxyHeaders bool
}

// minimum lengths for the two shared secrets. Short values are almost always a
// placeholder left in a .env by accident; refusing to start is safer than
// serving an admin panel behind a guessable token.
const (
	minSessionKeyLen = 32
	minAPITokenLen   = 24
	minTOTPSecretLen = 16
)

// FromEnv reads configuration from environment variables, applying defaults.
func FromEnv() (*Config, error) {
	contentDir := getenvDefault("DTCOM_CONTENT_DIR", "content")
	staticDir := getenvDefault("DTCOM_STATIC_DIR", "static")
	publicDir := getenvDefault("DTCOM_PUBLIC_DIR", "public")
	dataDir := getenvDefault("DTCOM_DATA_DIR", "data")
	templatesDir := getenvDefault("DTCOM_TEMPLATES_DIR", "templates")

	// Ordered so the error message lists missing vars deterministically.
	requiredKeys := []string{
		"DTCOM_BASE_URL",
		"DTCOM_ADMIN_PASSWORD_HASH",
		"DTCOM_TOTP_SECRET",
		"DTCOM_SESSION_KEY",
		"DTCOM_API_TOKEN",
	}
	missing := []string{}
	for _, k := range requiredKeys {
		if os.Getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %v", missing)
	}

	interval, err := time.ParseDuration(getenvDefault("DTCOM_RSS_INTERVAL", "30m"))
	if err != nil {
		return nil, fmt.Errorf("parse DTCOM_RSS_INTERVAL: %w", err)
	}
	if interval < time.Minute {
		return nil, fmt.Errorf("DTCOM_RSS_INTERVAL must be at least 1m (got %s)", interval)
	}

	baseURL, err := normalizeBaseURL(os.Getenv("DTCOM_BASE_URL"))
	if err != nil {
		return nil, err
	}

	if err := validateSecrets(); err != nil {
		return nil, err
	}

	cookieSecure := strings.HasPrefix(baseURL, "https://")
	if v := os.Getenv("DTCOM_COOKIE_SECURE"); v != "" {
		cookieSecure = isTruthy(v)
	}

	return &Config{
		BaseURL:           baseURL,
		ListenAddr:        getenvDefault("DTCOM_LISTEN_ADDR", ":8080"),
		AdminPasswordHash: os.Getenv("DTCOM_ADMIN_PASSWORD_HASH"),
		TOTPSecret:        os.Getenv("DTCOM_TOTP_SECRET"),
		SessionKey:        os.Getenv("DTCOM_SESSION_KEY"),
		APIToken:          os.Getenv("DTCOM_API_TOKEN"),
		RSSInterval:       interval,
		ContentDir:        contentDir,
		StaticDir:         staticDir,
		PublicDir:         publicDir,
		DataDir:           dataDir,
		TemplatesDir:      templatesDir,
		SiteYAMLPath:      contentDir + "/site.yml",
		DBPath:            dataDir + "/dtcom.db",
		ImagesDir:         dataDir + "/images",
		CookieSecure:      cookieSecure,
		TrustProxyHeaders: isTruthy(os.Getenv("DTCOM_TRUST_PROXY")),
	}, nil
}

// normalizeBaseURL validates the canonical site URL and strips any trailing
// slash. Every consumer (feed, sitemap, OG tags, canonical links) concatenates
// a path onto it, so normalizing once here keeps "https://x.com//posts/y" out
// of the generated markup.
func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse DTCOM_BASE_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("DTCOM_BASE_URL must be an absolute http(s) URL (got %q)", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("DTCOM_BASE_URL is missing a host (got %q)", raw)
	}
	return raw, nil
}

// validateSecrets rejects obviously-placeholder credentials at startup rather
// than letting a deploy come up with a guessable admin token.
func validateSecrets() error {
	hash := os.Getenv("DTCOM_ADMIN_PASSWORD_HASH")
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") && !strings.HasPrefix(hash, "$2y$") {
		return fmt.Errorf("DTCOM_ADMIN_PASSWORD_HASH must be a bcrypt hash (starts with $2a$/$2b$/$2y$)")
	}
	if n := len(os.Getenv("DTCOM_SESSION_KEY")); n < minSessionKeyLen {
		return fmt.Errorf("DTCOM_SESSION_KEY must be at least %d characters (got %d) — generate with: openssl rand -hex 32", minSessionKeyLen, n)
	}
	if n := len(os.Getenv("DTCOM_API_TOKEN")); n < minAPITokenLen {
		return fmt.Errorf("DTCOM_API_TOKEN must be at least %d characters (got %d) — generate with: openssl rand -hex 24", minAPITokenLen, n)
	}
	secret := strings.ToUpper(strings.TrimSpace(os.Getenv("DTCOM_TOTP_SECRET")))
	if len(secret) < minTOTPSecretLen {
		return fmt.Errorf("DTCOM_TOTP_SECRET must be at least %d base32 characters (got %d)", minTOTPSecretLen, len(secret))
	}
	// The otp library base32-decodes the secret at validation time; a bad
	// secret would otherwise only surface as "invalid credentials" at the
	// login form, which is a miserable thing to debug.
	if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(secret, "=")); err != nil {
		return fmt.Errorf("DTCOM_TOTP_SECRET is not valid base32: %w", err)
	}
	return nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
