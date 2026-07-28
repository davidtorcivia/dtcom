package config

import (
	"strings"
	"testing"
)

// setValidEnv populates every required var with a value that passes validation,
// so individual tests only override the one thing they're exercising.
func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DTCOM_BASE_URL", "https://example.com")
	t.Setenv("DTCOM_ADMIN_PASSWORD_HASH", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")
	t.Setenv("DTCOM_TOTP_SECRET", "JBSWY3DPEHPK3PXP")
	t.Setenv("DTCOM_SESSION_KEY", strings.Repeat("a", 32))
	t.Setenv("DTCOM_API_TOKEN", strings.Repeat("b", 24))
}

func TestFromEnvDefaults(t *testing.T) {
	setValidEnv(t)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr default = %q", cfg.ListenAddr)
	}
	if cfg.RSSInterval.Seconds() != 1800 {
		t.Errorf("RSSInterval default = %v", cfg.RSSInterval)
	}
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure should default to true for an https base URL")
	}
	if cfg.TrustProxyHeaders {
		t.Error("TrustProxyHeaders should default to false")
	}
}

// A trailing slash on the base URL would otherwise produce "https://x.com//posts/y"
// in the feed, sitemap, and canonical tags.
func TestBaseURLTrailingSlashStripped(t *testing.T) {
	setValidEnv(t)
	t.Setenv("DTCOM_BASE_URL", "https://example.com/")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q, want trailing slash stripped", cfg.BaseURL)
	}
}

// An http:// base URL means a local dev run, where a Secure cookie would never
// be sent back and login would silently fail.
func TestCookieSecureFollowsScheme(t *testing.T) {
	setValidEnv(t)
	t.Setenv("DTCOM_BASE_URL", "http://localhost:8080")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure should be false for an http base URL")
	}

	t.Setenv("DTCOM_COOKIE_SECURE", "true")
	cfg, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !cfg.CookieSecure {
		t.Error("DTCOM_COOKIE_SECURE=true should override the scheme default")
	}
}

func TestRejectsWeakSecrets(t *testing.T) {
	cases := []struct {
		name, key, value, wantSubstr string
	}{
		{"short session key", "DTCOM_SESSION_KEY", "sesskey", "DTCOM_SESSION_KEY"},
		{"short api token", "DTCOM_API_TOKEN", "apitok", "DTCOM_API_TOKEN"},
		{"plaintext password", "DTCOM_ADMIN_PASSWORD_HASH", "hunter2", "bcrypt"},
		{"short totp secret", "DTCOM_TOTP_SECRET", "ABC", "DTCOM_TOTP_SECRET"},
		{"non-base32 totp secret", "DTCOM_TOTP_SECRET", "0189!!!!!!!!!!!!!!!!", "base32"},
		{"relative base url", "DTCOM_BASE_URL", "example.com", "absolute http(s) URL"},
		{"non-http base url", "DTCOM_BASE_URL", "ftp://example.com", "absolute http(s) URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv(tc.key, tc.value)
			_, err := FromEnv()
			if err == nil {
				t.Fatalf("FromEnv accepted %s=%q", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %v, want mention of %q", err, tc.wantSubstr)
			}
		})
	}
}

// A sub-minute poll interval hammers other people's feed servers; the config
// refuses rather than letting a typo ("30" → parse error, "1s" → flood) ship.
func TestRejectsTooFastRSSInterval(t *testing.T) {
	setValidEnv(t)
	t.Setenv("DTCOM_RSS_INTERVAL", "5s")
	if _, err := FromEnv(); err == nil {
		t.Error("FromEnv accepted a 5s RSS interval")
	}
}

func TestMissingVarsListed(t *testing.T) {
	setValidEnv(t)
	t.Setenv("DTCOM_API_TOKEN", "")
	_, err := FromEnv()
	if err == nil || !strings.Contains(err.Error(), "DTCOM_API_TOKEN") {
		t.Errorf("err = %v, want it to name the missing var", err)
	}
}
