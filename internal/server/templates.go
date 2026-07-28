package server

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
)

// adminTemplateStore parses templates/admin/*.html once at startup and renders
// named templates on demand.
type adminTemplateStore struct {
	tmpl *template.Template
}

func newAdminTemplates(dir string, fp *assets.Fingerprinter) (*adminTemplateStore, error) {
	tmpl, err := template.New("").Funcs(adminTemplateFuncs(fp)).ParseGlob(filepath.Join(dir, "*.html"))
	if err != nil {
		return nil, err
	}
	return &adminTemplateStore{tmpl: tmpl}, nil
}

// adminTemplateFuncs are the helpers available to admin templates. Kept
// minimal: join (for tag lists), formatDate (for the date input value), asset
// (content-hashed /static URLs, same as the public templates), and add — which
// exists because Go templates have no arithmetic, and the nav/social reorder
// buttons need to know whether a row is the last one.
func adminTemplateFuncs(fp *assets.Fingerprinter) template.FuncMap {
	return template.FuncMap{
		"asset":          fp.URL,
		"join":           func(ss []string, sep string) string { return strings.Join(ss, sep) },
		"formatDate":     func(t time.Time) string { return t.Format("2006-01-02") },
		"formatDateUnix": func(u int64) string { return time.Unix(u, 0).UTC().Format("2006-01-02") },
		"add":            func(a, b int) int { return a + b },
	}
}

// render executes the named template into a buffer, then writes it.
//
// Buffering matters: html/template streams output and stops at the point of
// failure, so writing directly to the ResponseWriter turns a template bug into
// a 200 carrying half a page — which is exactly how one slipped through here
// (a `slice` past the end of a short string truncated the whole admin page
// while still reporting success). Rendering first means a broken template
// produces an honest 500 and nothing else.
func (a *adminTemplateStore) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("admin template render", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Debug("write admin page", "name", name, "err", err)
	}
}
