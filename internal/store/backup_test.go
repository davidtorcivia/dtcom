package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSnapshotAndReplace exercises the two halves against a real database: a
// snapshot has to be a working copy of the data as it stood, and replacing has
// to leave the store usable and holding the copy's contents.
func TestSnapshotAndReplace(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.AddLink(Link{Label: "before", Href: "https://example.com/a", SortDate: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}

	snap := filepath.Join(dir, "snapshot.db")
	if err := s.Snapshot(snap); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// The live database moves on after the snapshot was taken.
	if _, err := s.AddLink(Link{Label: "after", Href: "https://example.com/b", SortDate: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	links, err := s.ListLinks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("live database has %d links, want 2", len(links))
	}

	// The snapshot is a database in its own right, holding the earlier state.
	side, err := Open(snap)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	sideLinks, err := side.ListLinks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sideLinks) != 1 || sideLinks[0].Label != "before" {
		t.Errorf("snapshot holds %+v, want just the link that existed when it was taken", sideLinks)
	}
	side.Close()

	// Putting it back leaves the store working and holding the snapshot.
	if err := s.ReplaceWith(snap); err != nil {
		t.Fatalf("ReplaceWith: %v", err)
	}
	after, err := s.ListLinks(10)
	if err != nil {
		t.Fatalf("query after replace: %v", err)
	}
	if len(after) != 1 || after[0].Label != "before" {
		t.Errorf("after restore the store holds %+v, want the snapshot's single link", after)
	}
	// Still writable: the pool was genuinely reopened, not left closed.
	if _, err := s.AddLink(Link{Label: "later", Href: "https://example.com/c", SortDate: time.Now().Unix()}); err != nil {
		t.Fatalf("write after replace: %v", err)
	}
}

// TestReplaceWithRejectsGarbage covers the failure that used to be
// unrecoverable: a replacement that is not a database must be turned away with
// the live database untouched and the store still serving. Before the candidate
// was verified up front, this closed the pool, overwrote the live file, and left
// every later query answering "sql: database is closed" until a restart.
func TestReplaceWithRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.AddLink(Link{Label: "keep me", Href: "https://example.com/a", SortDate: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}

	junk := filepath.Join(dir, "not-a-database")
	if err := os.WriteFile(junk, []byte("this is not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceWith(junk); err == nil {
		t.Fatal("ReplaceWith accepted a file that is not a database")
	}

	// The store still works, and still holds what it held.
	links, err := s.ListLinks(10)
	if err != nil {
		t.Fatalf("store unusable after a rejected replacement: %v", err)
	}
	if len(links) != 1 || links[0].Label != "keep me" {
		t.Errorf("live data changed by a rejected replacement: %+v", links)
	}
	if _, err := s.AddLink(Link{Label: "still writable", Href: "https://example.com/b", SortDate: time.Now().Unix()}); err != nil {
		t.Errorf("store not writable after a rejected replacement: %v", err)
	}
}

// TestSnapshotOverwrites: a second backup must not fail because the first left
// a file behind. SQLite's VACUUM INTO refuses an existing destination, so the
// store clears it.
func TestSnapshotOverwrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	snap := filepath.Join(dir, "snapshot.db")
	for i := 0; i < 2; i++ {
		if err := s.Snapshot(snap); err != nil {
			t.Fatalf("Snapshot %d: %v", i, err)
		}
	}
}
