package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// firstLines trims a rendered page down to something readable in a test
// failure. A full admin page is several hundred lines and burying the useful
// part of a failure in it helps nobody.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// saveSite posts the Site form as a logged-in admin and returns the response.
func saveSite(t *testing.T, d *testDeps, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	sess := httptest.NewRecorder()
	if err := d.deps.Auth.SetSession(sess, "admin"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/site", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(sess.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec
}

// policyOf returns the Content-Security-Policy served for a path.
func policyOf(t *testing.T, d *testDeps, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec.Header().Get("Content-Security-Policy")
}

// TestAnalyticsSaveWidensPolicy is the whole point of the feature working at
// all: the site's script-src is 'self', so a tracker that is configured but not
// allowed through would be blocked by the browser and silently record nothing.
func TestAnalyticsSaveWidensPolicy(t *testing.T) {
	d := newTestDepsWithAdmin(t)

	if p := policyOf(t, d, "/"); !strings.Contains(p, "script-src 'self';") {
		t.Fatalf("baseline policy is not strict: %s", p)
	}

	rec := saveSite(t, d, url.Values{
		"title":            {"DT"},
		"analytics_script": {"https://stats.example.com/script.js"},
		"analytics_data":   {"website-id: abc123"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, body:\n%s", rec.Code, rec.Body.String())
	}

	p := policyOf(t, d, "/")
	if !strings.Contains(p, "script-src 'self' https://stats.example.com") {
		t.Errorf("script-src did not admit the tracker origin: %s", p)
	}
	if !strings.Contains(p, "connect-src 'self' https://stats.example.com") {
		t.Errorf("connect-src did not admit the tracker origin: %s", p)
	}
	// Only the origin, never the path.
	if strings.Contains(p, "script.js") {
		t.Errorf("policy carries the script path rather than its origin: %s", p)
	}

	// The admin surface stays strict: the tag is only ever put on public pages.
	if p := policyOf(t, d, "/admin/login"); !strings.Contains(p, "script-src 'self';") {
		t.Errorf("admin policy was widened by a public-site tracker: %s", p)
	}
}

// TestAnalyticsRejectsNonHTTPScript: this URL becomes both a <script src> and
// an entry in script-src, so a javascript:/data: value would be a hole in the
// one header that stops a post's raw HTML from executing.
func TestAnalyticsRejectsNonHTTPScript(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	for _, bad := range []string{
		"javascript:alert(1)",
		"data:text/javascript,alert(1)",
		"//stats.example.com/script.js",
	} {
		rec := saveSite(t, d, url.Values{"title": {"DT"}, "analytics_script": {bad}})
		if rec.Code == http.StatusSeeOther {
			t.Errorf("%q was accepted as an analytics script URL", bad)
		}
		if p := policyOf(t, d, "/"); !strings.Contains(p, "script-src 'self';") {
			t.Errorf("%q reached the policy: %s", bad, p)
		}
	}
}

// TestAnalyticsRejectsBadAttributeName: an attribute *name* cannot be escaped
// after the fact the way a value can, so it is checked rather than sanitised.
func TestAnalyticsRejectsBadAttributeName(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := saveSite(t, d, url.Values{
		"title":            {"DT"},
		"analytics_script": {"https://stats.example.com/script.js"},
		"analytics_data":   {`x" onload="alert(1): y`},
	})
	if rec.Code == http.StatusSeeOther {
		t.Error("an attribute name containing a quote was accepted")
	}
	if !strings.Contains(rec.Body.String(), "letters, digits and dashes") {
		t.Errorf("no useful error shown:\n%s", firstLines(rec.Body.String(), 40))
	}
}

// TestAnalyticsRejectedSaveKeepsTypedValues: the Site form carries the title,
// description and bio too, and losing all of them to a typo'd tracker URL would
// make the error worse than the mistake.
func TestAnalyticsRejectedSaveKeepsTypedValues(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := saveSite(t, d, url.Values{
		"title":            {"Kept Title"},
		"description":      {"Kept description"},
		"analytics_script": {"nonsense://x"},
	})
	body := rec.Body.String()
	if !strings.Contains(body, "Kept Title") || !strings.Contains(body, "Kept description") {
		t.Errorf("rejected save discarded the other fields:\n%s", firstLines(body, 60))
	}
	if !strings.Contains(body, "nonsense://x") {
		t.Error("rejected save discarded the value that needs correcting")
	}
}

// TestAnalyticsTagRendersIntoPages checks the tag reaches the built pages with
// its data-* attributes, and that turning it off removes it again.
func TestAnalyticsTagRendersIntoPages(t *testing.T) {
	d := newTestDepsWithAdmin(t)

	if rec := saveSite(t, d, url.Values{
		"title":            {"DT"},
		"analytics_script": {"https://stats.example.com/script.js"},
		"analytics_data":   {"website-id: abc123\ndata-host-url: https://stats.example.com"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, body:\n%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	home := rec.Body.String()
	if !strings.Contains(home, `<script defer src="https://stats.example.com/script.js"`) {
		t.Errorf("home page is missing the tracker tag:\n%s", firstLines(home, 30))
	}
	// A "data-" prefix pasted from the provider's docs is not doubled up.
	if !strings.Contains(home, `data-host-url="https://stats.example.com"`) {
		t.Error(`the "data-" prefix was not stripped from a pasted attribute name`)
	}
	if !strings.Contains(home, `data-website-id="abc123"`) {
		t.Error("the provider's website id is missing from the tag")
	}

	// Clearing the URL takes the tag and the policy exemption away again.
	if rec := saveSite(t, d, url.Values{"title": {"DT"}, "analytics_script": {""}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("clear status = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "stats.example.com") {
		t.Error("the tracker tag survived being cleared")
	}
	if p := policyOf(t, d, "/"); !strings.Contains(p, "script-src 'self';") {
		t.Errorf("the policy exemption survived being cleared: %s", p)
	}
}

// TestAnalyticsHandEditedConfigIsStillChecked: site.yml is a file the author
// can edit directly, so it never passes through the save-time validation. The
// renderer refuses what it cannot escape rather than trusting it.
func TestAnalyticsHandEditedConfigIsStillChecked(t *testing.T) {
	if err := siteconfig.ValidateAnalytics(siteconfig.Analytics{
		ScriptURL: "https://stats.example.com/script.js",
		Data:      map[string]string{"ok-name": "v"},
	}); err != nil {
		t.Errorf("a valid configuration was rejected: %v", err)
	}
	if siteconfig.ValidAttrName(`x" onload="y`) {
		t.Error("an attribute name containing a quote passed the check")
	}
	if siteconfig.ValidAttrName("") || siteconfig.ValidAttrName("-lead") || siteconfig.ValidAttrName("trail-") {
		t.Error("an empty or dash-edged attribute name passed the check")
	}
	if (siteconfig.Analytics{ScriptURL: "javascript:alert(1)"}).Origin() != "" {
		t.Error("a javascript: URL yielded an origin")
	}
}
