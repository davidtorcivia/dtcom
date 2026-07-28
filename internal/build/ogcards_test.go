package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A card is named after what it says, so an unchanged post reuses the file it
// already has. Rebuilds fire on every RSS poll, and re-encoding a PNG per post
// each time would be pure waste.
func TestOGCardIsReusedAndPrunedWithItsPost(t *testing.T) {
	te := newTestEngine(t)
	te.writePost(t, "2026-01-31-hello.md",
		"---\ntitle: Hello\ndate: 2026-01-31\ndescription: d\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}

	ogDir := filepath.Join(te.publicDir, "og")
	first := ogFiles(t, ogDir)
	if len(first) == 0 {
		t.Fatal("no og cards were generated")
	}
	stat, err := os.Stat(filepath.Join(ogDir, first[0]))
	if err != nil {
		t.Fatal(err)
	}

	// A rebuild with unchanged content must neither rewrite the card nor prune
	// it: prune deletes anything the rebuild did not record as written.
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	again := ogFiles(t, ogDir)
	if strings.Join(again, ",") != strings.Join(first, ",") {
		t.Errorf("card set changed on a no-op rebuild:\n%v\n%v", first, again)
	}
	stat2, err := os.Stat(filepath.Join(ogDir, first[0]))
	if err != nil {
		t.Fatalf("card was pruned by the second rebuild: %v", err)
	}
	if !stat2.ModTime().Equal(stat.ModTime()) {
		t.Error("card was re-rendered despite unchanged content")
	}

	// Retitling the post changes the card's content, so it must get a new URL
	// — that is what makes a scraper refetch instead of serving its cache —
	// and the old file must be pruned.
	te.writePost(t, "2026-01-31-hello.md",
		"---\ntitle: Hello Again\ndate: 2026-01-31\ndescription: d\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	after := ogFiles(t, ogDir)
	if strings.Join(after, ",") == strings.Join(first, ",") {
		t.Error("retitling the post did not change its card")
	}
	for _, name := range after {
		if name == first[0] {
			t.Errorf("the superseded card %s was not pruned", name)
		}
	}
}

func ogFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// An explicit cover is the author's choice and must win over a generated card.
func TestExplicitCoverWinsOverGeneratedCard(t *testing.T) {
	te := newTestEngine(t)
	te.writePost(t, "2026-01-31-hello.md",
		"---\ntitle: Hello\ndate: 2026-01-31\ndescription: d\ncover: https://cdn.example.org/pic.jpg\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	html := te.mustRead(t, "posts", "hello", "index.html")
	if !strings.Contains(html, `property="og:image" content="https://cdn.example.org/pic.jpg"`) {
		t.Errorf("cover was not used as og:image:\n%s", html)
	}
}
