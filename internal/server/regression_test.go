package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
)

// get is a small helper for the many "GET this path, look at the response"
// checks below.
func (d *testDeps) get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec
}

// The nav links to /search on every page. The engine had no renderSearch, so
// public/search/index.html was never produced and the link 404'd.
func TestSearchPageIsServed(t *testing.T) {
	d := newTestDeps(t)
	rec := d.get("/search")
	if rec.Code != http.StatusOK {
		t.Fatalf("/search = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="search-input"`) {
		t.Errorf("/search is missing the search input:\n%s", rec.Body.String())
	}
}

// Ordinary punctuation in the search box used to reach FTS5 as query syntax
// and fail the statement, surfacing as a 500 and "Search error." in the UI.
func TestSearchToleratesQuerySyntax(t *testing.T) {
	d := newTestDeps(t)
	for _, q := range []string{`"`, `foo AND`, `a OR`, `NEAR(`, `*`, `body"`, `-`, `((`, `body OR NOT`} {
		rec := d.get("/api/search?q=" + url.QueryEscape(q))
		if rec.Code != http.StatusOK {
			t.Errorf("q=%q → %d, want 200 (body %s)", q, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// A term with a trailing quote must still match the indexed word.
	rec := d.get("/api/search?q=" + url.QueryEscape(`body"`))
	if !strings.Contains(rec.Body.String(), `"Slug":"hello"`) {
		t.Errorf("quoted term found nothing: %s", rec.Body.String())
	}
}

// An empty result set must serialize as [] rather than null, which the
// front-end would otherwise have to special-case.
func TestSearchEmptyResultIsArray(t *testing.T) {
	d := newTestDeps(t)
	rec := d.get("/api/search?q=" + url.QueryEscape("zzzznotathing"))
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("empty search body = %q, want []", got)
	}
}

// Article text can contain angle brackets, and FTS5's snippet() copies the
// source verbatim. The excerpt is inserted into the results with innerHTML, so
// it must arrive escaped with only the highlight tags intact.
func TestSearchExcerptIsEscaped(t *testing.T) {
	d := newTestDeps(t)
	body := "---\ntitle: Danger\ndate: 2026-02-02\n---\n\nA sentence about <img src=x onerror=alert(1)> injection.\n"
	writePost(t, d, "2026-02-02-danger.md", body)
	if err := d.deps.Engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	rec := d.get("/api/search?q=injection")
	var hits []struct{ Excerpt string }
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(hits) == 0 {
		t.Fatalf("no hits for the seeded article: %s", rec.Body.String())
	}
	for _, h := range hits {
		if strings.Contains(h.Excerpt, "<img") || strings.Contains(h.Excerpt, "onerror") {
			t.Errorf("excerpt carries live markup: %q", h.Excerpt)
		}
	}
}

// Unmatched routes should land on the rendered 404 page, not net/http's
// plain-text default.
func TestNotFoundServesRenderedPage(t *testing.T) {
	d := newTestDeps(t)
	for _, p := range []string{"/nope", "/posts/does-not-exist", "/posts/nothing.md", "/deep/unknown/path"} {
		rec := d.get(p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s Content-Type = %q, want html", p, ct)
		}
	}
}

// Go's ServeMux rejects encoded slashes before we see them, but the handler
// validates the slug itself so the file lookup can never be steered outside
// public/posts even if that changes.
func TestArticleSlugTraversalRejected(t *testing.T) {
	d := newTestDeps(t)
	for _, p := range []string{
		"/posts/..%2f..%2f..%2fetc%2fpasswd",
		"/posts/hello%2f..%2f..%2findex.html",
		"/posts/..%2findex.html",
	} {
		if rec := d.get(p); rec.Code == http.StatusOK {
			t.Errorf("%s was served (status 200)", p)
		}
	}
	for _, slug := range []string{"../index", "..", ".hidden", "a/b"} {
		if validSlug(slug) {
			t.Errorf("validSlug(%q) = true", slug)
		}
	}
	for _, slug := range []string{"hello", "a-b_c.1", "Post2"} {
		if !validSlug(slug) {
			t.Errorf("validSlug(%q) = false", slug)
		}
	}
}

// Every response carries the hardening headers.
func TestSecurityHeaders(t *testing.T) {
	d := newTestDeps(t)
	rec := d.get("/")
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "object-src 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	// Inline scripts must stay forbidden — the templates were changed to need
	// none, and that is what makes this policy meaningful.
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP allows inline script: %q", csp)
	}
}

// Static assets and generated pages must be cacheable; admin pages must not be.
func TestCacheControl(t *testing.T) {
	d := newTestDeps(t)
	// Asset URLs are content-hashed, so they can be cached indefinitely.
	if got := d.get("/static/style.css").Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("static Cache-Control = %q, want immutable", got)
	}
	// Pages revalidate every time so an edit is never served stale.
	if got := d.get("/").Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("page Cache-Control = %q, want no-cache", got)
	}
	if got := d.get("/admin/login").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("admin Cache-Control = %q, want no-store", got)
	}
}

// A gzip-capable client should get compressed HTML.
func TestCompression(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q", rec.Header().Get("Vary"))
	}
	// Without the header, the body must stay identity-encoded.
	if got := d.get("/").Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q for a client that didn't ask", got)
	}
}

func TestHealthz(t *testing.T) {
	d := newTestDeps(t)
	rec := d.get("/healthz")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("/healthz = %d %s", rec.Code, rec.Body.String())
	}
}

// r.RemoteAddr is host:port, so hashing it whole made every page view unique
// and view dedup a no-op.
func TestClientIPStripsPort(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:51234"
	if got := d.deps.clientIP(req); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the bare address", got)
	}
}

// A forwarded address must only be believed when a proxy is configured;
// otherwise any client could claim to be someone else and evade the limiter.
func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:51234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := d.deps.clientIP(req); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the peer address when proxies aren't trusted", got)
	}

	d.deps.Cfg.TrustProxyHeaders = true
	if got := d.deps.clientIP(req); got != "1.2.3.4" {
		t.Errorf("clientIP = %q, want the forwarded address when proxies are trusted", got)
	}
	// Rightmost entry: a proxy that appends puts the address it saw last, so
	// the client cannot forge its identity by prepending entries.
	req.Header.Set("X-Forwarded-For", "5.6.7.8, 9.9.9.9")
	if got := d.deps.clientIP(req); got != "9.9.9.9" {
		t.Errorf("clientIP = %q, want the right-most forwarded entry", got)
	}
}

// The beacon accepts a path from anyone, so it must only record pages that
// exist — otherwise the views table takes unbounded junk.
func TestTrackRejectsUnknownPaths(t *testing.T) {
	d := newTestDeps(t)
	cases := map[string]bool{
		"/":                                  true,
		"/links":                             true,
		"/search":                            true,
		"/posts/hello":                       true,
		"/posts/not-a-post":                  false,
		"/../etc/passwd":                     false,
		"relative":                           false,
		"":                                   false,
		"/posts/" + strings.Repeat("x", 300): false,
	}
	for p, want := range cases {
		if got := d.deps.trackablePath(p); got != want {
			t.Errorf("trackablePath(%q) = %v, want %v", p, got, want)
		}
	}
}

// Repeated login attempts must be throttled: bcrypt is expensive and the
// second factor is only six digits.
func TestLoginIsRateLimited(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	post := func() int {
		req := httptest.NewRequest(http.MethodPost, "/admin/login",
			strings.NewReader("password=wrong&totp=000000"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "198.51.100.7:4444"
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		return rec.Code
	}
	sawLimit := false
	for range 12 {
		if post() == http.StatusTooManyRequests {
			sawLimit = true
			break
		}
	}
	if !sawLimit {
		t.Error("login never returned 429 after repeated failures")
	}
}

// Guessing the API token must be throttled too.
func TestBearerFailuresAreRateLimited(t *testing.T) {
	d := newTestDeps(t)
	sawLimit := false
	for range 20 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/articles", nil)
		req.Header.Set("Authorization", "Bearer nope")
		req.RemoteAddr = "198.51.100.8:4444"
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			sawLimit = true
			break
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status %d", rec.Code)
		}
	}
	if !sawLimit {
		t.Error("bearer auth never returned 429 after repeated failures")
	}
	// A correct token must never be throttled by those failures.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/articles", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	req.RemoteAddr = "198.51.100.8:4444"
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("authorized request after failures = %d, want 200", rec.Code)
	}
}

// A cookie-authenticated write from another origin must be refused.
func TestAdminRejectsCrossSitePost(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/admin/regenerate", nil)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", rec.Code)
	}

	// The same request from this origin must go through.
	req = httptest.NewRequest(http.MethodPost, "/admin/regenerate", nil)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin POST = %d, want 303", rec.Code)
	}
}

// Deleting a link that isn't there (or is RSS-imported, which the store
// refuses) must report 404 rather than a success the caller can't verify.
func TestDeleteMissingLinkReports404(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/links/9999", nil)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("delete of a missing link = %d, want 404", rec.Code)
	}
}

// A tag containing a comma or a bracket used to break out of the YAML flow
// sequence and corrupt every field after it.
func TestFrontmatterSurvivesAwkwardValues(t *testing.T) {
	in := articleInput{
		Title:       `He said "hi": a story\`,
		Description: "Line one\nline two",
		Tags:        []string{"color, grading", "a]b", "  spaced  "},
		Body:        "Body\r\nwith CRLF",
	}
	out := renderArticleFile(in, "2026-01-01", "slug")

	// The generated file must round-trip through the loader unchanged.
	dir := t.TempDir()
	writeFile(t, dir+"/2026-01-01-slug.md", out)
	arts := loadArticlesOrFail(t, dir)
	if len(arts) != 1 {
		t.Fatalf("expected 1 article, got %d", len(arts))
	}
	a := arts[0]
	if a.Title != in.Title {
		t.Errorf("title round-trip: got %q want %q", a.Title, in.Title)
	}
	if a.Description != in.Description {
		t.Errorf("description round-trip: got %q want %q", a.Description, in.Description)
	}
	want := []string{"color, grading", "a]b", "spaced"}
	if len(a.Tags) != len(want) {
		t.Fatalf("tags = %q, want %q", a.Tags, want)
	}
	for i := range want {
		if a.Tags[i] != want[i] {
			t.Errorf("tag %d = %q, want %q", i, a.Tags[i], want[i])
		}
	}
	if strings.Contains(a.Body, "\r") {
		t.Errorf("body kept CRLF: %q", a.Body)
	}
}

// Two posts sharing a slug would overwrite each other's rendered page; the
// create path has to catch that regardless of the date prefix.
func TestCreateRejectsDuplicateSlugAcrossDates(t *testing.T) {
	d := newTestDeps(t)
	in := articleInput{Title: "Hello", Date: "2027-05-05", Body: "x"}
	_, status, err := d.deps.createArticle(in)
	if err == nil {
		t.Fatalf("expected a conflict, got status %d", status)
	}
	if status != http.StatusConflict {
		t.Errorf("status = %d, want 409", status)
	}
}

// An image upload should be normalized, stored under a content-derived name,
// and served back from /images/.
func TestImageUploadRoundTrip(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(tinyPNG(t)); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/images", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	// Drain the background rendition goroutine before TempDir cleanup.
	d.deps.renditionWG.Wait()
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct{ URL, Markdown string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.URL, "/images/") {
		t.Fatalf("url = %q", out.URL)
	}
	if !strings.Contains(out.Markdown, out.URL) {
		t.Errorf("markdown %q doesn't reference %q", out.Markdown, out.URL)
	}
	if got := d.get(out.URL); got.Code != http.StatusOK {
		t.Errorf("serving %s = %d", out.URL, got.Code)
	}
}

// Anything that isn't a decodable image must be refused as a client error, not
// stored and served back.
func TestImageUploadRejectsNonImage(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "evil.png")
	_, _ = part.Write([]byte("<?php echo shell_exec($_GET['c']); ?>"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/images", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-image upload = %d, want 400", rec.Code)
	}
}

// The limiter must refill over time rather than locking a client out forever.
func TestRateLimiterRefills(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(2, time.Second)
	l.now = func() time.Time { return now }
	if !l.Allow("k") || !l.Allow("k") {
		t.Fatal("burst should permit the first two")
	}
	if l.Allow("k") {
		t.Error("third call should be denied")
	}
	now = now.Add(2 * time.Second)
	if !l.Allow("k") {
		t.Error("should be allowed again after refill")
	}
}

// Idle buckets must not accumulate — one request per unique IP would otherwise
// pin an entry forever.
func TestRateLimiterSweepsIdleBuckets(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(2, time.Second)
	l.now = func() time.Time { return now }
	for i := range 100 {
		l.Allow(string(rune('a' + i%26)))
	}
	now = now.Add(10 * time.Minute)
	l.Allow("trigger-sweep")
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n > 2 {
		t.Errorf("limiter retained %d buckets after they all refilled", n)
	}
}

// --- test fixtures -------------------------------------------------------

func writePost(t *testing.T, d *testDeps, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(d.deps.postsDir(), name), body)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadArticlesOrFail(t *testing.T, dir string) []build.Article {
	t.Helper()
	arts, err := build.LoadArticles(dir)
	if err != nil {
		t.Fatalf("LoadArticles: %v", err)
	}
	return arts
}

// tinyPNG returns a minimal valid 1x1 PNG for upload tests.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The limiter is consulted from every request goroutine, so its map must be
// safe under concurrent use. (This machine has no C toolchain, so `go test
// -race` can't run here — this at least exercises the path under contention.)
func TestRateLimiterConcurrentUse(t *testing.T) {
	l := newRateLimiter(50, time.Millisecond)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for range 100 {
				l.Allow("client-" + strconv.Itoa(n%8))
			}
		}(i)
	}
	wg.Wait()
}

// Rebuilds refresh the asset hashes while request handlers read them.
func TestAssetLookupsDuringRebuild(t *testing.T) {
	d := newTestDeps(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				d.deps.assets.URL("/static/style.css")
			}
		}
	}()

	for range 5 {
		if err := d.deps.Engine.Rebuild(); err != nil {
			t.Error(err)
			break
		}
	}
	close(stop)
	wg.Wait()
}

// Editing a stylesheet must change the URL the admin pages emit. The server
// used to hold its own fingerprinter that nothing ever refreshed, so an admin
// CSS edit kept serving from cache under the old URL until a restart.
func TestAdminAssetURLsRefreshAfterRebuild(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	cssPath := filepath.Join(d.deps.Cfg.StaticDir, "admin.css")
	writeFile(t, cssPath, "body{color:red}")
	if err := d.deps.Engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	first := d.deps.assets.URL("/static/admin.css")

	writeFile(t, cssPath, "body{color:blue}")
	if err := d.deps.Engine.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if second := d.deps.assets.URL("/static/admin.css"); second == first {
		t.Errorf("admin asset URL unchanged after an edit: %q", second)
	}
}
