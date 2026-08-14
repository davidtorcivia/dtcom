// Package server wires HTTP handlers for the public site, admin UI, REST API,
// and MCP server into a single http.Handler.
package server

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
	"davidtorcivia.com/dtcom/internal/auth"
	"davidtorcivia.com/dtcom/internal/backup"
	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/config"
	"davidtorcivia.com/dtcom/internal/feeds"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// Deps bundles every collaborator the handlers need. The Site field is a
// function (not a pointer) so handlers always see the latest config after a
// site.yml rewrite.
//
// ReloadSite re-reads site.yml from disk and atomically swaps the pointer
// that Site() returns. Handlers that mutate site.yml (admin save, REST
// site-section PUT, MCP update_* tools) must NOT edit the live *Config in
// place — that would race the engine's concurrent reads during Rebuild.
// Instead they write a fresh copy to disk and call ReloadSite before
// rebuilding, so every reader observes a single consistent pointer.
type Deps struct {
	Cfg        *config.Config
	Site       func() *siteconfig.Config
	ReloadSite func() error
	Store      *store.Store
	Engine     *build.Engine
	Poller     *feeds.Poller
	Backups    *backup.Service
	Auth       *auth.Auth
	adminTmpls *adminTemplateStore
	limits     *limiters

	// Assets fingerprints /static URLs for the admin templates. Share the
	// engine's instance so a rebuild's refresh is visible here too; left nil,
	// one is created and then never updated, and an edited stylesheet keeps
	// serving from cache under its old URL.
	Assets *assets.Fingerprinter
	assets *assets.Fingerprinter

	// tokenTouched throttles last-used writes for API tokens; see touchToken.
	tokenTouchMu sync.Mutex
	tokenTouched map[int64]time.Time

	// csp memoises the Content-Security-Policy, which varies only with the
	// analytics origin configured in site.yml; see securityHeaders.
	csp cspCache

	// postMu serializes create/update/delete of post files. The engine's own
	// mutex only serializes rebuilds; the check-then-write in createArticle
	// (does this slug exist?) needs its own lock or two concurrent writers
	// can both pass the check and clobber each other.
	postMu sync.Mutex

	// renditionWG tracks in-flight background rendition goroutines, so tests
	// can drain them before TempDir cleanup; see generateRenditions.
	renditionWG sync.WaitGroup

	// mcpArticleRes is the set of article URIs currently registered as MCP
	// resources, so a sync knows which ones to withdraw; see
	// syncArticleResources.
	mcpResMu      sync.Mutex
	mcpArticleRes map[string]bool
}

// New wires every route group and wraps the mux in the shared middleware:
// security headers → request logging → panic recovery → gzip.
func New(d *Deps) http.Handler {
	d.limits = newLimiters()
	d.tokenTouched = map[int64]time.Time{}
	if d.Assets != nil {
		d.assets = d.Assets
	} else {
		staticDir := "static"
		if d.Cfg != nil && d.Cfg.StaticDir != "" {
			staticDir = d.Cfg.StaticDir
		}
		d.assets = assets.New(staticDir)
	}

	// Admin templates live under <TemplatesDir>/admin, resolved the same way
	// build.Engine resolves its own templates. A missing dir is tolerated
	// (adminTmpls stays nil and admin handlers return 503) so the public site
	// and API still work in stripped-down test setups.
	tmplDir := "templates"
	if d.Cfg != nil && d.Cfg.TemplatesDir != "" {
		tmplDir = d.Cfg.TemplatesDir
	}
	adminDir := filepath.Join(tmplDir, "admin")
	if info, err := os.Stat(adminDir); err == nil && info.IsDir() {
		if ts, err := newAdminTemplates(adminDir, d.assets); err == nil {
			d.adminTmpls = ts
		} else {
			// A broken admin template is a deploy-time mistake worth shouting
			// about — the admin UI is entirely unavailable until it's fixed.
			slog.Error("admin templates failed to parse; admin UI disabled", "dir", adminDir, "err", err)
		}
	} else {
		slog.Warn("admin templates directory not found; admin UI disabled", "dir", adminDir)
	}

	mux := http.NewServeMux()
	registerPublic(mux, d)
	registerAdmin(mux, d)
	registerAPI(mux, d)
	registerMCP(mux, d)
	return d.securityHeaders(d.logging(recoverer(compression(mux))))
}
