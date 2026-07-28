package store

import (
	"fmt"
	"strings"
	"time"
)

type Link struct {
	ID        int64
	Label     string
	Href      string
	Note      string
	Source    string // "manual" or "rss"
	SortDate  int64  // unix seconds
	FeedURL   string
	CreatedAt int64
}

// sanitizeHref returns the href unchanged if it has a safe scheme (http, https,
// mailto) or is relative (starts with / or #). Otherwise it returns "" which
// the caller treats as "drop this link".
//
// This blocks javascript:, data:, vbscript:, and other dangerous schemes from
// ever being stored or rendered as a clickable link — important because link
// hrefs come from external RSS feeds (unauthenticated inbound) where a
// malicious feed item like <link>javascript:alert(1)</link> would otherwise
// round-trip into an <a href="javascript:..."> on /links. Go's html/template
// escapes the attribute but does not block the scheme itself.
func sanitizeHref(href string) string {
	h := strings.TrimSpace(href)
	if h == "" {
		return ""
	}
	// relative URLs are safe (no scheme).
	if strings.HasPrefix(h, "/") || strings.HasPrefix(h, "#") {
		return h
	}
	// absolute URL: the scheme must be allowlisted.
	lower := strings.ToLower(h)
	for _, scheme := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(lower, scheme) {
			return h
		}
	}
	// anything else (javascript:, data:, vbscript:, unknown:) — reject.
	return ""
}

// AddLink inserts a manual link. A disallowed href scheme (javascript:, data:,
// vbscript:, etc.) is rejected with an error so the authed caller gets
// feedback rather than a silent drop.
func (s *Store) AddLink(l Link) (int64, error) {
	if l.Source == "" {
		l.Source = "manual"
	}
	if l.CreatedAt == 0 {
		l.CreatedAt = time.Now().Unix()
	}
	if sanitizeHref(l.Href) == "" {
		return 0, fmt.Errorf("link href has disallowed scheme or is empty")
	}
	res, err := s.db.Exec(
		`INSERT INTO links(label, href, note, source, sort_date, feed_url, created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		l.Label, l.Href, l.Note, l.Source, l.SortDate, l.FeedURL, l.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert link: %w", err)
	}
	return res.LastInsertId()
}

// UpsertRSSLink inserts an RSS link if its href isn't already present and
// returns (id, inserted, err). When inserted is false the href was already
// present (dedup); id is then the existing row's id. Items with a disallowed
// href scheme (javascript:/data:/etc.) are silently skipped (treated as
// not-inserted) since RSS is unauthenticated inbound — we can't push back on
// the feed, only refuse to render the payload.
func (s *Store) UpsertRSSLink(l Link) (int64, bool, error) {
	l.Source = "rss"
	l.CreatedAt = time.Now().Unix()
	if sanitizeHref(l.Href) == "" {
		// skip silently — RSS items with javascript:/data: hrefs are treated
		// as already-present (dedup) so they don't appear on /links.
		return 0, false, nil
	}
	res, err := s.db.Exec(
		`INSERT INTO links(label, href, note, source, sort_date, feed_url, created_at)
		 VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT DO NOTHING`,
		l.Label, l.Href, l.Note, l.Source, l.SortDate, l.FeedURL, l.CreatedAt,
	)
	if err != nil {
		return 0, false, fmt.Errorf("upsert rss link: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var id int64
		_ = s.db.QueryRow("SELECT id FROM links WHERE href=?", l.Href).Scan(&id)
		return id, false, nil
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

func (s *Store) RemoveLink(id int64) error {
	_, err := s.db.Exec("DELETE FROM links WHERE id=? AND source='manual'", id)
	if err != nil {
		return fmt.Errorf("remove link: %w", err)
	}
	return nil
}

// ListLinks returns all links ordered by sort_date desc.
func (s *Store) ListLinks(limit int) ([]Link, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, label, href, note, source, sort_date, feed_url
		 FROM links ORDER BY sort_date DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.Label, &l.Href, &l.Note, &l.Source, &l.SortDate, &l.FeedURL); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
