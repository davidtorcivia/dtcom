package store

import "testing"

func TestRecordAndAggregateViews(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()

	if err := s.RecordView(View{Path: "/posts/a", Day: "2026-07-27", IPHash: "ip1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordView(View{Path: "/posts/a", Day: "2026-07-27", IPHash: "ip1"}); err != nil {
		t.Fatal(err) // dedup: same ip+day
	}
	if err := s.RecordView(View{Path: "/posts/a", Day: "2026-07-27", IPHash: "ip2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordView(View{Path: "/posts/b", Day: "2026-07-27", IPHash: "ip2"}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Total != 3 { // (ip1 path a), (ip2 path a), (ip2 path b)
		t.Errorf("Total = %d, want 3", stats.Total)
	}
	if len(stats.ByPath) != 2 {
		t.Errorf("ByPath len = %d, want 2", len(stats.ByPath))
	}
}

// TestAudienceAggregates covers the three numbers the dashboard's audience row
// is built from. The median is the one worth pinning: an average would be
// dragged into the hours by the one tab left open overnight, which is exactly
// the row this fixture includes.
func TestAudienceAggregates(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()

	day := "2026-07-27"
	views := []struct {
		path, ip, ref, city string
		dwell               int
	}{
		{"/posts/a", "ip1", "news.ycombinator.com", "Lisbon", 20},
		{"/posts/b", "ip1", "", "Lisbon", 40},
		{"/posts/a", "ip2", "news.ycombinator.com", "Berlin", 30},
		{"/posts/c", "ip3", "lobste.rs", "", 86400}, // left open overnight
	}
	for _, v := range views {
		if err := s.RecordView(View{
			Path: v.path, Day: day, IPHash: v.ip, Referrer: v.ref, City: v.city, Country: "PT",
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.AddDwell(v.path, day, v.ip, v.dwell); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := s.UniqueVisitors(""); err != nil || n != 3 {
		t.Errorf("UniqueVisitors = %d (%v), want 3", n, err)
	}
	refs, err := s.TopReferrers("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Label != "news.ycombinator.com" || refs[0].Count != 2 {
		t.Errorf("TopReferrers = %v, want HN first with 2", refs)
	}
	places, err := s.TopPlaces("", 10)
	if err != nil {
		t.Fatal(err)
	}
	// The cityless row falls back to its country rather than vanishing.
	if len(places) != 3 || places[0].Label != "Lisbon, PT" {
		t.Errorf("TopPlaces = %v, want Lisbon first and a PT fallback row", places)
	}
	// Sorted dwells are 20, 30, 40, 86400; the middle pair's upper value is
	// the median this returns, and the outlier does not move it.
	if n, err := s.MedianDwell(""); err != nil || n != 40 {
		t.Errorf("MedianDwell = %d (%v), want 40", n, err)
	}
	if n, err := s.MedianDwell("2026-08-01"); err != nil || n != 0 {
		t.Errorf("MedianDwell outside the window = %d (%v), want 0", n, err)
	}
}
