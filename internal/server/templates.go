package server

import (
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
)

// adminTemplateStore parses templates/admin/*.html once at startup and renders
// named templates on demand.
type adminTemplateStore struct {
	tmpl *template.Template
}

func newAdminTemplates(dir string) (*adminTemplateStore, error) {
	tmpl, err := template.New("").ParseGlob(filepath.Join(dir, "*.html"))
	if err != nil {
		return nil, err
	}
	return &adminTemplateStore{tmpl: tmpl}, nil
}

// render executes the named template; on error it logs and writes a 500. We
// don't try to clear already-written bytes — admin templates either render
// fully or fail before the first write.
func (a *adminTemplateStore) render(w http.ResponseWriter, name string, data any) {
	if err := a.tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("admin template render", "name", name, "err", err)
		// Headers may already be flushed; only emit a textual error if not.
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}
