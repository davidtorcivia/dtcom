package server

import (
	"encoding/json"
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

// The editor saves over fetch and stays in the post, so the response has to
// carry the slug rather than a redirect. On a new post that slug is the only
// thing standing between the second save and a collision with the file the
// first one created.
func TestAdminPostSaveInPlaceReturnsSlug(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	save := func(slug, title string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{
			"slug": {slug}, "title": {title}, "date": {"2026-03-01"},
			"body": {"Saved without leaving the editor."},
		}
		req := httptest.NewRequest(http.MethodPost, "/admin/posts/save", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.AddCookie(cookie)
		out := httptest.NewRecorder()
		d.mux.ServeHTTP(out, req)
		return out
	}

	rec = save("", "In Place")
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	var body struct{ Slug string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if body.Slug != "in-place" {
		t.Fatalf("slug = %q, want in-place", body.Slug)
	}

	// The second save carries the slug back, so it must update that file
	// rather than trying to create a second one.
	if rec := save(body.Slug, "In Place, Retitled"); rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	matches, _ := filepath.Glob(filepath.Join(d.deps.Cfg.ContentDir, "posts", "*in-place*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected one file for the post, got %v", matches)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "In Place, Retitled") {
		t.Errorf("second save did not land:\n%s", b)
	}
}

// A save that cannot land has to say why. The editor shows the message beside
// the Save button, which is the only thing telling the author which field to
// fix — a generic status text would leave them guessing.
func TestAdminPostSaveInPlaceReportsError(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	form := url.Values{"slug": {""}, "title": {""}, "body": {"no title"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/posts/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct{ Error string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if !strings.Contains(body.Error, "slug") {
		t.Errorf("error = %q, want it to name the problem", body.Error)
	}
}

// An expired session used to answer a save with 303 to the login page, which
// for a fetch means the POST body — the post — vanished into a redirect the
// editor could not distinguish from success. A navigation still gets the login
// page; a fetch gets a status it can report.
func TestAdminUnauthedSaveIs401ForFetch(t *testing.T) {
	d := newTestDeps(t)
	form := strings.NewReader("title=x&body=y")

	req := httptest.NewRequest(http.MethodPost, "/admin/posts/save", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("fetch save = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/posts/new", nil)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("navigation = %d, want 303 to the login page", rec.Code)
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
