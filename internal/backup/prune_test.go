package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func at(days int, kind Kind) Info {
	when := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
	return Info{Name: archiveName(when, kind), Kind: kind, Created: when, Size: 1}
}

func names(in []Info) map[string]bool {
	out := map[string]bool{}
	for _, i := range in {
		out[i.Name] = true
	}
	return out
}

// TestRetentionKeepsTheLastN: the policy counts archives, not days, because an
// unchanged site produces none — so N is the last N states the site was in.
func TestRetentionKeepsTheLastN(t *testing.T) {
	var all []Info
	for d := 0; d < 50; d++ {
		all = append(all, at(d, KindScheduled))
	}
	p := Policy{Keep: 30, Safety: 3}
	dropped := names(selectForRemoval(all, p))

	var kept int
	for _, in := range all {
		if !dropped[in.Name] {
			kept++
		}
	}
	if kept != p.Keep {
		t.Errorf("kept %d of 50, want %d", kept, p.Keep)
	}
	// The ones kept are the newest.
	for d := 0; d < p.Keep; d++ {
		if dropped[at(d, KindScheduled).Name] {
			t.Errorf("dropped the archive from %d days ago while keeping older ones", d)
		}
	}
	for d := p.Keep; d < 50; d++ {
		if !dropped[at(d, KindScheduled).Name] {
			t.Errorf("kept the archive from %d days ago, past the limit of %d", d, p.Keep)
		}
	}
}

// TestRetentionCountsSafetyCopiesSeparately: a pre-restore archive is the state
// immediately before somebody replaced the site, so it does not compete with
// ordinary archives for places — but it is still counted, or a run of restores
// fills the disk.
func TestRetentionCountsSafetyCopiesSeparately(t *testing.T) {
	p := Policy{Keep: 5, Safety: 2}
	var all []Info
	for d := 0; d < 5; d++ {
		all = append(all, at(d, KindScheduled))
	}
	for d := 10; d < 15; d++ {
		all = append(all, at(d, KindPreRestore))
	}
	dropped := names(selectForRemoval(all, p))

	var ordinary, safety int
	for _, in := range all {
		if dropped[in.Name] {
			continue
		}
		if in.Kind == KindPreRestore {
			safety++
		} else {
			ordinary++
		}
	}
	if ordinary != 5 {
		t.Errorf("kept %d ordinary archives, want 5 — safety copies took their places", ordinary)
	}
	if safety != p.Safety {
		t.Errorf("kept %d pre-restore archives, want %d", safety, p.Safety)
	}
	for d := 10; d < 10+p.Safety; d++ {
		if dropped[at(d, KindPreRestore).Name] {
			t.Errorf("dropped a recent pre-restore archive (%d days old) while keeping older ones", d)
		}
	}
}

func TestPruneRemovesFromDestination(t *testing.T) {
	f := newFixture(t)
	f.svc.cfg.Keep = Policy{Keep: 2, Safety: 1}

	var made []Info
	for i := 0; i < 5; i++ {
		// Each one has to differ, or the change check declines to write it.
		write(t, filepath.Join(f.content, "posts", "one.md"), "# One, revision "+string(rune('a'+i)))
		when := time.Now().UTC().Add(time.Duration(i) * time.Second)
		in, err := f.svc.create(KindScheduled, when)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, in)
	}
	removed, _, err := f.svc.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("pruned %d of 5 with a limit of 2: %v", len(removed), removed)
	}
	after, err := f.svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Errorf("%d archives left, want 2", len(after))
	}
}

// TestPruneSweepsUnreferencedImages: an image nothing refers to any more should
// leave the pool with the archives that named it, or the pool only ever grows.
func TestPruneSweepsUnreferencedImages(t *testing.T) {
	f := newFixture(t)
	f.svc.cfg.Keep = Policy{Keep: 1, Safety: 1}

	// First archive names aaaa.png and bbbb.jpg.
	if _, err := f.svc.create(KindScheduled, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The site loses one image and gains another.
	if err := os.Remove(filepath.Join(f.images, "bbbb.jpg")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.images, "dddd.png"), "a newer picture")
	if _, err := f.svc.create(KindScheduled, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Pooled images written moments ago are inside the grace period, so age
	// them past it before sweeping.
	agePool(t, f.backups, -time.Hour)

	_, swept, err := f.svc.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if swept != 1 {
		t.Errorf("swept %d pooled images, want 1 (bbbb.jpg, which no remaining archive names)", swept)
	}
	if _, err := os.Stat(filepath.Join(f.backups, "images", "bbbb.jpg")); !os.IsNotExist(err) {
		t.Error("an image no archive refers to is still pooled")
	}
	for _, keep := range []string{"aaaa.png", "dddd.png"} {
		if _, err := os.Stat(filepath.Join(f.backups, "images", keep)); err != nil {
			t.Errorf("%s was swept but the surviving archive names it", keep)
		}
	}
}

// agePool backdates every pooled file, standing in for the passage of time.
func agePool(t *testing.T, backupDir string, by time.Duration) {
	t.Helper()
	dir := filepath.Join(backupDir, "images")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(by)
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), when, when); err != nil {
			t.Fatal(err)
		}
	}
}
