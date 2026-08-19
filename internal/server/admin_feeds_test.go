package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
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

// The config blocks are copied straight into a client's config file, so their
// shape is the whole product. This page shipped an entry with a url and no
// type, which every client reads as a local command — Claude Code skips it and
// Claude Desktop rejects the file outright, since its config has no url key at
// all and only ever launches a local program.
func TestMCPConfigShapes(t *testing.T) {
	const base = "https://example.com"
	const token = "sekrit-token"

	var direct struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Command string            `json:"command"`
		} `json:"mcpServers"`
	}
	raw, err := mcpConfigJSON(base, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &direct); err != nil {
		t.Fatalf("config is not valid JSON: %v\n%s", err, raw)
	}
	got, ok := direct.MCPServers["dtcom"]
	if !ok {
		t.Fatalf("no dtcom entry:\n%s", raw)
	}
	// Without this the entry is read as stdio and the server never loads.
	if got.Type != "http" {
		t.Errorf(`type = %q, want "http"`, got.Type)
	}
	if got.URL != base+"/mcp" {
		t.Errorf("url = %q", got.URL)
	}
	if got.Headers["Authorization"] != "Bearer "+token {
		t.Errorf("Authorization = %q", got.Headers["Authorization"])
	}
	if got.Command != "" {
		t.Errorf("a URL server must not carry a command, got %q", got.Command)
	}

	var desktop struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
	}
	raw, err = mcpDesktopConfigJSON(base, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &desktop); err != nil {
		t.Fatalf("desktop config is not valid JSON: %v\n%s", err, raw)
	}
	dt, ok := desktop.MCPServers["dtcom"]
	if !ok {
		t.Fatalf("no dtcom entry:\n%s", raw)
	}
	// claude_desktop_config.json has no url key; a stdio entry is the only
	// thing it will load.
	if dt.URL != "" {
		t.Errorf("desktop entry must not carry a url, got %q", dt.URL)
	}
	if dt.Command != "npx" {
		t.Errorf("command = %q, want npx", dt.Command)
	}
	if !slices.Contains(dt.Args, base+"/mcp") {
		t.Errorf("args do not name the endpoint: %v", dt.Args)
	}
	if dt.Env["DTCOM_TOKEN"] != "Bearer "+token {
		t.Errorf("DTCOM_TOKEN = %q", dt.Env["DTCOM_TOKEN"])
	}
	// Every space must live in env: Claude Desktop on Windows does not escape
	// spaces inside args when it launches npx, so a header written inline
	// arrives mangled and the connection fails with no useful message.
	for _, a := range dt.Args {
		if strings.Contains(a, " ") {
			t.Errorf("arg %q contains a space, which Windows will mangle", a)
		}
	}
	// mcp-remote splits the header on the first colon and expands ${VAR} from
	// the environment, so this exact spelling is what makes the two halves meet.
	if !slices.Contains(dt.Args, "Authorization:${DTCOM_TOKEN}") {
		t.Errorf("args do not carry the header reference: %v", dt.Args)
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
