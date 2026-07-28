package store

import "testing"

func TestRecordAndAggregateViews(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()

	if err := s.RecordView("/posts/a", "2026-07-27", "ip1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordView("/posts/a", "2026-07-27", "ip1"); err != nil {
		t.Fatal(err) // dedup: same ip+day
	}
	if err := s.RecordView("/posts/a", "2026-07-27", "ip2"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordView("/posts/b", "2026-07-27", "ip2"); err != nil {
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
