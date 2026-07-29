package backup

import (
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

// TestRetentionThinsWithAge is the whole point of a rolling policy: recent days
// kept in full, older ones thinned to one a week and then one a month, so there
// is always something from long enough ago to predate a quiet mistake.
func TestRetentionThinsWithAge(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	var all []Info
	for d := 0; d < 400; d++ {
		all = append(all, at(d, KindScheduled))
	}

	drop := selectForRemoval(all, now, DefaultPolicy)
	dropped := names(drop)
	kept := 0
	for _, in := range all {
		if !dropped[in.Name] {
			kept++
		}
	}

	// Everything inside the daily window survives. Checked to one day short of
	// the boundary: an archive taken at 04:00 seven days ago is seven days and
	// eight hours old at noon, which is outside a seven-day window, and whether
	// it survives then depends on the weekly rule rather than the daily one.
	for d := 0; d < DefaultPolicy.Days; d++ {
		if dropped[at(d, KindScheduled).Name] {
			t.Errorf("dropped an archive from %d days ago", d)
		}
	}
	// A year of daily backups thins to something in the low tens, not 400.
	if kept > 25 || kept < 10 {
		t.Errorf("kept %d of 400; expected the policy to thin to roughly a dozen", kept)
	}
	// And there is still something old to fall back to.
	var oldest Info
	for _, in := range all {
		if !dropped[in.Name] && (oldest.Name == "" || in.Created.Before(oldest.Created)) {
			oldest = in
		}
	}
	if now.Sub(oldest.Created) < 120*24*time.Hour {
		t.Errorf("oldest kept archive is only %v old; months of history were thrown away",
			now.Sub(oldest.Created))
	}
}

// TestRetentionKeepsSafetyCopies: the archive taken just before a restore is
// the one someone reaches for when the restore was a mistake, and there are
// only ever as many as there have been restores.
func TestRetentionKeepsSafetyCopies(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	all := []Info{at(0, KindScheduled), at(300, KindPreRestore), at(301, KindScheduled)}
	dropped := names(selectForRemoval(all, now, DefaultPolicy))
	if dropped[at(300, KindPreRestore).Name] {
		t.Error("a pre-restore archive was pruned")
	}
}

// TestRetentionNeverEmptiesTheDirectory: with a handful of very old archives
// and nothing recent, the policy still leaves the newest few alone rather than
// deleting every copy there is.
func TestRetentionNeverEmptiesTheDirectory(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	all := []Info{at(900, KindScheduled), at(901, KindScheduled), at(902, KindScheduled), at(903, KindScheduled)}
	drop := selectForRemoval(all, now, DefaultPolicy)
	if len(all)-len(drop) < DefaultPolicy.Least {
		t.Errorf("kept %d of %d, below the floor of %d", len(all)-len(drop), len(all), DefaultPolicy.Least)
	}
}

func TestPruneRemovesFromDestination(t *testing.T) {
	f := newFixture(t)
	// Two archives an hour apart, both far enough back that only the floor
	// keeps them, plus enough others to push past it.
	var made []Info
	for i := 0; i < 6; i++ {
		when := time.Now().UTC().AddDate(0, 0, -30*(i+1))
		in, err := f.svc.create(KindScheduled, when)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, in)
	}
	removed, err := f.svc.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) == 0 {
		t.Fatal("nothing pruned from six monthly archives spanning half a year")
	}
	after, err := f.svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(made)-len(removed) {
		t.Errorf("listed %d after pruning %d of %d", len(after), len(removed), len(made))
	}
}

// TestRetentionCapsSafetyCopies: exempting pre-restore archives from the date
// rules must not exempt them from counting, or a run of restores fills the disk.
func TestRetentionCapsSafetyCopies(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	var all []Info
	// Ten restores over ten days, plus something recent so the floor is not
	// what is doing the keeping.
	for d := 10; d < 20; d++ {
		all = append(all, at(d, KindPreRestore))
	}
	all = append(all, at(0, KindScheduled), at(1, KindScheduled), at(2, KindScheduled))

	dropped := names(selectForRemoval(all, now, DefaultPolicy))
	var keptSafety int
	for _, in := range all {
		if in.Kind == KindPreRestore && !dropped[in.Name] {
			keptSafety++
		}
	}
	if keptSafety != DefaultPolicy.Safety {
		t.Errorf("kept %d pre-restore archives, want %d", keptSafety, DefaultPolicy.Safety)
	}
	// And the ones kept are the newest.
	for d := 10; d < 10+DefaultPolicy.Safety; d++ {
		if dropped[at(d, KindPreRestore).Name] {
			t.Errorf("dropped a recent pre-restore archive (%d days old) while keeping older ones", d)
		}
	}
}
