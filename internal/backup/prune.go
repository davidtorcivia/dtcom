package backup

// Retention.
//
// The naive rule — "keep the last N" — has a failure it cannot see: if
// something goes wrong quietly, and nightly backups keep being taken, the
// damage is copied N times and the last good archive falls off the end. What
// protects against that is keeping copies at spreading intervals, so there is
// always something from a week ago and something from months ago.
//
// So: every archive from the last few days, then one per week, then one per
// month, thinning with age. A grandfather-father-son rotation, which is the
// oldest idea in backups and still the right one.

import (
	"log/slog"
	"time"
)

// Policy is how much history to keep.
type Policy struct {
	Days   int // keep every archive from this many days back
	Weeks  int // then the newest from each of this many ISO weeks
	Months int // then the newest from each of this many months
	Least  int // never go below this many archives, whatever the dates say

	// Safety is how many pre-restore archives to keep, newest first. They are
	// exempt from the date rules — each is the state of the site immediately
	// before somebody replaced it — but not from counting, or a run of
	// restores would fill the disk with copies of an afternoon.
	Safety int
}

// DefaultPolicy keeps roughly a fortnight of detail and half a year of shape.
// At this site's size — an archive is the posts, the database, and the image
// masters, so a few megabytes each — that is on the order of a hundred
// megabytes, which is a fair price for being able to say "put it back the way
// it was in March".
var DefaultPolicy = Policy{Days: 7, Weeks: 4, Months: 6, Least: 3, Safety: 3}

// selectForRemoval returns the archives that the policy does not keep, given
// the full list and the current time.
//
// Pre-restore archives are held to a count rather than to the dates: each one
// is the state of the site immediately before somebody replaced it, which is
// precisely the moment someone might need to reach back to, so age is the wrong
// way to judge them — but the newest few are the ones anybody would want, and
// an afternoon of restores should not fill the disk.
func selectForRemoval(all []Info, now time.Time, p Policy) []Info {
	if p.Least <= 0 {
		p.Least = 1
	}
	if p.Safety <= 0 {
		p.Safety = 1
	}
	// Newest first.
	sorted := make([]Info, len(all))
	copy(sorted, all)
	sortByCreatedDesc(sorted)

	keep := make(map[string]bool, len(sorted))
	var weeks, months = map[string]bool{}, map[string]bool{}
	var safety int

	for i, in := range sorted {
		switch {
		case i < p.Least:
			keep[in.Name] = true
		case in.Kind == KindPreRestore:
			// Counted newest first, so the ones that survive are the most
			// recent restores.
			if safety < p.Safety {
				safety++
				keep[in.Name] = true
			}
		case now.Sub(in.Created) <= time.Duration(p.Days)*24*time.Hour:
			keep[in.Name] = true
		default:
			y, w := in.Created.ISOWeek()
			wk := isoKey(y, w)
			mk := in.Created.Format("2006-01")
			// The newest archive in a week keeps that week; the newest in a
			// month keeps that month. Because the list is newest first, the
			// first one seen for a bucket is the one to keep.
			if !weeks[wk] && len(weeks) < p.Weeks {
				weeks[wk] = true
				keep[in.Name] = true
			} else if !months[mk] && len(months) < p.Months {
				months[mk] = true
				keep[in.Name] = true
			}
		}
		// A kept archive still claims its buckets, so a daily archive from
		// yesterday does not leave this week unclaimed and cause a second one
		// to be kept for it.
		if keep[in.Name] {
			y, w := in.Created.ISOWeek()
			weeks[isoKey(y, w)] = true
			months[in.Created.Format("2006-01")] = true
		}
	}

	var drop []Info
	for _, in := range sorted {
		if !keep[in.Name] {
			drop = append(drop, in)
		}
	}
	return drop
}

func isoKey(year, week int) string {
	return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006") + "-W" + pad2(week)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func sortByCreatedDesc(in []Info) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Created.After(in[j-1].Created); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// Prune removes the archives the policy no longer keeps, returning their names.
func (s *Service) Prune() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prune(time.Now().UTC())
}

func (s *Service) prune(now time.Time) ([]string, error) {
	all, err := s.dest.List()
	if err != nil {
		return nil, err
	}
	drop := selectForRemoval(all, now, s.cfg.Keep)
	var removed []string
	for _, in := range drop {
		if err := s.dest.Delete(in.Name); err != nil {
			// One that will not delete should not stop the others; a full disk
			// is exactly when pruning matters.
			slog.Warn("prune backup", "name", in.Name, "err", err)
			continue
		}
		removed = append(removed, in.Name)
	}
	return removed, nil
}
