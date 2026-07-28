package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

func (d *testDeps) site(t *testing.T) *siteconfig.Config {
	t.Helper()
	site, err := siteconfig.Load(d.deps.Cfg.SiteYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	return site
}

// navLabels is the nav list flattened to its labels, which is what these tests
// actually assert on. The fixture site.yml already ships one entry, so tests
// compare label sequences rather than assuming nav starts empty.
func (d *testDeps) navLabels(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, n := range d.site(t).Nav {
		out = append(out, n.Label)
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAdminNavAddRemoveReorder(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	// The fixture ships with one nav entry; everything added lands after it.
	base := d.navLabels(t)

	for _, n := range []struct{ label, href string }{
		{"About", "/about"},
		{"Elsewhere", "https://example.com"},
	} {
		rec := d.adminPost(t, "/admin/site/nav/add", url.Values{
			"label": {n.label}, "href": {n.href},
		})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("add %s = %d, body:\n%s", n.label, rec.Code, rec.Body.String())
		}
	}
	want := append(append([]string(nil), base...), "About", "Elsewhere")
	if got := d.navLabels(t); !eqStrings(got, want) {
		t.Fatalf("nav = %v, want %v", got, want)
	}
	// The href has to survive the round trip, not just the label.
	if nav := d.site(t).Nav; nav[len(nav)-1].Href != "https://example.com" {
		t.Errorf("last nav href = %q", nav[len(nav)-1].Href)
	}

	// Swap the last two.
	last := len(want) - 1
	if rec := d.adminPost(t, "/admin/site/nav/"+itoa(int64(last))+"/move", url.Values{"dir": {"up"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("move up = %d", rec.Code)
	}
	want[last], want[last-1] = want[last-1], want[last]
	if got := d.navLabels(t); !eqStrings(got, want) {
		t.Errorf("after move up, nav = %v, want %v", got, want)
	}

	// And swap them back with a downward move.
	if rec := d.adminPost(t, "/admin/site/nav/"+itoa(int64(last-1))+"/move", url.Values{"dir": {"down"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("move down = %d", rec.Code)
	}
	want[last], want[last-1] = want[last-1], want[last]
	if got := d.navLabels(t); !eqStrings(got, want) {
		t.Errorf("after move down, nav = %v, want %v", got, want)
	}

	if rec := d.adminPost(t, "/admin/site/nav/"+itoa(int64(last))+"/remove", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove = %d", rec.Code)
	}
	want = want[:last]
	if got := d.navLabels(t); !eqStrings(got, want) {
		t.Errorf("after remove, nav = %v, want %v", got, want)
	}

	// The live config, not just the file, has to reflect all of it.
	if live := d.deps.Site().Nav; len(live) != len(want) {
		t.Errorf("live config has %d nav entries, want %d", len(live), len(want))
	}
}

// A nav or social href must not be able to carry a script-executing scheme.
// The rendered header links it directly, and html/template escapes the
// attribute without blocking the scheme.
func TestAdminNavRejectsUnsafeHref(t *testing.T) {
	for _, href := range []string{
		"javascript:alert(1)",
		"JavaScript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox",
		"",
		"   ",
	} {
		t.Run(href, func(t *testing.T) {
			d := newTestDepsWithAdmin(t)
			base := d.navLabels(t)
			rec := d.adminPost(t, "/admin/site/nav/add", url.Values{
				"label": {"Bad"}, "href": {href},
			})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("add = %d, want 400", rec.Code)
			}
			if got := d.navLabels(t); !eqStrings(got, base) {
				t.Errorf("unsafe href was stored: nav = %v, want %v", got, base)
			}
		})
	}
}

func TestAdminNavRequiresLabel(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	base := d.navLabels(t)
	rec := d.adminPost(t, "/admin/site/nav/add", url.Values{"label": {"  "}, "href": {"/x"}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("add = %d, want 400", rec.Code)
	}
	if got := d.navLabels(t); !eqStrings(got, base) {
		t.Errorf("nav = %v, want %v unchanged", got, base)
	}
}

func TestAdminSocialAddRemove(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := d.adminPost(t, "/admin/site/social/add", url.Values{
		"label": {"GitHub"}, "href": {"https://github.com/davidtorcivia"}, "icon": {"github"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	soc := d.site(t).Social
	if len(soc) != 1 || soc[0].Icon != "github" || soc[0].Label != "GitHub" {
		t.Fatalf("social = %+v", soc)
	}
	// mailto is a legitimate social href (the contact link uses it).
	if rec := d.adminPost(t, "/admin/site/social/add", url.Values{
		"label": {"Contact"}, "href": {"mailto:a@b.co"}, "icon": {"email"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("mailto add = %d", rec.Code)
	}
	if soc := d.site(t).Social; len(soc) != 2 {
		t.Fatalf("social = %+v, want 2", soc)
	}
	if rec := d.adminPost(t, "/admin/site/social/0/remove", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove = %d", rec.Code)
	}
	if soc := d.site(t).Social; len(soc) != 1 || soc[0].Label != "Contact" {
		t.Errorf("social = %+v", soc)
	}
}

// An unknown icon renders as empty markup, so the link would show up as an
// invisible gap under the bio. It has to be refused at the door.
func TestAdminSocialRejectsUnknownIcon(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	for _, icon := range []string{"", "mastodon", "GitHub", "twitter"} {
		rec := d.adminPost(t, "/admin/site/social/add", url.Values{
			"label": {"X"}, "href": {"https://example.com"}, "icon": {icon},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("icon %q: add = %d, want 400", icon, rec.Code)
		}
	}
	if soc := d.site(t).Social; len(soc) != 0 {
		t.Errorf("social = %+v, want empty", soc)
	}
}

// An index past the end must not panic or truncate the list.
func TestAdminSiteListIndexOutOfRange(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	if rec := d.adminPost(t, "/admin/site/nav/add", url.Values{"label": {"A"}, "href": {"/a"}}); rec.Code != http.StatusSeeOther {
		t.Fatal("setup add failed")
	}
	before := d.navLabels(t)
	for _, path := range []string{
		"/admin/site/nav/9/remove",
		"/admin/site/nav/-1/remove",
		"/admin/site/nav/9/move",
		"/admin/site/social/3/remove",
	} {
		if rec := d.adminPost(t, path, url.Values{"dir": {"up"}}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", path, rec.Code)
		}
	}
	if got := d.navLabels(t); !eqStrings(got, before) {
		t.Errorf("nav = %v, want %v intact", got, before)
	}
}

// Moving the first entry up (or the last down) has nowhere to go. The buttons
// for it are hidden, so this only happens on a double submit — it should be a
// quiet no-op, not an error banner.
func TestAdminSiteListMoveAtEdgeIsNoOp(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	for _, l := range []string{"A", "B"} {
		if rec := d.adminPost(t, "/admin/site/nav/add", url.Values{"label": {l}, "href": {"/" + l}}); rec.Code != http.StatusSeeOther {
			t.Fatal("setup add failed")
		}
	}
	before := d.navLabels(t)
	if rec := d.adminPost(t, "/admin/site/nav/0/move", url.Values{"dir": {"up"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("move first up = %d, want a quiet 303", rec.Code)
	}
	last := itoa(int64(len(before) - 1))
	if rec := d.adminPost(t, "/admin/site/nav/"+last+"/move", url.Values{"dir": {"down"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("move last down = %d, want a quiet 303", rec.Code)
	}
	if got := d.navLabels(t); !eqStrings(got, before) {
		t.Errorf("nav = %v, want %v unchanged", got, before)
	}
}

// The editors write site.yml; the header and footer are built from it, so the
// change has to reach the generated pages.
func TestNavAndSocialReachRenderedPages(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	if rec := d.adminPost(t, "/admin/site/nav/add", url.Values{
		"label": {"Colophon"}, "href": {"/colophon"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("nav add = %d", rec.Code)
	}
	body := d.get("/").Body.String()
	if !strings.Contains(body, `href="/colophon"`) || !strings.Contains(body, "Colophon") {
		t.Error("the new nav link is missing from the rendered header")
	}
}
