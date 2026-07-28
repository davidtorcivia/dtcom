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

// maxAdminForm caps an admin form POST. Article bodies are the largest thing
// posted here; 10 MB is far beyond any hand-written essay and keeps
// ParseForm from buffering an unbounded body.
const maxAdminForm = 10 << 20

// registerAdmin wires the session-cookie-authenticated admin UI. Every route
// other than the login form/login POST is wrapped in requireAuth.
func registerAdmin(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("GET /admin/login", d.adminLoginForm)
	mux.HandleFunc("POST /admin/login", d.adminLogin)
	mux.HandleFunc("POST /admin/logout", d.requireAuth(d.adminLogout))
	mux.HandleFunc("GET /admin", d.requireAuth(d.adminDashboard))
	mux.HandleFunc("GET /admin/{$}", d.requireAuth(d.adminDashboard))
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
	mux.HandleFunc("POST /admin/images", d.requireAuth(d.adminImageUpload))
	mux.HandleFunc("GET /admin/integrations", d.requireAuth(d.adminIntegrations))
	mux.HandleFunc("POST /admin/tokens", d.requireAuth(d.adminTokenCreate))
	mux.HandleFunc("POST /admin/tokens/{id}/revoke", d.requireAuth(d.adminTokenRevoke))
	mux.HandleFunc("POST /admin/regenerate", d.requireAuth(d.adminRegenerate))
	registerAdminFeeds(mux, d)
}

// requireAuth redirects unauthenticated requests to the login page and rejects
// cross-site state-changing requests. Authenticated same-origin requests fall
// through to the wrapped handler.
func (d *Deps) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.Auth.SessionUser(r); !ok {
			// no-store so a browser never serves a cached admin page to the
			// next person at the machine.
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r, d.baseURL()) {
			writeError(w, http.StatusForbidden, nil)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

func (d *Deps) baseURL() string {
	if d.Cfg == nil {
		return ""
	}
	return d.Cfg.BaseURL
}

// adminReady reports whether the admin templates loaded. Without them the
// admin UI cannot render at all, which is a server-configuration problem
// (503), not a client error.
func (d *Deps) adminReady(w http.ResponseWriter) bool {
	if d.adminTmpls == nil {
		writeError(w, http.StatusServiceUnavailable, nil)
		return false
	}
	return true
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

// parseAdminForm bounds and parses an admin form POST, handling both encodings
// the admin UI actually sends.
//
// This has to branch on the content type. r.ParseForm reads the body only for
// application/x-www-form-urlencoded; for a multipart body it succeeds while
// leaving the values unparsed, and — because it sets r.Form — it also stops
// the later r.FormValue from parsing them itself. The result was that the
// editor's Preview, which posts a FormData, silently rendered an empty body.
func parseAdminForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminForm)
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(4 << 20)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func (d *Deps) adminLoginForm(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	d.adminTmpls.render(w, "login", adminData("Login", nil))
}

func (d *Deps) adminLogin(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r, d.baseURL()) {
		writeError(w, http.StatusForbidden, nil)
		return
	}
	// Throttle before doing any bcrypt work: the hash comparison is the
	// expensive half of this handler and the reason an unthrottled login is a
	// CPU-exhaustion lever as well as a brute-force one.
	if !d.limits.login.Allow(d.clientIP(r)) || !d.limits.loginGlobal.Allow("global") {
		slog.Warn("login rate limited", "ip", d.clientIP(r))
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		d.adminTmpls.render(w, "login", adminData("Login", map[string]any{
			"Error": "Too many attempts. Wait a moment and try again.",
		}))
		return
	}
	if !parseAdminForm(w, r) {
		return
	}
	if !d.Auth.CheckPasswordAndTOTP(r.FormValue("password"), r.FormValue("totp")) {
		slog.Warn("failed admin login", "ip", d.clientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		d.adminTmpls.render(w, "login", adminData("Login", map[string]any{"Error": "Invalid credentials"}))
		return
	}
	if err := d.Auth.SetSession(w, "admin"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("admin login", "ip", d.clientIP(r))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (d *Deps) adminLogout(w http.ResponseWriter, r *http.Request) {
	d.Auth.ClearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// barRow is a stats row scaled to the largest value in its set, so the
// template can draw a proportional bar without doing arithmetic itself.
type barRow struct {
	Path    string
	Day     string
	Count   int64
	Percent int
}

// maxDashboardRows bounds the "most read" list; the full breakdown is
// available through /api/v1/stats.
const maxDashboardRows = 10

func (d *Deps) adminDashboard(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	stats, err := d.Store.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	arts, err := build.LoadArticles(d.postsDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	links, err := d.Store.ListLinks(500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	var published, drafts int
	for _, a := range arts {
		if a.Draft {
			drafts++
		} else {
			published++
		}
	}
	recent := arts
	if len(recent) > 5 {
		recent = recent[:5]
	}

	// Top paths, scaled against the busiest one.
	top := stats.ByPath
	if len(top) > maxDashboardRows {
		top = top[:maxDashboardRows]
	}
	var maxPath int64
	for _, p := range top {
		maxPath = max(maxPath, p.Count)
	}
	topRows := make([]barRow, 0, len(top))
	for _, p := range top {
		topRows = append(topRows, barRow{Path: p.Path, Count: p.Count, Percent: percentOf(p.Count, maxPath)})
	}

	// Per-day bars, scaled against the busiest day.
	var maxDay int64
	var today int64
	todayStr := todayUTC()
	for _, dc := range stats.ByDay {
		maxDay = max(maxDay, dc.Count)
		if dc.Day == todayStr {
			today = dc.Count
		}
	}
	dayRows := make([]barRow, 0, len(stats.ByDay))
	for _, dc := range stats.ByDay {
		dayRows = append(dayRows, barRow{Day: dc.Day, Count: dc.Count, Percent: percentOf(dc.Count, maxDay)})
	}
	var rangeStart, rangeEnd string
	if len(dayRows) > 0 {
		rangeStart, rangeEnd = dayRows[0].Day, dayRows[len(dayRows)-1].Day
	}

	d.adminTmpls.render(w, "dashboard", adminData("Dashboard", map[string]any{
		"Stats":         stats,
		"Site":          d.Site(),
		"PostCount":     published,
		"DraftCount":    drafts,
		"LinkCount":     len(links),
		"Today":         today,
		"TopPaths":      topRows,
		"Days":          dayRows,
		"DayRangeStart": rangeStart,
		"DayRangeEnd":   rangeEnd,
		"Recent":        recent,
	}))
}

// percentOf scales n against maxN for a bar width, with a floor of 2% so a
// non-zero value is always visible.
func percentOf(n, maxN int64) int {
	if maxN <= 0 || n <= 0 {
		return 0
	}
	p := int(n * 100 / maxN)
	return max(2, p)
}

func (d *Deps) adminPostsList(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	arts, err := build.LoadArticles(d.postsDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	d.adminTmpls.render(w, "posts-list", adminData("Posts", map[string]any{"Articles": arts}))
}

func (d *Deps) adminPostEdit(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	slug := r.PathValue("slug")
	data := adminData("New Post", nil)
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
		data["Title"] = "Edit Post"
		data["Article"] = *a
	}
	d.adminTmpls.render(w, "post-edit", data)
}

// adminPostSave handles both create and update. The form posts the existing
// slug (hidden field) when editing; an empty value means a new post, in which
// case we derive the slug from the title and the file is created.
func (d *Deps) adminPostSave(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	if !parseAdminForm(w, r) {
		return
	}
	origSlug := r.FormValue("slug")
	date := r.FormValue("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	in := articleInput{
		Title:       strings.TrimSpace(r.FormValue("title")),
		Date:        date,
		Description: strings.TrimSpace(r.FormValue("description")),
		Tags:        parseTags(r.FormValue("tags")),
		Body:        r.FormValue("body"),
		Draft:       r.FormValue("draft") == "on",
	}

	// Editing an existing post: reuse its slug and overwrite its source file.
	if origSlug != "" {
		if _, err := d.updateArticle(origSlug, in); err != nil {
			d.renderPostEditError(w, in, origSlug, err)
			return
		}
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
		return
	}

	// New post: slugify the title and create.
	if _, _, err := d.createArticle(in); err != nil {
		d.renderPostEditError(w, in, "", err)
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

// renderPostEditError re-renders the editor with the submitted values intact
// and the failure explained, rather than throwing away a long draft on a
// validation error.
func (d *Deps) renderPostEditError(w http.ResponseWriter, in articleInput, slug string, cause error) {
	slog.Warn("admin post save failed", "slug", slug, "err", cause)
	date, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		date = time.Now()
	}
	w.WriteHeader(http.StatusBadRequest)
	d.adminTmpls.render(w, "post-edit", adminData("Edit Post", map[string]any{
		"Error": cause.Error(),
		"Article": build.Article{
			Slug: slug, Title: in.Title, Date: date, Description: in.Description,
			Tags: in.Tags, Draft: in.Draft, Body: in.Body,
		},
	}))
}

// adminPostPreview renders the posted markdown body to HTML for the editor's
// "Preview" tab. It does not touch disk.
func (d *Deps) adminPostPreview(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
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
	d.renderLinksList(w, "")
}

func (d *Deps) renderLinksList(w http.ResponseWriter, errMsg string) {
	if !d.adminReady(w) {
		return
	}
	links, err := d.Store.ListLinks(500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	data := map[string]any{
		"Links": links,
		"Feeds": d.Site().RSSFeeds,
		// Shown next to the feed list so the operator knows how long until
		// the next automatic poll.
		"PollInterval": d.Cfg.RSSInterval.String(),
	}
	if errMsg != "" {
		data["Error"] = errMsg
		w.WriteHeader(http.StatusBadRequest)
	}
	d.adminTmpls.render(w, "links-list", adminData("Links", data))
}

func (d *Deps) adminLinkAdd(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	href := strings.TrimSpace(r.FormValue("href"))
	if label == "" || href == "" {
		d.renderLinksList(w, "Both a label and a URL are required.")
		return
	}
	if _, err := d.Store.AddLink(store.Link{
		Label: label, Href: href, Note: strings.TrimSpace(r.FormValue("note")),
		Source: "manual", SortDate: time.Now().Unix(),
	}); err != nil {
		// A rejected scheme or a duplicate href is the operator's input
		// problem, not a server fault — say so in the page instead of
		// replacing the admin UI with a 500.
		slog.Warn("admin link add", "err", err)
		d.renderLinksList(w, linkErrorMessage(err))
		return
	}
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

// linkErrorMessage turns a store error into something an operator can act on.
func linkErrorMessage(err error) string {
	switch {
	case strings.Contains(err.Error(), "disallowed scheme"):
		return "That URL scheme isn't allowed. Use http://, https://, or mailto:."
	case strings.Contains(err.Error(), "UNIQUE"), strings.Contains(err.Error(), "already"):
		return "That URL is already in the links list."
	default:
		return "Could not add the link."
	}
}

func (d *Deps) adminLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id > 0 {
		if _, err := d.Store.RemoveLink(id); err != nil {
			slog.Error("admin link delete", "id", id, "err", err)
		}
	}
	if err := d.Engine.Rebuild(); err != nil {
		slog.Error("rebuild after link delete", "err", err)
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

func (d *Deps) adminSiteEdit(w http.ResponseWriter, r *http.Request) {
	if !d.adminReady(w) {
		return
	}
	d.adminTmpls.render(w, "site-edit", adminData("Site Config", map[string]any{"Site": d.Site()}))
}

// adminSiteSave edits only the scalar/textarea fields exposed in the site form
// (title, description, bio). The richer sections (nav/social/rss_feeds) are
// managed via the REST API; mutating them through a freeform textarea would be
// error-prone.
//
// As with updateSiteSection, we apply the edits to a freshly-loaded copy and
// publish it via ReloadSite rather than mutating the live shared pointer,
// which would race the engine's reads during Rebuild.
func (d *Deps) adminSiteSave(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	site, err := siteconfig.Load(d.Cfg.SiteYAMLPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	site.Title = strings.TrimSpace(r.FormValue("title"))
	site.Description = strings.TrimSpace(r.FormValue("description"))
	site.Bio = splitLines(r.FormValue("bio"))
	// Anything other than the explicit "minimal" means the full style, so a
	// missing or unexpected value can't turn the summaries off by accident.
	if r.FormValue("links_style") == siteconfig.LinksStyleMinimal {
		site.LinksStyle = siteconfig.LinksStyleMinimal
	} else {
		site.LinksStyle = siteconfig.LinksStyleFull
	}
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.reloadSite(); err != nil {
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

// postsDir returns the directory holding article source files.
func (d *Deps) postsDir() string {
	return filepath.Join(d.Cfg.ContentDir, "posts")
}
