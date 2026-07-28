package store

import "testing"

func TestAddListRemoveManualLink(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()

	id, err := s.AddLink(Link{Label: "GitHub", Href: "https://github.com", Source: "manual", SortDate: 1000})
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if id <= 0 {
		t.Fatal("expected positive id")
	}
	links, err := s.ListLinks(100)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if len(links) != 1 || links[0].Label != "GitHub" {
		t.Fatalf("got %+v", links)
	}
	if err := s.RemoveLink(id); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}
	links, _ = s.ListLinks(100)
	if len(links) != 0 {
		t.Fatalf("expected empty, got %+v", links)
	}
}

func TestUpsertRSSLinkDedups(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()

	l := Link{Label: "Post", Href: "https://sub.com/p/1", Source: "rss", FeedURL: "https://sub.com/feed", SortDate: 2000}
	if _, err := s.UpsertRSSLink(l); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRSSLink(l); err != nil {
		t.Fatal(err)
	}
	links, _ := s.ListLinks(100)
	if len(links) != 1 {
		t.Fatalf("expected dedup to 1, got %d", len(links))
	}
}
