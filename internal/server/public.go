package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
)

// registerPublic wires the unauthenticated public surface: static assets,
// uploaded images, the search/track JSON endpoints, content-negotiated article
// routes, and the pre-rendered public/ files (home, links, search, feeds).
func registerPublic(mux *http.ServeMux, d *Deps) {
	// static assets (long cache)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(d.Cfg.StaticDir))))
	// uploaded images
	mux.Handle("GET /images/", http.StripPrefix("/images/", http.FileServer(http.Dir(d.Cfg.ImagesDir))))

	// unauthed dynamic endpoints
	mux.HandleFunc("GET /api/search", d.handleSearch)
	mux.HandleFunc("POST /api/track", d.handleTrack)

	// content-negotiated article routes. Go's ServeMux doesn't allow a literal
	// suffix after a {wildcard}, so both /posts/<slug> and /posts/<slug>.md are
	// dispatched from one handler that inspects the matched slug for a ".md"
	// suffix. An explicit "Accept: text/markdown" also selects the .md variant.
	mux.HandleFunc("GET /posts/{slug}", d.handleArticle)

	// pre-rendered public files (home, links, search, feed, sitemap, robots)
	mux.HandleFunc("GET /{$}", d.servePublicFile("index.html"))
	mux.HandleFunc("GET /links", d.servePublicFile("links/index.html"))
	mux.HandleFunc("GET /search", d.servePublicFile("search/index.html"))
	mux.HandleFunc("GET /feed.xml", d.servePublicFile("feed.xml"))
	mux.HandleFunc("GET /sitemap.xml", d.servePublicFile("sitemap.xml"))
	mux.HandleFunc("GET /robots.txt", d.servePublicFile("robots.txt"))
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(d.Cfg.StaticDir, "favicon.svg"))
	})
}

// servePublicFile returns a handler that serves a specific file from PublicDir.
func (d *Deps) servePublicFile(rel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(d.Cfg.PublicDir, rel))
	}
}

func (d *Deps) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	hits, err := d.Store.SearchArticles(q, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

// handleTrack records a page view from the client-side beacon. Bots and
// malformed bodies are silently dropped (204) — a tracking beacon must never
// surface a 500 to the browser.
func (d *Deps) handleTrack(w http.ResponseWriter, r *http.Request) {
	if isBot(r.UserAgent()) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = r.Body.Close()
	if body.Path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = d.Store.RecordView(body.Path, todayUTC(), hashIP(r.RemoteAddr))
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) handleArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	// A trailing ".md" selects the markdown source variant. We strip it before
	// resolving the file so the on-disk layout is posts/<slug>.md.
	if strings.HasSuffix(slug, ".md") {
		d.serveArticleMD(w, r, strings.TrimSuffix(slug, ".md"))
		return
	}
	// Content negotiation: if the client explicitly asks for markdown, serve
	// the .md source variant instead of the rendered HTML.
	if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
		d.serveArticleMD(w, r, slug)
		return
	}
	http.ServeFile(w, r, filepath.Join(d.Cfg.PublicDir, "posts", slug, "index.html"))
}

// serveArticleMD is the shared body for the .md route and the markdown branch
// of content negotiation.
func (d *Deps) serveArticleMD(w http.ResponseWriter, r *http.Request, slug string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	http.ServeFile(w, r, filepath.Join(d.Cfg.PublicDir, "posts", slug+".md"))
}
