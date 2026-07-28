package build

import (
	"html/template"
	"os"
	"path/filepath"
	"time"
)

type templateStore struct {
	tmpl *template.Template
}

// Load parses all *.html templates from dir, applying the given helper funcs.
func (t *templateStore) Load(templatesDir string, funcs template.FuncMap) error {
	tmpl, err := template.New("").Funcs(funcs).ParseGlob(filepath.Join(templatesDir, "*.html"))
	if err != nil {
		return err
	}
	t.tmpl = tmpl
	return nil
}

func (t *templateStore) render(name, outPath string, data any) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.tmpl.ExecuteTemplate(f, name, data)
}

// helperFuncs returns the template function map used across all templates.
// (Task 7.1 adds more funcs like socialIcon, formatDate, ogImage; here we
// only define htmlSafe which the article template uses.)
func helperFuncs() template.FuncMap {
	return template.FuncMap{
		"htmlSafe":   func(s string) template.HTML { return template.HTML(s) },
		"formatDate": func(t time.Time) string { return t.Format("2006-01-02") },
	}
}
