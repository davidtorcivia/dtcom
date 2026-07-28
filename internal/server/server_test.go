package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/auth"
	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/config"
	"davidtorcivia.com/dtcom/internal/feeds"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// testDeps bundles the wired mux with the bits individual tests need to poke
// at (the live Deps.Site closure, the bearer token, the public dir).
type testDeps struct {
	mux      http.Handler
	deps     *Deps
	apiToken string
	pubDir   string
}

// newTestDeps builds a fully wired server against a tempdir content tree and
// the real repo templates/. Rebuild runs once so public/ is populated and the
// search index is filled.
func newTestDeps(t *testing.T) *testDeps {
	t.Helper()
	contentDir := t.TempDir()
	pubDir := t.TempDir()
	staticDir := t.TempDir()

	siteYML := strings.Join([]string{
		"title: DT",
		"author: David",
		"base_url: https://x",
		"description: d",
		`bio: ["hi"]`,
		`nav: [{label: Search, href: "/search"}]`,
		`social: []`,
		`rss_feeds: []`,
		`footer_left: ["NYC"]`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(contentDir, "site.yml"), []byte(siteYML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(contentDir, "posts"), 0o755); err != nil {
		t.Fatal(err)
	}
	art := "---\ntitle: Hello\ndate: 2026-01-31\ndescription: d\ntags: [a]\ndraft: false\n---\n\nBody text.\n"
	if err := os.WriteFile(filepath.Join(contentDir, "posts", "2026-01-31-hello.md"), []byte(art), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "style.css"), []byte("body{}"), 0o644); err != nil {
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

	cfg := &config.Config{
		BaseURL:           "https://x",
		ListenAddr:        ":0",
		AdminPasswordHash: "$2a$10$abc",
		TOTPSecret:        "JBSWY3DPEHPK3PXP",
		SessionKey:        "sesskey",
		APIToken:          "apitok",
		ContentDir:        contentDir,
		StaticDir:         staticDir,
		PublicDir:         pubDir,
		DBPath:            filepath.Join(t.TempDir(), "db"),
		ImagesDir:         t.TempDir(),
		SiteYAMLPath:      filepath.Join(contentDir, "site.yml"),
	}

	engine := build.NewEngine(build.EngineConfig{
		ContentDir:   contentDir,
		PublicDir:    pubDir,
		Site:         func() *siteconfig.Config { return site },
		Store:        st,
		TemplatesDir: filepath.Join("..", "..", "templates"),
	})
	if err := engine.Rebuild(); err != nil {
		t.Fatal(err)
	}

	a := auth.New(cfg.SessionKey, cfg.AdminPasswordHash, cfg.TOTPSecret)

	d := &Deps{
		Cfg:    cfg,
		Site:   func() *siteconfig.Config { return site },
		Store:  st,
		Engine: engine,
		Poller: feeds.NewPoller(st),
		Auth:   a,
	}
	return &testDeps{mux: New(d), deps: d, apiToken: "apitok", pubDir: pubDir}
}

func TestStaticFileServed(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSearchEndpoint(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=body", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Hello") {
		t.Errorf("missing hit:\n%s", rec.Body.String())
	}
}

func TestViewBeacon(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/api/track", strings.NewReader(`{"path":"/posts/hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestMarkdownContentNegotiation(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/posts/hello", nil)
	req.Header.Set("Accept", "text/markdown")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestArticleMDRoute(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/posts/hello.md", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestBotsIgnoredByBeacon(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/api/track", strings.NewReader(`{"path":"/posts/hello"}`))
	req.Header.Set("User-Agent", "GoogleBot/2.1")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	// bot should still get 204 but no view recorded
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d", rec.Code)
	}
}
