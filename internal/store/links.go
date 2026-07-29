package store

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
	"unicode/utf8"
)

// Sentinel errors so callers can map a failure to the right HTTP status
// without matching on driver message text.
var (
	// ErrDisallowedScheme means the href was empty or used a scheme that must
	// never become a clickable link (javascript:, data:, vbscript:, …).
	ErrDisallowedScheme = errors.New("link href has disallowed scheme or is empty")
	// ErrDuplicateLink means a link with that href is already stored.
	ErrDuplicateLink = errors.New("link href already exists")
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

// SanitizeHref returns the href unchanged if it has a safe scheme (http, https,
// mailto) or is relative (starts with / or #). Otherwise it returns "" which
// the caller treats as "drop this link".
//
// It is exported because the admin nav and social-link editors need the same
// guarantee for hrefs an operator types in, and a second copy of an allowlist
// is a second place for it to rot.
//
// This blocks javascript:, data:, vbscript:, and other dangerous schemes from
// ever being stored or rendered as a clickable link — important because link
// hrefs come from external RSS feeds (unauthenticated inbound) where a
// malicious feed item like <link>javascript:alert(1)</link> would otherwise
// round-trip into an <a href="javascript:..."> on /links. Go's html/template
// escapes the attribute but does not block the scheme itself.
func SanitizeHref(href string) string {
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
	href := SanitizeHref(l.Href)
	if href == "" {
		return 0, ErrDisallowedScheme
	}
	if l.Label = strings.TrimSpace(l.Label); l.Label == "" {
		return 0, fmt.Errorf("link label is required")
	}
	res, err := s.conn().Exec(
		`INSERT INTO links(label, href, note, source, sort_date, feed_url, created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		l.Label, href, l.Note, l.Source, l.SortDate, l.FeedURL, l.CreatedAt,
	)
	if err != nil {
		// The href column carries a unique index; a repeat submission is an
		// ordinary conflict, not a server fault.
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
			return 0, ErrDuplicateLink
		}
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
	href := SanitizeHref(l.Href)
	if href == "" {
		// skip silently — RSS items with javascript:/data: hrefs are treated
		// as already-present (dedup) so they don't appear on /links.
		return 0, false, nil
	}
	// Feed titles and descriptions are attacker-influenced free text of
	// arbitrary length; bound them before they reach the database and the
	// rendered /links page.
	l.Label = truncate(collapseSpace(plainText(l.Label)), maxLinkLabel)
	if l.Label == "" {
		l.Label = href
	}
	l.Note = truncate(collapseSpace(plainText(l.Note)), maxLinkNote)
	res, err := s.conn().Exec(
		`INSERT INTO links(label, href, note, source, sort_date, feed_url, created_at)
		 VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT DO NOTHING`,
		l.Label, href, l.Note, l.Source, l.SortDate, l.FeedURL, l.CreatedAt,
	)
	if err != nil {
		return 0, false, fmt.Errorf("upsert rss link: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var id int64
		_ = s.conn().QueryRow("SELECT id FROM links WHERE href=?", href).Scan(&id)
		return id, false, nil
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

// Bounds on text imported from third-party feeds.
const (
	maxLinkLabel = 300
	maxLinkNote  = 500
)

// plainText turns feed-supplied HTML into the plain text the links page
// renders.
//
// Entities have to be decoded after the tags come out, or an apostrophe that
// the feed wrote as &#39; survives into the database and is then escaped again
// by the template — showing up on the page as a literal "&#39;".
func plainText(s string) string {
	return html.UnescapeString(stripTags(s))
}

// stripTags removes HTML markup from feed-supplied text. RSS descriptions are
// commonly full HTML documents; the links page renders the note as plain text,
// so the markup is noise at best.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary so the stored string stays valid UTF-8.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// RemoveLink deletes a manual link, reporting whether a row actually went
// away. RSS-imported links are deliberately not deletable — the next poll
// would re-import them — and the caller needs to know that so it can say so
// instead of reporting a silent success.
func (s *Store) RemoveLink(id int64) (bool, error) {
	res, err := s.conn().Exec("DELETE FROM links WHERE id=? AND source='manual'", id)
	if err != nil {
		return false, fmt.Errorf("remove link: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove link: %w", err)
	}
	return n > 0, nil
}

// ListLinks returns all links ordered by sort_date desc.
func (s *Store) ListLinks(limit int) ([]Link, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.conn().Query(
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
