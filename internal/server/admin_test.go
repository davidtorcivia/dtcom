package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// newTestDepsWithAdmin is newTestDeps but with admin templates loaded from the
// real repo templates/admin dir, so admin handlers can actually render.
func newTestDepsWithAdmin(t *testing.T) *testDeps {
	t.Helper()
	td := newTestDeps(t)
	dir := filepath.Join("..", "..", "templates", "admin")
	ts, err := newAdminTemplates(dir, td.deps.assets)
	if err != nil {
		t.Fatalf("load admin templates from %s: %v", dir, err)
	}
	td.deps.adminTmpls = ts
	return td
}

func TestAdminRedirectsToLoginWhenUnauth(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", loc)
	}
}

func TestAdminLoginRenders(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Login") {
		t.Errorf("login page missing title:\n%s", rec.Body.String())
	}
}

// TestAdminFullFlow logs in, hits every admin page, and confirms each renders.
// It can't easily test TOTP, so it forges a session cookie directly via the
// Auth helper.
func TestAdminAuthedPagesRender(t *testing.T) {
	d := newTestDepsWithAdmin(t)

	// Forge a valid session cookie by calling SetSession against a throwaway
	// recorder, then reuse the cookie value on subsequent requests.
	rec := httptest.NewRecorder()
	if err := d.deps.Auth.SetSession(rec, "admin"); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]

	pages := []string{
		"/admin",
		"/admin/posts",
		"/admin/posts/new",
		"/admin/links",
		"/admin/site",
	}
	for _, p := range pages {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, body:\n%s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestAdminPostEditExistingSlug(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/admin/posts/hello/edit", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Hello") {
		t.Errorf("edit form missing title:\n%s", rec.Body.String())
	}
}

func TestAdminPostPreview(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	form := strings.NewReader("body=" + "**bold**")
	req := httptest.NewRequest(http.MethodPost, "/admin/posts/preview", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<strong>bold</strong>") {
		t.Errorf("preview missing rendered markdown:\n%s", rec.Body.String())
	}
}

func TestAdminPostSaveNew(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	form := strings.NewReader(strings.Join([]string{
		"title=Admin+Post",
		"date=2026-03-01",
		"description=d",
		"tags=t1,+t2",
		"body=Created+via+admin.",
	}, "&"))
	req := httptest.NewRequest(http.MethodPost, "/admin/posts/save", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body:\n%s", rec.Code, rec.Body.String())
	}

	// The post should now exist on disk under the slugified title.
	target := filepath.Join(d.deps.Cfg.ContentDir, "posts", "2026-03-01-admin-post.md")
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected file %s: %v", target, err)
	}
	if !strings.Contains(string(b), "Created via admin.") {
		t.Errorf("saved file missing body:\n%s", b)
	}
}

func TestAdminLoginBadCredentials(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	form := strings.NewReader("password=wrong&totp=000000")
	req := httptest.NewRequest(http.MethodPost, "/admin/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// The Links page checkbox and the Site page dropdown write the same site.yml
// key, so a round trip through the checkbox has to land in the file, in the
// live config, and in the rebuilt /links page.
//
// The unchecked case is the one worth pinning down: a cleared checkbox submits
// no field at all, and the handler reads that absence as "show the notes". Get
// that backwards and the box becomes impossible to untick.
func TestAdminLinksStyleCheckbox(t *testing.T) {
	d := newTestDepsWithAdmin(t)

	linksStyle := func() string {
		t.Helper()
		site, err := siteconfig.Load(d.deps.Cfg.SiteYAMLPath)
		if err != nil {
			t.Fatal(err)
		}
		return site.LinksStyle
	}

	// Ticking the box hides the notes.
	if rec := d.adminPost(t, "/admin/links/style", url.Values{"hide_notes": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("hide = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if got := linksStyle(); got != siteconfig.LinksStyleMinimal {
		t.Errorf("links_style = %q, want %q", got, siteconfig.LinksStyleMinimal)
	}
	if d.deps.Site().ShowLinkNotes() {
		t.Error("live config still shows link notes after hiding them")
	}

	// The checkbox must render ticked so the page reflects the saved state.
	if body := d.adminGet(t, "/admin/links").Body.String(); !strings.Contains(body, `name="hide_notes" value="1" checked`) {
		t.Error("checkbox is not rendered checked while notes are hidden")
	}

	// Clearing it submits an empty form, which must restore the notes.
	if rec := d.adminPost(t, "/admin/links/style", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("show = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if got := linksStyle(); got != siteconfig.LinksStyleFull {
		t.Errorf("links_style = %q, want %q", got, siteconfig.LinksStyleFull)
	}
	if !d.deps.Site().ShowLinkNotes() {
		t.Error("live config still hides link notes after clearing the box")
	}
}
