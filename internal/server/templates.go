package server

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
)

// adminTemplateStore parses templates/admin/*.html and re-parses them when
// the files change on disk, so the live-editing workflow documented for the
// compose bind mounts works for the admin UI too.
type adminTemplateStore struct {
	dir     string
	funcs   template.FuncMap
	loaded  atomic.Pointer[template.Template]
	mu      sync.Mutex // serializes reloads
	stamp   time.Time  // newest mtime at last parse
	checked int64      // unix nano of the last staleness stat
}

const adminTmplCheckInterval = 5 * time.Second

func newAdminTemplates(dir string, fp *assets.Fingerprinter) (*adminTemplateStore, error) {
	ts := &adminTemplateStore{dir: dir, funcs: adminTemplateFuncs(fp)}
	tmpl, stamp, err := ts.parse()
	if err != nil {
		return nil, err
	}
	ts.stamp = stamp
	ts.loaded.Store(tmpl)
	return ts, nil
}

func (ts *adminTemplateStore) parse() (*template.Template, time.Time, error) {
	tmpl, err := template.New("").Funcs(ts.funcs).ParseGlob(filepath.Join(ts.dir, "*.html"))
	if err != nil {
		return nil, time.Time{}, err
	}
	var stamp time.Time
	entries, err := os.ReadDir(ts.dir)
	if err != nil {
		return nil, time.Time{}, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(stamp) {
			stamp = info.ModTime()
		}
	}
	return tmpl, stamp, nil
}

// current returns the template set, reloading first if the files changed.
// The mtime stat is throttled to one per interval; the common steady state
// costs nothing.
func (ts *adminTemplateStore) current() *template.Template {
	now := time.Now().UnixNano()
	last := atomic.LoadInt64(&ts.checked)
	if now-last > int64(adminTmplCheckInterval) && atomic.CompareAndSwapInt64(&ts.checked, last, now) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		var stamp time.Time
		entries, err := os.ReadDir(ts.dir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if info, err := e.Info(); err == nil && info.ModTime().After(stamp) {
					stamp = info.ModTime()
				}
			}
		}
		if err == nil && stamp.After(ts.stamp) {
			tmpl, parsedStamp, err := ts.parse()
			if err != nil {
				// A template with a syntax error keeps the last good set
				// rather than taking the admin UI down.
				slog.Error("admin template reparse failed", "dir", ts.dir, "err", err)
			} else {
				ts.stamp = parsedStamp
				ts.loaded.Store(tmpl)
			}
		}
	}
	return ts.loaded.Load()
}

// adminTemplateFuncs are the helpers available to admin templates.
func adminTemplateFuncs(fp *assets.Fingerprinter) template.FuncMap {
	return template.FuncMap{
		"asset":          fp.URL,
		"join":           func(ss []string, sep string) string { return strings.Join(ss, sep) },
		"formatDate":     func(t time.Time) string { return t.Format("2006-01-02") },
		"formatDateUnix": func(u int64) string { return time.Unix(u, 0).UTC().Format("2006-01-02") },
		"add":            func(a, b int) int { return a + b },
		// lower lets a range label ("30 days") sit mid-sentence without a
		// second copy of the string in lower case.
		"lower": strings.ToLower,
		// halfOf labels the chart's midpoint gridline, rounded up so the
		// midpoint of a 1-view chart reads 1 rather than 0.
		"halfOf": func(n int64) int64 { return (n + 1) / 2 },
	}
}

// render executes the named template into a buffer, then writes it. Buffering
// means a template bug produces an honest 500 instead of a 200 carrying half
// a page.
func (ts *adminTemplateStore) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := ts.current().ExecuteTemplate(&buf, name, data); err != nil {
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
