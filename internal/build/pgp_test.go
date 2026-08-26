package build

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/pgp"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

const testArmored = `-----BEGIN PGP PUBLIC KEY BLOCK-----
Comment: 0956 E8AA E515 2D94 50B1  CA3F 4B88 B196 0413 50BC
Comment: Test <a@example.com>

d2tkLWJpbmFyeS1ib2R5
=xxxx
-----END PGP PUBLIC KEY BLOCK-----
`

func pgpTestEngine(t *testing.T, siteYML string, hits *atomic.Int32) *testEngine {
	t.Helper()
	contentDir := t.TempDir()
	publicDir := t.TempDir()
	templatesDir := filepath.Join("..", "..", "templates")
	if err := os.WriteFile(filepath.Join(contentDir, "site.yml"), []byte(siteYML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(contentDir, "posts"), 0o755); err != nil {
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	}))
	t.Cleanup(srv.Close)

	cache := pgp.New(filepath.Join(t.TempDir(), "pgp.json"))
	cache.BaseURL = srv.URL
	cache.Client = srv.Client()
	cache.Now = func() time.Time {
		return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	}

	engine, err := NewEngine(EngineConfig{
		ContentDir:   contentDir,
		PublicDir:    publicDir,
		Site:         func() *siteconfig.Config { return site },
		Store:        st,
		TemplatesDir: templatesDir,
		PGP:          cache,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return &testEngine{engine: engine, contentDir: contentDir, publicDir: publicDir, postsDir: filepath.Join(contentDir, "posts"), store: st}
}

func TestRebuildPublishesPGPAndWKD(t *testing.T) {
	var hits atomic.Int32
	siteYML := strings.Join([]string{
		"title: DT",
		"author: David",
		"base_url: https://example.com",
		"description: d",
		`bio: ["hello"]`,
		`nav: []`,
		`social: [{label: Contact, href: "mailto:a@example.com", icon: email}]`,
		"rss_feeds: []",
		`footer_left: ["DT"]`,
		"",
	}, "\n")
	te := pgpTestEngine(t, siteYML, &hits)
	te.writePost(t, "2026-01-31-hello.md", "---\ntitle: Hello\ndate: 2026-01-31\ndescription: d\ntags: [a]\ndraft: false\n---\n\nBody.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}

	asc := te.mustRead(t, "pgp.asc")
	if !strings.Contains(asc, "BEGIN PGP PUBLIC KEY BLOCK") {
		t.Fatalf("pgp.asc missing armor:\n%s", asc)
	}
	hash := pgp.WKDHash("a")
	bin := te.mustRead(t, ".well-known", "openpgpkey", "hu", hash)
	if bin != "wkd-binary-body" {
		t.Fatalf("direct WKD body = %q", bin)
	}
	adv := te.mustRead(t, ".well-known", "openpgpkey", "example.com", "hu", hash)
	if adv != "wkd-binary-body" {
		t.Fatalf("advanced WKD body = %q", adv)
	}
	if policy := te.mustRead(t, ".well-known", "openpgpkey", "policy"); policy != "\n" {
		t.Fatalf("policy = %q", policy)
	}

	home := te.mustRead(t, "index.html")
	for _, want := range []string{
		`rel="pgpkey"`,
		`data-contact-sheet`,
		`id="contact-sheet"`,
		`a@example.com`,
		`0956 E8AA E515 2D94 50B1 CA3F 4B88 B196 0413 50BC`,
		`BEGIN PGP PUBLIC KEY BLOCK`,
		`href="/pgp.asc"`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home missing %q", want)
		}
	}
	if strings.Contains(home, `mailto:a@example.com" target=`) {
		t.Errorf("mailto link opened in a new tab:\n%s", home)
	}
	article := te.mustRead(t, "posts", "hello", "index.html")
	if !strings.Contains(article, `data-contact-sheet`) || !strings.Contains(article, `id="contact-sheet"`) {
		t.Error("article page missing the contact sheet")
	}

	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("second rebuild hit the keyserver; hits = %d", hits.Load())
	}
}

func TestRebuildSkipsWKDWhenDomainDiffers(t *testing.T) {
	siteYML := strings.Join([]string{
		"title: DT",
		"author: David",
		"base_url: https://example.com",
		"description: d",
		`bio: ["hello"]`,
		`nav: []`,
		`social: [{label: Contact, href: "mailto:a@b.c", icon: email}]`,
		"rss_feeds: []",
		`footer_left: ["DT"]`,
		"",
	}, "\n")
	te := pgpTestEngine(t, siteYML, nil)
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(te.publicDir, "pgp.asc")); err != nil {
		t.Fatalf("pgp.asc: %v", err)
	}
	if _, err := os.Stat(filepath.Join(te.publicDir, ".well-known")); !os.IsNotExist(err) {
		t.Fatal("WKD published for a foreign email domain")
	}
}

func TestRebuildPrunesPGPWhenKeyGoesAway(t *testing.T) {
	var found atomic.Bool
	found.Store(true)
	contentDir := t.TempDir()
	publicDir := t.TempDir()
	siteYML := strings.Join([]string{
		"title: DT",
		"author: David",
		"base_url: https://example.com",
		"description: d",
		`bio: ["hello"]`,
		`nav: []`,
		`social: [{label: Contact, href: "mailto:a@example.com", icon: email}]`,
		"rss_feeds: []",
		`footer_left: ["DT"]`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(contentDir, "site.yml"), []byte(siteYML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(contentDir, "posts"), 0o755); err != nil {
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !found.Load() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	}))
	t.Cleanup(srv.Close)
	cache := pgp.New(filepath.Join(t.TempDir(), "pgp.json"))
	cache.BaseURL = srv.URL
	cache.Client = srv.Client()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	cache.Now = func() time.Time { return now }

	engine, err := NewEngine(EngineConfig{
		ContentDir:   contentDir,
		PublicDir:    publicDir,
		Site:         func() *siteconfig.Config { return site },
		Store:        st,
		TemplatesDir: filepath.Join("..", "..", "templates"),
		PGP:          cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(publicDir, "pgp.asc")); err != nil {
		t.Fatal(err)
	}
	found.Store(false)
	now = now.Add(25 * time.Hour)
	if err := engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(publicDir, "pgp.asc")); !os.IsNotExist(err) {
		t.Fatal("pgp.asc survived a 404")
	}
	home, err := os.ReadFile(filepath.Join(publicDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(home), "data-contact-sheet") {
		t.Fatal("contact sheet left on the page after the key vanished")
	}
}

func TestRebuildWithoutPGPCacheLeavesMailto(t *testing.T) {
	te := newTestEngine(t)
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	home := te.mustRead(t, "index.html")
	if !strings.Contains(home, `href="mailto:a@b.c"`) {
		t.Fatal("mailto vanished")
	}
	if strings.Contains(home, "data-contact-sheet") {
		t.Fatal("contact sheet appeared without a cache")
	}
	if _, err := os.Stat(filepath.Join(te.publicDir, "pgp.asc")); !os.IsNotExist(err) {
		t.Fatal("pgp.asc written with no cache")
	}
}
