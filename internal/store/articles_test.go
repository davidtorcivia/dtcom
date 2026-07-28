package store

import "testing"

func TestReindexAndSearch(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()

	arts := []IndexedArticle{
		{Slug: "a", Title: "SchedLock", Description: "agents", Body: "kernel lockouts security", Tags: "ai,security"},
		{Slug: "b", Title: "Color", Description: "gamut", Body: "perceptual mapping davinci", Tags: "color"},
	}
	if err := s.ReindexArticles(arts); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	got, err := s.SearchArticles("security", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "a" {
		t.Fatalf("got %+v", got)
	}
}

func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
