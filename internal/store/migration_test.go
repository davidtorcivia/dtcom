package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestVerifyDatabaseMigratesOldBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE api_tokens (
		id INTEGER PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
		prefix TEXT NOT NULL, created_at INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0, revoked_at INTEGER NOT NULL DEFAULT 0)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyDatabase(path); err != nil {
		t.Fatal(err)
	}
	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var scopes string
	if err := db.QueryRow(`SELECT scopes FROM api_tokens LIMIT 1`).Scan(&scopes); err != sql.ErrNoRows {
		t.Fatalf("scopes query = %v, want no rows", err)
	}
	for _, table := range []string{"article_audit", "webmentions"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("%s was not migrated: %v", table, err)
		}
	}
}
