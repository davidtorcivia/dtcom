package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The og:image a page advertises has to be fetchable. It is generated into
// public/og/, which is served by its own route — public/ is not exposed by a
// blanket file server, so a missing route means every card 404s while the
// markup still points at it.
func TestOGCardIsServed(t *testing.T) {
	d := newTestDeps(t)

	page := d.get("/posts/hello")
	if page.Code != http.StatusOK {
		t.Fatalf("article = %d", page.Code)
	}
	m := regexp.MustCompile(`property="og:image" content="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatal("article advertises no og:image")
	}
	path := m[1]
	if i := strings.Index(path, "/og/"); i >= 0 {
		path = path[i:]
	} else {
		t.Fatalf("og:image is not an /og/ URL: %q", m[1])
	}

	rec := d.get(path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache-control = %q, want an immutable cache on a content-addressed name", cc)
	}
}

// A card must not be reachable by climbing out of the og directory.
func TestOGRouteRejectsTraversal(t *testing.T) {
	d := newTestDeps(t)
	for _, p := range []string{"/og/../index.html", "/og/%2e%2e/index.html"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("%s served an HTML page from outside og/", p)
		}
	}
}
