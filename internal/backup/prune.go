package backup

// Retention: keep the last N archives plus the last few pre-restore safety
// copies, counted separately. Counting archives rather than days is correct
// because archives are only written on change — N is the last N states, not
// the last N days, and identical copies never push anything out, so
// grandfather-father-son thinning has nothing to thin.

import (
	"log/slog"
	"time"
)

// Policy is how much history to keep.
type Policy struct {
	// Keep is how many archives to retain, newest first.
	Keep int

	// Safety is how many pre-restore archives to keep, counted separately.
	// Each is the state of the site immediately before somebody replaced it —
	// the moment most worth being able to reach — so they do not compete with
	// the ordinary ones for places. They are still counted, or a run of
	// restores would fill the disk with copies of one afternoon.
	Safety int
}

// DefaultPolicy is deliberately generous. Since the images inside an archive
// are stored once and shared (see pool.go), thirty archives cost about as much
// as one did before, so there is no reason to be stingy with history.
var DefaultPolicy = Policy{Keep: 30, Safety: 3}

// selectForRemoval returns the archives the policy does not keep.
func selectForRemoval(all []Info, p Policy) []Info {
	if p.Keep <= 0 {
		p.Keep = 1
	}
	if p.Safety <= 0 {
		p.Safety = 1
	}
	sorted := make([]Info, len(all))
	copy(sorted, all)
	sortByCreatedDesc(sorted)

	var ordinary, safety int
	var drop []Info
	for _, in := range sorted {
		if in.Kind == KindPreRestore {
			safety++
			if safety > p.Safety {
				drop = append(drop, in)
			}
			continue
		}
		ordinary++
		if ordinary > p.Keep {
			drop = append(drop, in)
		}
	}
	return drop
}

func sortByCreatedDesc(in []Info) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Created.After(in[j-1].Created); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// Prune removes the archives the policy no longer keeps, then sweeps any
// pooled image no archive refers to any more. Returns the archive names it
// removed and how many pooled images went with them.
func (s *Service) Prune() ([]string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prune()
}

func (s *Service) prune() ([]string, int, error) {
	all, err := s.dest.List()
	if err != nil {
		return nil, 0, err
	}
	var removed []string
	for _, in := range selectForRemoval(all, s.cfg.Keep) {
		if err := s.dest.Delete(in.Name); err != nil {
			// One that will not delete should not stop the others; a full disk
			// is exactly when pruning matters.
			slog.Warn("prune backup", "name", in.Name, "err", err)
			continue
		}
		removed = append(removed, in.Name)
	}
	swept, err := s.sweepPool()
	if err != nil {
		slog.Warn("sweep backup image pool", "err", err)
	}
	return removed, swept, nil
}

// unusedSince is a small grace period on pooled images. An image put in the
// pool by a backup being written this instant is not yet referenced by any
// listed archive, and sweeping between those two steps would delete it out
// from under the archive that is about to name it.
const unusedSince = 10 * time.Minute
