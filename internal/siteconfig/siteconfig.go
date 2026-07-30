// Package siteconfig reads and writes content/site.yml.
package siteconfig

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

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

	// Analytics is an optional third-party tracker. Empty means none, which is
	// the default and the state a fresh site starts in.
	Analytics Analytics `yaml:"analytics,omitempty"`
}

// Analytics describes a self-hosted or third-party analytics script — Umami,
// Plausible, Fathom, GoatCounter and the rest are all one <script> tag with a
// handful of data-* attributes, so that is what this models rather than a list
// of named providers.
//
// The site's own view counter keeps running either way. They measure different
// things: this one is deduplicated per day by a keyed hash of the address and
// only counts what the beacon reports, and a tracker sees sessions, referrers
// and everything else it is built for.
type Analytics struct {
	// ScriptURL is the tag's src, e.g. "https://cloud.umami.is/script.js".
	// Must be an absolute http(s) URL; anything else is rejected on save.
	ScriptURL string `yaml:"script_url,omitempty"`

	// Data holds the provider's configuration, rendered as data-* attributes.
	// Umami wants {"website-id": "…"}, Plausible {"domain": "…"}, Fathom
	// {"site": "…"}. The key is written verbatim after "data-", so it is
	// restricted to the characters an attribute name may contain.
	//
	// The json tag carries omitempty rather than a rename: this config is the
	// output of the get_site MCP tool, which validates against a schema
	// inferred from these types, and a nil map is not an object as far as JSON
	// Schema is concerned. (A nil *slice* is fine — those infer as
	// ["null","array"] — so this is the only field that needs it.) The name is
	// spelled as Go spells it so the key on the wire does not change.
	Data map[string]string `yaml:"data,omitempty" json:"Data,omitempty"`
}

// Enabled reports whether a tracker is configured.
func (a Analytics) Enabled() bool { return a.ScriptURL != "" }

// Origin is the scheme://host the script is loaded from, which is what has to
// be allowed through the Content-Security-Policy for the tag to run at all.
// Empty when no tracker is configured or the URL will not parse.
func (a Analytics) Origin() string {
	if a.ScriptURL == "" {
		return ""
	}
	u, err := url.Parse(a.ScriptURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// DataKeys lists the data-* attribute names in a stable order, so the rendered
// tag does not reshuffle its attributes between builds.
func (a Analytics) DataKeys() []string {
	out := make([]string, 0, len(a.Data))
	for k := range a.Data {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidateAnalytics checks a tracker configuration before it is saved.
//
// The URL is the part that matters: it becomes a <script src> and an entry in
// the site's script-src policy, so a "javascript:" or "data:" URL here would be
// both a broken tag and a hole in the one header that stops a post's raw HTML
// from running anything.
func ValidateAnalytics(a Analytics) error {
	if a.ScriptURL == "" {
		if len(a.Data) > 0 {
			return errors.New("analytics data attributes were given without a script URL")
		}
		return nil
	}
	u, err := url.Parse(a.ScriptURL)
	if err != nil {
		return fmt.Errorf("analytics script URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("analytics script URL must be http:// or https:// (got %q)", a.ScriptURL)
	}
	if u.Host == "" {
		return fmt.Errorf("analytics script URL is missing a host (got %q)", a.ScriptURL)
	}
	for k := range a.Data {
		if !ValidAttrName(k) {
			return fmt.Errorf("analytics attribute name %q may only contain letters, digits and dashes", k)
		}
	}
	return nil
}

// ValidAttrName reports whether k is safe to write directly after "data-" in an
// attribute name. Deliberately narrower than the HTML grammar allows: every
// provider's attribute is lowercase words joined by dashes, and refusing the
// rest means nothing can smuggle a quote or a space out of the attribute.
//
// Exported because the renderer checks it again on the way out: a site.yml
// edited by hand never went through ValidateAnalytics, and an attribute name
// cannot be escaped after the fact the way a value can.
func ValidAttrName(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	// A leading or trailing dash would produce "data--x" or "data-x-", which is
	// legal but always a typo.
	return !strings.HasPrefix(k, "-") && !strings.HasSuffix(k, "-")
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
