package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" fill="#FACC15"/></svg>`

// SVG cannot go through the raster pipeline — the decoder cannot open a
// drawing — so it takes its own path and is stored as written. Before this it
// was rejected outright as an unsupported image.
func TestUploadSVG(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := d.adminUpload(t, "/admin/images", "file", "chart.svg", []byte(sampleSVG))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, ".svg") {
		t.Fatalf("response does not name an svg:\n%s", body)
	}

	url := extractJSONField(t, body, "url")
	if !strings.HasPrefix(url, "/images/") || !strings.HasSuffix(url, ".svg") {
		t.Fatalf("url = %q, want /images/<hash>.svg", url)
	}
	stored, err := os.ReadFile(filepath.Join(d.deps.Cfg.ImagesDir, filepath.Base(url)))
	if err != nil {
		t.Fatal(err)
	}
	// A vector has no resampling to do; it must come back exactly as uploaded.
	if string(stored) != sampleSVG {
		t.Errorf("svg was altered in storage:\ngot  %s\nwant %s", stored, sampleSVG)
	}
}

// The markdown handed back is what gets pasted into the post, so it has to
// point at the stored file.
func TestUploadSVGReturnsUsableMarkdown(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := d.adminUpload(t, "/admin/images", "file", "chart.svg", []byte(sampleSVG))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d", rec.Code)
	}
	md := extractJSONField(t, rec.Body.String(), "markdown")
	if !strings.HasPrefix(md, "![](/images/") || !strings.HasSuffix(md, ".svg)") {
		t.Errorf("markdown = %q", md)
	}
}

// An uploaded SVG has to be fetchable, and served as an SVG rather than as a
// download or as text.
func TestUploadedSVGIsServed(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := d.adminUpload(t, "/admin/images", "file", "chart.svg", []byte(sampleSVG))
	url := extractJSONField(t, rec.Body.String(), "url")

	got := d.get(url)
	if got.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", url, got.Code)
	}
	if ct := got.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
		t.Errorf("content-type = %q, want image/svg+xml", ct)
	}
	// SVG is a document: navigate to one and the browser parses it. The
	// site-wide policy already bars inline script; an upload should not be able
	// to reach the network at all.
	csp := got.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("svg served under the general policy, not the strict one: %q", csp)
	}
	if !strings.Contains(csp, "sandbox") {
		t.Errorf("svg policy is missing the sandbox directive: %q", csp)
	}
}

// A raster upload must keep the ordinary policy — tightening everything to
// 'none' would be a silent change to how the rest of the site is served.
func TestUploadedRasterKeepsTheGeneralPolicy(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := d.adminUpload(t, "/admin/images", "file", "pic.png", testPNG(t))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	url := extractJSONField(t, rec.Body.String(), "url")
	got := d.get(url)
	if csp := got.Header().Get("Content-Security-Policy"); strings.Contains(csp, "default-src 'none'") {
		t.Errorf("png was served under the svg policy: %q", csp)
	}
}

// Claiming to be an SVG is not enough. The file is checked by parsing, so
// something that merely opens with "<svg" cannot be stored and then handed back
// with an image/svg+xml content type.
func TestUploadRejectsFakeSVG(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"not well formed", `<svg>unclosed`},
		{"xml but not svg", `<note><to>you</to></note>`},
		{"html", `<!DOCTYPE html><html><body>hi</body></html>`},
		{"plain text", `just some text`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDepsWithAdmin(t)
			rec := d.adminUpload(t, "/admin/images", "file", "x.svg", []byte(tc.body))
			if rec.Code == http.StatusCreated {
				t.Errorf("upload accepted %q:\n%s", tc.name, rec.Body.String())
			}
		})
	}
}

// The API and MCP clients share the same storage path as the admin UI, so SVG
// has to work there too rather than only in the browser.
func TestAPIUploadSVG(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := d.apiUpload(t, "/api/v1/images", "file", "chart.svg", []byte(sampleSVG))
	if rec.Code != http.StatusCreated {
		t.Fatalf("api upload = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if url := extractJSONField(t, rec.Body.String(), "url"); !strings.HasSuffix(url, ".svg") {
		t.Errorf("url = %q, want an .svg", url)
	}
}

// extractJSONField pulls one string field out of a small JSON response without
// pulling in a struct for it.
func extractJSONField(t *testing.T, body, field string) string {
	t.Helper()
	key := `"` + field + `":"`
	i := strings.Index(body, key)
	if i < 0 {
		t.Fatalf("no %q in response: %s", field, body)
	}
	rest := body[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unterminated %q in response: %s", field, body)
	}
	return strings.ReplaceAll(rest[:j], `\/`, "/")
}

// apiUpload posts a multipart file with bearer auth rather than a session.
func (d *testDeps) apiUpload(t *testing.T, path, field, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := multipartRequest(t, path, field, filename, body)
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec
}
