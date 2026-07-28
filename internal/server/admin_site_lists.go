package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

// The nav and social lists in site.yml were previously reachable only through
// the REST API, MCP, or a text editor on the server — every other part of the
// config had a form. These handlers give them one, following the same
// load-edit-save-reload path as the feed controls on the Links page.

func registerAdminSiteLists(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("POST /admin/site/nav/add", d.requireAuth(d.adminNavAdd))
	mux.HandleFunc("POST /admin/site/nav/{index}/remove", d.requireAuth(d.adminNavRemove))
	mux.HandleFunc("POST /admin/site/nav/{index}/move", d.requireAuth(d.adminNavMove))
	mux.HandleFunc("POST /admin/site/social/add", d.requireAuth(d.adminSocialAdd))
	mux.HandleFunc("POST /admin/site/social/{index}/remove", d.requireAuth(d.adminSocialRemove))
	mux.HandleFunc("POST /admin/site/social/{index}/move", d.requireAuth(d.adminSocialMove))
}

var (
	errLabelRequired = errors.New("a label is required")
	errHrefRequired  = errors.New("a URL is required")
	errBadHref       = errors.New("that URL scheme isn't allowed — use http://, https://, mailto:, or a site-relative path like /about")
	errBadIcon       = errors.New("pick one of the available icons")
	errNoSuchEntry   = errors.New("no such entry")
)

// mutateSite loads site.yml, hands it to fn, and saves + republishes the
// result. Every handler here goes through it so none mutates the live shared
// config, which the engine reads during Rebuild.
func (d *Deps) mutateSite(fn func(*siteconfig.Config) error) error {
	site, err := siteconfig.Load(d.Cfg.SiteYAMLPath)
	if err != nil {
		return err
	}
	if err := fn(site); err != nil {
		return err
	}
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		return err
	}
	if err := d.reloadSite(); err != nil {
		return err
	}
	// Nav and social links are baked into every generated page's header and
	// footer, so nothing changes on the site until it is rebuilt.
	return d.Engine.Rebuild()
}

// checkedHref trims an href and rejects anything that is not a safe scheme or
// a site-relative path. Reuses the same allowlist that guards inbound RSS
// links, so a nav entry cannot smuggle in a javascript: URL.
func checkedHref(raw string) (string, error) {
	h := strings.TrimSpace(raw)
	if h == "" {
		return "", errHrefRequired
	}
	if safe := store.SanitizeHref(h); safe == "" {
		return "", errBadHref
	}
	return h, nil
}

// moveDelta reads the direction of a reorder request. Anything other than "up"
// moves the entry down, so a malformed value cannot produce a surprising jump.
func moveDelta(r *http.Request) int {
	if r.FormValue("dir") == "up" {
		return -1
	}
	return 1
}

// swapAt moves the entry at idx by delta, returning an error if idx is out of
// range. A move that would fall off either end is a no-op rather than an
// error: the buttons for it are hidden, so reaching this means a double
// submit, and failing that with a red banner would be noise.
func swapAt[T any](list []T, idx, delta int) ([]T, error) {
	if idx < 0 || idx >= len(list) {
		return nil, errNoSuchEntry
	}
	target := idx + delta
	if target < 0 || target >= len(list) {
		return list, nil
	}
	out := append([]T(nil), list...)
	out[idx], out[target] = out[target], out[idx]
	return out, nil
}

func removeAt[T any](list []T, idx int) ([]T, error) {
	if idx < 0 || idx >= len(list) {
		return nil, errNoSuchEntry
	}
	out := append([]T(nil), list[:idx]...)
	return append(out, list[idx+1:]...), nil
}

// siteListAction runs fn and re-renders the Site page with an error banner if
// it fails, rather than replacing the page with a bare error.
func (d *Deps) siteListAction(w http.ResponseWriter, r *http.Request, fn func() error) {
	if err := fn(); err != nil {
		d.renderSiteEdit(w, err.Error())
		return
	}
	http.Redirect(w, r, "/admin/site", http.StatusSeeOther)
}

func formIndex(r *http.Request) (int, error) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		return 0, errNoSuchEntry
	}
	return idx, nil
}

// ---------------------------------------------------------------------------
// Nav
// ---------------------------------------------------------------------------

func (d *Deps) adminNavAdd(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	d.siteListAction(w, r, func() error {
		label := strings.TrimSpace(r.FormValue("label"))
		if label == "" {
			return errLabelRequired
		}
		href, err := checkedHref(r.FormValue("href"))
		if err != nil {
			return err
		}
		return d.mutateSite(func(s *siteconfig.Config) error {
			s.Nav = append(s.Nav, siteconfig.NavLink{Label: label, Href: href})
			return nil
		})
	})
}

func (d *Deps) adminNavRemove(w http.ResponseWriter, r *http.Request) {
	d.siteListAction(w, r, func() error {
		idx, err := formIndex(r)
		if err != nil {
			return err
		}
		return d.mutateSite(func(s *siteconfig.Config) error {
			out, err := removeAt(s.Nav, idx)
			if err != nil {
				return err
			}
			s.Nav = out
			return nil
		})
	})
}

func (d *Deps) adminNavMove(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	d.siteListAction(w, r, func() error {
		idx, err := formIndex(r)
		if err != nil {
			return err
		}
		delta := moveDelta(r)
		return d.mutateSite(func(s *siteconfig.Config) error {
			out, err := swapAt(s.Nav, idx, delta)
			if err != nil {
				return err
			}
			s.Nav = out
			return nil
		})
	})
}

// ---------------------------------------------------------------------------
// Social
// ---------------------------------------------------------------------------

func (d *Deps) adminSocialAdd(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	d.siteListAction(w, r, func() error {
		label := strings.TrimSpace(r.FormValue("label"))
		if label == "" {
			return errLabelRequired
		}
		href, err := checkedHref(r.FormValue("href"))
		if err != nil {
			return err
		}
		icon := strings.TrimSpace(r.FormValue("icon"))
		// An unknown icon renders as empty markup, so the link would appear
		// as an invisible gap in the footer. Refuse it here instead.
		if !build.HasSocialIcon(icon) {
			return errBadIcon
		}
		return d.mutateSite(func(s *siteconfig.Config) error {
			s.Social = append(s.Social, siteconfig.SocialLink{Label: label, Href: href, Icon: icon})
			return nil
		})
	})
}

func (d *Deps) adminSocialRemove(w http.ResponseWriter, r *http.Request) {
	d.siteListAction(w, r, func() error {
		idx, err := formIndex(r)
		if err != nil {
			return err
		}
		return d.mutateSite(func(s *siteconfig.Config) error {
			out, err := removeAt(s.Social, idx)
			if err != nil {
				return err
			}
			s.Social = out
			return nil
		})
	})
}

func (d *Deps) adminSocialMove(w http.ResponseWriter, r *http.Request) {
	if !parseAdminForm(w, r) {
		return
	}
	d.siteListAction(w, r, func() error {
		idx, err := formIndex(r)
		if err != nil {
			return err
		}
		delta := moveDelta(r)
		return d.mutateSite(func(s *siteconfig.Config) error {
			out, err := swapAt(s.Social, idx, delta)
			if err != nil {
				return err
			}
			s.Social = out
			return nil
		})
	})
}
