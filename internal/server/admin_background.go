package server

import (
	"net/http"
	"strings"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

func (d *Deps) adminBackground(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	d.adminTmpls.render(w, "background", d.adminData("Background", map[string]any{"Background": d.Site().Background.JSON()}))
}
func (d *Deps) adminBackgroundSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var b siteconfig.Background
	if err := decodeJSONReader(r.Body, &b); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := siteconfig.ValidateBackground(&b); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := d.mutateSite(func(s *siteconfig.Config) error { s.Background = &b; return nil }); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}

// Only this authenticated preview can be framed, and only by this origin.
func (d *Deps) adminBackgroundPreview(w http.ResponseWriter, r *http.Request) {
	var body []byte
	var err error
	if slug := r.URL.Query().Get("article"); slug != "" {
		a, e := d.findArticleBySlug(slug)
		if e != nil || a == nil {
			http.NotFound(w, r)
			return
		}
		body, err = d.Engine.Preview(*a)
	} else {
		body, err = d.Engine.PreviewHome()
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	w.Header().Set("Content-Security-Policy", strings.Replace(contentSecurityPolicy, "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body)
}
