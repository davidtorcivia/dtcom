package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ArticleAudit struct {
	ID             int64  `json:"id"`
	Actor          string `json:"actor"`
	Action         string `json:"action"`
	Slug           string `json:"slug"`
	SourceName     string `json:"source_name"`
	BeforeRevision string `json:"before_revision"`
	AfterRevision  string `json:"after_revision"`
	BeforeSource   []byte `json:"-"`
	AfterSource    []byte `json:"-"`
	CreatedAt      int64  `json:"created_at"`
}

func (s *Store) RecordArticleAudit(a ArticleAudit) error {
	if a.BeforeSource == nil {
		a.BeforeSource = []byte{}
	}
	if a.AfterSource == nil {
		a.AfterSource = []byte{}
	}
	_, err := s.conn().Exec(`INSERT INTO article_audit
		(actor, action, slug, source_name, before_revision, after_revision, before_source, after_source, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, a.Actor, a.Action, a.Slug, a.SourceName, a.BeforeRevision,
		a.AfterRevision, a.BeforeSource, a.AfterSource, time.Now().Unix())
	return err
}

func (s *Store) ListArticleAudit(limit int) ([]ArticleAudit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.conn().Query(`SELECT id, actor, action, slug, source_name,
		before_revision, after_revision, created_at FROM article_audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArticleAudit
	for rows.Next() {
		var a ArticleAudit
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Slug, &a.SourceName,
			&a.BeforeRevision, &a.AfterRevision, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ArticleAudit(id int64) (*ArticleAudit, error) {
	var a ArticleAudit
	err := s.conn().QueryRow(`SELECT id, actor, action, slug, source_name, before_revision,
		after_revision, before_source, after_source, created_at FROM article_audit WHERE id=?`, id).
		Scan(&a.ID, &a.Actor, &a.Action, &a.Slug, &a.SourceName, &a.BeforeRevision,
			&a.AfterRevision, &a.BeforeSource, &a.AfterSource, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("article audit: %w", err)
	}
	return &a, nil
}
