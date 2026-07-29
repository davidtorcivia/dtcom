package store

// Snapshot and restore of the database file itself.
//
// The database is one of the two things on this machine that cannot be
// reconstructed from anything else — view counts, links, and the API tokens'
// digests. (The search index in it can be rebuilt from the posts, and is, on
// every rebuild.)

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Snapshot writes a consistent copy of the database to path.
//
// VACUUM INTO, not a file copy. Copying dtcom.db while the process is running
// takes the main file without the write-ahead log beside it, which is a
// database missing its most recent commits — and, if a checkpoint happens
// mid-copy, one that never existed in that form at all. VACUUM INTO is
// performed by SQLite itself against a consistent read of the whole database,
// and it compacts as it goes.
//
// The destination must not exist; SQLite refuses to overwrite.
func (s *Store) Snapshot(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	// The path is interpolated as a SQL string literal because VACUUM INTO
	// takes a literal, not a bound parameter. Doubling any quote is the whole
	// escaping rule for SQLite string literals, and the caller's path is
	// server-side — never anything a request supplied.
	quoted := "'" + escapeSQLString(path) + "'"
	if _, err := s.conn().Exec("VACUUM INTO " + quoted); err != nil {
		return fmt.Errorf("vacuum into %s: %w", path, err)
	}
	return nil
}

// ContentFingerprint summarises the parts of the database a person changed, so
// a backup can tell whether taking another copy would achieve anything.
//
// Deliberately not a digest of the file. View counts tick up whenever anybody
// reads the site, so a whole-file hash is different every few minutes and would
// mean every scheduled backup writes another copy of an unchanged site to
// justify a handful of incremented counters. The counters still travel in every
// archive that is taken; they are just not a reason to take one.
//
// Links and tokens do count: a link added by hand or imported from a feed is
// authored state that exists nowhere else. The search index is left out because
// it is derived from the posts, which are fingerprinted from disk.
func (s *Store) ContentFingerprint() (string, error) {
	h := sha256.New()
	rows, err := s.conn().Query(`
        SELECT label, href, note, source, sort_date, feed_url, created_at
        FROM links ORDER BY id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var label, href, note, source, feedURL string
		var sortDate, createdAt int64
		if err := rows.Scan(&label, &href, &note, &source, &sortDate, &feedURL, &createdAt); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "link\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%d\n",
			label, href, note, source, sortDate, feedURL, createdAt)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	tokens, err := s.conn().Query(`SELECT name, token_hash, created_at, revoked_at FROM api_tokens ORDER BY id`)
	if err != nil {
		return "", err
	}
	defer tokens.Close()
	for tokens.Next() {
		var name, hash string
		var created, revoked int64
		if err := tokens.Scan(&name, &hash, &created, &revoked); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "token\x00%s\x00%s\x00%d\x00%d\n", name, hash, created, revoked)
	}
	if err := tokens.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func escapeSQLString(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\'' {
			out = append(out, '\'')
		}
		out = append(out, r)
	}
	return string(out)
}

// ReplaceWith swaps the live database for the file at path.
//
// The pool is closed first, which is what makes this safe: an open SQLite
// connection holds the old file by descriptor, so replacing the file underneath
// it would leave the process writing to a database nobody can see and reading
// one that no longer exists. Close waits for queries already in flight; queries
// that arrive during the swap fail rather than being served wrong, and the
// window is a file rename wide.
//
// The write-ahead log and shared-memory files are removed along with the old
// database. They belong to it, and leaving them beside a different database is
// how SQLite is handed a log of changes to pages that are not there.
func (s *Store) ReplaceWith(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("replacement database: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close before replace: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(s.path + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", s.path+suffix, err)
		}
	}
	if err := os.Rename(path, s.path); err != nil {
		// A rename across filesystems fails; fall back to a copy so a temp
		// directory on another mount still works.
		if err := copyFile(path, s.path); err != nil {
			return fmt.Errorf("install replacement database: %w", err)
		}
		_ = os.Remove(path)
	}

	db, err := openDB(s.path)
	if err != nil {
		return fmt.Errorf("reopen after replace: %w", err)
	}
	s.db = db
	// Migrations run against whatever came out of the archive, so a backup
	// taken before a schema change still opens.
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate after replace: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
