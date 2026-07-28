package siteconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yml")
	in := []byte("title: DT\nauthor: David\nbase_url: https://x\ndescription: d\nbio: [\"a\"]\nnav: [{label: Search, href: \"/search\"}]\nsocial: []\nrss_feeds: []\nfooter_left: [\"NYC\"]\n")
	if err := os.WriteFile(path, in, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Title != "DT" {
		t.Fatalf("Title = %q", cfg.Title)
	}
	if len(cfg.Nav) != 1 || cfg.Nav[0].Label != "Search" {
		t.Fatalf("Nav = %+v", cfg.Nav)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if cfg2.Title != cfg.Title {
		t.Fatal("round-trip mismatch")
	}
}

// content/ is gitignored, so a fresh deployment has no site.yml. The binary
// has to seed one and come up rather than refusing to start.
func TestLoadOrSeedCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.yml")
	c, err := LoadOrSeed(path)
	if err != nil {
		t.Fatalf("LoadOrSeed on a missing file: %v", err)
	}
	if c.Title == "" {
		t.Error("seeded config has no title")
	}
	if len(c.Nav) == 0 {
		t.Error("seeded config has no nav, so /search and /links are unreachable")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("site.yml was not written to disk: %v", err)
	}
	// Seeding must be a one-off: a second call reads what is there rather
	// than overwriting the author's edits.
	c.Title = "Edited By Hand"
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrSeed(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Title != "Edited By Hand" {
		t.Errorf("title = %q, want the edit preserved — LoadOrSeed overwrote an existing file", again.Title)
	}
}

// A file that exists but is malformed is a problem to report, not to silently
// replace with defaults.
func TestLoadOrSeedDoesNotClobberBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.yml")
	if err := os.WriteFile(path, []byte("title: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrSeed(path); err == nil {
		t.Fatal("LoadOrSeed accepted malformed YAML")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "unclosed") {
		t.Error("LoadOrSeed overwrote a malformed site.yml instead of reporting it")
	}
}
