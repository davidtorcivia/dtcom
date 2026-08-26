package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
	"davidtorcivia.com/dtcom/internal/pgp"
)

func TestPGPAscMissingIs404(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/pgp.asc", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPGPAscAndWKDServed(t *testing.T) {
	d := newTestDeps(t)
	hash := pgp.WKDHash("a")
	if err := os.WriteFile(filepath.Join(d.pubDir, "pgp.asc"), []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nxx\n-----END PGP PUBLIC KEY BLOCK-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wkd := filepath.Join(d.pubDir, ".well-known", "openpgpkey")
	if err := os.MkdirAll(filepath.Join(wkd, "hu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wkd, "policy"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wkd, "hu", hash), []byte("wkd-binary-body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wkd, "example.com", "hu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wkd, "example.com", "policy"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wkd, "example.com", "hu", hash), []byte("wkd-binary-body"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		ct   string
		body string
	}{
		{"/pgp.asc", "application/pgp-keys", "BEGIN PGP PUBLIC KEY BLOCK"},
		{"/.well-known/openpgpkey/policy", "text/plain", "\n"},
		{"/.well-known/openpgpkey/hu/" + hash, "application/octet-stream", "wkd-binary-body"},
		{"/.well-known/openpgpkey/example.com/policy", "text/plain", "\n"},
		{"/.well-known/openpgpkey/example.com/hu/" + hash, "application/octet-stream", "wkd-binary-body"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d", tc.path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.ct) {
			t.Errorf("%s: Content-Type = %q, want %s", tc.path, ct, tc.ct)
		}
		if !strings.Contains(rec.Body.String(), tc.body) && rec.Body.String() != tc.body {
			t.Errorf("%s: body = %q, want to contain %q", tc.path, rec.Body.String(), tc.body)
		}
		head := httptest.NewRequest(http.MethodHead, tc.path, nil)
		headRec := httptest.NewRecorder()
		d.mux.ServeHTTP(headRec, head)
		if headRec.Code != http.StatusOK {
			t.Errorf("HEAD %s: status = %d", tc.path, headRec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/openpgpkey/hu/not-a-hash", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("bogus hu filename: status = %d, want 404", rec.Code)
	}
}

func TestAdminSitePGPTemplateExecutes(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	rec := httptest.NewRecorder()
	d.deps.adminTmpls.render(rec, "site-edit", d.deps.adminData("Site Config", map[string]any{
		"Site":         d.deps.Site(),
		"Icons":        build.SocialIconNames(),
		"PGPEnabled":   true,
		"ContactEmail": "a@example.com",
		"PGP": &pgp.Record{
			Email:       "a@example.com",
			FetchedAt:   time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
			Armored:     "-----BEGIN PGP PUBLIC KEY BLOCK-----\nxx\n-----END PGP PUBLIC KEY BLOCK-----",
			Fingerprint: "0956E8AAE5152D9450B1CA3F4B88B196041350BC",
		},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"keys.openpgp.org",
		"a@example.com",
		"0956 E8AA E515 2D94 50B1 CA3F 4B88 B196 0413 50BC",
		"/admin/site/pgp/refresh",
		"Refresh key",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin site missing %q", want)
		}
	}
}

func TestAdminPGPRefreshRequiresAuth(t *testing.T) {
	d := newTestDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/site/pgp/refresh", nil)
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
}
