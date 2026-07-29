package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// APIToken is a named bearer credential for the REST API and MCP server.
//
// The raw token is never stored — only its SHA-256 digest — so a copy of the
// database yields no usable credentials. That also means a token's value can
// be shown exactly once, at creation; after that only its prefix identifies it.
// SHA-256 rather than bcrypt is deliberate: these are 32 bytes of machine
// randomness, not human-chosen passwords, so there is nothing for a slow hash
// to defend against, and it is checked on every API request.
type APIToken struct {
	ID         int64
	Name       string
	Prefix     string // first 8 characters, for identification in the UI
	CreatedAt  int64
	LastUsedAt int64
	RevokedAt  int64
}

// Active reports whether the token may still authenticate.
func (t APIToken) Active() bool { return t.RevokedAt == 0 }

// ErrTokenNotFound means no active token matched.
var ErrTokenNotFound = errors.New("api token not found")

const maxTokenName = 60

// hashToken is the one-way function used for both storage and lookup.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CreateAPIToken mints a new token, stores its digest, and returns the raw
// value. The raw value is returned once and never recoverable afterwards.
func (s *Store) CreateAPIToken(name string) (string, *APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unnamed"
	}
	if len(name) > maxTokenName {
		name = name[:maxTokenName]
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	raw := hex.EncodeToString(buf)

	now := time.Now().Unix()
	res, err := s.conn().Exec(
		`INSERT INTO api_tokens(name, token_hash, prefix, created_at) VALUES(?,?,?,?)`,
		name, hashToken(raw), raw[:8], now,
	)
	if err != nil {
		return "", nil, fmt.Errorf("insert api token: %w", err)
	}
	id, _ := res.LastInsertId()
	return raw, &APIToken{ID: id, Name: name, Prefix: raw[:8], CreatedAt: now}, nil
}

// LookupAPIToken returns the active token matching raw, or ErrTokenNotFound.
func (s *Store) LookupAPIToken(raw string) (*APIToken, error) {
	if raw == "" {
		return nil, ErrTokenNotFound
	}
	var t APIToken
	err := s.conn().QueryRow(
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at
		 FROM api_tokens WHERE token_hash = ? AND revoked_at = 0`,
		hashToken(raw),
	).Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup api token: %w", err)
	}
	return &t, nil
}

// TouchAPIToken records that a token was just used, so the admin page can show
// which credentials are live and which are forgotten.
func (s *Store) TouchAPIToken(id int64) error {
	_, err := s.conn().Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// ListAPITokens returns every token, newest first, including revoked ones so
// the admin page can show what was withdrawn and when.
func (s *Store) ListAPITokens() ([]APIToken, error) {
	rows, err := s.conn().Query(
		`SELECT id, name, prefix, created_at, last_used_at, revoked_at
		 FROM api_tokens ORDER BY revoked_at = 0 DESC, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken withdraws a token, reporting whether one was actually
// revoked. Revoking is a soft delete: the row stays so the admin page can show
// that the credential existed and when it stopped working.
func (s *Store) RevokeAPIToken(id int64) (bool, error) {
	res, err := s.conn().Exec(
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at = 0`,
		time.Now().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("revoke api token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("revoke api token: %w", err)
	}
	return n > 0, nil
}

// CountActiveAPITokens reports how many tokens can currently authenticate.
func (s *Store) CountActiveAPITokens() (int, error) {
	var n int
	err := s.conn().QueryRow(`SELECT count(*) FROM api_tokens WHERE revoked_at = 0`).Scan(&n)
	return n, err
}
