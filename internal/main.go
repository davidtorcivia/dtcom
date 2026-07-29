// Package main is the dtcom binary entrypoint.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"davidtorcivia.com/dtcom/internal/assets"
	"davidtorcivia.com/dtcom/internal/auth"
	"davidtorcivia.com/dtcom/internal/backup"
	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/config"
	"davidtorcivia.com/dtcom/internal/feeds"
	"davidtorcivia.com/dtcom/internal/server"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
	"davidtorcivia.com/dtcom/internal/watcher"
)

// Version is the build version. Declared as a var, not a const, so it can be
// set at link time: go build -ldflags "-X main.Version=$(git rev-parse --short HEAD)".
var Version = "dev"

// rebuildDebounce is how long the watcher waits for the filesystem to settle
// before rebuilding. Editors write in bursts (temp file, rename, chmod); one
// rebuild per burst is the goal.
const rebuildDebounce = 500 * time.Millisecond

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run holds the whole lifecycle so every deferred cleanup actually executes;
// os.Exit in main would skip them.
func run() error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel(),
	})))
	slog.Info("starting dtcom", "version", Version)

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	// ensure dirs exist
	for _, dir := range []string{cfg.PublicDir, cfg.DataDir, cfg.ImagesDir, filepath.Join(cfg.ContentDir, "posts")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	// site config — held in an atomic pointer so site-config writes (via
	// admin/API/MCP) can swap in a new pointer without restarting the engine
	// and without racing the engine's reads during Rebuild.
	// LoadOrSeed, not Load: content/ is the author's data and is not tracked
	// in git, so a fresh deployment legitimately has no site.yml yet.
	site, err := siteconfig.LoadOrSeed(cfg.SiteYAMLPath)
	if err != nil {
		return err
	}
	// The canonical URL is configured in one place — the environment — and
	// site.yml's copy follows it, so the feed, sitemap, and OG tags can't
	// disagree with the deployment.
	site.BaseURL = cfg.BaseURL
	var sitePtr atomic.Pointer[siteconfig.Config]
	sitePtr.Store(site)
	siteFn := func() *siteconfig.Config { return sitePtr.Load() }

	// store
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// One fingerprinter shared by the engine (public pages) and the server
	// (admin pages), so a rebuild refreshes the hashes both of them emit.
	fingerprints := assets.New(cfg.StaticDir)

	// engine
	engine, err := build.NewEngine(build.EngineConfig{
		ContentDir:   cfg.ContentDir,
		PublicDir:    cfg.PublicDir,
		StaticDir:    cfg.StaticDir,
		ImagesDir:    cfg.ImagesDir,
		Assets:       fingerprints,
		Site:         siteFn,
		Store:        st,
		TemplatesDir: cfg.TemplatesDir,
	})
	if err != nil {
		return err
	}
	if err := engine.Rebuild(); err != nil {
		return err
	}

	// Fill in any missing image renditions, in the background: an image
	// uploaded before this pipeline existed has none, and encoding a directory
	// of them takes long enough that doing it before the first request would be
	// a visible outage. Pages rendered in the meantime simply reference the
	// master, and the rebuild at the end brings them up to date.
	//
	// Idempotent, so the usual case — everything already generated — costs one
	// header read per file and finishes in milliseconds.
	go func() {
		wrote, err := engine.Images().Backfill()
		if err != nil {
			slog.Warn("image rendition backfill", "err", err)
		}
		if wrote > 0 {
			slog.Info("image renditions generated", "files", wrote)
			if err := engine.Rebuild(); err != nil {
				slog.Warn("rebuild after rendition backfill", "err", err)
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// poller — runs on interval until shutdown. OnPoll fires after each Poll
	// (both the initial one and every periodic tick); when imports happened it
	// rebuilds so RSS-imported links surface on /links without waiting for
	// some other event to trigger one.
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
	// startup doesn't block serving.
	go poller.Poll(ctx, siteFn())

	// backups — content/ and data/ are in no other copy on this machine, by
	// design (see .gitignore). This is the other copy.
	backups := backup.New(backup.Config{
		ContentDir: cfg.ContentDir,
		ImagesDir:  cfg.ImagesDir,
		DBPath:     cfg.DBPath,
		Interval:   cfg.BackupInterval,
	}, backup.NewLocal(cfg.BackupDir), st)
	go backupLoop(ctx, backups)

	// watcher — debounce + rebuild on content changes
	watchEvents := make(chan string, 64)
	w, err := watcher.Watch(cfg.ContentDir, watchEvents)
	if err != nil {
		return err
	}
	defer w.Close()
	go watchLoop(ctx, watchEvents, engine)

	// auth
	a := auth.New(auth.Options{
		SessionKey:   cfg.SessionKey,
		PasswordHash: cfg.AdminPasswordHash,
		TOTPSecret:   cfg.TOTPSecret,
		SecureCookie: cfg.CookieSecure,
	})

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
			s.BaseURL = cfg.BaseURL
			sitePtr.Store(s)
			return nil
		},
		Store:   st,
		Engine:  engine,
		Poller:  poller,
		Backups: backups,
		Auth:    a,
		Assets:  fingerprints,
	})

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
		// Without these a single slow or idle client can hold a connection
		// (and its goroutine) open indefinitely. WriteTimeout is generous
		// because a rebuild-triggering API call does real work before it
		// responds.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// A failure to bind must end the process, not just log — otherwise the
	// container stays "up" while serving nothing.
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	// graceful shutdown on SIGINT/SIGTERM
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		return err
	case s := <-sig:
		slog.Info("shutting down", "signal", s.String())
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
	return nil
}

// watchLoop coalesces filesystem events and rebuilds once the burst settles.
// The timer is only armed while there is pending work, so an idle site does no
// periodic wakeups at all.
// backupLoop takes a scheduled archive whenever the newest one is older than
// the configured interval, then prunes to the retention policy.
//
// Driven by the age of the last archive rather than by a wall-clock hour, which
// is what makes it survive restarts: a machine that is rebooted every evening
// at a fixed time would, on a cron-style schedule, never reach the hour its
// backup was due. Here, coming up to find the last one a day old is enough.
func backupLoop(ctx context.Context, svc *backup.Service) {
	if svc.Interval() <= 0 {
		slog.Info("scheduled backups disabled")
		return
	}
	// Checked far more often than the interval, so the first archive after a
	// long downtime is taken promptly rather than a full period late.
	const check = 15 * time.Minute
	t := time.NewTicker(check)
	defer t.Stop()
	for {
		if due, err := backupDue(svc); err != nil {
			slog.Warn("backup schedule", "err", err)
		} else if due {
			info, err := svc.Create(backup.KindScheduled)
			if err != nil {
				slog.Error("scheduled backup failed", "err", err)
			} else {
				slog.Info("scheduled backup", "name", info.Name, "bytes", info.Size)
				if removed, err := svc.Prune(); err != nil {
					slog.Warn("prune backups", "err", err)
				} else if len(removed) > 0 {
					slog.Info("old backups pruned", "count", len(removed), "names", removed)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func backupDue(svc *backup.Service) (bool, error) {
	list, err := svc.List()
	if err != nil {
		return false, err
	}
	for _, in := range list {
		// A restore's safety copy is not the scheduled one, but it is a
		// complete archive taken at a known moment, so it counts as one for the
		// purpose of "how long since everything was last saved".
		if time.Since(in.Created) < svc.Interval() {
			return false, nil
		}
	}
	return true, nil
}

func watchLoop(ctx context.Context, events <-chan string, engine *build.Engine) {
	timer := time.NewTimer(rebuildDebounce)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-events:
			// Restart the window on every event, so the rebuild happens once
			// the writes stop rather than 500ms after they started — an editor
			// saving a large file can still be writing at that point.
			if pending && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			pending = true
			timer.Reset(rebuildDebounce)
		case <-timer.C:
			pending = false
			if err := engine.Rebuild(); err != nil {
				slog.Warn("watcher-triggered rebuild", "err", err)
			} else {
				slog.Info("rebuilt after content change")
			}
		}
	}
}

// logLevel reads DTCOM_LOG_LEVEL (debug|info|warn|error), defaulting to info.
func logLevel() slog.Level {
	switch os.Getenv("DTCOM_LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
