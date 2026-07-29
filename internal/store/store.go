// Package store is the SQLite persistence layer.
package store

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite" // pure-Go sqlite driver
)

type Store struct {
	// mu guards the pool pointer itself, not the queries running through it.
	// Only ReplaceWith takes it for writing, and only for as long as it takes
	// to close one pool and open another.
	mu   sync.RWMutex
	db   *sql.DB
	path string
}

// conn returns the current pool. Every query goes through it so that a restore,
// which swaps the pool underneath, is picked up by whatever runs next rather
// than by whatever happens to be restarted.
func (s *Store) conn() *sql.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

// Open opens (or creates) the database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// openDB opens the pool with the pragmas this database is always used under.
// Separate from Open because a restore reopens the same file the same way.
func openDB(path string) (*sql.DB, error) {
	// busy_timeout: with a single connection, lock contention is rare, but WAL
	// checkpoints and an external reader (a backup, a sqlite3 shell) can still
	// hold a lock briefly — waiting beats failing the request.
	// synchronous(NORMAL) is the standard WAL pairing: durable across process
	// crashes, one fsync per checkpoint instead of one per commit.
	dsn := path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // keep the connection; reopening re-applies pragmas
	// sql.Open is lazy — without a ping, a bad path or an unwritable volume
	// wouldn't surface until the first request.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}
	return db, nil
}

func (s *Store) Close() error { return s.conn().Close() }

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
-- ListLinks orders every read by sort_date desc; without this the whole table
-- is sorted on each page render and each rebuild.
CREATE INDEX IF NOT EXISTS idx_links_sort_date ON links(sort_date DESC);

CREATE TABLE IF NOT EXISTS views (
    path      TEXT NOT NULL,
    day       TEXT NOT NULL,
    ip_hash   TEXT NOT NULL DEFAULT '',
    ts        INTEGER NOT NULL,
    PRIMARY KEY (path, day, ip_hash)
);
-- The dashboard's 30-day breakdown filters and groups on day, which the
-- (path, day, ip_hash) primary key can't serve.
CREATE INDEX IF NOT EXISTS idx_views_day ON views(day);

-- Bearer credentials for the REST API and MCP server. Only the digest is
-- stored, so the database never holds a usable token.
CREATE TABLE IF NOT EXISTS api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_active ON api_tokens(revoked_at);

CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts USING fts5(
    slug, title, body, description, tags, tags_unindexed UNINDEXED,
    tokenize = 'porter unicode61'
);
`

func (s *Store) migrate() error {
	_, err := s.conn().Exec(schema)
	return err
}
