package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// View is one recorded page view. Referrer, Country and City are whatever the
// beacon and the proxy could tell us; all three are routinely empty.
type View struct {
	Path     string
	Day      string
	IPHash   string
	Referrer string // hostname only, "" for direct or same-site
	Country  string
	City     string
}

// RecordView records a unique (path, day, ip_hash) page view. Repeated calls
// with the same key are no-ops (dedup), satisfying the PRIMARY KEY constraint —
// which also means the referrer and place kept are the first ones seen that
// day, not the last.
func (s *Store) RecordView(v View) error {
	if v.Path == "" || v.Day == "" {
		return nil
	}
	_, err := s.conn().Exec(
		`INSERT INTO views(path, day, ip_hash, ts, referrer, country, city) VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT(path, day, ip_hash) DO NOTHING`,
		v.Path, v.Day, v.IPHash, time.Now().Unix(), v.Referrer, v.Country, v.City,
	)
	if err != nil {
		return fmt.Errorf("record view: %w", err)
	}
	return nil
}

// AddDwell adds seconds of reading time to an existing view. It never inserts:
// a dwell report without a matching view is either a lost beacon or a forged
// one, and neither should create a page view.
func (s *Store) AddDwell(path, day, ipHash string, secs int) error {
	if secs <= 0 {
		return nil
	}
	_, err := s.conn().Exec(
		`UPDATE views SET dwell = dwell + ? WHERE path = ? AND day = ? AND ip_hash = ?`,
		secs, path, day, ipHash)
	if err != nil {
		return fmt.Errorf("add dwell: %w", err)
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

// LabelCount is a counted thing that is not a path — a referring host, a
// place. Same shape as PathCount, different meaning, so the dashboard can't
// accidentally render one as a link to the other.
type LabelCount struct {
	Label string
	Count int64
}

// UniqueVisitors counts distinct address hashes in a window. The hash has no
// day salt, so somebody who reads on Monday and again on Friday is one visitor
// across a week — which is what the number is asked to mean. Their address
// changing (phone off wifi, ISP rotation) still shows up as two.
func (s *Store) UniqueVisitors(since string) (int64, error) {
	var n int64
	var err error
	if since == "" {
		err = s.conn().QueryRow(`SELECT count(DISTINCT ip_hash) FROM views`).Scan(&n)
	} else {
		err = s.conn().QueryRow(`SELECT count(DISTINCT ip_hash) FROM views WHERE day >= ?`, since).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("unique visitors: %w", err)
	}
	return n, nil
}

// TopReferrers returns the sites visitors arrived from, busiest first. Rows
// with no referrer (typed the URL, opened a bookmark, came from an app that
// strips it) are excluded rather than lumped into a "direct" bucket, because
// the bucket would be a majority that tells you nothing.
func (s *Store) TopReferrers(since string, limit int) ([]LabelCount, error) {
	return s.labelCounts("referrer", since, limit)
}

// TopPlaces returns "City, CC" counts. Rows without a city fall back to the
// country alone; rows with neither are excluded.
func (s *Store) TopPlaces(since string, limit int) ([]LabelCount, error) {
	return s.labelCounts(
		`CASE WHEN city <> '' AND country <> '' THEN city || ', ' || country
		      WHEN city <> '' THEN city ELSE country END`, since, limit)
}

// labelCounts groups views by an expression, dropping empties. expr is a SQL
// fragment chosen here in the package, never anything a request supplies.
func (s *Store) labelCounts(expr, since string, limit int) ([]LabelCount, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := ""
	args := []any{}
	if since != "" {
		where = ` AND day >= ?`
		args = append(args, since)
	}
	args = append(args, limit)
	rows, err := s.conn().Query(
		`SELECT `+expr+` label, count(*) c FROM views WHERE `+expr+` <> ''`+where+
			` GROUP BY label ORDER BY c DESC, label LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("views by %s: %w", expr, err)
	}
	defer rows.Close()
	var out []LabelCount
	for rows.Next() {
		var lc LabelCount
		if err := rows.Scan(&lc.Label, &lc.Count); err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}

// MedianDwell is the typical seconds spent on a page, over the views that
// reported any. The median, not the mean: one tab left open over a weekend
// drags a mean into the hours and makes the number useless.
func (s *Store) MedianDwell(since string) (int64, error) {
	where := `dwell > 0`
	args := []any{}
	if since != "" {
		where += ` AND day >= ?`
		args = append(args, since)
	}
	var n sql.NullInt64
	err := s.conn().QueryRow(
		`SELECT dwell FROM views WHERE `+where+
			` ORDER BY dwell LIMIT 1 OFFSET (SELECT count(*) FROM views WHERE `+where+`) / 2`,
		append(args, args...)...).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("median dwell: %w", err)
	}
	return n.Int64, nil
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
