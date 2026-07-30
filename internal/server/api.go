package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/store"
)

// dateRe matches a strict YYYY-MM-DD literal. Used to validate caller-supplied
// dates before they reach filepath.Join / YAML frontmatter, since a malicious
// date like "../../etc" could escape the posts dir.
var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validDate(s string) bool { return dateRe.MatchString(s) }

// registerAPI wires the bearer-token-authenticated REST API under /api/v1/.
// The public /api/search and /api/track endpoints (no auth) are registered
// separately in registerPublic.
func registerAPI(mux *http.ServeMux, d *Deps) {
	mux.Handle("/api/v1/", d.apiMiddleware(http.HandlerFunc(d.apiRouter)))
}

// apiMiddleware enforces the bearer token for every /api/v1/ request and
// throttles repeated failures so the token can't be guessed at line rate.
func (d *Deps) apiMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.authorizeBearer(w, r) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// authorizeBearer checks the API token, writing the failure response itself.
// Rate limiting keys on the client address and only charges a token on a
// failed attempt, so a correctly-authenticated client is never throttled.
func (d *Deps) authorizeBearer(w http.ResponseWriter, r *http.Request) bool {
	if d.authorizeToken(r) {
		return true
	}
	ip := d.clientIP(r)
	if !d.limits.bearer.Allow(ip) {
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusTooManyRequests, nil)
		return false
	}
	slog.Warn("bearer auth failed", "ip", ip, "path", r.URL.Path)
	w.Header().Set("WWW-Authenticate", `Bearer realm="dtcom"`)
	writeError(w, http.StatusUnauthorized, nil)
	return false
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
		n := d.Poller.Poll(r.Context(), d.Site())
		writeJSON(w, http.StatusOK, map[string]int{"imported": n})
	// images
	case p == "/api/v1/images" && m == http.MethodPost:
		d.apiUploadImage(w, r)
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

// articleSummary is one article as the list endpoints project it: enough to
// choose between them, without the body.
//
// Description is in here because both list surfaces have always claimed it —
// the REST endpoint's documentation and the list_articles tool description
// both say "and description" — and neither actually sent it. Now that the tool
// publishes an output schema, that gap would be advertised.
type articleSummary struct {
	Slug, Title, Date string
	Description       string
	Draft             bool
}

func (d *Deps) apiListArticles(w http.ResponseWriter, r *http.Request) {
	arts, err := build.LoadArticles(d.postsDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	res := make([]articleSummary, 0, len(arts))
	for _, a := range arts {
		res = append(res, articleSummary{
			Slug: a.Slug, Title: a.Title, Date: a.Date.Format("2006-01-02"),
			Description: a.Description, Draft: a.Draft,
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
	if !validSlug(slug) {
		return nil, nil
	}
	arts, err := build.LoadArticles(d.postsDir())
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
	// ALWAYS sanitize: never trust caller-supplied slug/date verbatim, since
	// both reach filepath.Join and could escape the posts dir (path traversal).
	// in.Slug is run through slugify (dropping every char except [a-z0-9-]),
	// and in.Date is validated against a strict YYYY-MM-DD regex.
	slug := slugify(in.Slug)
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
	if !validDate(date) {
		return "", http.StatusBadRequest, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", date)
	}
	// A slug collision must be detected against every existing post, not just
	// the <date>-<slug>.md filename: two posts with the same slug but
	// different dates would silently overwrite each other's rendered page.
	existing, err := d.findArticleBySlug(slug)
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	if existing != nil {
		return "", http.StatusConflict, fmt.Errorf("article %q already exists", slug)
	}
	path := filepath.Join(d.postsDir(), date+"-"+slug+".md")
	if err := writeFileAtomic(path, []byte(renderArticleFile(in, date, slug))); err != nil {
		return "", http.StatusInternalServerError, err
	}
	if err := d.Engine.Rebuild(); err != nil {
		return "", http.StatusInternalServerError, err
	}
	return slug, http.StatusCreated, nil
}

// writeFileAtomic writes to a temp file in the destination directory and
// renames it into place, so a reader (the watcher-triggered rebuild, which
// fires on the first write event) never observes a half-written post.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
	if !validDate(date) {
		return http.StatusBadRequest, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", date)
	}
	if strings.TrimSpace(in.Title) == "" {
		return http.StatusBadRequest, fmt.Errorf("title is required")
	}
	if err := writeFileAtomic(a.SourcePath, []byte(renderArticleFile(in, date, slug))); err != nil {
		return http.StatusInternalServerError, err
	}
	// Keep the filename's date prefix in step with the frontmatter date, so
	// content/posts stays sorted and self-describing after a date edit. The
	// slug (everything after the prefix) is unchanged, so no URL moves.
	if want := filepath.Join(filepath.Dir(a.SourcePath), date+"-"+slug+".md"); want != a.SourcePath {
		if err := os.Rename(a.SourcePath, want); err != nil {
			slog.Warn("could not rename post file after date change", "from", a.SourcePath, "to", want, "err", err)
		}
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
		// A disallowed href scheme (javascript:, data:, …) and a repeat href
		// are both client-supplied input problems with their own status;
		// anything else is a server fault.
		switch {
		case errors.Is(err, store.ErrDisallowedScheme):
			writeError(w, http.StatusBadRequest, err)
		case errors.Is(err, store.ErrDuplicateLink):
			writeError(w, http.StatusConflict, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
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
	// Sscanf("12abc", "%d") succeeds and leaves the trailing junk unread, so
	// parse strictly instead.
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid id %q", idStr))
		return
	}
	removed, err := d.Store.RemoveLink(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !removed {
		// Either no such id, or it's an RSS-imported link, which can't be
		// deleted because the next poll would re-import it. Reporting 204
		// here (as an earlier version did) told the caller a delete had
		// happened when nothing changed.
		writeError(w, http.StatusNotFound, nil)
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
// post file. Every string is double-quoted with escaping so a title containing
// a colon or quote can't corrupt the frontmatter.
func renderArticleFile(in articleInput, date, slug string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("title: " + yamlQuote(in.Title) + "\n")
	sb.WriteString("date: " + date + "\n")
	sb.WriteString("description: " + yamlQuote(in.Description) + "\n")
	// Each tag is quoted individually: an unquoted tag containing a comma or a
	// bracket would silently split or terminate the flow sequence, corrupting
	// every subsequent field.
	quoted := make([]string, 0, len(in.Tags))
	for _, t := range in.Tags {
		if t = strings.TrimSpace(t); t != "" {
			quoted = append(quoted, yamlQuote(t))
		}
	}
	fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(quoted, ", "))
	sb.WriteString("draft: " + boolStr(in.Draft) + "\n")
	sb.WriteString("---\n\n")
	body := normalizeNewlines(in.Body)
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

// yamlQuote renders s as a YAML double-quoted scalar. Backslash and quote are
// escaped, and the control characters a browser textarea can smuggle in
// (newline, tab, CR) are emitted as escapes rather than raw bytes, which would
// otherwise break the single-line key: value form.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// normalizeNewlines converts the CRLF a browser form submits into the LF the
// markdown renderer and the on-disk convention expect.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
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
