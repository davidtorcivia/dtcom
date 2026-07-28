package store

import (
	"fmt"
	"html"
	"strings"
	"unicode"
)

// IndexedArticle is a flat article projection for the FTS5 search index.
// (Named IndexedArticle to avoid collision with build.Article, the canonical
// content type. Conversion happens in the rebuild engine.)
type IndexedArticle struct {
	Slug        string
	Title       string
	Description string
	Body        string // plain text, markdown stripped for indexing
	Tags        string // comma-separated
}

// ReindexArticles replaces the entire article index.
func (s *Store) ReindexArticles(arts []IndexedArticle) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM articles_fts"); err != nil {
		return fmt.Errorf("clear fts: %w", err)
	}
	// One prepared statement reused across every row: a rebuild re-inserts the
	// whole corpus, and re-planning the insert per article is pure overhead.
	stmt, err := tx.Prepare(
		"INSERT INTO articles_fts(slug, title, body, description, tags, tags_unindexed) VALUES(?,?,?,?,?,?)")
	if err != nil {
		return fmt.Errorf("prepare fts insert: %w", err)
	}
	defer stmt.Close()
	for _, a := range arts {
		if _, err := stmt.Exec(a.Slug, a.Title, a.Body, a.Description, a.Tags, ""); err != nil {
			return fmt.Errorf("insert fts row: %w", err)
		}
	}
	return tx.Commit()
}

type SearchHit struct {
	Slug        string
	Title       string
	Description string
	// Excerpt is an HTML fragment: the matched text HTML-escaped, with the
	// matching terms wrapped in <mark>. Safe to insert into the page as-is.
	Excerpt string
}

// Sentinels handed to FTS5's snippet() in place of the literal <mark> tags.
// The snippet is built from indexed article text, which can legitimately
// contain angle brackets; escaping the whole snippet first and swapping these
// control characters for real tags afterwards keeps that text from turning
// into live markup in the search results.
const (
	markOpen  = "\x02"
	markClose = "\x03"
)

// SearchArticles runs an FTS5 query.
func (s *Store) SearchArticles(query string, limit int) ([]SearchHit, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT slug, title, description, snippet(articles_fts, 2, ?, ?, '…', 24)
		 FROM articles_fts WHERE articles_fts MATCH ?
		 ORDER BY rank LIMIT ?`,
		markOpen, markClose, match, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.Slug, &h.Title, &h.Description, &h.Excerpt); err != nil {
			return nil, err
		}
		h.Excerpt = markupSnippet(h.Excerpt)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// markupSnippet escapes the snippet text, then restores the highlight markers
// as real <mark> elements.
func markupSnippet(s string) string {
	s = html.EscapeString(s)
	s = strings.ReplaceAll(s, markOpen, "<mark>")
	s = strings.ReplaceAll(s, markClose, "</mark>")
	return s
}

// maxSearchTerms bounds how much work one query can ask FTS5 to do.
const maxSearchTerms = 12

// ftsQuery turns a raw user query into a safe FTS5 MATCH expression:
// "kernel lockouts" becomes `"kernel"* "lockouts"*`.
//
// Every term is wrapped in double quotes (with any embedded quote doubled, the
// FTS5 escape) so that a query containing operators or punctuation is treated
// as literal text. Without this, ordinary input — a stray quote, a trailing
// "AND", a bare "*" — is parsed as query syntax and fails the whole statement,
// which surfaced to visitors as a 500 from the search box.
//
// Returns "" when the query has no usable terms, which the caller treats as
// "no results" rather than running a query.
func ftsQuery(q string) string {
	fields := strings.Fields(q)
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(terms) >= maxSearchTerms {
			break
		}
		if !hasIndexableRune(f) {
			// Pure punctuation ("--", "?"): the tokenizer would produce no
			// tokens and FTS5 rejects an empty quoted term outright.
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(f, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}

// hasIndexableRune reports whether a term contains anything the unicode61
// tokenizer would actually index.
func hasIndexableRune(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
