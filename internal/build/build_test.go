package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// testEngine builds an engine over a temp content tree and the real
// templates/ directory, returning it alongside the dirs the test needs.
type testEngine struct {
	engine     *Engine
	contentDir string
	publicDir  string
	postsDir   string
	store      *store.Store
}

func newTestEngine(t *testing.T) *testEngine {
	t.Helper()
	contentDir := t.TempDir()
	publicDir := t.TempDir()
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
		`social: [{label: X, href: "https://x.com/x", icon: x}, {label: Contact, href: "mailto:a@b.c", icon: email}]`,
		"rss_feeds: []",
		`footer_left: ["DT 2026", "NYC"]`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(contentDir, "site.yml"), []byte(siteYML), 0o644); err != nil {
		t.Fatal(err)
	}
	postsDir := filepath.Join(contentDir, "posts")
	if err := os.MkdirAll(postsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	site, err := siteconfig.Load(filepath.Join(contentDir, "site.yml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	engine, err := NewEngine(EngineConfig{
		ContentDir:   contentDir,
		PublicDir:    publicDir,
		Site:         func() *siteconfig.Config { return site },
		Store:        st,
		TemplatesDir: templatesDir,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return &testEngine{engine: engine, contentDir: contentDir, publicDir: publicDir, postsDir: postsDir, store: st}
}

func (te *testEngine) writePost(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(te.postsDir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (te *testEngine) mustRead(t *testing.T, rel ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{te.publicDir}, rel...)...)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// TestRebuildWritesPublic exercises the real templates/ directory against a
// minimal content tree. It verifies that every render method in Rebuild
// produces the expected output files: article HTML + .md, home, links, search,
// 404, feed, sitemap, and robots.
func TestRebuildWritesPublic(t *testing.T) {
	te := newTestEngine(t)
	te.writePost(t, "2026-01-31-hello.md",
		"---\ntitle: Hello\ndate: 2026-01-31\ndescription: d\ntags: [a]\ndraft: false\n---\n\nBody text.\n")

	// seed one manual link so the links index is non-empty
	if _, err := te.store.AddLink(store.Link{
		Label:    "A link",
		Href:     "https://example.org",
		Source:   "manual",
		SortDate: 1738368000, // 2025-02-01
	}); err != nil {
		t.Fatal(err)
	}

	if err := te.engine.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// article HTML + .md
	artHTML := te.mustRead(t, "posts", "hello", "index.html")
	for _, want := range []string{
		"<p>Body text.</p>",
		"<title>Hello — DT</title>",
		`rel="canonical" href="https://example.com/posts/hello"`,
		`data-track-path="/posts/hello"`,
	} {
		if !strings.Contains(artHTML, want) {
			t.Errorf("article HTML missing %q:\n%s", want, artHTML)
		}
	}
	// A post with no cover must not claim an og:image — the old default
	// pointed at a file that was never shipped.
	if strings.Contains(artHTML, `property="og:image"`) {
		t.Errorf("article advertises og:image with no cover set:\n%s", artHTML)
	}
	// No inline <script> anywhere, so the CSP can forbid them.
	if strings.Contains(artHTML, "<script>") {
		t.Errorf("article contains an inline script:\n%s", artHTML)
	}
	if md := te.mustRead(t, "posts", "hello.md"); !strings.Contains(md, "Body text.") {
		t.Errorf("article md missing body:\n%s", md)
	}

	// home: article index row + bio + a social icon
	homeHTML := te.mustRead(t, "index.html")
	for _, want := range []string{
		`href="/posts/hello"`, ">Hello<", `class="bio-section"`,
		`class="social-icon"`, `class="footer-left"`, `href="mailto:a@b.c"`,
	} {
		if !strings.Contains(homeHTML, want) {
			t.Errorf("home HTML missing %q:\n%s", want, homeHTML)
		}
	}

	if links := te.mustRead(t, "links", "index.html"); !strings.Contains(links, `href="https://example.org"`) {
		t.Errorf("links missing the seeded link:\n%s", links)
	}
	// The search page shell must exist — the route that serves it 404'd for as
	// long as the engine had no renderSearch.
	if search := te.mustRead(t, "search", "index.html"); !strings.Contains(search, `id="search-input"`) {
		t.Errorf("search page missing its input:\n%s", search)
	}
	if nf := te.mustRead(t, "404.html"); !strings.Contains(nf, "404") {
		t.Errorf("404 page not rendered:\n%s", nf)
	}
	if feed := te.mustRead(t, "feed.xml"); !strings.Contains(feed, "<title>Hello</title>") {
		t.Errorf("feed missing article title:\n%s", feed)
	}
	sitemap := te.mustRead(t, "sitemap.xml")
	if !strings.Contains(sitemap, "https://example.com/posts/hello") {
		t.Errorf("sitemap missing article url:\n%s", sitemap)
	}
	if !strings.Contains(sitemap, "<lastmod>2026-01-31</lastmod>") {
		t.Errorf("sitemap missing lastmod:\n%s", sitemap)
	}
	if robots := te.mustRead(t, "robots.txt"); !strings.Contains(robots, "Sitemap: https://example.com/sitemap.xml") {
		t.Errorf("robots missing sitemap line:\n%s", robots)
	}
}

// A deleted post must stop being served. The rebuild writes first and prunes
// afterwards, so this checks the prune half actually retires stale output.
func TestRebuildPrunesDeletedPost(t *testing.T) {
	te := newTestEngine(t)
	te.writePost(t, "2026-01-31-hello.md", "---\ntitle: Hello\ndate: 2026-01-31\n---\n\nBody.\n")
	te.writePost(t, "2026-02-01-gone.md", "---\ntitle: Gone\ndate: 2026-02-01\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(te.publicDir, "posts", "gone", "index.html")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("expected %s after first build: %v", stale, err)
	}

	if err := os.Remove(filepath.Join(te.postsDir, "2026-02-01-gone.md")); err != nil {
		t.Fatal(err)
	}
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("deleted post is still published at %s (err=%v)", stale, err)
	}
	if _, err := os.Stat(filepath.Join(te.publicDir, "posts", "gone")); !os.IsNotExist(err) {
		t.Error("emptied post directory was not removed")
	}
	if _, err := os.Stat(filepath.Join(te.publicDir, "posts", "gone.md")); !os.IsNotExist(err) {
		t.Error("deleted post's .md variant is still published")
	}
	// The surviving post must be untouched by the prune.
	if _, err := os.Stat(filepath.Join(te.publicDir, "posts", "hello", "index.html")); err != nil {
		t.Errorf("prune removed a live page: %v", err)
	}
}

// Flipping a post to draft must unpublish it, not just hide it from the index.
func TestRebuildPrunesDraftedPost(t *testing.T) {
	te := newTestEngine(t)
	te.writePost(t, "2026-01-31-hello.md", "---\ntitle: Hello\ndate: 2026-01-31\ndraft: false\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(te.publicDir, "posts", "hello", "index.html")
	if _, err := os.Stat(page); err != nil {
		t.Fatal(err)
	}

	te.writePost(t, "2026-01-31-hello.md", "---\ntitle: Hello\ndate: 2026-01-31\ndraft: true\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(page); !os.IsNotExist(err) {
		t.Errorf("drafted post is still published (err=%v)", err)
	}
}

// The home page must stay readable for the whole rebuild. Emptying public/
// first (the previous approach) left every URL 404ing mid-rebuild.
func TestRebuildKeepsPagesReadable(t *testing.T) {
	te := newTestEngine(t)
	te.writePost(t, "2026-01-31-hello.md", "---\ntitle: Hello\ndate: 2026-01-31\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- te.engine.Rebuild() }()
	home := filepath.Join(te.publicDir, "index.html")
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Rebuild: %v", err)
			}
			return
		default:
			if _, err := os.Stat(home); err != nil {
				t.Fatalf("index.html vanished during rebuild: %v", err)
			}
		}
	}
}

func TestStripMarkdownRemovesHTML(t *testing.T) {
	in := "Text with <script>alert(1)</script> and **bold** and `code` and [a link](http://x)."
	got := stripMarkdown(in)
	for _, unwanted := range []string{"<script>", "</script>", "**", "`"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("stripMarkdown left %q in %q", unwanted, got)
		}
	}
	if !strings.Contains(got, "a link") {
		t.Errorf("stripMarkdown dropped link text: %q", got)
	}
}

// A template that fails to parse must be reported at construction, not
// swallowed into a nil template that panics on the first render.
func TestNewEngineReportsTemplateError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.html"), []byte(`{{define "x"}}{{ .Unclosed `), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(EngineConfig{ContentDir: t.TempDir(), PublicDir: t.TempDir(), TemplatesDir: dir}); err == nil {
		t.Error("NewEngine accepted an unparseable template")
	}
}
