// Package server wires HTTP handlers for the public site, admin UI, REST API,
// and MCP server into a single http.Handler.
package server

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"

	"davidtorcivia.com/dtcom/internal/auth"
	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/config"
	"davidtorcivia.com/dtcom/internal/feeds"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// Deps bundles every collaborator the handlers need. The Site field is a
// function (not a pointer) so handlers always see the latest config after a
// site.yml rewrite.
type Deps struct {
	Cfg        *config.Config
	Site       func() *siteconfig.Config
	Store      *store.Store
	Engine     *build.Engine
	Poller     *feeds.Poller
	Auth       *auth.Auth
	adminTmpls *adminTemplateStore
}

// New wires every route group and wraps the mux in logging + panic recovery.
func New(d *Deps) http.Handler {
	if d != nil {
		// Admin templates live at <repo>/templates/admin. This path is relative
		// to the process working directory, matching how build.Engine resolves
		// "templates". A missing dir is tolerated (adminTmpls stays nil and
		// admin handlers return 500) so the public site and API still work in
		// stripped-down test setups.
		if info, err := os.Stat("templates/admin"); err == nil && info.IsDir() {
			if ts, err := newAdminTemplates(filepath.Join("templates", "admin")); err == nil {
				d.adminTmpls = ts
			} else {
				slog.Warn("admin templates failed to load", "err", err)
			}
		}
	}

	mux := http.NewServeMux()
	registerPublic(mux, d)
	registerAdmin(mux, d)
	registerAPI(mux, d)
	registerMCP(mux, d)
	return logging(recoverer(mux))
}

// logging emits one structured line per request.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// recoverer turns panics in handlers into 500s so one bad request can't kill
// the whole process.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "rec", rec, "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
