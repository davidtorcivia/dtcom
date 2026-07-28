package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// adminIntegrations renders the API / MCP credentials and setup instructions.
//
// The bearer token is the same DTCOM_API_TOKEN the process was started with.
// Showing it here is deliberate: it is the value an MCP client or script needs,
// and the alternative — reading it back off the server's environment — is worse
// for everyone. The page is behind the admin session and served no-store
// (requireAuth sets that), and the token is masked until revealed.
func (d *Deps) adminIntegrations(w http.ResponseWriter, r *http.Request) {
	d.renderIntegrations(w, "", nil)
}

// renderIntegrations draws the page. newToken is non-empty exactly once, right
// after a token is minted — it is the only moment the raw value exists outside
// the client's clipboard.
func (d *Deps) renderIntegrations(w http.ResponseWriter, errMsg string, newToken map[string]string) {
	if !d.adminReady(w) {
		return
	}
	base := d.Cfg.BaseURL
	token := d.Cfg.APIToken

	// The MCP config block is built server-side and marshalled properly so a
	// token containing a quote or backslash can't produce invalid JSON for the
	// operator to paste. Two versions are produced: the shown one carries the
	// masked token (there is no point masking the field above if the same
	// value sits in plain sight two sections down), while Copy puts the real
	// config on the clipboard.
	mcpConfig, err := mcpConfigJSON(base, token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	mcpConfigShown, err := mcpConfigJSON(base, maskToken(token))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	tokens, err := d.Store.ListAPITokens()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	data := map[string]any{
		"BaseURL":        base,
		"Token":          token,
		"TokenMask":      maskToken(token),
		"TokenPrefix":    tokenPrefix(token),
		"MCPConfig":      mcpConfig,
		"MCPConfigShown": mcpConfigShown,
		"Endpoints":      apiEndpoints,
		"MCPTools":       mcpToolGroups,
		"Tokens":         tokens,
	}
	if errMsg != "" {
		data["Error"] = errMsg
	}
	if newToken != nil {
		data["NewToken"] = newToken
	}
	d.adminTmpls.render(w, "integrations", adminData("API & MCP", data))
}

// adminTokenCreate mints a managed token and renders it once.
func (d *Deps) adminTokenCreate(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	raw, t, err := d.Store.CreateAPIToken(name)
	if err != nil {
		slog.Error("create api token", "err", err)
		d.renderIntegrations(w, "Could not create the token.", nil)
		return
	}
	slog.Info("api token created", "id", t.ID, "name", t.Name, "prefix", t.Prefix)

	// Rendered rather than redirected: the raw value is not stored, so a
	// redirect would lose the only copy.
	mcpConfig, err := mcpConfigJSON(d.Cfg.BaseURL, raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	d.renderIntegrations(w, "", map[string]string{
		"Name":      t.Name,
		"Value":     raw,
		"MCPConfig": mcpConfig,
	})
}

// adminTokenRevoke withdraws a managed token.
func (d *Deps) adminTokenRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	revoked, err := d.Store.RevokeAPIToken(id)
	if err != nil {
		slog.Error("revoke api token", "id", id, "err", err)
		d.renderIntegrations(w, "Could not revoke that token.", nil)
		return
	}
	if !revoked {
		d.renderIntegrations(w, "That token was already revoked.", nil)
		return
	}
	slog.Info("api token revoked", "id", id)
	http.Redirect(w, r, "/admin/integrations", http.StatusSeeOther)
}

// mcpConfigJSON renders the client config block for a given token value.
func mcpConfigJSON(base, token string) (string, error) {
	b, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			"dtcom": map[string]any{
				"url":     base + "/mcp",
				"headers": map[string]string{"Authorization": "Bearer " + token},
			},
		},
	}, "", "  ")
	return string(b), err
}

// tokenPrefix returns the leading characters used to identify a token in the
// UI, without assuming a minimum length — a template doing this inline blew up
// the whole page whenever the token was shorter than the slice bound.
func tokenPrefix(t string) string {
	if len(t) <= 8 {
		return t
	}
	return t[:8]
}

// maskToken renders a token as its first and last few characters so the
// operator can tell which credential they're looking at without the whole
// value sitting on screen.
func maskToken(t string) string {
	if len(t) <= 12 {
		return strings.Repeat("•", len(t))
	}
	return t[:4] + strings.Repeat("•", 24) + t[len(t)-4:]
}

// apiEndpoint documents one REST route for the integrations page. Keeping the
// list here (rather than in the template) means it sits next to the router it
// describes and is easy to keep in step.
type apiEndpoint struct {
	Method, Path, Description string
}

var apiEndpoints = []apiEndpoint{
	{"GET", "/api/v1/articles", "List every article, including drafts."},
	{"POST", "/api/v1/articles", "Create an article. Body: {title, body, date, description, tags, slug, draft}."},
	{"GET", "/api/v1/articles/{slug}", "Fetch one article's frontmatter and markdown body."},
	{"PUT", "/api/v1/articles/{slug}", "Replace an article. Same body shape as create."},
	{"DELETE", "/api/v1/articles/{slug}", "Delete an article and its rendered pages."},
	{"GET", "/api/v1/links", "List links (manual + RSS-imported)."},
	{"POST", "/api/v1/links", "Add a link. Body: {label, href, note, sort_date}."},
	{"DELETE", "/api/v1/links/{id}", "Remove a manual link."},
	{"GET", "/api/v1/site", "Read the whole site.yml config."},
	{"PUT", "/api/v1/site/{section}", "Replace bio, nav, social, rss_feeds, or footer_left."},
	{"POST", "/api/v1/images", "Upload an image (multipart, field \"file\"). Returns its URL."},
	{"POST", "/api/v1/regenerate", "Force a full rebuild."},
	{"GET", "/api/v1/stats", "Page-view totals, by path and by day."},
	{"POST", "/api/v1/feeds/refresh", "Poll every enabled RSS feed now."},
}

// mcpToolGroup lists the MCP tools by what they touch.
type mcpToolGroup struct {
	Name  string
	Tools []string
}

var mcpToolGroups = []mcpToolGroup{
	{"Articles", []string{"list_articles", "get_article", "create_article", "update_article", "delete_article", "search_articles"}},
	{"Links", []string{"list_links", "add_link", "remove_link"}},
	{"Site", []string{"get_site", "update_bio", "update_nav", "update_social", "update_rss_feeds"}},
	{"Ops", []string{"regenerate", "get_stats", "refresh_feeds"}},
}
