package store

import (
	"fmt"
	"time"
)

// RecordView records a unique (path, day, ip_hash) page view. Repeated calls
// with the same key are no-ops (dedup), satisfying the PRIMARY KEY constraint.
func (s *Store) RecordView(path, day, ipHash string) error {
	if path == "" || day == "" {
		return nil
	}
	_, err := s.conn().Exec(
		`INSERT INTO views(path, day, ip_hash, ts) VALUES(?,?,?,?)
		 ON CONFLICT(path, day, ip_hash) DO NOTHING`,
		path, day, ipHash, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("record view: %w", err)
	}
	return nil
}

type Stats struct {
	Total  int64
	ByPath []PathCount
	ByDay  []DayCount
}

type PathCount struct {
	Path  string
	Count int64
}
type DayCount struct {
	Day   string
	Count int64
}

func (s *Store) Stats() (*Stats, error) {
	st := &Stats{}
	if err := s.conn().QueryRow("SELECT count(*) FROM views").Scan(&st.Total); err != nil {
		return nil, fmt.Errorf("total views: %w", err)
	}
	rows, err := s.conn().Query("SELECT path, count(*) c FROM views GROUP BY path ORDER BY c DESC LIMIT 50")
	if err != nil {
		return nil, fmt.Errorf("views by path: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pc PathCount
		if err := rows.Scan(&pc.Path, &pc.Count); err != nil {
			return nil, err
		}
		st.ByPath = append(st.ByPath, pc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	dayRows, err := s.conn().Query(`SELECT day, count(*) c FROM views
		WHERE day >= date('now','-30 days') GROUP BY day ORDER BY day`)
	if err != nil {
		return nil, fmt.Errorf("views by day: %w", err)
	}
	defer dayRows.Close()
	for dayRows.Next() {
		var dc DayCount
		if err := dayRows.Scan(&dc.Day, &dc.Count); err != nil {
			return nil, err
		}
		st.ByDay = append(st.ByDay, dc)
	}
	return st, dayRows.Err()
}
