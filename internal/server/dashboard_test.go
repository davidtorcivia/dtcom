package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/store"
)

// dashboardFor renders /admin with the given query string as a logged-in
// admin, and returns the page.
func dashboardFor(t *testing.T, d *testDeps, query string) string {
	t.Helper()
	sess := httptest.NewRecorder()
	if err := d.deps.Auth.SetSession(sess, "admin"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin"+query, nil)
	req.AddCookie(sess.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/admin%s status = %d, body:\n%s", query, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestDashboardRangesRender walks every range option through every selector.
// The four are independent, so the point is as much that changing one carries
// the other three along as that each renders.
func TestDashboardRangesRender(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	today := time.Now().UTC().Format("2006-01-02")
	if err := d.deps.Store.RecordView(store.View{Path: "/posts/hello", Day: today, IPHash: "hash-a"}); err != nil {
		t.Fatal(err)
	}

	for _, sel := range dashboardSelectors {
		for _, r := range dashboardRanges {
			body := dashboardFor(t, d, "?"+sel.Param+"="+r.Key)
			if !strings.Contains(body, `<select id="`+sel.ID+`"`) {
				t.Fatalf("%s: no selector rendered", sel.Param)
			}
			if !strings.Contains(body, `<option value="`+r.Key+`" selected>`) {
				t.Errorf("%s range %q did not render as the selected option", sel.Param, r.Key)
			}
			// A picker submits only its own field, so every other selector's
			// current value has to travel with it as a hidden input. There are
			// four selectors, so each range appears in the other three forms.
			if n := strings.Count(body, `<input type="hidden" name="`+sel.Param+`" value="`+r.Key+`">`); n != len(dashboardSelectors)-1 {
				t.Errorf("%s range %q was carried by %d other pickers, want %d",
					sel.Param, r.Key, n, len(dashboardSelectors)-1)
			}
		}
	}
}

// TestDashboardUnknownRangeFallsBack: the range comes from the query string, so
// a hand-edited or stale URL must land on the default rather than a blank page.
func TestDashboardUnknownRangeFallsBack(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	body := dashboardFor(t, d, "?chart=../etc&top=99y&ref=nonsense&place=")
	for _, sel := range dashboardSelectors {
		want := defaultTopRange
		if sel.Param == "chart" {
			want = defaultChartRange
		}
		if !strings.Contains(body, `<input type="hidden" name="`+sel.Param+`" value="`+want+`">`) {
			t.Errorf("unknown %s range did not fall back to %s", sel.Param, want)
		}
	}
}

// TestDashboardChartFillsQuietDays is the reason the chart is built in Go
// rather than straight from a GROUP BY: SQL returns only the days that have a
// row, and a month with two busy days used to render as two bars side by side.
func TestDashboardChartFillsQuietDays(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	today := time.Now().UTC()
	// Two views a week apart, nothing in between.
	for _, day := range []string{
		today.AddDate(0, 0, -7).Format("2006-01-02"),
		today.Format("2006-01-02"),
	} {
		if err := d.deps.Store.RecordView(store.View{Path: "/posts/hello", Day: day, IPHash: "hash-a"}); err != nil {
			t.Fatal(err)
		}
	}

	body := dashboardFor(t, d, "?chart=30d&top=30d")
	if got := strings.Count(body, `class="chart-col`); got != 30 {
		t.Errorf("30-day chart drew %d columns, want 30 — quiet days are missing", got)
	}
	// The quiet days are drawn, and drawn as empty.
	if got := strings.Count(body, `data-count="0"`); got != 28 {
		t.Errorf("chart marked %d days as empty, want 28", got)
	}
	if !strings.Contains(body, `data-count="1"`) {
		t.Error("chart did not carry the counts the tooltip reads")
	}
}

// TestDashboardTopRangeScopesCounts: a path read only outside the window must
// not appear in it.
func TestDashboardTopRangeScopesCounts(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	today := time.Now().UTC()
	old := today.AddDate(0, 0, -60).Format("2006-01-02")
	if err := d.deps.Store.RecordView(store.View{Path: "/posts/ancient", Day: old, IPHash: "hash-a"}); err != nil {
		t.Fatal(err)
	}
	if err := d.deps.Store.RecordView(store.View{Path: "/posts/current", Day: today.Format("2006-01-02"), IPHash: "hash-a"}); err != nil {
		t.Fatal(err)
	}

	recent := dashboardFor(t, d, "?chart=30d&top=7d")
	if strings.Contains(recent, "/posts/ancient") {
		t.Error("a 60-day-old view appeared in the 7-day most-read list")
	}
	if !strings.Contains(recent, "/posts/current") {
		t.Error("today's view is missing from the 7-day most-read list")
	}

	all := dashboardFor(t, d, "?chart=30d&top=all")
	if !strings.Contains(all, "/posts/ancient") {
		t.Error("the 60-day-old view is missing from the all-time most-read list")
	}
}

// TestDashboardMonthlyGrain: past the daily limit the chart buckets by month,
// or a year of history would be a bar per pixel.
func TestDashboardMonthlyGrain(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	today := time.Now().UTC()
	if err := d.deps.Store.RecordView(store.View{Path: "/posts/hello", Day: today.AddDate(0, -6, 0).Format("2006-01-02"), IPHash: "h"}); err != nil {
		t.Fatal(err)
	}

	body := dashboardFor(t, d, "?chart=12m&top=30d")
	if !strings.Contains(body, "busiest month") {
		t.Errorf("12-month chart did not bucket by month:\n%s", firstLines(body, 60))
	}
	// Twelve months back plus the current one.
	if got := strings.Count(body, `class="chart-col`); got != 13 {
		t.Errorf("12-month chart drew %d columns, want 13", got)
	}

	body = dashboardFor(t, d, "?chart=30d&top=30d")
	if !strings.Contains(body, "busiest day") && strings.Contains(body, "busiest") {
		t.Error("30-day chart did not bucket by day")
	}
}

// TestNiceCeil pins the axis scale: at or above the peak, even so the midpoint
// label is a whole count, and never wasteful enough to squash the plot.
func TestNiceCeil(t *testing.T) {
	cases := []struct{ peak, want int64 }{
		{0, 2}, {1, 2}, {2, 2},
		{3, 4}, {7, 8}, {11, 12},
		{99, 100}, {100, 100},
		// 101 rounds to 110, not 102: two significant figures is the point, so
		// the axis reads 110 / 55 / 0 rather than 102 / 51 / 0.
		{101, 110},
		{290, 290}, {291, 300}, {1234, 1300},
	}
	for _, c := range cases {
		if got := niceCeil(c.peak); got != c.want {
			t.Errorf("niceCeil(%d) = %d, want %d", c.peak, got, c.want)
		}
	}
	// The properties the two literal columns above are standing in for.
	for peak := int64(0); peak < 5000; peak++ {
		v := niceCeil(peak)
		if v < peak {
			t.Fatalf("niceCeil(%d) = %d, below the peak", peak, v)
		}
		if v%2 != 0 {
			t.Fatalf("niceCeil(%d) = %d, odd — the midpoint label would be a half", peak, v)
		}
		// Never so generous that the tallest bar is dwarfed by empty axis.
		if peak > 2 && v > peak*3/2 {
			t.Fatalf("niceCeil(%d) = %d, more than half again the peak", peak, v)
		}
	}
}
