package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// A favicon is small by definition, so the ceiling is far below the one for
// post images. Anything near this is a mistake worth rejecting early.
const maxFaviconUpload = 1 << 20 // 1 MB

// faviconMaxDim bounds a raster favicon. Browsers ask for 16-64px and take the
// largest available for other uses (touch icons, tab previews); 512 covers
// every one of those without storing a photograph.
const faviconMaxDim = 512

var errFaviconUnsupported = errors.New("favicon must be a PNG, JPEG, GIF, WebP, or SVG file — .ico is not supported, convert it to a PNG")

// adminFaviconUpload replaces the site favicon with an uploaded file.
//
// The file is stored beside post images, content-addressed, and site.yml is
// pointed at it. That reuses the /images/ handler and its immutable cache
// header rather than adding a mutable /favicon route: a changed favicon gets a
// new URL, so no browser is left holding a stale one.
func (d *Deps) adminFaviconUpload(w http.ResponseWriter, r *http.Request) {
	url, err := d.storeFavicon(w, r)
	if err != nil {
		d.renderSiteEdit(w, faviconErrorMessage(err))
		return
	}
	if err := d.setFavicon(url); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/site", http.StatusSeeOther)
}

// adminFaviconReset drops a custom favicon and falls back to the built-in one.
// The stored file is deliberately left in place: it is content-addressed and
// may still be referenced by a cached page, and reclaiming a few KB is not
// worth the risk of deleting something still in use.
func (d *Deps) adminFaviconReset(w http.ResponseWriter, r *http.Request) {
	if err := d.setFavicon(""); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/admin/site", http.StatusSeeOther)
}

// setFavicon writes the URL into site.yml and republishes the site.
//
// As everywhere else that touches site.yml, the edit is applied to a freshly
// loaded copy rather than to the live shared pointer, which would race the
// engine's reads during Rebuild.
func (d *Deps) setFavicon(url string) error {
	site, err := siteconfig.Load(d.Cfg.SiteYAMLPath)
	if err != nil {
		return err
	}
	site.Favicon = url
	if err := siteconfig.Save(d.Cfg.SiteYAMLPath, site); err != nil {
		return err
	}
	if err := d.reloadSite(); err != nil {
		return err
	}
	// The favicon link lives in every generated page's <head>, so the change
	// is only visible once the site is rebuilt.
	return d.Engine.Rebuild()
}

func faviconErrorMessage(err error) string {
	switch {
	case errors.Is(err, errFaviconUnsupported), errors.Is(err, build.ErrUnsupportedImage):
		return errFaviconUnsupported.Error()
	case errors.Is(err, errImageTooLarge):
		return "That file is too large for a favicon — the limit is 1 MB."
	case errors.Is(err, errNoImageField):
		return "No file was attached."
	default:
		return "Could not save that favicon."
	}
}

// storeFavicon validates an uploaded favicon and writes it into ImagesDir,
// returning the URL it is served at.
//
// SVG takes a separate path from the raster formats because it is not an image
// the decoder can open — it is a document. It is checked by parsing rather
// than by trusting the extension or sniffing a prefix, and stored as-is.
func (d *Deps) storeFavicon(w http.ResponseWriter, r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFaviconUpload)
	defer r.Body.Close()

	if err := r.ParseMultipartForm(maxFaviconUpload); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return "", errImageTooLarge
		}
		return "", fmt.Errorf("%w: %v", errNoImageField, err)
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("favicon")
	if err != nil {
		return "", errNoImageField
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errNoImageField
	}

	data, ext := raw, ""
	if isSVG(raw) {
		ext = ".svg"
	} else {
		img, err := build.ProcessImage(bytes.NewReader(raw), faviconMaxDim)
		if err != nil {
			return "", err
		}
		data, ext = img.Data, img.Ext
	}

	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])[:32] + ext
	if err := os.MkdirAll(d.Cfg.ImagesDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(d.Cfg.ImagesDir, name)
	if _, statErr := os.Stat(path); statErr != nil {
		if err := writeFileAtomic(path, data); err != nil {
			return "", err
		}
	}
	if header != nil {
		slog.Info("favicon uploaded", "name", name, "original", header.Filename, "bytes", len(data))
	}
	return "/images/" + name, nil
}

// isSVG reports whether raw parses as XML whose root element is <svg>.
//
// Checking by parse rather than by extension or a "<svg" prefix means a file
// that merely looks like SVG cannot be stored and later served with an
// image/svg+xml content type. The document is served same-origin, so the
// site's CSP (script-src 'self', no unsafe-inline) is what stops a script
// inside a hand-crafted SVG from running; this only establishes that the bytes
// really are an SVG document.
func isSVG(raw []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	// Strict stays on. With it off the decoder tolerates malformed markup and
	// happily reports a root <svg> for input like "<svg>not really<", which
	// would then be stored and served as image/svg+xml.
	//
	// SVG is XML by definition, so a real file always parses. Named HTML
	// entities are permitted because they appear in hand-authored files and
	// would otherwise be an "unknown entity" error.
	dec.Entity = xml.HTMLEntity

	root := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			// Well-formed all the way to the end.
			return root
		}
		if err != nil {
			return false
		}
		if start, ok := tok.(xml.StartElement); ok && !root {
			if start.Name.Local != "svg" {
				return false
			}
			root = true
		}
	}
}
