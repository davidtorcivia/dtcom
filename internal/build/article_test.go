package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadArticleFromDisk(t *testing.T) {
	dir := t.TempDir()
	body := "---\ntitle: Test\ndate: 2026-01-31\ndescription: d\ntags: [a, b]\ndraft: false\n---\n\nHello ==world==.\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-01-31-test.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	arts, err := LoadArticles(dir)
	if err != nil {
		t.Fatalf("LoadArticles: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("got %d articles", len(arts))
	}
	a := arts[0]
	if a.Title != "Test" || a.Slug != "test" {
		t.Errorf("got %+v", a)
	}
	if a.Date.Year() != 2026 {
		t.Errorf("Date = %v", a.Date)
	}
	if a.Body != "Hello ==world==.\n" {
		t.Errorf("Body = %q", a.Body)
	}
}
