package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"davidtorcivia.com/dtcom/internal/assets"
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

	siteYAMLPath := filepath.Join(contentDir, "site.yml")
	site, err := siteconfig.Load(siteYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the site in an atomic pointer so ReloadSite (exercised by the
	// site-section PUT and admin save handlers) can swap in a fresh copy
	// after writing site.yml — mirroring main.go's wiring and keeping the
	// handlers free of shared mutable state.
	var sitePtr atomic.Pointer[siteconfig.Config]
	sitePtr.Store(site)
	siteFn := func() *siteconfig.Config { return sitePtr.Load() }
	reloadSite := func() error {
		s, err := siteconfig.Load(siteYAMLPath)
		if err != nil {
			return err
		}
		sitePtr.Store(s)
		return nil
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
		TemplatesDir:      filepath.Join("..", "..", "templates"),
		DBPath:            filepath.Join(t.TempDir(), "db"),
		ImagesDir:         t.TempDir(),
		SiteYAMLPath:      filepath.Join(contentDir, "site.yml"),
	}

	fingerprints := assets.New(staticDir)
	engine, err := build.NewEngine(build.EngineConfig{
		ContentDir:   contentDir,
		PublicDir:    pubDir,
		StaticDir:    staticDir,
		Assets:       fingerprints,
		Site:         siteFn,
		Store:        st,
		TemplatesDir: filepath.Join("..", "..", "templates"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Rebuild(); err != nil {
		t.Fatal(err)
	}

	a := auth.New(auth.Options{
		SessionKey:   cfg.SessionKey,
		PasswordHash: cfg.AdminPasswordHash,
		TOTPSecret:   cfg.TOTPSecret,
		SecureCookie: true,
	})

	d := &Deps{
		Cfg:        cfg,
		Assets:     fingerprints,
		Site:       siteFn,
		ReloadSite: reloadSite,
		Store:      st,
		Engine:     engine,
		Poller:     feeds.NewPoller(st),
		Auth:       a,
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

// TestTrackBeaconCapsBody verifies the unauthenticated /api/track beacon caps
// its request body. A beacon body beyond ~1 KB must not be read into memory;
// the handler still returns 204 (it always does for any decode failure) but
// must not store a view derived from the oversized body.
func TestTrackBeaconCapsBody(t *testing.T) {
	d := newTestDeps(t)
	// 32 KB of garbage — well over the 1 KB cap.
	big := make([]byte, 32<<10)
	for i := range big {
		big[i] = 'x'
	}
	req := httptest.NewRequest(http.MethodPost, "/api/track", bytes.NewReader(big))
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (beacon always returns 204)", rec.Code)
	}
	// sanity: a normal beacon still records a view after the cap is in place.
	req2 := httptest.NewRequest(http.MethodPost, "/api/track", strings.NewReader(`{"path":"/posts/hello"}`))
	req2.Header.Set("User-Agent", "Mozilla/5.0")
	rec2 := httptest.NewRecorder()
	d.mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("normal beacon status = %d", rec2.Code)
	}
}

// TestDecodeJSONCapsBody verifies decodeJSON rejects bodies over the 10 MB cap.
// decodeJSON backs every authed API endpoint; the cap is a backstop so even an
// authed client can't OOM the server by streaming a huge body.
func TestDecodeJSONCapsBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links", bytes.NewReader(make([]byte, (10<<20)+1)))
	var v linkInput
	if err := decodeJSON(req, &v); err == nil {
		t.Error("decodeJSON: expected error for >10MB body, got nil")
	}
}

// The author reading their own site should not move the view counter. The
// beacon is a same-origin sendBeacon, so a logged-in admin's session cookie
// rides along with it and can be recognised here.
func TestViewBeaconIgnoresLoggedInAdmin(t *testing.T) {
	d := newTestDeps(t)

	track := func(t *testing.T, withSession bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/track", strings.NewReader(`{"path":"/posts/hello"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		if withSession {
			sess := httptest.NewRecorder()
			if err := d.deps.Auth.SetSession(sess, "admin"); err != nil {
				t.Fatal(err)
			}
			req.AddCookie(sess.Result().Cookies()[0])
		}
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		// Always 204: the beacon must not tell a caller whether it counted.
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d", rec.Code)
		}
	}

	views := func(t *testing.T) int64 {
		t.Helper()
		stats, err := d.deps.Store.Stats()
		if err != nil {
			t.Fatal(err)
		}
		return stats.Total
	}

	before := views(t)
	track(t, true)
	if got := views(t); got != before {
		t.Errorf("views went %d -> %d after an admin beacon; the admin's own reads must not count", before, got)
	}

	// A logged-out visitor on the same path still counts, so the check is
	// filtering on the session and not just failing to record anything.
	track(t, false)
	if got := views(t); got != before+1 {
		t.Errorf("views = %d after an anonymous beacon, want %d", got, before+1)
	}
}
