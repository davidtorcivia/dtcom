package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/store"
)

// beacon posts a /api/track body as an ordinary browser would, with optional
// extra headers, and fails the test if the endpoint stops answering 204.
func beacon(t *testing.T, d *testDeps, body string, headers map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/track", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("track status = %d", rec.Code)
	}
}

// referrers returns the recorded referring hosts, all time.
func referrers(t *testing.T, d *testDeps) []string {
	t.Helper()
	rows, err := d.deps.Store.TopReferrers("", 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.Label)
	}
	return out
}

// TestReferrerKeepsHostOnly is the privacy line the feature has to hold: the
// browser sends a full URL, and a search referrer's query string is the terms
// somebody typed. Only the site name is allowed into the database.
func TestReferrerKeepsHostOnly(t *testing.T) {
	d := newTestDeps(t)
	beacon(t, d, `{"path":"/posts/hello","ref":"https://News.YCombinator.com/item?id=123"}`, nil)

	got := referrers(t, d)
	if len(got) != 1 || got[0] != "news.ycombinator.com" {
		t.Fatalf("referrers = %v, want [news.ycombinator.com]", got)
	}
}

// TestReferrerDropsSelfAndJunk: internal navigation is most of the traffic on
// any site, so leaving it in would make the panel a list of one entry that is
// always us. The non-http schemes are the untrusted-input half — the value
// comes from a public endpoint.
func TestReferrerDropsSelfAndJunk(t *testing.T) {
	d := newTestDeps(t)                 // BaseURL is https://x
	d.deps.Cfg.TrustProxyHeaders = true // so each case can arrive from its own address
	for i, ref := range []string{
		"https://x/posts/other",          // ourselves
		"https://sub.x/posts/other",      // a subdomain of ourselves
		"javascript:alert(1)",            //
		"data:text/html,<b>",             //
		"not a url at all",               //
		"android-app://com.example",      // a real referrer form, not a web page
		strings.Repeat("https://a", 400), // longer than any real referrer
	} {
		// A distinct address per case, or dedup would drop everything after
		// the first and the test would pass without checking anything: the key
		// is (path, day, ip_hash).
		beacon(t, d, `{"path":"/posts/hello","ref":`+quote(ref)+`}`,
			map[string]string{"CF-Connecting-IP": "203.0.113." + strconv.Itoa(i+1)})
		if got := referrers(t, d); len(got) != 0 {
			t.Fatalf("%q was recorded as a referrer: %v", ref, got)
		}
	}
}

// quote is json string quoting for the test bodies above.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// TestPlaceNeedsTrustedProxy: the city header is only meaningful because a
// proxy sets it. Served directly, it is whatever the client typed, and
// believing it would let anyone paint the map.
func TestPlaceNeedsTrustedProxy(t *testing.T) {
	d := newTestDeps(t)
	geo := map[string]string{"CF-IPCity": "Lisbon", "CF-IPCountry": "PT"}

	d.deps.Cfg.TrustProxyHeaders = false
	beacon(t, d, `{"path":"/posts/hello"}`, geo)
	places, err := d.deps.Store.TopPlaces("", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 0 {
		t.Fatalf("headers were believed without a proxy in front: %v", places)
	}

	d.deps.Cfg.TrustProxyHeaders = true
	beacon(t, d, `{"path":"/links"}`, geo)
	places, err = d.deps.Store.TopPlaces("", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].Label != "Lisbon, PT" {
		t.Fatalf("places = %v, want [{Lisbon, PT 1}]", places)
	}
}

// TestDwellAccumulatesAndIsCapped: the beacon reports reading time more than
// once per page (every time the tab is hidden), so the reports add up; and the
// number is a claim from an unauthenticated endpoint, so it is bounded.
func TestDwellAccumulatesAndIsCapped(t *testing.T) {
	d := newTestDeps(t)
	beacon(t, d, `{"path":"/posts/hello"}`, nil)
	beacon(t, d, `{"path":"/posts/hello","dwell":20}`, nil)
	beacon(t, d, `{"path":"/posts/hello","dwell":10}`, nil)

	if got := median(t, d); got != 30 {
		t.Errorf("dwell after two reports = %d, want 30", got)
	}

	beacon(t, d, `{"path":"/posts/hello","dwell":9999999}`, nil)
	if got := median(t, d); got != 30+maxDwellSeconds {
		t.Errorf("dwell after an absurd report = %d, want %d", got, 30+maxDwellSeconds)
	}
}

// TestDwellDoesNotCreateViews: a dwell report for a page this visitor never
// loaded is a lost or forged beacon, and either way it must not count as
// somebody reading the page.
func TestDwellDoesNotCreateViews(t *testing.T) {
	d := newTestDeps(t)
	beacon(t, d, `{"path":"/links","dwell":60}`, nil)

	total, err := d.deps.Store.TotalViews("")
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("a dwell report created %d view(s)", total)
	}
}

func median(t *testing.T, d *testDeps) int64 {
	t.Helper()
	n, err := d.deps.Store.MedianDwell("")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDashboardShowsAudience is the end-to-end check: a beacon carrying a
// referrer, a location and a reading time reaches the page a person actually
// looks at.
func TestDashboardShowsAudience(t *testing.T) {
	d := newTestDeps(t)
	d.deps.Cfg.TrustProxyHeaders = true
	visitor := map[string]string{
		"CF-Connecting-IP": "203.0.113.7", "CF-IPCity": "Lisbon", "CF-IPCountry": "PT",
	}
	beacon(t, d, `{"path":"/posts/hello","ref":"https://news.ycombinator.com/item?id=1"}`, visitor)
	beacon(t, d, `{"path":"/posts/hello","dwell":95}`, visitor)

	page := dashboardFor(t, d, "")
	for _, want := range []string{
		"Unique visitors", "Where from", "news.ycombinator.com",
		"Places", "Lisbon, PT", "1m 35s", "<strong>1.0</strong> pages each",
		// Most read names the page, not its URL — the path stays as the link
		// target and the tooltip.
		`title="/posts/hello">Hello</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("dashboard is missing %q", want)
		}
	}
}

// TestPlaceMapPlotsCoordinates: the map is the one place a stored coordinate
// is used, so this covers the whole path from the proxy's headers to the dot's
// position. Lisbon is 38.7N 9.1W, which on a map cropped to 84N..56S lands
// about 48 percent across and 32 percent down.
func TestPlaceMapPlotsCoordinates(t *testing.T) {
	d := newTestDeps(t)
	d.deps.Cfg.TrustProxyHeaders = true
	beacon(t, d, `{"path":"/posts/hello"}`, map[string]string{
		"CF-IPCity": "Lisbon", "CF-IPCountry": "PT",
		"CF-IPLatitude": "38.7167", "CF-IPLongitude": "-9.1333",
	})

	points, err := d.deps.Store.PlacePoints("", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Label != "Lisbon, PT" {
		t.Fatalf("points = %+v", points)
	}
	dots := mapDots(points)
	if len(dots) != 1 {
		t.Fatalf("dots = %+v", dots)
	}
	if got := dots[0].Left; got < 47 || got > 48 {
		t.Errorf("dot left = %.1f%%, want about 47.5%%", got)
	}
	if got := dots[0].Top; got < 32 || got > 33 {
		t.Errorf("dot top = %.1f%%, want about 32.3%%", got)
	}

	page := dashboardFor(t, d, "")
	if !strings.Contains(page, `class="places-map"`) || !strings.Contains(page, `class="place-dot"`) {
		t.Error("the dashboard did not draw the map")
	}
}

// TestCoordinatesRejectImpossibleValues: the headers are only as good as the
// proxy, and a bad pair would drag a dot to a corner of the map rather than
// showing nothing.
func TestCoordinatesRejectImpossibleValues(t *testing.T) {
	for _, tc := range []struct{ lat, lon string }{
		{"91", "0"}, {"-90.1", "0"}, {"0", "181"}, {"0", "-200"},
		{"NaN", "0"}, {"", ""}, {"north", "west"},
	} {
		d := newTestDeps(t)
		d.deps.Cfg.TrustProxyHeaders = true
		beacon(t, d, `{"path":"/posts/hello"}`, map[string]string{
			"CF-IPLatitude": tc.lat, "CF-IPLongitude": tc.lon,
		})
		points, err := d.deps.Store.PlacePoints("", 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(points) != 0 {
			t.Errorf("lat %q lon %q was plotted: %+v", tc.lat, tc.lon, points)
		}
	}
}

// TestMapDotsSkipUncroppedLatitudes: the map has no Antarctica, so a point
// below the crop has nowhere honest to go.
func TestMapDotsSkipUncroppedLatitudes(t *testing.T) {
	dots := mapDots([]store.Place{
		{Label: "McMurdo", Lat: -77.8, Lon: 166.7, Count: 3},
		{Label: "Longyearbyen", Lat: 78.2, Lon: 15.6, Count: 1},
	})
	if len(dots) != 1 || dots[0].Label != "Longyearbyen" {
		t.Errorf("dots = %+v, want only Longyearbyen", dots)
	}
}
