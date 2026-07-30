package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestDashboardRangesRender walks every range option through both selectors.
// The two are independent, so the point is as much that switching one carries
// the other along as that each renders.
func TestDashboardRangesRender(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	today := time.Now().UTC().Format("2006-01-02")
	if err := d.deps.Store.RecordView("/posts/hello", today, "hash-a"); err != nil {
		t.Fatal(err)
	}

	for _, r := range dashboardRanges {
		body := dashboardFor(t, d, "?chart="+r.Key+"&top=30d")
		if !strings.Contains(body, `href="/admin?chart=`+r.Key+`&amp;top=30d" class="range-tab is-active"`) {
			t.Errorf("chart range %q did not render as the active tab", r.Key)
		}
		// Switching the other selector must preserve this one.
		if !strings.Contains(body, "chart="+r.Key+"&amp;top=7d") {
			t.Errorf("chart range %q lost when offering the 7d most-read link", r.Key)
		}

		body = dashboardFor(t, d, "?chart=30d&top="+r.Key)
		if !strings.Contains(body, `&amp;top=`+r.Key+`" class="range-tab is-active"`) {
			t.Errorf("top range %q did not render as the active tab", r.Key)
		}
	}
}

// TestDashboardUnknownRangeFallsBack: the range comes from the query string, so
// a hand-edited or stale URL must land on the default rather than a blank page.
func TestDashboardUnknownRangeFallsBack(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	body := dashboardFor(t, d, "?chart=../etc&top=99y")
	if !strings.Contains(body, `&amp;top=`+defaultTopRange+`" class="range-tab is-active"`) {
		t.Errorf("unknown top range did not fall back to %s", defaultTopRange)
	}
	if !strings.Contains(body, `href="/admin?chart=`+defaultChartRange+`&amp;top=`+defaultTopRange+`" class="range-tab is-active"`) {
		t.Errorf("unknown chart range did not fall back to %s", defaultChartRange)
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
		if err := d.deps.Store.RecordView("/posts/hello", day, "hash-a"); err != nil {
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
	if err := d.deps.Store.RecordView("/posts/ancient", old, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if err := d.deps.Store.RecordView("/posts/current", today.Format("2006-01-02"), "hash-a"); err != nil {
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
	if err := d.deps.Store.RecordView("/posts/hello", today.AddDate(0, -6, 0).Format("2006-01-02"), "h"); err != nil {
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

