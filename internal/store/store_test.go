package store

import "testing"

func TestOpenCreatesSchema(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	var n int
	err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Errorf("expected at least 3 tables, got %d", n)
	}
}
