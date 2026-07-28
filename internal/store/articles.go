package store

import (
	"fmt"
	"strings"
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
	for _, a := range arts {
		if _, err := tx.Exec(
			"INSERT INTO articles_fts(slug, title, body, description, tags, tags_unindexed) VALUES(?,?,?,?,?,?)",
			a.Slug, a.Title, a.Body, a.Description, a.Tags, "",
		); err != nil {
			return fmt.Errorf("insert fts row: %w", err)
		}
	}
	return tx.Commit()
}

type SearchHit struct {
	Slug        string
	Title       string
	Description string
	Excerpt     string
}

// SearchArticles runs an FTS5 query.
func (s *Store) SearchArticles(query string, limit int) ([]SearchHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT slug, title, description, snippet(articles_fts, 2, '<mark>', '</mark>', '…', 24)
		 FROM articles_fts WHERE articles_fts MATCH ?
		 ORDER BY rank LIMIT ?`,
		ftsQuery(q), limit,
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
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// ftsQuery turns "kernel lockouts" into "kernel* lockouts*".
func ftsQuery(q string) string {
	parts := strings.Fields(q)
	for i, p := range parts {
		parts[i] = p + "*"
	}
	return strings.Join(parts, " ")
}
