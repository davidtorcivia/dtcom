package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

func TestRebuildWritesPublic(t *testing.T) {
	contentDir := t.TempDir()
	publicDir := t.TempDir()
	templatesDir := t.TempDir()

	// site.yml
	siteYML := "title: DT\nauthor: David\nbase_url: https://x\ndescription: d\nbio: [\"hi\"]\nnav: []\nsocial: []\nrss_feeds: []\nfooter_left: [\"NYC\"]\n"
	if err := os.WriteFile(filepath.Join(contentDir, "site.yml"), []byte(siteYML), 0o644); err != nil {
		t.Fatal(err)
	}
	// posts dir with one article
	if err := os.MkdirAll(filepath.Join(contentDir, "posts"), 0o755); err != nil {
		t.Fatal(err)
	}
	art := "---\ntitle: Hello\ndate: 2026-01-31\ndescription: d\ntags: [a]\ndraft: false\n---\n\nBody text.\n"
	if err := os.WriteFile(filepath.Join(contentDir, "posts", "2026-01-31-hello.md"), []byte(art), 0o644); err != nil {
		t.Fatal(err)
	}
	// minimal templates (these will be replaced with real ones in Task 7.1)
	articleTmpl := `{{define "article"}}<article>{{.HTML | htmlSafe}}</article>{{end}}`
	if err := os.WriteFile(filepath.Join(templatesDir, "article.html"), []byte(articleTmpl), 0o644); err != nil {
		t.Fatal(err)
	}

	site, err := siteconfig.Load(filepath.Join(contentDir, "site.yml"))
	if err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b := NewEngine(EngineConfig{
		ContentDir:   contentDir,
		PublicDir:    publicDir,
		Site:         func() *siteconfig.Config { return site },
		Store:        st,
		TemplatesDir: templatesDir,
	})
	if err := b.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// article HTML + .md (home/links/feed rendering is tested in Task 7.1,
	// which adds those render methods + templates; here we only verify the
	// article path and .md variant that this task implements).
	artHTML, err := os.ReadFile(filepath.Join(publicDir, "posts", "hello", "index.html"))
	if err != nil {
		t.Fatalf("read article html: %v", err)
	}
	if !strings.Contains(string(artHTML), "<p>Body text.</p>") {
		t.Errorf("article body not rendered:\n%s", artHTML)
	}
	artMD, err := os.ReadFile(filepath.Join(publicDir, "posts", "hello.md"))
	if err != nil {
		t.Fatalf("read article md: %v", err)
	}
	if !strings.Contains(string(artMD), "Body text.") {
		t.Errorf("article md missing body:\n%s", artMD)
	}
}
