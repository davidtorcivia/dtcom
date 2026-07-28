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
	id1, inserted, err := s.UpsertRSSLink(l)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first upsert: want inserted=true")
	}
	if id1 <= 0 {
		t.Fatalf("first upsert: want positive id, got %d", id1)
	}
	id2, inserted2, err := s.UpsertRSSLink(l)
	if err != nil {
		t.Fatal(err)
	}
	if inserted2 {
		t.Fatal("second upsert: want inserted=false (dedup)")
	}
	if id2 != id1 {
		t.Fatalf("second upsert returned id %d, want same as first %d", id2, id1)
	}
	links, _ := s.ListLinks(100)
	if len(links) != 1 {
		t.Fatalf("expected dedup to 1, got %d", len(links))
	}
}

// TestAddLinkRejectsJavascriptScheme verifies AddLink refuses to persist a
// link whose href uses a dangerous scheme — these render as <a href="..."> on
// /links and the scheme is an XSS vector (html/template escapes the attribute
// but does not block javascript:).
func TestAddLinkRejectsJavascriptScheme(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()
	for _, href := range []string{
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)", // case-insensitive
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox",
		"ftp://x.com", // not in allowlist
	} {
		if _, err := s.AddLink(Link{Label: "X", Href: href, Source: "manual", SortDate: 1}); err == nil {
			t.Errorf("AddLink(%q): expected error, got nil", href)
		}
	}
}

// TestAddLinkAcceptsHttpMailtoRelative verifies safe schemes/relative hrefs
// are accepted unchanged.
func TestAddLinkAcceptsHttpMailtoRelative(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()
	cases := []string{"https://x.com", "http://x.com", "mailto:a@b.com", "/posts/x", "#anchor"}
	for i, href := range cases {
		if _, err := s.AddLink(Link{Label: "L", Href: href, Source: "manual", SortDate: int64(i + 1)}); err != nil {
			t.Errorf("href %q rejected: %v", href, err)
		}
	}
	links, _ := s.ListLinks(100)
	if len(links) != len(cases) {
		t.Errorf("expected %d links persisted, got %d", len(cases), len(links))
	}
}

// TestUpsertRSSLinkSkipsJavascriptScheme verifies RSS-imported links with a
// disallowed scheme are silently skipped (not persisted, not flagged as
// inserted) rather than erroring — RSS is unauthenticated inbound so the
// caller can't push back, only refuse to render.
func TestUpsertRSSLinkSkipsJavascriptScheme(t *testing.T) {
	s := mustOpen(t)
	defer s.Close()
	_, inserted, err := s.UpsertRSSLink(Link{Label: "X", Href: "javascript:alert(1)", Source: "rss", FeedURL: "f", SortDate: 1})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if inserted {
		t.Error("javascript: link should be skipped, not inserted")
	}
	links, _ := s.ListLinks(10)
	if len(links) != 0 {
		t.Errorf("expected 0 links, got %d", len(links))
	}
}
