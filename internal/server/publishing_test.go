package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

func TestSignedPreviewAndReadiness(t *testing.T) {
	d := newTestDeps(t)
	if slug, _, _, err := d.deps.createArticle(articleInput{Title: "Private Draft", Body: "secret preview", Draft: true}); err != nil || slug != "private-draft" {
		t.Fatalf("create draft = %q, %v", slug, err)
	}

	req := httptest.NewRequest(http.MethodGet, d.deps.previewURL("private-draft", testFuture()), nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "secret preview") || !strings.Contains(rec.Body.String(), `name="robots" content="noindex`) {
		t.Fatalf("signed preview = %d:\n%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "data-track-path") {
		t.Fatal("preview includes reader tracking")
	}

	tampered := strings.Replace(req.URL.String(), "private-draft", "hello", 1)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tampered, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("tampered preview = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d: %s", rec.Code, rec.Body.String())
	}
	if err := os.WriteFile(filepath.Join(d.deps.Cfg.ContentDir, "posts", "broken.md"), []byte("---\ntitle: [broken\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.deps.Engine.Rebuild(); err == nil {
		t.Fatal("broken source unexpectedly rebuilt")
	}
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "last_build_error") {
		t.Fatalf("last-good readiness = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSiteMutationRollsBackWhenBuildFails(t *testing.T) {
	d := newTestDeps(t)
	before, err := os.ReadFile(d.deps.Cfg.SiteYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.deps.Cfg.ContentDir, "posts", "broken.md"), []byte("---\ntitle: [broken\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = d.deps.mutateSite(func(site *siteconfig.Config) error {
		site.Title = "Must Roll Back"
		return nil
	})
	if err == nil {
		t.Fatal("mutation succeeded despite broken content")
	}
	after, err := os.ReadFile(d.deps.Cfg.SiteYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || d.deps.Site().Title == "Must Roll Back" {
		t.Fatal("failed rebuild left the site mutation applied")
	}
}

func TestAuditUndoRestoresOriginalDateAndSource(t *testing.T) {
	d := newTestDeps(t)
	slug, _, _, err := d.deps.createArticle(articleInput{Title: "Undo Me", Date: "2026-01-01", Body: "before"})
	if err != nil {
		t.Fatal(err)
	}
	revision := articleRevision(t, d, slug)
	if _, _, err := d.deps.updateArticle(slug, articleInput{Title: "Undo Me", Date: "2026-02-02", Body: "after", ExpectedRevision: revision}); err != nil {
		t.Fatal(err)
	}
	audits, err := d.deps.Store.ListArticleAudit(10)
	if err != nil || len(audits) < 1 {
		t.Fatalf("audit = %+v, %v", audits, err)
	}
	audit, err := d.deps.Store.ArticleAudit(audits[0].ID)
	if err != nil || audit == nil {
		t.Fatalf("load audit = %+v, %v", audit, err)
	}
	if err := d.deps.undoArticleAudit(*audit); err != nil {
		t.Fatal(err)
	}
	a, err := d.deps.findArticleBySlug(slug)
	if err != nil || a == nil || a.Date.Format("2006-01-02") != "2026-01-01" || filepath.Base(a.SourcePath) != "2026-01-01-undo-me.md" {
		t.Fatalf("restored article = %+v, %v", a, err)
	}
}

func TestDraftTokenCannotPublish(t *testing.T) {
	d := newTestDeps(t)
	liveSlug, _, _, err := d.deps.createArticle(articleInput{Title: "Existing Live", Body: "live"})
	if err != nil {
		t.Fatal(err)
	}
	liveRevision := articleRevision(t, d, liveSlug)
	raw, _, err := d.deps.Store.CreateScopedAPIToken("draft agent", "read,drafts")
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/articles", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+raw)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"title":"Unsafe Live","body":"no"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("published create = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"title":"Safe Draft","body":"yes","draft":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("draft create = %d: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/articles/"+liveSlug,
		strings.NewReader(`{"title":"Existing Live","body":"changed","draft":true,"expected_revision":"`+liveRevision+`"}`))
	req.Header.Set("Authorization", "Bearer "+raw)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unpublish existing article = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentArticleVariant(t *testing.T) {
	d := newTestDeps(t)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/posts/hello/agent.md", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Agent-Optimized") != "true" || !strings.Contains(rec.Body.String(), "Canonical:") {
		t.Fatalf("agent variant = %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
}
