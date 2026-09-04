package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"gopkg.in/yaml.v3"
)

// dateRe matches a strict YYYY-MM-DD literal. Used to validate caller-supplied
// dates before they reach filepath.Join / YAML frontmatter, since a malicious
// date like "../../etc" could escape the posts dir.
var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// validDate also rejects values the regex accepts but the calendar does not
// ("2026-99-99"): such a date writes a post file whose frontmatter then fails
// to parse on every future rebuild.
func validDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// registerAPI wires the bearer-token-authenticated REST API under /api/v1/.
// The public /api/search and /api/track endpoints (no auth) are registered
// separately in registerPublic.
func registerAPI(mux *http.ServeMux, d *Deps) {
	route := func(pattern, scope string, fn http.HandlerFunc) {
		mux.Handle(pattern, d.apiMiddleware(scope, fn))
	}
	route("GET /api/v1/articles", scopeRead, d.apiListArticles)
	route("POST /api/v1/articles", scopeDrafts, d.apiCreateArticle)
	route("GET /api/v1/articles/{slug}", scopeRead, d.apiGetArticle)
	route("PUT /api/v1/articles/{slug}", scopeDrafts, d.apiUpdateArticle)
	route("DELETE /api/v1/articles/{slug}", scopeDelete, d.apiDeleteArticle)
	route("GET /api/v1/links", scopeRead, d.apiListLinks)
	route("POST /api/v1/links", scopePublish, d.apiAddLink)
	route("DELETE /api/v1/links/{id}", scopeDelete, d.apiDeleteLink)
	route("GET /api/v1/site", scopeRead, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, d.Site()) })
	route("PUT /api/v1/site/{section}", scopePublish, d.apiUpdateSiteSection)
	route("POST /api/v1/regenerate", scopeOps, d.apiRegenerate)
	route("GET /api/v1/stats", scopeRead, d.apiStats)
	route("POST /api/v1/feeds/refresh", scopeOps, d.apiRefreshFeeds)
	route("POST /api/v1/images", scopeDrafts, d.apiUploadImage)
}

// apiMiddleware enforces the bearer token for every /api/v1/ request and
// throttles repeated failures so the token can't be guessed at line rate.
func (d *Deps) apiMiddleware(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := d.authorizeBearer(w, r)
		if !ok {
			return
		}
		if !p.allows(scope) {
			writeError(w, http.StatusForbidden, nil)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, p)))
	})
}

// authorizeBearer checks the API token, writing the failure response itself.
// Rate limiting keys on the client address and only charges a token on a
// failed attempt, so a correctly-authenticated client is never throttled.
func (d *Deps) authorizeBearer(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	if p, ok := d.authorizeToken(r); ok {
		return p, true
	}
	ip := d.clientIP(r)
	if !d.limits.bearer.Allow(ip) {
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusTooManyRequests, nil)
		return nil, false
	}
	slog.Warn("bearer auth failed", "ip", ip, "path", r.URL.Path)
	w.Header().Set("WWW-Authenticate", `Bearer realm="dtcom"`)
	writeError(w, http.StatusUnauthorized, nil)
	return nil, false
}

func (d *Deps) apiRegenerate(w http.ResponseWriter, _ *http.Request) {
	if err := d.Engine.Rebuild(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d *Deps) apiStats(w http.ResponseWriter, _ *http.Request) {
	s, err := d.Store.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (d *Deps) apiRefreshFeeds(w http.ResponseWriter, r *http.Request) {
	n := d.Poller.Poll(context.WithoutCancel(r.Context()), d.Site())
	writeJSON(w, http.StatusOK, map[string]int{"imported": n})
}

// articleInput is the JSON shape for create/update article payloads. It's
// shared by the REST API and the MCP tools.
type articleInput struct {
	Title            string   `json:"title"`
	Slug             string   `json:"slug"`
	Date             string   `json:"date"`
	Description      string   `json:"description"`
	Tags             []string `json:"tags"`
	Body             string   `json:"body"`
	Cover            string   `json:"cover"`
	Draft            bool     `json:"draft"`
	Agent            string   `json:"agent"`
	PublishAt        string   `json:"publish_at"`
	ExpectedRevision string   `json:"expected_revision"`
	Updated          string   `json:"-"`
	Actor            string   `json:"-"`
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
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Draft       bool   `json:"draft"`
	PublishAt   string `json:"publish_at,omitempty"`
	Revision    string `json:"revision"`
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
			Description: a.Description, Draft: a.Draft, PublishAt: formatOptionalTime(a.PublishAt), Revision: a.Revision,
		})
	}
	writeJSON(w, http.StatusOK, res)
}

func (d *Deps) apiGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a, err := d.findArticleBySlug(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if a == nil {
		writeError(w, http.StatusNotFound, nil)
		return
	}
	w.Header().Set("ETag", `"`+a.Revision+`"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":        a.Slug,
		"title":       a.Title,
		"date":        a.Date.Format("2006-01-02"),
		"description": a.Description,
		"tags":        a.Tags,
		"draft":       a.Draft,
		"body":        a.Body,
		"agent":       a.Agent,
		"publish_at":  formatOptionalTime(a.PublishAt),
		"updated":     formatOptionalTime(a.Updated),
		"revision":    a.Revision,
	})
}

func (d *Deps) apiCreateArticle(w http.ResponseWriter, r *http.Request) {
	var in articleInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !in.Draft && requireScope(r.Context(), scopePublish) != nil {
		writeError(w, http.StatusForbidden, nil)
		return
	}
	in.Actor = actorFromContext(r.Context())
	slug, status, err := d.createArticle(in)
	if err != nil {
		writeError(w, status, err)
		return
	}
	w.Header().Set("Location", "/api/v1/articles/"+slug)
	revision := d.currentArticleRevision(slug)
	if revision != "" {
		w.Header().Set("ETag", `"`+revision+`"`)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"slug": slug, "revision": revision})
}

func (d *Deps) apiUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var in articleInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current, err := d.findArticleBySlug(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if (!in.Draft || current != nil && !current.Draft) && requireScope(r.Context(), scopePublish) != nil {
		writeError(w, http.StatusForbidden, nil)
		return
	}
	in.Actor = actorFromContext(r.Context())
	if in.ExpectedRevision == "" {
		in.ExpectedRevision = strings.Trim(r.Header.Get("If-Match"), `"`)
	}
	if in.ExpectedRevision == "" {
		writeError(w, http.StatusPreconditionRequired, nil)
		return
	}
	status, err := d.updateArticle(slug, in)
	if err != nil {
		writeError(w, status, err)
		return
	}
	revision := d.currentArticleRevision(slug)
	if revision != "" {
		w.Header().Set("ETag", `"`+revision+`"`)
	}
	writeJSON(w, http.StatusOK, map[string]string{"slug": slug, "revision": revision})
}

func (d *Deps) apiDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	expected := strings.Trim(r.Header.Get("If-Match"), `"`)
	if expected == "" {
		writeError(w, http.StatusPreconditionRequired, nil)
		return
	}
	status, err := d.deleteArticleRevision(slug, actorFromContext(r.Context()), expected)
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

func (d *Deps) currentArticleRevision(slug string) string {
	a, _ := d.findArticleBySlug(slug)
	if a == nil {
		return ""
	}
	return a.Revision
}

// createArticle writes a new post file and rebuilds. Returns
// (slug, httpStatus, err) where status is 409 on a filename collision.
func (d *Deps) createArticle(in articleInput) (string, int, error) {
	d.postMu.Lock()
	defer d.postMu.Unlock()
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
	if in.PublishAt != "" {
		if _, err := time.Parse(time.RFC3339, in.PublishAt); err != nil {
			return "", http.StatusBadRequest, fmt.Errorf("invalid publish_at: %w", err)
		}
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
	after := []byte(renderArticleFile(in, date))
	if err := writeFileAtomic(path, after); err != nil {
		return "", http.StatusInternalServerError, err
	}
	if err := d.Engine.Rebuild(); err != nil {
		_ = os.Remove(path)
		return "", http.StatusInternalServerError, err
	}
	d.recordArticleAudit(store.ArticleAudit{Actor: mutationActor(in.Actor), Action: "create", Slug: slug,
		SourceName: filepath.Base(path), AfterRevision: revisionOf(after), AfterSource: after})
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
	// Same lock as createArticle and deleteArticle, which mux.go has always
	// claimed covered all three. It did not: this function looks the article
	// up, overwrites its file and then renames it, so a create running
	// concurrently could pass its "does this slug exist?" check against a path
	// this one is about to move out from under it. Save-in-place makes
	// concurrent writers ordinary rather than theoretical.
	d.postMu.Lock()
	defer d.postMu.Unlock()
	a, err := d.findArticleBySlug(slug)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if a == nil {
		return http.StatusNotFound, fmt.Errorf("article %q not found", slug)
	}
	if in.ExpectedRevision != "" && in.ExpectedRevision != a.Revision {
		return http.StatusConflict, fmt.Errorf("article changed: expected revision %s, current revision %s", in.ExpectedRevision, a.Revision)
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
	if in.PublishAt != "" {
		if _, err := time.Parse(time.RFC3339, in.PublishAt); err != nil {
			return http.StatusBadRequest, fmt.Errorf("invalid publish_at: %w", err)
		}
	}
	before, err := os.ReadFile(a.SourcePath)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	in.Updated = time.Now().UTC().Format(time.RFC3339)
	after := []byte(renderArticleFile(in, date))
	if err := writeFileAtomic(a.SourcePath, after); err != nil {
		return http.StatusInternalServerError, err
	}
	// Keep the filename's date prefix in step with the frontmatter date, so
	// content/posts stays sorted and self-describing after a date edit. The
	// slug (everything after the prefix) is unchanged, so no URL moves.
	finalPath := a.SourcePath
	if want := filepath.Join(filepath.Dir(a.SourcePath), date+"-"+slug+".md"); want != a.SourcePath {
		if err := os.Rename(a.SourcePath, want); err != nil {
			slog.Warn("could not rename post file after date change", "from", a.SourcePath, "to", want, "err", err)
		} else {
			finalPath = want
		}
	}
	if err := d.Engine.Rebuild(); err != nil {
		_ = os.Remove(finalPath)
		_ = writeFileAtomic(a.SourcePath, before)
		return http.StatusInternalServerError, err
	}
	d.recordArticleAudit(store.ArticleAudit{Actor: mutationActor(in.Actor), Action: "update", Slug: slug,
		SourceName: filepath.Base(a.SourcePath), BeforeRevision: a.Revision, AfterRevision: revisionOf(after),
		BeforeSource: before, AfterSource: after})
	return http.StatusOK, nil
}

// deleteArticle removes the post file matching slug and rebuilds.
func (d *Deps) deleteArticle(slug string) (int, error) {
	return d.deleteArticleAs(slug, "admin")
}

func (d *Deps) deleteArticleAs(slug, actor string) (int, error) {
	return d.deleteArticleRevision(slug, actor, "")
}

func (d *Deps) deleteArticleRevision(slug, actor, expected string) (int, error) {
	d.postMu.Lock()
	defer d.postMu.Unlock()
	a, err := d.findArticleBySlug(slug)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if a == nil {
		return http.StatusNotFound, fmt.Errorf("article %q not found", slug)
	}
	if expected != "" && expected != a.Revision {
		return http.StatusConflict, fmt.Errorf("article changed: expected revision %s, current revision %s", expected, a.Revision)
	}
	before, err := os.ReadFile(a.SourcePath)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if err := os.Remove(a.SourcePath); err != nil {
		return http.StatusInternalServerError, err
	}
	if err := d.Engine.Rebuild(); err != nil {
		_ = writeFileAtomic(a.SourcePath, before)
		return http.StatusInternalServerError, err
	}
	d.recordArticleAudit(store.ArticleAudit{Actor: mutationActor(actor), Action: "delete", Slug: slug,
		SourceName: filepath.Base(a.SourcePath), BeforeRevision: a.Revision, BeforeSource: before})
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
	idStr := r.PathValue("id")
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
	// Same cap as every other JSON endpoint; decodeJSONReader itself is also
	// used with already-bounded readers (the MCP tools).
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBody)
	section := r.PathValue("section")
	if err := d.updateSiteSection(section, r.Body); err != nil {
		writeError(w, httpToStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Article file rendering + slugify (shared with admin handlers and MCP)
// ---------------------------------------------------------------------------

// renderArticleFile assembles YAML frontmatter and a markdown body. yaml.v3
// handles quoting so punctuation in an agent-written field cannot corrupt it.
func renderArticleFile(in articleInput, date string) string {
	tags := make([]string, 0, len(in.Tags))
	for _, t := range in.Tags {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	front := struct {
		Title       string   `yaml:"title"`
		Date        yamlDate `yaml:"date"`
		Description string   `yaml:"description"`
		Tags        []string `yaml:"tags"`
		Cover       string   `yaml:"cover,omitempty"`
		Draft       bool     `yaml:"draft"`
		Updated     string   `yaml:"updated,omitempty"`
		PublishAt   string   `yaml:"publish_at,omitempty"`
		Agent       string   `yaml:"agent,omitempty"`
	}{Title: in.Title, Date: yamlDate(date), Description: in.Description, Tags: tags, Cover: in.Cover, Draft: in.Draft,
		Updated: in.Updated, PublishAt: in.PublishAt, Agent: in.Agent}
	b, _ := yaml.Marshal(front)
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(b)
	sb.WriteString("---\n\n")
	body := normalizeNewlines(in.Body)
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

type yamlDate string

func (d yamlDate) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: string(d)}, nil
}

// normalizeNewlines converts the CRLF a browser form submits into the LF the
// markdown renderer and the on-disk convention expect.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func revisionOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

func mutationActor(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return "admin"
	}
	return actor
}

func (d *Deps) recordArticleAudit(a store.ArticleAudit) {
	if d.Store != nil {
		if err := d.Store.RecordArticleAudit(a); err != nil {
			slog.Error("record article audit", "slug", a.Slug, "action", a.Action, "err", err)
		}
	}
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
