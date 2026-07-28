package server

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/markdown"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// registerAdmin wires the session-cookie-authenticated admin UI. Every route
// other than the login form/login POST is wrapped in requireAuth.
func registerAdmin(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("GET /admin/login", d.adminLoginForm)
	mux.HandleFunc("POST /admin/login", d.adminLogin)
	mux.HandleFunc("POST /admin/logout", d.requireAuth(d.adminLogout))
	mux.HandleFunc("GET /admin", d.requireAuth(d.adminDashboard))
	mux.HandleFunc("GET /admin/posts", d.requireAuth(d.adminPostsList))
	mux.HandleFunc("GET /admin/posts/new", d.requireAuth(d.adminPostEdit))
	mux.HandleFunc("GET /admin/posts/{slug}/edit", d.requireAuth(d.adminPostEdit))
	mux.HandleFunc("POST /admin/posts/save", d.requireAuth(d.adminPostSave))
	mux.HandleFunc("POST /admin/posts/preview", d.requireAuth(d.adminPostPreview))
	mux.HandleFunc("POST /admin/posts/{slug}/delete", d.requireAuth(d.adminPostDelete))
	mux.HandleFunc("GET /admin/links", d.requireAuth(d.adminLinksList))
	mux.HandleFunc("POST /admin/links/add", d.requireAuth(d.adminLinkAdd))
	mux.HandleFunc("POST /admin/links/{id}/delete", d.requireAuth(d.adminLinkDelete))
	mux.HandleFunc("GET /admin/site", d.requireAuth(d.adminSiteEdit))
	mux.HandleFunc("POST /admin/site", d.requireAuth(d.adminSiteSave))
	mux.HandleFunc("POST /admin/regenerate", d.requireAuth(d.adminRegenerate))
}

// requireAuth redirects unauthenticated requests to the login page. Authenticated
// requests fall through to the wrapped handler.
func (d *Deps) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.Auth.SessionUser(r); !ok {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// adminData is the base map every admin page renders with. Every page must
// carry a Title (consumed by the admin-header partial) plus its own fields.
func adminData(title string, extra map[string]any) map[string]any {
	out := map[string]any{"Title": title}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (d *Deps) adminLoginForm(w http.ResponseWriter, r *http.Request) {
	if d.adminTmpls == nil {
		http.Error(w, "admin templates not configured", http.StatusInternalServerError)
		return
	}
	d.adminTmpls.render(w, "login", adminData("Login", nil))
}

func (d *Deps) adminLogin(w http.ResponseWriter, r *http.Request) {
	if d.adminTmpls == nil {
		http.Error(w, "admin templates not configured", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pw := r.FormValue("password")
	code := r.FormValue("totp")
	if !d.Auth.CheckPasswordAndTOTP(pw, code) {
		w.WriteHeader(http.StatusUnauthorized)
		d.adminTmpls.render(w, "login", adminData("Login", map[string]any{"Error": "Invalid credentials"}))
		return
	}
	if err := d.Auth.SetSession(w, "admin"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (d *Deps) adminLogout(w http.ResponseWriter, r *http.Request) {
	d.Auth.ClearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (d *Deps) adminDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := d.Store.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	d.adminTmpls.render(w, "dashboard", adminData("Dashboard", map[string]any{
		"Stats": stats,
		"Site":  d.Site(),
	}))
}

func (d *Deps) adminPostsList(w http.ResponseWriter, r *http.Request) {
	arts, err := build.LoadArticles(filepath.Join(d.Cfg.ContentDir, "posts"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	d.adminTmpls.render(w, "posts-list", adminData("Posts", map[string]any{"Articles": arts}))
}

func (d *Deps) adminPostEdit(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	data := adminData("Edit Post", nil)
	if slug != "" {
		a, err := d.findArticleBySlug(slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if a == nil {
			writeError(w, http.StatusNotFound, nil)
			return
		}
		data["Article"] = *a
	}
	d.adminTmpls.render(w, "post-edit", data)
}

// adminPostSave handles both create and update. The form posts the existing
// slug (hidden field) when editing; an empty value means a new post, in which
// case we derive the slug from the title and the file is created.
func (d *Deps) adminPostSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	origSlug := r.FormValue("slug")
	title := r.FormValue("title")
	date := r.FormValue("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	in := articleInput{
		Title:       title,
		Date:        date,
		Description: r.FormValue("description"),
		Tags:        parseTags(r.FormValue("tags")),
		Body:        r.FormValue("body"),
		Draft:       r.FormValue("draft") == "on",
	}

	// Editing an existing post: reuse its slug and overwrite its source file.
	if origSlug != "" {
		if status, err := d.updateArticle(origSlug, in); err != nil {
			writeError(w, status, err)
			return
		}
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
		return
	}

	// New post: slugify the title and create.
	slug, status, err := d.createArticle(in)
	if err != nil {
		writeError(w, status, err)
		return
	}
	_ = slug
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

// adminPostPreview renders the posted markdown body to HTML for the editor's
// "Preview" tab. It does not touch disk.
func (d *Deps) adminPostPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	html, err := markdown.Render(r.FormValue("body"))
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (d *Deps) adminPostDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if _, err := d.deleteArticle(slug); err != nil {
		// Non-fatal in the admin UI: log and still bounce back to the list so the
		// user isn't stuck on an error page.
		slog.Error("admin post delete", "slug", slug, "err", err)
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (d *Deps) adminLinksList(w http.ResponseWriter, r *http.Request) {
	links, err := d.Store.ListLinks(500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	d.adminTmpls.render(w, "links-list", adminData("Links", map[string]any{"Links": links}))
}

func (d *Deps) adminLinkAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	label := r.FormValue("label")
	href := r.FormValue("href")
	if label == "" || href == "" {
		http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
		return
	}
	if _, err := d.Store.AddLink(store.Link{
		Label: label, Href: href, Source: "manual", SortDate: time.Now().Unix(),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

func (d *Deps) adminLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id > 0 {
		if err := d.Store.RemoveLink(id); err != nil {
			slog.Error("admin link delete", "id", id, "err", err)
		}
	}
	_ = d.Engine.Rebuild()
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

func (d *Deps) adminSiteEdit(w http.ResponseWriter, r *http.Request) {
	d.adminTmpls.render(w, "site-edit", adminData("Site Config", map[string]any{"Site": d.Site()}))
}

// adminSiteSave edits only the scalar/textarea fields exposed in the site form
// (title, description, bio). The richer sections (nav/social/rss_feeds) are
// managed via the REST API; mutating them through a freeform textarea would be
// error-prone.
func (d *Deps) adminSiteSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	site := d.Site()
	site.Title = r.FormValue("title")
	site.Description = r.FormValue("description")
	site.Bio = splitLines(r.FormValue("bio"))
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/site", http.StatusSeeOther)
}

func (d *Deps) adminRegenerate(w http.ResponseWriter, r *http.Request) {
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// splitLines breaks a textarea blob into trimmed non-empty lines. Used for the
// bio field, which the front page renders one paragraph per line.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
