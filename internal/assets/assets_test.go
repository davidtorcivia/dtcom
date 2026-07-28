package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestURLAppendsContentHash(t *testing.T) {
	dir := t.TempDir()
	css := filepath.Join(dir, "style.css")
	if err := os.WriteFile(css, []byte("body{color:red}"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := New(dir)

	first := f.URL("/static/style.css")
	if !strings.HasPrefix(first, "/static/style.css?v=") {
		t.Fatalf("URL = %q, want a versioned path", first)
	}

	// Same contents → same URL, so the cached copy stays valid.
	f.Refresh()
	if again := f.URL("/static/style.css"); again != first {
		t.Errorf("URL changed without an edit: %q → %q", first, again)
	}

	// Edited contents → new URL, so a returning visitor picks it up at once
	// rather than waiting out the cache TTL.
	if err := os.WriteFile(css, []byte("body{color:blue}"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.Refresh()
	if changed := f.URL("/static/style.css"); changed == first {
		t.Errorf("URL did not change after an edit: %q", changed)
	}
}

func TestURLPassesThroughUnknownPaths(t *testing.T) {
	f := New(t.TempDir())
	if got := f.URL("/static/missing.css"); got != "/static/missing.css" {
		t.Errorf("URL = %q, want the path unchanged", got)
	}
}

func TestURLHandlesNestedAndQueriedPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img", "logo.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := New(dir)
	got := f.URL("/static/img/logo.svg")
	if !strings.HasPrefix(got, "/static/img/logo.svg?v=") {
		t.Errorf("nested URL = %q", got)
	}
	// An already-versioned path must not accumulate query strings.
	if twice := f.URL(got); twice != got {
		t.Errorf("re-versioning produced %q, want %q", twice, got)
	}
}

// A nil Fingerprinter must degrade to a plain path rather than panic, so a
// stripped-down test wiring can render templates.
func TestNilFingerprinterIsSafe(t *testing.T) {
	var f *Fingerprinter
	f.Refresh()
	if got := f.URL("/static/style.css"); got != "/static/style.css" {
		t.Errorf("URL = %q", got)
	}
}
