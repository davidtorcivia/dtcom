package store

import (
	"database/sql"
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

// Bucket is one column of the views chart: a span of time and what it holds.
//
// Key is the machine form the query grouped on — "2026-07-30" for a day,
// "2026-07" for a month — and Label is what a person reads.
type Bucket struct {
	Key   string
	Label string
	Count int64
}

// dayFormat is how the views table stores a day, and therefore the only format
// the range arguments below are understood in.
const dayFormat = "2006-01-02"

// ViewsByDay returns one bucket per day from since to until inclusive,
// including the days nobody visited.
//
// The zero days are the point. Grouping in SQL only returns days that have a
// row, so a month with five busy days came back as five bars sitting side by
// side — a chart that reads as a steady week when it was five scattered days.
// Filling the gaps here makes the horizontal axis mean elapsed time again.
func (s *Store) ViewsByDay(since, until string) ([]Bucket, error) {
	counts, err := s.dayCounts(since, until)
	if err != nil {
		return nil, err
	}
	from, err := time.Parse(dayFormat, since)
	if err != nil {
		return nil, fmt.Errorf("views since %q: %w", since, err)
	}
	to, err := time.Parse(dayFormat, until)
	if err != nil {
		return nil, fmt.Errorf("views until %q: %w", until, err)
	}
	var out []Bucket
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format(dayFormat)
		out = append(out, Bucket{Key: key, Label: d.Format("2 Jan 2006"), Count: counts[key]})
	}
	return out, nil
}

// ViewsByMonth is ViewsByDay at a coarser grain, for windows long enough that a
// bar per day would be a bar per pixel.
func (s *Store) ViewsByMonth(since, until string) ([]Bucket, error) {
	counts, err := s.monthCounts(since, until)
	if err != nil {
		return nil, err
	}
	from, err := time.Parse(dayFormat, since)
	if err != nil {
		return nil, fmt.Errorf("views since %q: %w", since, err)
	}
	to, err := time.Parse(dayFormat, until)
	if err != nil {
		return nil, fmt.Errorf("views until %q: %w", until, err)
	}
	// Walk from the first of the starting month so a window beginning mid-month
	// still produces that whole month's bucket.
	from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	var out []Bucket
	for m := from; !m.After(to); m = m.AddDate(0, 1, 0) {
		key := m.Format("2006-01")
		out = append(out, Bucket{Key: key, Label: m.Format("Jan 2006"), Count: counts[key]})
	}
	return out, nil
}

func (s *Store) dayCounts(since, until string) (map[string]int64, error) {
	rows, err := s.conn().Query(
		`SELECT day, count(*) FROM views WHERE day >= ? AND day <= ? GROUP BY day`, since, until)
	if err != nil {
		return nil, fmt.Errorf("views by day: %w", err)
	}
	return scanCounts(rows)
}

func (s *Store) monthCounts(since, until string) (map[string]int64, error) {
	rows, err := s.conn().Query(
		`SELECT substr(day, 1, 7) m, count(*) FROM views WHERE day >= ? AND day <= ? GROUP BY m`, since, until)
	if err != nil {
		return nil, fmt.Errorf("views by month: %w", err)
	}
	return scanCounts(rows)
}

func scanCounts(rows *sql.Rows) (map[string]int64, error) {
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var key string
		var n int64
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}

// TopPaths returns the most-viewed paths in a window, busiest first. An empty
// since means all time.
func (s *Store) TopPaths(since string, limit int) ([]PathCount, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT path, count(*) c FROM views GROUP BY path ORDER BY c DESC, path LIMIT ?`
	args := []any{limit}
	if since != "" {
		query = `SELECT path, count(*) c FROM views WHERE day >= ? GROUP BY path ORDER BY c DESC, path LIMIT ?`
		args = []any{since, limit}
	}
	rows, err := s.conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("views by path: %w", err)
	}
	defer rows.Close()
	var out []PathCount
	for rows.Next() {
		var pc PathCount
		if err := rows.Scan(&pc.Path, &pc.Count); err != nil {
			return nil, err
		}
		out = append(out, pc)
	}
	return out, rows.Err()
}

// TotalViews counts views in a window. An empty since means all time.
func (s *Store) TotalViews(since string) (int64, error) {
	var n int64
	var err error
	if since == "" {
		err = s.conn().QueryRow(`SELECT count(*) FROM views`).Scan(&n)
	} else {
		err = s.conn().QueryRow(`SELECT count(*) FROM views WHERE day >= ?`, since).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("total views: %w", err)
	}
	return n, nil
}

// FirstViewDay is the earliest day holding a view, or "" when there are none.
// It is what bounds an "all time" window to the history that exists rather than
// to whenever the table happened to be created.
func (s *Store) FirstViewDay() (string, error) {
	var day sql.NullString
	if err := s.conn().QueryRow(`SELECT min(day) FROM views`).Scan(&day); err != nil {
		return "", fmt.Errorf("first view day: %w", err)
	}
	if !day.Valid {
		return "", nil
	}
	return day.String, nil
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
