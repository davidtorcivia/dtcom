package siteconfig

import (
	"os"
	"path/filepath"
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
