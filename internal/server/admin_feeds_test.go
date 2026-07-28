package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// testFeedServer serves a minimal valid RSS document. Subscribing triggers an
// immediate poll, so without a local target these tests would do real DNS and
// HTTP — slow, and broken anywhere without egress.
func testFeedServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel>` +
			`<title>Test</title><item><title>An item</title>` +
			`<link>https://example.org/item/1</link></item></channel></rss>`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/feed.xml"
}

// adminPost issues an authenticated, same-origin form POST.
func (d *testDeps) adminPost(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := d.deps.Auth.SetSession(rec, "admin"); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	d.mux.ServeHTTP(out, req)
	return out
}

func (d *testDeps) feeds(t *testing.T) []siteconfig.RSSFeed {
	t.Helper()
	site, err := siteconfig.Load(d.deps.Cfg.SiteYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	return site.RSSFeeds
}

// Subscribing to a feed from the admin UI has to land in site.yml, which is
// what the poller reads. Before this existed the only way to add a feed was to
// edit that file on the server by hand.
func TestAdminFeedAddPersistsToSiteYAML(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	feedURL := testFeedServer(t)
	rec := d.adminPost(t, "/admin/feeds/add", url.Values{
		"url":   {feedURL},
		"label": {"Example"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add feed = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	feeds := d.feeds(t)
	if len(feeds) != 1 {
		t.Fatalf("feeds = %+v, want one entry", feeds)
	}
	if feeds[0].URL != feedURL || feeds[0].Label != "Example" || !feeds[0].Enabled {
		t.Errorf("feed = %+v", feeds[0])
	}
	// The live config must reflect the change too, not just the file.
	if live := d.deps.Site().RSSFeeds; len(live) != 1 {
		t.Errorf("in-memory site config has %d feeds, want 1", len(live))
	}
	// Subscribing polls straight away, so the feed's items should already be
	// on /links rather than waiting out the interval.
	links, err := d.deps.Store.ListLinks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		t.Error("subscribing did not import the feed's items")
	}
}

// A missing label is filled in from the host rather than rendering as a blank
// row.
func TestAdminFeedAddDefaultsLabelToHost(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	feedURL := testFeedServer(t)
	d.adminPost(t, "/admin/feeds/add", url.Values{"url": {feedURL}})
	feeds := d.feeds(t)
	host, err := url.Parse(feedURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 || feeds[0].Label != host.Host {
		t.Errorf("feeds = %+v, want the host %q as a label", feeds, host.Host)
	}
}

func TestAdminFeedAddRejectsBadURLs(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	for _, bad := range []string{"", "notaurl", "ftp://example.com/feed", "file:///etc/passwd", "https://"} {
		rec := d.adminPost(t, "/admin/feeds/add", url.Values{"url": {bad}})
		if rec.Code == http.StatusSeeOther {
			t.Errorf("add feed %q was accepted", bad)
		}
		if len(d.feeds(t)) != 0 {
			t.Fatalf("feed %q reached site.yml", bad)
		}
	}
}

func TestAdminFeedAddRejectsDuplicate(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	d.adminPost(t, "/admin/feeds/add", url.Values{"url": {"http://127.0.0.1:1/feed"}})
	rec := d.adminPost(t, "/admin/feeds/add", url.Values{"url": {"http://127.0.0.1:1/feed"}})
	if rec.Code == http.StatusSeeOther {
		t.Error("duplicate feed was accepted")
	}
	if !strings.Contains(rec.Body.String(), "already subscribed") {
		t.Errorf("body doesn't explain the conflict:\n%s", rec.Body.String())
	}
	if got := len(d.feeds(t)); got != 1 {
		t.Errorf("feeds = %d, want 1", got)
	}
}

func TestAdminFeedToggleAndRemove(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	d.adminPost(t, "/admin/feeds/add", url.Values{"url": {"http://127.0.0.1:1/a"}, "label": {"A"}})
	d.adminPost(t, "/admin/feeds/add", url.Values{"url": {"http://127.0.0.1:1/b"}, "label": {"B"}})
	if got := len(d.feeds(t)); got != 2 {
		t.Fatalf("feeds = %d, want 2", got)
	}

	if rec := d.adminPost(t, "/admin/feeds/1/toggle", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("toggle = %d", rec.Code)
	}
	feeds := d.feeds(t)
	if feeds[1].Enabled {
		t.Error("toggle did not pause the second feed")
	}
	if !feeds[0].Enabled {
		t.Error("toggle changed the wrong feed")
	}

	if rec := d.adminPost(t, "/admin/feeds/0/remove", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove = %d", rec.Code)
	}
	feeds = d.feeds(t)
	if len(feeds) != 1 || feeds[0].Label != "B" {
		t.Errorf("after removing index 0, feeds = %+v", feeds)
	}
}

// An out-of-range index must not panic or corrupt the list.
func TestAdminFeedIndexOutOfRange(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	d.adminPost(t, "/admin/feeds/add", url.Values{"url": {"http://127.0.0.1:1/a"}})
	for _, path := range []string{"/admin/feeds/9/remove", "/admin/feeds/9/toggle", "/admin/feeds/-1/remove"} {
		rec := d.adminPost(t, path, nil)
		if rec.Code == http.StatusSeeOther {
			t.Errorf("%s reported success", path)
		}
	}
	if got := len(d.feeds(t)); got != 1 {
		t.Errorf("feeds = %d, want the list untouched", got)
	}
}

// The feed controls are state-changing and cookie-authenticated, so they must
// refuse a cross-site submission like every other admin write.
func TestAdminFeedRequiresAuthAndSameOrigin(t *testing.T) {
	d := newTestDepsWithAdmin(t)

	// unauthenticated
	req := httptest.NewRequest(http.MethodPost, "/admin/feeds/add", strings.NewReader("url=https://x.example/feed"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Errorf("unauthenticated add = %d (%s)", rec.Code, rec.Header().Get("Location"))
	}

	// authenticated but cross-site
	sess := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(sess, "admin")
	req = httptest.NewRequest(http.MethodPost, "/admin/feeds/add", strings.NewReader("url=https://x.example/feed"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(sess.Result().Cookies()[0])
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site add = %d, want 403", rec.Code)
	}
	if got := len(d.feeds(t)); got != 0 {
		t.Errorf("a rejected request still wrote %d feeds", got)
	}
}

// The integrations page is what an operator reads to wire up an MCP client, so
// it has to carry the token and a valid config block — and stay behind auth.
func TestIntegrationsPage(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	_ = d.deps.Auth.SetSession(rec, "admin")
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/admin/integrations", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"mcpServers", "/api/v1/articles", "create_article", "Bearer"} {
		if !strings.Contains(body, want) {
			t.Errorf("integrations page missing %q", want)
		}
	}
	// The page must never be cached to disk by a shared browser.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	// And it must not be reachable without a session.
	req = httptest.NewRequest(http.MethodGet, "/admin/integrations", nil)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated = %d, want a redirect to login", rec.Code)
	}
}

func TestMaskToken(t *testing.T) {
	full := "abcdef0123456789abcdef0123456789"
	masked := maskToken(full)
	if strings.Contains(masked, "0123456789abcdef") {
		t.Errorf("mask leaks the token body: %q", masked)
	}
	if !strings.HasPrefix(masked, "abcd") || !strings.HasSuffix(masked, "6789") {
		t.Errorf("mask = %q, want the first and last four kept", masked)
	}
	if got := maskToken("short"); strings.Contains(got, "s") {
		t.Errorf("short token was not fully masked: %q", got)
	}
}
