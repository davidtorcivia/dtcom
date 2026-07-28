// Package siteconfig reads and writes content/site.yml.
package siteconfig

import (
	"errors"
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

	// LinksStyle controls how /links renders each entry: "full" shows the
	// summary line under the title, "minimal" is date :: title only. Empty
	// means "full".
	LinksStyle string `yaml:"links_style"`

	// Favicon is the site-relative URL of an uploaded favicon, e.g.
	// "/images/<hash>.png". Empty means the built-in /static/favicon.svg.
	//
	// It holds a URL rather than a filename because the file lives in the
	// data volume next to post images and is served by the same handler with
	// the same immutable-cache header — the name is a content hash, so a new
	// favicon is a new URL and no cache ever has to be invalidated.
	Favicon string `yaml:"favicon"`
}

// Link list styles.
const (
	LinksStyleFull    = "full"
	LinksStyleMinimal = "minimal"
)

// ShowLinkNotes reports whether /links should render each entry's summary.
func (c *Config) ShowLinkNotes() bool {
	return c != nil && c.LinksStyle != LinksStyleMinimal
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

// Default returns the config a brand-new site starts from.
//
// content/ is not tracked in git — it is the author's data, not the project's
// source — so a fresh checkout has no site.yml at all. Rather than refuse to
// start, the binary seeds this and comes up serving an empty but working site
// that can then be edited from /admin.
func Default() *Config {
	return &Config{
		Title:       "A dtcom site",
		Author:      "",
		Description: "",
		Nav: []NavLink{
			{Label: "Search", Href: "/search"},
			{Label: "Links", Href: "/links"},
		},
		RSSFeeds: []RSSFeed{},
	}
}

// LoadOrSeed reads site.yml, creating it from Default first if it is absent.
// Any error other than "not there" is returned as-is: a malformed or
// unreadable file is a problem to surface, not to overwrite.
func LoadOrSeed(path string) (*Config, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := Save(path, Default()); err != nil {
			return nil, fmt.Errorf("seed site.yml: %w", err)
		}
	}
	return Load(path)
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
