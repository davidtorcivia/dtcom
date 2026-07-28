package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// adminUpload posts a multipart file to an admin route with a valid session.
func (d *testDeps) adminUpload(t *testing.T, path, field, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	sess := httptest.NewRecorder()
	if err := d.deps.Auth.SetSession(sess, "admin"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(sess.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 250, G: 204, B: 21, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (d *testDeps) favicon(t *testing.T) string {
	t.Helper()
	site, err := siteconfig.Load(d.deps.Cfg.SiteYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	return site.Favicon
}

// A raster upload has to be stored, pointed at from site.yml, and actually
// served — the URL in the config is worthless if /images/ can't resolve it.
func TestAdminFaviconUploadPNG(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := d.adminUpload(t, "/admin/site/favicon", "favicon", "icon.png", testPNG(t))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	url := d.favicon(t)
	if !strings.HasPrefix(url, "/images/") || !strings.HasSuffix(url, ".png") {
		t.Fatalf("favicon = %q, want an /images/*.png URL", url)
	}
	if got := d.deps.Site().Favicon; got != url {
		t.Errorf("live config favicon = %q, want %q", got, url)
	}
	if _, err := os.Stat(filepath.Join(d.deps.Cfg.ImagesDir, filepath.Base(url))); err != nil {
		t.Errorf("favicon file not written: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, url, nil)
	served := httptest.NewRecorder()
	d.mux.ServeHTTP(served, req)
	if served.Code != http.StatusOK {
		t.Errorf("GET %s = %d, want 200", url, served.Code)
	}
}

// SVG cannot go through the image decoder, so it takes its own path. It must
// still end up stored and referenced.
func TestAdminFaviconUploadSVG(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32"/></svg>`)
	if rec := d.adminUpload(t, "/admin/site/favicon", "favicon", "icon.svg", svg); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	url := d.favicon(t)
	if !strings.HasSuffix(url, ".svg") {
		t.Fatalf("favicon = %q, want an .svg URL", url)
	}
	stored, err := os.ReadFile(filepath.Join(d.deps.Cfg.ImagesDir, filepath.Base(url)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, svg) {
		t.Error("SVG was modified in storage; it should be written through as-is")
	}
}

// A file that is neither a decodable image nor a real SVG document must be
// refused rather than stored and later served as an image.
func TestAdminFaviconRejectsNonImage(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"plain text", []byte("this is not an icon")},
		// Looks like SVG to a naive prefix check, but is not well-formed XML.
		{"fake svg", []byte("<svg>not really<")},
		// Well-formed XML, but the root element is not <svg>.
		{"xml but not svg", []byte(`<note><to>you</to></note>`)},
		{"html", []byte("<!DOCTYPE html><html><body>hi</body></html>")},
		// A truncated PNG: the magic bytes are right, the image is not.
		{"truncated png", append([]byte("\x89PNG\r\n\x1a\n"), 0x00, 0x01, 0x02)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh server per case: sharing one would let an earlier
			// success mask a later failure to reject.
			d := newTestDepsWithAdmin(t)
			rec := d.adminUpload(t, "/admin/site/favicon", "favicon", "x.svg", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("upload = %d, want 400", rec.Code)
			}
			if url := d.favicon(t); url != "" {
				t.Errorf("favicon was set to %q despite a rejected upload", url)
			}
		})
	}
}

// Reset clears the pointer so the built-in icon comes back.
func TestAdminFaviconReset(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	if rec := d.adminUpload(t, "/admin/site/favicon", "favicon", "icon.png", testPNG(t)); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d", rec.Code)
	}
	if d.favicon(t) == "" {
		t.Fatal("favicon not set by upload")
	}
	if rec := d.adminPost(t, "/admin/site/favicon/reset", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("reset = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if url := d.favicon(t); url != "" {
		t.Errorf("favicon = %q after reset, want empty", url)
	}
}

// Saving the Site form must not wipe a favicon it does not expose as a field.
func TestAdminSiteSavePreservesFavicon(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	if rec := d.adminUpload(t, "/admin/site/favicon", "favicon", "icon.png", testPNG(t)); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d", rec.Code)
	}
	want := d.favicon(t)
	rec := d.adminPost(t, "/admin/site", map[string][]string{
		"title": {"New Title"}, "description": {"d"}, "bio": {"one"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("site save = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if got := d.favicon(t); got != want {
		t.Errorf("favicon = %q after saving the site form, want %q preserved", got, want)
	}
}

// The uploaded icon has to reach the generated pages, not just site.yml.
func TestFaviconAppearsInRenderedHead(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	if rec := d.adminUpload(t, "/admin/site/favicon", "favicon", "icon.png", testPNG(t)); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d", rec.Code)
	}
	url := d.favicon(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `<link rel="icon" href="`+url+`">`) {
		t.Errorf("home page head does not reference the uploaded favicon %q", url)
	}
	if strings.Contains(body, "/static/favicon.svg") {
		t.Error("home page still links the built-in favicon after an upload")
	}
}

// Tightening isSVG to require a well-formed parse must not start rejecting
// real files. These are the shapes that actually turn up: the built-in icon,
// an XML declaration, a DOCTYPE, and named entities.
func TestIsSVGAcceptsRealFiles(t *testing.T) {
	builtin, err := os.ReadFile(filepath.Join("..", "..", "static", "favicon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"the built-in favicon", builtin},
		{"xml declaration", []byte(`<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)},
		{"doctype", []byte(`<?xml version="1.0"?><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg xmlns="http://www.w3.org/2000/svg"><g/></svg>`)},
		{"named entity", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><title>a&nbsp;b</title></svg>`)},
		{"leading whitespace", []byte("\n  <svg xmlns=\"http://www.w3.org/2000/svg\"/>\n")},
		{"trailing comment", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/><!-- built by hand -->`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !isSVG(tc.body) {
				t.Errorf("isSVG rejected a valid SVG:\n%s", tc.body)
			}
		})
	}
}
