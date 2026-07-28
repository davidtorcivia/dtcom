// Package config holds runtime configuration derived from environment variables.
package config

import (
	"fmt"
	"os"
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
	SiteYAMLPath      string
	DBPath            string
	ImagesDir         string
}

// FromEnv reads configuration from environment variables, applying defaults.
func FromEnv() (*Config, error) {
	contentDir := getenvDefault("DTCOM_CONTENT_DIR", "content")
	staticDir := getenvDefault("DTCOM_STATIC_DIR", "static")
	publicDir := getenvDefault("DTCOM_PUBLIC_DIR", "public")
	dataDir := getenvDefault("DTCOM_DATA_DIR", "data")

	required := map[string]string{
		"DTCOM_BASE_URL":            "",
		"DTCOM_ADMIN_PASSWORD_HASH": "",
		"DTCOM_TOTP_SECRET":         "",
		"DTCOM_SESSION_KEY":         "",
		"DTCOM_API_TOKEN":           "",
	}
	missing := []string{}
	for k := range required {
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

	return &Config{
		BaseURL:           os.Getenv("DTCOM_BASE_URL"),
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
		SiteYAMLPath:      contentDir + "/site.yml",
		DBPath:            dataDir + "/dtcom.db",
		ImagesDir:         dataDir + "/images",
	}, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
