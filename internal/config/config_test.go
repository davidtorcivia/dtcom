package config

import "testing"

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("DTCOM_BASE_URL", "https://example.com")
	t.Setenv("DTCOM_ADMIN_PASSWORD_HASH", "$2a$10$abc")
	t.Setenv("DTCOM_TOTP_SECRET", "JBSWY3DPEHPK3PXP")
	t.Setenv("DTCOM_SESSION_KEY", "sesskey")
	t.Setenv("DTCOM_API_TOKEN", "apitok")

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
}
