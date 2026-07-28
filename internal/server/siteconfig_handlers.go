package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// httpStatusErr wraps an error with the HTTP status a handler should emit.
type httpStatusErr struct {
	status int
	err    error
}

func (e *httpStatusErr) Error() string {
	if e.err == nil {
		return fmt.Sprintf("status %d", e.status)
	}
	return e.err.Error()
}

func statusErr(status int, err error) error {
	return &httpStatusErr{status: status, err: err}
}

// httpToStatus maps an error to an HTTP status. httpStatusErr carries its own;
// anything else is treated as a 500.
func httpToStatus(err error) int {
	if se, ok := err.(*httpStatusErr); ok {
		return se.status
	}
	return http.StatusInternalServerError
}

// decodeJSONReader is like decodeJSON but takes a raw io.Reader (used by the
// site-section endpoint which receives r.Body directly).
func decodeJSONReader(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// updateSiteSection replaces one of the list-valued site.yml sections. It
// reads the JSON body, mutates the live *siteconfig.Config (returned by
// d.Site()), writes it back to disk, and rebuilds. The body is closed.
func (d *Deps) updateSiteSection(section string, body io.Reader) error {
	defer func() {
		if c, ok := body.(io.Closer); ok {
			_ = c.Close()
		}
	}()

	site := d.Site()
	switch section {
	case "bio":
		var v []string
		if err := decodeJSONReader(body, &v); err != nil {
			return statusErr(http.StatusBadRequest, err)
		}
		site.Bio = v
	case "nav":
		var v []siteconfig.NavLink
		if err := decodeJSONReader(body, &v); err != nil {
			return statusErr(http.StatusBadRequest, err)
		}
		site.Nav = v
	case "social":
		var v []siteconfig.SocialLink
		if err := decodeJSONReader(body, &v); err != nil {
			return statusErr(http.StatusBadRequest, err)
		}
		site.Social = v
	case "rss_feeds":
		var v []siteconfig.RSSFeed
		if err := decodeJSONReader(body, &v); err != nil {
			return statusErr(http.StatusBadRequest, err)
		}
		site.RSSFeeds = v
	case "footer_left":
		var v []string
		if err := decodeJSONReader(body, &v); err != nil {
			return statusErr(http.StatusBadRequest, err)
		}
		site.FooterLeft = v
	default:
		return statusErr(http.StatusNotFound, fmt.Errorf("unknown site section %q", section))
	}
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		return statusErr(http.StatusInternalServerError, err)
	}
	if err := d.Engine.Rebuild(); err != nil {
		return statusErr(http.StatusInternalServerError, err)
	}
	return nil
}
