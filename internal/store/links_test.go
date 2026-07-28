package store

import (
	"errors"
	"strings"
	"testing"
)

// newTestStore opens a throwaway store for one test.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := mustOpen(t)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

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
	if _, err := s.RemoveLink(id); err != nil {
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

// Feed text arrives as HTML: tags have to come out and entities have to be
// decoded, or the links page shows a literal "&#39;" where an apostrophe
// belongs (the template escapes the ampersand a second time).
func TestUpsertRSSLinkCleansFeedText(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.UpsertRSSLink(Link{
		Label: "How Go 1.26&#39;s inliner works",
		Href:  "https://example.org/inliner",
		Note:  "<p>A <b>description</b> with <a href=\"x\">markup</a> &amp; entities.</p>",
	}); err != nil {
		t.Fatal(err)
	}
	links, err := s.ListLinks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if got, want := links[0].Label, "How Go 1.26's inliner works"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
	if got, want := links[0].Note, "A description with markup & entities."; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

// A feed can emit an unbounded description; storing it whole would let a third
// party dictate how much of the database and the links page it occupies.
func TestUpsertRSSLinkBoundsLength(t *testing.T) {
	s := newTestStore(t)
	long := strings.Repeat("word ", 500)
	if _, _, err := s.UpsertRSSLink(Link{Label: long, Href: "https://example.org/long", Note: long}); err != nil {
		t.Fatal(err)
	}
	links, err := s.ListLinks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links[0].Label) > maxLinkLabel+8 {
		t.Errorf("label length = %d, want it bounded near %d", len(links[0].Label), maxLinkLabel)
	}
	if len(links[0].Note) > maxLinkNote+8 {
		t.Errorf("note length = %d, want it bounded near %d", len(links[0].Note), maxLinkNote)
	}
}

// An item with no usable title falls back to its URL rather than rendering as
// a blank row.
func TestUpsertRSSLinkFallsBackToHref(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.UpsertRSSLink(Link{Label: "  <em></em> ", Href: "https://example.org/untitled"}); err != nil {
		t.Fatal(err)
	}
	links, _ := s.ListLinks(10)
	if links[0].Label != "https://example.org/untitled" {
		t.Errorf("label = %q, want the href as a fallback", links[0].Label)
	}
}

// A duplicate manual link is a client conflict, not a server fault.
func TestAddLinkReportsDuplicate(t *testing.T) {
	s := newTestStore(t)
	l := Link{Label: "One", Href: "https://example.org/one", Source: "manual", SortDate: 1}
	if _, err := s.AddLink(l); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLink(l); !errors.Is(err, ErrDuplicateLink) {
		t.Errorf("second AddLink err = %v, want ErrDuplicateLink", err)
	}
}

func TestAddLinkRejectsDisallowedScheme(t *testing.T) {
	s := newTestStore(t)
	for _, href := range []string{"javascript:alert(1)", "data:text/html,x", "  JavaScript:alert(1)", ""} {
		if _, err := s.AddLink(Link{Label: "x", Href: href}); !errors.Is(err, ErrDisallowedScheme) {
			t.Errorf("AddLink(%q) err = %v, want ErrDisallowedScheme", href, err)
		}
	}
}

// RSS links can't be deleted (the next poll would re-import them), and the
// caller has to be able to tell that nothing happened.
func TestRemoveLinkReportsWhetherAnythingWent(t *testing.T) {
	s := newTestStore(t)
	id, err := s.AddLink(Link{Label: "Manual", Href: "https://example.org/m", Source: "manual", SortDate: 1})
	if err != nil {
		t.Fatal(err)
	}
	rssID, _, err := s.UpsertRSSLink(Link{Label: "Feed item", Href: "https://example.org/r"})
	if err != nil {
		t.Fatal(err)
	}

	if removed, err := s.RemoveLink(id); err != nil || !removed {
		t.Errorf("RemoveLink(manual) = %v, %v; want true, nil", removed, err)
	}
	if removed, err := s.RemoveLink(rssID); err != nil || removed {
		t.Errorf("RemoveLink(rss) = %v, %v; want false, nil", removed, err)
	}
	if removed, err := s.RemoveLink(99999); err != nil || removed {
		t.Errorf("RemoveLink(missing) = %v, %v; want false, nil", removed, err)
	}
}
