package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"davidtorcivia.com/dtcom/internal/store"
)

// maxSearchQuery caps the length of a search term. FTS5 tokenizes the whole
// string, so an unbounded query on an unauthenticated endpoint is free work.
const maxSearchQuery = 128

// maxTrackPath caps the length of a beacon path before it is validated against
// the set of pages that actually exist.
const maxTrackPath = 200

// registerPublic wires the unauthenticated public surface: static assets,
// uploaded images, the search/track JSON endpoints, content-negotiated article
// routes, and the pre-rendered public/ files (home, links, search, feeds).
func registerPublic(mux *http.ServeMux, d *Deps) {
	// static assets — cached for a week with revalidation
	mux.Handle("GET /static/", cacheControl(staticCacheControl,
		http.StripPrefix("/static/", http.FileServer(http.Dir(d.Cfg.StaticDir)))))
	// uploaded images — content-addressed names, so cache them indefinitely
	mux.Handle("GET /images/", cacheControl(imageCacheControl,
		http.StripPrefix("/images/", http.FileServer(http.Dir(d.Cfg.ImagesDir)))))

	// unauthed dynamic endpoints
	mux.HandleFunc("GET /api/search", d.handleSearch)
	mux.HandleFunc("POST /api/track", d.handleTrack)

	// liveness/readiness probe for the reverse proxy or orchestrator. Cheap
	// and dependency-free on purpose: it answers "is this process serving?",
	// not "is every subsystem healthy", so a flapping RSS feed can't take the
	// site out of rotation.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// content-negotiated article routes. Go's ServeMux doesn't allow a literal
	// suffix after a {wildcard}, so both /posts/<slug> and /posts/<slug>.md are
	// dispatched from one handler that inspects the matched slug for a ".md"
	// suffix. An explicit "Accept: text/markdown" also selects the .md variant.
	mux.HandleFunc("GET /posts/{slug}", d.handleArticle)

	// pre-rendered public files (home, links, search, feed, sitemap, robots)
	mux.HandleFunc("GET /{$}", d.servePublicFile("index.html"))
	mux.HandleFunc("GET /links", d.servePublicFile("links/index.html"))
	mux.HandleFunc("GET /links/{$}", d.servePublicFile("links/index.html"))
	mux.HandleFunc("GET /search", d.servePublicFile("search/index.html"))
	mux.HandleFunc("GET /search/{$}", d.servePublicFile("search/index.html"))
	mux.HandleFunc("GET /feed.xml", d.servePublicFile("feed.xml"))
	mux.HandleFunc("GET /sitemap.xml", d.servePublicFile("sitemap.xml"))
	mux.HandleFunc("GET /robots.txt", d.servePublicFile("robots.txt"))
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", staticCacheControl)
		http.ServeFile(w, r, filepath.Join(d.Cfg.StaticDir, "favicon.svg"))
	})
	// Browsers request /favicon.ico unprompted; point them at the SVG rather
	// than letting it fall through to a 404 page on every visit.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/favicon.svg", http.StatusMovedPermanently)
	})

	// Catch-all: anything not matched above gets the rendered 404 page rather
	// than net/http's bare "404 page not found" text.
	mux.HandleFunc("/", d.handleNotFound)
}

// servePublicFile returns a handler that serves a specific file from PublicDir.
// A missing file means the build hasn't produced that page, which is a 404 for
// the visitor rather than a server error.
func (d *Deps) servePublicFile(rel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(d.Cfg.PublicDir, rel)
		if !fileExists(path) {
			d.handleNotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", pageCacheControl)
		http.ServeFile(w, r, path)
	}
}

// handleNotFound serves the rendered 404 page, falling back to plain text if
// the build hasn't produced one yet.
func (d *Deps) handleNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	page := filepath.Join(d.Cfg.PublicDir, "404.html")
	body, err := os.ReadFile(page)
	if err != nil {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(body)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (d *Deps) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !d.limits.search.Allow(d.clientIP(r)) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, nil)
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > maxSearchQuery {
		q = q[:maxSearchQuery]
	}
	hits, err := d.Store.SearchArticles(q, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if hits == nil {
		hits = []store.SearchHit{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, hits)
}

// handleTrack records a page view from the client-side beacon. Bots and
// malformed bodies are silently dropped (204) — a tracking beacon must never
// surface a 500 to the browser. The body is capped at 1 KB (the payload is
// just {"path":"/posts/x"}) so this unauthenticated endpoint can't be used to
// OOM the server by streaming an unbounded body.
func (d *Deps) handleTrack(w http.ResponseWriter, r *http.Request) {
	// Every exit path is 204: the beacon is fire-and-forget and the browser
	// does nothing with the response, so there is no reason to distinguish
	// "dropped" from "recorded" to a caller that might be probing.
	defer w.WriteHeader(http.StatusNoContent)

	// The author reading their own site is not a view worth counting, and it
	// is the single most frequent way these numbers get inflated — every
	// proofread after a publish used to register as a hit.
	//
	// This works because the beacon is a same-origin sendBeacon, which carries
	// cookies, so an admin session is visible here. It is checked before the
	// rate limiter so that browsing the site while logged in cannot use up the
	// budget that real visitors from the same address share.
	if _, ok := d.Auth.SessionUser(r); ok {
		return
	}
	if !d.limits.track.Allow(d.clientIP(r)) {
		return
	}
	if isBot(r.UserAgent()) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024) // 1 KB; beacon bodies are tiny
	defer r.Body.Close()
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return
	}
	if !d.trackablePath(body.Path) {
		return
	}
	_ = d.Store.RecordView(body.Path, todayUTC(), d.hashIP(d.clientIP(r)))
}

// trackablePath reports whether a beacon path names a page this site actually
// serves. The beacon is unauthenticated, so without this check any client
// could insert unbounded distinct rows into the views table — filling the disk
// and drowning the stats page in junk paths.
func (d *Deps) trackablePath(p string) bool {
	if p == "" || len(p) > maxTrackPath || !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	switch p {
	case "/", "/links", "/search":
		return true
	}
	slug, ok := strings.CutPrefix(p, "/posts/")
	if !ok || !validSlug(slug) {
		return false
	}
	return fileExists(filepath.Join(d.Cfg.PublicDir, "posts", slug, "index.html"))
}

func (d *Deps) handleArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	// A trailing ".md" selects the markdown source variant. We strip it before
	// resolving the file so the on-disk layout is posts/<slug>.md.
	if md, ok := strings.CutSuffix(slug, ".md"); ok {
		d.serveArticleMD(w, r, md)
		return
	}
	// Content negotiation: if the client explicitly asks for markdown, serve
	// the .md source variant instead of the rendered HTML.
	if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
		d.serveArticleMD(w, r, slug)
		return
	}
	if !validSlug(slug) {
		d.handleNotFound(w, r)
		return
	}
	path := filepath.Join(d.Cfg.PublicDir, "posts", slug, "index.html")
	if !fileExists(path) {
		d.handleNotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", pageCacheControl)
	http.ServeFile(w, r, path)
}

// serveArticleMD is the shared body for the .md route and the markdown branch
// of content negotiation.
func (d *Deps) serveArticleMD(w http.ResponseWriter, r *http.Request, slug string) {
	if !validSlug(slug) {
		d.handleNotFound(w, r)
		return
	}
	path := filepath.Join(d.Cfg.PublicDir, "posts", slug+".md")
	if !fileExists(path) {
		d.handleNotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", pageCacheControl)
	http.ServeFile(w, r, path)
}
