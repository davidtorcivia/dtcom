// Package siteconfig reads and writes content/site.yml.
package siteconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Title       string       `yaml:"title"`
	Author      string       `yaml:"author"`
	BaseURL     string       `yaml:"base_url"`
	Description string       `yaml:"description"`
	Bio         []string     `yaml:"bio"`
	Nav         []NavLink    `yaml:"nav"`
	Social      []SocialLink `yaml:"social"`
	RSSFeeds    []RSSFeed    `yaml:"rss_feeds"`
	FooterLeft  []string     `yaml:"footer_left"`
}

type NavLink struct {
	Label string `yaml:"label"`
	Href  string `yaml:"href"`
}

type SocialLink struct {
	Label string `yaml:"label"`
	Href  string `yaml:"href"`
	Icon  string `yaml:"icon"`
}

type RSSFeed struct {
	URL     string `yaml:"url"`
	Label   string `yaml:"label"`
	Enabled bool   `yaml:"enabled"`
}

// Load reads and parses a site.yml file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read site.yml: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse site.yml: %w", err)
	}
	return &c, nil
}

// Save serializes the config back to a site.yml file.
func Save(path string, c *Config) error {
	out, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal site.yml: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write site.yml: %w", err)
	}
	return nil
}
