// Package main is the dtcom binary entrypoint.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"davidtorcivia.com/dtcom/internal/auth"
	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/config"
	"davidtorcivia.com/dtcom/internal/feeds"
	"davidtorcivia.com/dtcom/internal/server"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
	"davidtorcivia.com/dtcom/internal/watcher"
)

// Version is the build version, overridden via -ldflags.
const Version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("starting dtcom", "version", Version)

	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	// ensure dirs exist
	for _, dir := range []string{cfg.PublicDir, cfg.DataDir, cfg.ImagesDir, cfg.ContentDir + "/posts"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Error("mkdir", "dir", dir, "err", err)
			os.Exit(1)
		}
	}

	// site config — held in an atomic pointer so site-config writes (via
	// admin/API/MCP) can swap in a new pointer without restarting the engine
	// and without racing the engine's reads during Rebuild.
	site, err := siteconfig.Load(cfg.SiteYAMLPath)
	if err != nil {
		slog.Error("site.yml", "err", err)
		os.Exit(1)
	}
	var sitePtr atomic.Pointer[siteconfig.Config]
	sitePtr.Store(site)
	siteFn := func() *siteconfig.Config { return sitePtr.Load() }

	// store
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		slog.Error("store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// engine
	engine := build.NewEngine(build.EngineConfig{
		ContentDir:   cfg.ContentDir,
		PublicDir:    cfg.PublicDir,
		Site:         siteFn,
		Store:        st,
		TemplatesDir: "templates",
	})
	if err := engine.Rebuild(); err != nil {
		slog.Error("initial rebuild", "err", err)
		os.Exit(1)
	}

	// poller — runs on interval until shutdown. OnPoll fires after each Poll
	// (both the initial one and every periodic tick); when imports happened it
	// rebuilds so RSS-imported links surface on /links without waiting for
	// some other event to trigger one.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	poller := feeds.NewPoller(st)
	poller.OnPoll = func(imported int) {
		if imported > 0 {
			slog.Info("rss poll imported", "count", imported)
			if err := engine.Rebuild(); err != nil {
				slog.Warn("rebuild after rss poll", "err", err)
			}
		}
	}
	go poller.Start(ctx, siteFn, cfg.RSSInterval)
	// initial poll, best-effort. Runs in its own goroutine so a slow feed on
	// startup doesn't block serving. OnPoll rebuilds synchronously if imports
	// happened — fine at startup.
	go poller.Poll(sitePtr.Load())

	// watcher — debounce + rebuild on content changes
	watchEvents := make(chan string, 64)
	w, err := watcher.Watch(cfg.ContentDir, watchEvents)
	if err != nil {
		slog.Error("watcher", "err", err)
		os.Exit(1)
	}
	defer w.Close()
	go func() {
		dirty := false
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-watchEvents:
				dirty = true
			case <-tick.C:
				if dirty {
					dirty = false
					if err := engine.Rebuild(); err != nil {
						slog.Warn("watcher-triggered rebuild", "err", err)
					} else {
						slog.Info("rebuilt after content change")
					}
				}
			}
		}
	}()

	// auth
	a := auth.New(cfg.SessionKey, cfg.AdminPasswordHash, cfg.TOTPSecret)

	// server
	handler := server.New(&server.Deps{
		Cfg:  cfg,
		Site: siteFn,
		// ReloadSite re-reads site.yml and atomically swaps the pointer that
		// siteFn returns, so handlers that save site.yml publish their changes
		// to the engine/other readers without mutating shared state.
		ReloadSite: func() error {
			s, err := siteconfig.Load(cfg.SiteYAMLPath)
			if err != nil {
				return err
			}
			sitePtr.Store(s)
			return nil
		},
		Store:  st,
		Engine: engine,
		Poller: poller,
		Auth:   a,
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	// graceful shutdown on SIGINT/SIGTERM
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("shutting down")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
}
