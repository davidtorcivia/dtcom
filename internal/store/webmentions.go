package store

import (
	"database/sql"
	"errors"
	"time"
)

type Webmention struct {
	ID         int64  `json:"id"`
	Direction  string `json:"direction"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Status     string `json:"status"`
	Title      string `json:"title"`
	Error      string `json:"error"`
	CreatedAt  int64  `json:"created_at"`
	VerifiedAt int64  `json:"verified_at"`
}

func (s *Store) QueueWebmention(source, target string) (int64, error) {
	now := time.Now().Unix()
	_, err := s.conn().Exec(`INSERT INTO webmentions(direction,source,target,status,created_at)
		VALUES('incoming',?,?,'pending',?) ON CONFLICT(direction,source,target)
		DO UPDATE SET status='pending', error='', created_at=excluded.created_at`, source, target, now)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.conn().QueryRow(`SELECT id FROM webmentions WHERE direction='incoming' AND source=? AND target=?`, source, target).Scan(&id)
	return id, err
}

func (s *Store) VerifyWebmention(id int64, status, title, message string) error {
	_, err := s.conn().Exec(`UPDATE webmentions SET status=?, title=?, error=?, verified_at=? WHERE id=?`,
		status, title, message, time.Now().Unix(), id)
	return err
}

func (s *Store) ModerateWebmention(id int64, status string) (bool, error) {
	res, err := s.conn().Exec(`UPDATE webmentions SET status=? WHERE id=? AND direction='incoming' AND status IN ('verified','approved','rejected')`, status, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) ListWebmentions(limit int) ([]Webmention, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.conn().Query(`SELECT id,direction,source,target,status,title,error,created_at,verified_at
		FROM webmentions ORDER BY direction='incoming' DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Webmention
	for rows.Next() {
		var m Webmention
		if err := rows.Scan(&m.ID, &m.Direction, &m.Source, &m.Target, &m.Status, &m.Title, &m.Error, &m.CreatedAt, &m.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) OutgoingWebmention(source, target, status, message string) error {
	_, err := s.conn().Exec(`INSERT INTO webmentions(direction,source,target,status,error,created_at,verified_at)
		VALUES('outgoing',?,?,?,?,?,?) ON CONFLICT(direction,source,target) DO UPDATE SET
		status=excluded.status, error=excluded.error, verified_at=excluded.verified_at`,
		source, target, status, message, time.Now().Unix(), time.Now().Unix())
	return err
}

func (s *Store) HasOutgoingWebmention(source, target string) (bool, error) {
	var status string
	err := s.conn().QueryRow(`SELECT status FROM webmentions WHERE direction='outgoing' AND source=? AND target=?`, source, target).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && status != "failed", err
}
