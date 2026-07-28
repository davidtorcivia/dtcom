package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/store"
)

// registerAPI wires the bearer-token-authenticated REST API under /api/v1/.
// The public /api/search and /api/track endpoints (no auth) are registered
// separately in registerPublic.
func registerAPI(mux *http.ServeMux, d *Deps) {
	mux.Handle("/api/v1/", d.apiMiddleware(http.HandlerFunc(d.apiRouter)))
}

// apiMiddleware enforces the bearer token for every /api/v1/ request.
func (d *Deps) apiMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkBearer(r, d.Cfg.APIToken) {
			writeError(w, http.StatusUnauthorized, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiRouter dispatches a single /api/v1/ prefix by exact path + method. The
// prefix match on the wrapping mux.Handle guarantees we only see API traffic.
func (d *Deps) apiRouter(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	m := r.Method
	switch {
	// articles
	case p == "/api/v1/articles" && m == http.MethodGet:
		d.apiListArticles(w, r)
	case p == "/api/v1/articles" && m == http.MethodPost:
		d.apiCreateArticle(w, r)
	case strings.HasPrefix(p, "/api/v1/articles/") && m == http.MethodGet:
		d.apiGetArticle(w, r)
	case strings.HasPrefix(p, "/api/v1/articles/") && m == http.MethodPut:
		d.apiUpdateArticle(w, r)
	case strings.HasPrefix(p, "/api/v1/articles/") && m == http.MethodDelete:
		d.apiDeleteArticle(w, r)
	// links
	case p == "/api/v1/links" && m == http.MethodGet:
		d.apiListLinks(w, r)
	case p == "/api/v1/links" && m == http.MethodPost:
		d.apiAddLink(w, r)
	case strings.HasPrefix(p, "/api/v1/links/") && m == http.MethodDelete:
		d.apiDeleteLink(w, r)
	// site config
	case p == "/api/v1/site" && m == http.MethodGet:
		writeJSON(w, http.StatusOK, d.Site())
	case strings.HasPrefix(p, "/api/v1/site/") && m == http.MethodPut:
		d.apiUpdateSiteSection(w, r)
	// ops
	case p == "/api/v1/regenerate" && m == http.MethodPost:
		if err := d.Engine.Rebuild(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case p == "/api/v1/stats" && m == http.MethodGet:
		s, err := d.Store.Stats()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	case p == "/api/v1/feeds/refresh" && m == http.MethodPost:
		n, err := d.Poller.Poll(d.Site())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"imported": n})
	default:
		writeError(w, http.StatusNotFound, nil)
	}
}

// articleInput is the JSON shape for create/update article payloads. It's
// shared by the REST API and the MCP tools.
type articleInput struct {
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Date        string   `json:"date"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Body        string   `json:"body"`
	Draft       bool     `json:"draft"`
}

// linkInput is the JSON shape for add-link payloads.
type linkInput struct {
	Label    string `json:"label"`
	Href     string `json:"href"`
	Note     string `json:"note"`
	SortDate int64  `json:"sort_date"`
}

// ---------------------------------------------------------------------------
// Articles
// ---------------------------------------------------------------------------

type articleSummary struct {
	Slug, Title, Date string
	Draft             bool
}

func (d *Deps) apiListArticles(w http.ResponseWriter, r *http.Request) {
	arts, err := build.LoadArticles(filepath.Join(d.Cfg.ContentDir, "posts"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	res := make([]articleSummary, 0, len(arts))
	for _, a := range arts {
		res = append(res, articleSummary{
			Slug: a.Slug, Title: a.Title,
			Date: a.Date.Format("2006-01-02"), Draft: a.Draft,
		})
	}
	writeJSON(w, http.StatusOK, res)
}

func (d *Deps) apiGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/articles/")
	a, err := d.findArticleBySlug(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if a == nil {
		writeError(w, http.StatusNotFound, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":        a.Slug,
		"title":       a.Title,
		"date":        a.Date.Format("2006-01-02"),
		"description": a.Description,
		"tags":        a.Tags,
		"draft":       a.Draft,
		"body":        a.Body,
	})
}

func (d *Deps) apiCreateArticle(w http.ResponseWriter, r *http.Request) {
	var in articleInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	slug, status, err := d.createArticle(in)
	if err != nil {
		writeError(w, status, err)
		return
	}
	w.Header().Set("Location", "/api/v1/articles/"+slug)
	writeJSON(w, http.StatusCreated, map[string]string{"slug": slug})
}

func (d *Deps) apiUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/articles/")
	var in articleInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := d.updateArticle(slug, in)
	if err != nil {
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"slug": slug})
}

func (d *Deps) apiDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/articles/")
	status, err := d.deleteArticle(slug)
	if err != nil {
		writeError(w, status, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Articles — shared core logic (also used by MCP tools)
// ---------------------------------------------------------------------------

// findArticleBySlug loads all articles and returns the one whose slug matches.
// Returns (nil, nil) when no match — callers map that to 404.
func (d *Deps) findArticleBySlug(slug string) (*build.Article, error) {
	arts, err := build.LoadArticles(filepath.Join(d.Cfg.ContentDir, "posts"))
	if err != nil {
		return nil, err
	}
	for i := range arts {
		if arts[i].Slug == slug {
			return &arts[i], nil
		}
	}
	return nil, nil
}

// createArticle writes a new post file and rebuilds. Returns
// (slug, httpStatus, err) where status is 409 on a filename collision.
func (d *Deps) createArticle(in articleInput) (string, int, error) {
	slug := in.Slug
	if slug == "" {
		slug = slugify(in.Title)
	}
	if slug == "" {
		return "", http.StatusBadRequest, fmt.Errorf("could not derive slug (title empty?)")
	}
	date := in.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	path := filepath.Join(d.Cfg.ContentDir, "posts", date+"-"+slug+".md")
	if _, err := os.Stat(path); err == nil {
		return "", http.StatusConflict, fmt.Errorf("article %q already exists", slug)
	}
	if err := os.WriteFile(path, []byte(renderArticleFile(in, date, slug)), 0o644); err != nil {
		return "", http.StatusInternalServerError, err
	}
	if err := d.Engine.Rebuild(); err != nil {
		return "", http.StatusInternalServerError, err
	}
	return slug, http.StatusCreated, nil
}

// updateArticle overwrites an existing post file (matched by slug) and
// rebuilds. If in.Date is empty, the original date is preserved.
func (d *Deps) updateArticle(slug string, in articleInput) (int, error) {
	a, err := d.findArticleBySlug(slug)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if a == nil {
		return http.StatusNotFound, fmt.Errorf("article %q not found", slug)
	}
	date := in.Date
	if date == "" {
		date = a.Date.Format("2006-01-02")
	}
	if err := os.WriteFile(a.SourcePath, []byte(renderArticleFile(in, date, slug)), 0o644); err != nil {
		return http.StatusInternalServerError, err
	}
	if err := d.Engine.Rebuild(); err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusOK, nil
}

// deleteArticle removes the post file matching slug and rebuilds.
func (d *Deps) deleteArticle(slug string) (int, error) {
	a, err := d.findArticleBySlug(slug)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if a == nil {
		return http.StatusNotFound, fmt.Errorf("article %q not found", slug)
	}
	if err := os.Remove(a.SourcePath); err != nil {
		return http.StatusInternalServerError, err
	}
	if err := d.Engine.Rebuild(); err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusNoContent, nil
}

// ---------------------------------------------------------------------------
// Links
// ---------------------------------------------------------------------------

func (d *Deps) apiListLinks(w http.ResponseWriter, r *http.Request) {
	links, err := d.Store.ListLinks(500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (d *Deps) apiAddLink(w http.ResponseWriter, r *http.Request) {
	var in linkInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if in.Label == "" || in.Href == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("label and href required"))
		return
	}
	sd := in.SortDate
	if sd == 0 {
		sd = time.Now().Unix()
	}
	id, err := d.Store.AddLink(store.Link{
		Label: in.Label, Href: in.Href, Note: in.Note,
		Source: "manual", SortDate: sd,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (d *Deps) apiDeleteLink(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/links/")
	var id int64
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid id"))
		return
	}
	if err := d.Store.RemoveLink(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Site config
// ---------------------------------------------------------------------------

// apiUpdateSiteSection handles PUT /api/v1/site/{bio|nav|social|rss_feeds|footer_left}.
func (d *Deps) apiUpdateSiteSection(w http.ResponseWriter, r *http.Request) {
	section := strings.TrimPrefix(r.URL.Path, "/api/v1/site/")
	if err := d.updateSiteSection(section, r.Body); err != nil {
		writeError(w, httpToStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Article file rendering + slugify (shared with admin handlers and MCP)
// ---------------------------------------------------------------------------

// renderArticleFile assembles a YAML-frontmatter + markdown body for writing a
// post file. Every string is double-quoted with backslash/dquote escaping so a
// title containing a colon or quote can't corrupt the frontmatter.
func renderArticleFile(in articleInput, date, slug string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("title: " + yamlQuote(in.Title) + "\n")
	sb.WriteString("date: " + date + "\n")
	sb.WriteString("description: " + yamlQuote(in.Description) + "\n")
	fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(in.Tags, ", "))
	sb.WriteString("draft: " + boolStr(in.Draft) + "\n")
	sb.WriteString("---\n\n")
	sb.WriteString(in.Body)
	if !strings.HasSuffix(in.Body, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// slugify lowercases s, separates on spaces/underscores, and drops every
// character that isn't [a-z0-9-].
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var out []rune
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			out = append(out, r)
		}
	}
	return strings.Trim(string(out), "-")
}

// parseTags splits a comma-separated tag string into trimmed non-empty tags.
func parseTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
