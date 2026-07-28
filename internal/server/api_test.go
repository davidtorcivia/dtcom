package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIRequiresToken(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/articles", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAPIArticleCRUD(t *testing.T) {
	d := newTestDeps(t)
	// create
	body := strings.NewReader(`{"title":"New Post","date":"2026-02-01","description":"d","tags":["x"],"body":"Hello."}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/articles", body)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body.String())
	}
	// list should include it
	req = httptest.NewRequest(http.MethodGet, "/api/v1/articles", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "New Post") {
		t.Errorf("list missing new post:\n%s", rec.Body.String())
	}
	// get by slug
	req = httptest.NewRequest(http.MethodGet, "/api/v1/articles/new-post", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"body":"Hello.`) {
		t.Errorf("get missing body:\n%s", rec.Body.String())
	}
	// delete
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/articles/new-post", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete status = %d", rec.Code)
	}
}

func TestAPIArticleCreateConflict(t *testing.T) {
	d := newTestDeps(t)
	body := strings.NewReader(`{"title":"Hello","date":"2026-01-31","body":"dup"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/articles", body)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate slug+date, got %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestAPILinkAddList(t *testing.T) {
	d := newTestDeps(t)
	body := strings.NewReader(`{"label":"GitHub","href":"https://github.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links", body)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d, body %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/links", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "GitHub") {
		t.Errorf("list missing link:\n%s", rec.Body.String())
	}
}

func TestAPISiteBioUpdate(t *testing.T) {
	d := newTestDeps(t)
	body := strings.NewReader(`["new bio line"]`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/site/bio", body)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if bio := d.deps.Site().Bio; len(bio) != 1 || bio[0] != "new bio line" {
		t.Errorf("bio not updated: %v", bio)
	}
}

func TestAPIStatsAndRegenerate(t *testing.T) {
	d := newTestDeps(t)
	// stats
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status = %d", rec.Code)
	}
	// regenerate
	req = httptest.NewRequest(http.MethodPost, "/api/v1/regenerate", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("regenerate status = %d", rec.Code)
	}
}

func TestAPINotFound(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// TestAPIRejectsPathTraversalSlug verifies a caller-supplied slug containing
// ".." traversal characters is sanitized (rather than reaching filepath.Join
// verbatim and escaping the posts dir). The slug is run through slugify, which
// drops every char except [a-z0-9-]; "../../evil" becomes "evil".
func TestAPIRejectsPathTraversalSlug(t *testing.T) {
	d := newTestDeps(t)
	body := strings.NewReader(`{"title":"X","slug":"../../evil","date":"2026-01-01","body":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/articles", body)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	// should either 400 (rejected) or create with sanitized slug "evil" — NOT
	// write outside posts/.
	if rec.Code == http.StatusCreated {
		// the traversal target would be content/../evil.md (i.e. parent of posts,
		// in content dir) if the slug had escaped.
		evilPath := filepath.Join(d.deps.Cfg.ContentDir, "..", "evil.md")
		if _, err := os.Stat(evilPath); err == nil {
			t.Fatalf("traversal succeeded — file written outside posts dir: %s", evilPath)
		}
	}
	// verify no file with literal "../.." reached the filesystem.
	matches, _ := filepath.Glob(filepath.Join(d.deps.Cfg.ContentDir, "posts", "*evil*.md"))
	for _, m := range matches {
		if strings.Contains(m, "..") {
			t.Fatalf("unsanitized slug reached filesystem: %s", m)
		}
	}
}

// TestAPIRejectsBadDate verifies a caller-supplied date that isn't a strict
// YYYY-MM-DD literal is rejected (rather than reaching filepath.Join and the
// YAML frontmatter, where "../../etc" could escape the posts dir).
func TestAPIRejectsBadDate(t *testing.T) {
	d := newTestDeps(t)
	body := strings.NewReader(`{"title":"X","date":"../../etc/passwd","body":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/articles", body)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malicious date, got %d", rec.Code)
	}
}
