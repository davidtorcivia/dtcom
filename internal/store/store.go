// Package store is the SQLite persistence layer.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go sqlite driver
)

type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS links (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    label      TEXT NOT NULL,
    href       TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT 'manual',
    sort_date  INTEGER NOT NULL,
    feed_url   TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_links_source ON links(source);
CREATE INDEX IF NOT EXISTS idx_links_feed_url ON links(feed_url);
CREATE UNIQUE INDEX IF NOT EXISTS idx_links_href ON links(href);

CREATE TABLE IF NOT EXISTS views (
    path      TEXT NOT NULL,
    day       TEXT NOT NULL,
    ip_hash   TEXT NOT NULL DEFAULT '',
    ts        INTEGER NOT NULL,
    PRIMARY KEY (path, day, ip_hash)
);

CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts USING fts5(
    slug, title, body, description, tags, tags_unindexed UNINDEXED,
    tokenize = 'porter unicode61'
);
`

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}
