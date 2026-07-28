package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// registerAdminFeeds wires the inbound-RSS controls on the admin Links page.
//
// These feeds live in content/site.yml, which previously could only be edited
// by hand or through the REST API — so subscribing the links page to a new
// feed meant editing a file on the server. They're managed here now, saved
// through the same load-edit-save-reload path every other site.yml writer
// uses.
func registerAdminFeeds(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("POST /admin/feeds/add", d.requireAuth(d.adminFeedAdd))
	mux.HandleFunc("POST /admin/feeds/{index}/remove", d.requireAuth(d.adminFeedRemove))
	mux.HandleFunc("POST /admin/feeds/{index}/toggle", d.requireAuth(d.adminFeedToggle))
	mux.HandleFunc("POST /admin/feeds/refresh", d.requireAuth(d.adminFeedRefresh))
}

// validFeedURL checks a feed address before it is stored. The poller will
// fetch whatever is here on a timer, so it has to be an ordinary http(s) URL —
// not a file:// path or a scheme-less string that would fail on every tick.
func validFeedURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("feed URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("that doesn't parse as a URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("feed URL must start with http:// or https://")
	}
	if u.Host == "" {
		return fmt.Errorf("feed URL is missing a host")
	}
	return nil
}

// mutateFeeds loads site.yml, hands the feed list to fn, and saves + republishes
// the result. Every feed handler goes through here so none of them mutates the
// live shared config.
func (d *Deps) mutateFeeds(fn func([]siteconfig.RSSFeed) ([]siteconfig.RSSFeed, error)) error {
	site, err := siteconfig.Load(d.Cfg.SiteYAMLPath)
	if err != nil {
		return err
	}
	feeds, err := fn(site.RSSFeeds)
	if err != nil {
		return err
	}
	site.RSSFeeds = feeds
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		return err
	}
	return d.reloadSite()
}

func (d *Deps) adminFeedAdd(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	feedURL := strings.TrimSpace(r.FormValue("url"))
	label := strings.TrimSpace(r.FormValue("label"))
	if err := validFeedURL(feedURL); err != nil {
		d.renderLinksList(w, err.Error())
		return
	}
	if label == "" {
		// A missing label is filled in from the host, which is what an
		// operator would have typed anyway.
		if u, err := url.Parse(feedURL); err == nil {
			label = u.Host
		}
	}

	err := d.mutateFeeds(func(feeds []siteconfig.RSSFeed) ([]siteconfig.RSSFeed, error) {
		for _, f := range feeds {
			if strings.EqualFold(f.URL, feedURL) {
				return nil, fmt.Errorf("that feed is already subscribed")
			}
		}
		return append(feeds, siteconfig.RSSFeed{URL: feedURL, Label: label, Enabled: true}), nil
	})
	if err != nil {
		d.renderLinksList(w, err.Error())
		return
	}

	// Poll the new feed immediately so its items appear without waiting for
	// the next tick — subscribing and then seeing nothing for half an hour
	// looks broken.
	imported := d.Poller.Poll(r.Context(), d.Site())
	slog.Info("feed subscribed", "url", feedURL, "imported", imported)
	if err := d.Engine.Rebuild(); err != nil {
		slog.Error("rebuild after feed add", "err", err)
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

func (d *Deps) adminFeedRemove(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	err = d.mutateFeeds(func(feeds []siteconfig.RSSFeed) ([]siteconfig.RSSFeed, error) {
		if idx < 0 || idx >= len(feeds) {
			return nil, fmt.Errorf("no such feed")
		}
		return append(feeds[:idx:idx], feeds[idx+1:]...), nil
	})
	if err != nil {
		d.renderLinksList(w, err.Error())
		return
	}
	// Links already imported from the feed are deliberately left in place:
	// they're part of the published archive, and re-adding the feed would only
	// re-import them anyway.
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

func (d *Deps) adminFeedToggle(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	err = d.mutateFeeds(func(feeds []siteconfig.RSSFeed) ([]siteconfig.RSSFeed, error) {
		if idx < 0 || idx >= len(feeds) {
			return nil, fmt.Errorf("no such feed")
		}
		out := append([]siteconfig.RSSFeed(nil), feeds...)
		out[idx].Enabled = !out[idx].Enabled
		return out, nil
	})
	if err != nil {
		d.renderLinksList(w, err.Error())
		return
	}
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}

func (d *Deps) adminFeedRefresh(w http.ResponseWriter, r *http.Request) {
	imported := d.Poller.Poll(r.Context(), d.Site())
	if imported > 0 {
		if err := d.Engine.Rebuild(); err != nil {
			slog.Error("rebuild after manual feed refresh", "err", err)
		}
	}
	slog.Info("manual feed refresh", "imported", imported)
	http.Redirect(w, r, "/admin/links", http.StatusSeeOther)
}
