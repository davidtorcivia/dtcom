package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// TestRebuildWritesPublic exercises the real templates/ directory against a
// minimal content tree. It verifies that every render method in Rebuild
// produces the expected output files: article HTML + .md, home, links, feed,
// sitemap, and robots.
func TestRebuildWritesPublic(t *testing.T) {
	contentDir := t.TempDir()
	publicDir := t.TempDir()
	// real templates directory (../../templates relative to this test file)
	templatesDir := filepath.Join("..", "..", "templates")
	if _, err := os.Stat(filepath.Join(templatesDir, "home.html")); err != nil {
		t.Fatalf("real templates dir missing home.html: %v", err)
	}

	// site.yml — fields exercised by home/article/footer/social templates.
	siteYML := strings.Join([]string{
		"title: DT",
		"author: David",
		"base_url: https://example.com",
		"description: d",
		`bio: ["hello <strong>world</strong>"]`,
		`nav: [{label: Links, href: "/links"}, {label: Search, href: "/search"}]`,
		`social: [{label: X, href: "https://x.com/x", icon: x}]`,
		"rss_feeds: []",
		`footer_left: ["DT 2026", "NYC"]`,
		"",
	}, "\n")
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

	site, err := siteconfig.Load(filepath.Join(contentDir, "site.yml"))
	if err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// seed one manual link so the links index is non-empty
	if _, err := st.AddLink(store.Link{
		Label:    "A link",
		Href:     "https://example.org",
		Source:   "manual",
		SortDate: 1738368000, // 2025-02-01
	}); err != nil {
		t.Fatal(err)
	}

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

	mustRead := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(b)
	}

	// article HTML + .md
	artHTML := mustRead(filepath.Join(publicDir, "posts", "hello", "index.html"))
	if !strings.Contains(artHTML, "<p>Body text.</p>") {
		t.Errorf("article body not rendered:\n%s", artHTML)
	}
	if !strings.Contains(artHTML, "<title>Hello — DT</title>") {
		t.Errorf("article title tag wrong:\n%s", artHTML)
	}
	if !strings.Contains(artHTML, `property="og:image"`) {
		t.Errorf("article missing og:image:\n%s", artHTML)
	}
	artMD := mustRead(filepath.Join(publicDir, "posts", "hello.md"))
	if !strings.Contains(artMD, "Body text.") {
		t.Errorf("article md missing body:\n%s", artMD)
	}

	// home: contains article index row + bio + a social icon
	homeHTML := mustRead(filepath.Join(publicDir, "index.html"))
	for _, want := range []string{
		`href="/posts/hello"`,
		">Hello<",
		`class="bio-section"`,
		`class="social-icon"`,
		`class="footer-left"`,
	} {
		if !strings.Contains(homeHTML, want) {
			t.Errorf("home HTML missing %q:\n%s", want, homeHTML)
		}
	}

	// links
	linksHTML := mustRead(filepath.Join(publicDir, "links", "index.html"))
	if !strings.Contains(linksHTML, `href="https://example.org"`) {
		t.Errorf("links missing the seeded link:\n%s", linksHTML)
	}

	// feed
	feed := mustRead(filepath.Join(publicDir, "feed.xml"))
	if !strings.Contains(feed, "<title>Hello</title>") {
		t.Errorf("feed missing article title:\n%s", feed)
	}

	// sitemap
	sitemap := mustRead(filepath.Join(publicDir, "sitemap.xml"))
	if !strings.Contains(sitemap, "https://example.com/posts/hello") {
		t.Errorf("sitemap missing article url:\n%s", sitemap)
	}

	// robots
	robots := mustRead(filepath.Join(publicDir, "robots.txt"))
	if !strings.Contains(robots, "Sitemap: https://example.com/sitemap.xml") {
		t.Errorf("robots missing sitemap line:\n%s", robots)
	}
}
