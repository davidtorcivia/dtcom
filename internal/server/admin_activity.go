package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"davidtorcivia.com/dtcom/internal/store"
)

func (d *Deps) adminActivity(w http.ResponseWriter, _ *http.Request) {
	d.renderActivity(w, "")
}

func (d *Deps) renderActivity(w http.ResponseWriter, message string) {
	items, err := d.Store.ListArticleAudit(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	data := map[string]any{"Items": items}
	if message != "" {
		data["Error"] = message
	}
	d.adminTmpls.render(w, "activity", d.adminData("Activity", data))
}

func (d *Deps) adminAuditUndo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		d.renderActivity(w, "Invalid activity entry.")
		return
	}
	audit, err := d.Store.ArticleAudit(id)
	if err != nil || audit == nil {
		d.renderActivity(w, "Activity entry not found.")
		return
	}
	if err := d.undoArticleAudit(*audit); err != nil {
		d.renderActivity(w, err.Error())
		return
	}
	http.Redirect(w, r, "/admin/activity", http.StatusSeeOther)
}

func (d *Deps) undoArticleAudit(a store.ArticleAudit) error {
	d.postMu.Lock()
	defer d.postMu.Unlock()
	current, err := d.findArticleBySlug(a.Slug)
	if err != nil {
		return err
	}
	if a.AfterRevision == "" {
		if current != nil {
			return fmt.Errorf("%s changed after this activity; undo refused", a.Slug)
		}
	} else if current == nil || current.Revision != a.AfterRevision {
		return fmt.Errorf("%s changed after this activity; undo refused", a.Slug)
	}
	if filepath.Base(a.SourceName) != a.SourceName || !strings.HasSuffix(a.SourceName, ".md") {
		return fmt.Errorf("invalid audit source name")
	}
	var currentRaw []byte
	currentPath := ""
	if current != nil {
		currentPath = current.SourcePath
		currentRaw, err = os.ReadFile(currentPath)
		if err != nil {
			return err
		}
		if err := os.Remove(currentPath); err != nil {
			return err
		}
	}
	target := filepath.Join(d.postsDir(), a.SourceName)
	if len(a.BeforeSource) > 0 {
		if err := writeFileAtomic(target, a.BeforeSource); err != nil {
			if currentPath != "" {
				_ = writeFileAtomic(currentPath, currentRaw)
			}
			return err
		}
	}
	if err := d.Engine.Rebuild(); err != nil {
		_ = os.Remove(target)
		if currentPath != "" {
			_ = writeFileAtomic(currentPath, currentRaw)
		}
		return err
	}
	d.recordArticleAudit(store.ArticleAudit{Actor: "admin", Action: "undo " + a.Action, Slug: a.Slug,
		SourceName: a.SourceName, BeforeRevision: a.AfterRevision, AfterRevision: a.BeforeRevision,
		BeforeSource: currentRaw, AfterSource: a.BeforeSource})
	return nil
}
