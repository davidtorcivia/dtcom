package build

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
	"davidtorcivia.com/dtcom/internal/siteconfig"
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

// render executes a named template to outPath.
//
// The output is buffered and written in one shot rather than streamed into an
// os.Create'd file: a template error partway through would otherwise leave a
// truncated page on disk, which the server would then happily serve.
func (t *templateStore) render(name, outPath string, data any) error {
	if t.tmpl == nil {
		return fmt.Errorf("templates not loaded")
	}
	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("execute %s: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, buf.Bytes(), 0o644)
}

// helperFuncs returns the template function map used across all templates.
func helperFuncs(fp *assets.Fingerprinter) template.FuncMap {
	return template.FuncMap{
		// asset appends a content hash to a /static/ URL so the file can be
		// cached hard and still update the moment it changes.
		"asset": fp.URL,
		// htmlSafe marks a string as pre-rendered HTML. Only ever applied to
		// values the author controls: the goldmark output for a post body and
		// the bio paragraphs from site.yml.
		"htmlSafe": func(s string) template.HTML { return template.HTML(s) },
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02")
		},
		// formatDateLong renders a human-facing date ("31 January 2026") for
		// the byline, where the ISO form reads like a filename.
		"formatDateLong": func(t time.Time) string {
			return t.Format("2 January 2006")
		},
		"formatDateUnix": func(u int64) string {
			return time.Unix(u, 0).UTC().Format("2006-01-02")
		},
		// rfc3339 renders a machine-readable date for <time datetime>.
		"rfc3339":    func(t time.Time) string { return t.Format(time.RFC3339) },
		"socialIcon": socialIconSVG,
		// contactHref finds the site's contact address in the social list
		// instead of hardcoding one in the footer, where it could (and did)
		// drift out of step with site.yml.
		"contactHref": func(site *siteconfig.Config) string {
			if site == nil {
				return ""
			}
			for _, s := range site.Social {
				if s.Icon == "email" || strings.HasPrefix(strings.ToLower(s.Href), "mailto:") {
					return s.Href
				}
			}
			return ""
		},
		// absURL turns a site-relative path into an absolute one for og: tags
		// and canonical links, which require it.
		"absURL": func(site *siteconfig.Config, path string) string {
			return strings.TrimRight(site.BaseURL, "/") + path
		},
		// ogImage returns the absolute URL of a post's social preview image,
		// or "" when the post has no cover.
		//
		// It used to fall back to /static/images/og-default.jpg, a file that
		// does not exist in this repo — every article advertised a broken
		// image to every link unfurler. Returning "" lets the template omit
		// the tag, and social platforms then fall back to their own defaults.
		"ogImage": func(a Article, site *siteconfig.Config) string {
			if a.Cover == "" {
				return ""
			}
			if strings.HasPrefix(a.Cover, "http://") || strings.HasPrefix(a.Cover, "https://") {
				return a.Cover
			}
			return strings.TrimRight(site.BaseURL, "/") + "/" + strings.TrimLeft(a.Cover, "/")
		},
		// readingTime estimates minutes to read a post body, at the ~200 wpm
		// figure typical for prose.
		"readingTime": func(body string) int {
			words := len(strings.Fields(body))
			if words == 0 {
				return 1
			}
			return max(1, (words+199)/200)
		},
	}
}

// socialIcons maps a site.yml social icon name to its inline SVG markup.
// The markup is lifted verbatim from the original index.html scaffolding so
// the rendered DOM matches what static/style.css expects (.social-icon).
var socialIcons = map[string]string{
	"x":         `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="currentColor"><path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>`,
	"instagram": `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.2"><rect x="2" y="2" width="20" height="20" rx="5" ry="5"/><path d="M16 11.37A4 4 0 1112.63 8 4 4 0 0116 11.37z"/><line x1="17.5" y1="6.5" x2="17.51" y2="6.5"/></svg>`,
	"github":    `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="currentColor"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>`,
	"substack":  `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="currentColor"><path d="M22.539 8.242H1.46V5.406h21.08v2.836zM1.46 10.812V24L12 18.11 22.54 24V10.812H1.46zM22.539 0H1.46v2.836h21.08V0z"/></svg>`,
	"email":     `<svg class="social-icon" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>`,
}

// socialIconSVG returns the inline SVG markup for a named social icon, or an
// empty HTML value if the name is unknown. Output is template.HTML so the SVG
// is injected raw (the markup is static and author-controlled, not user input).
func socialIconSVG(name string) template.HTML {
	if svg, ok := socialIcons[name]; ok {
		return template.HTML(svg)
	}
	return template.HTML("")
}
