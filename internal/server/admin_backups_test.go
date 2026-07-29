package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"davidtorcivia.com/dtcom/internal/backup"
)

// withBackups gives a test-deps set a working backup service over its own
// temporary directories.
func withBackups(t *testing.T, td *testDeps) *backup.Service {
	t.Helper()
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		ContentDir: td.deps.Cfg.ContentDir,
		ImagesDir:  td.deps.Cfg.ImagesDir,
		DBPath:     td.deps.Cfg.DBPath,
	}, backup.NewLocal(dir), td.deps.Store)
	td.deps.Backups = svc
	return svc
}

func adminCookie(t *testing.T, td *testDeps) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := td.deps.Auth.SetSession(rec, "admin"); err != nil {
		t.Fatal(err)
	}
	return rec.Result().Cookies()[0]
}

func do(t *testing.T, td *testDeps, method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	td.mux.ServeHTTP(rec, req)
	return rec
}

var archiveNameRe = regexp.MustCompile(`dtcom-\d{8}T\d{6}Z-[a-z-]+\.tar\.gz`)

// TestBackupsPageFlow walks the page the way a person would: take a backup,
// see it listed, download it, and put it back — through the real routes, with
// the real templates.
func TestBackupsPageFlow(t *testing.T) {
	td := newTestDepsWithAdmin(t)
	svc := withBackups(t, td)
	cookie := adminCookie(t, td)

	// A post to lose and get back.
	post := filepath.Join(td.deps.Cfg.ContentDir, "posts", "keeper.md")
	if err := os.MkdirAll(filepath.Dir(post), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(post, []byte("---\ntitle: Keeper\n---\n\nBody."), 0o644); err != nil {
		t.Fatal(err)
	}

	if rec := do(t, td, http.MethodGet, "/admin/backups", nil, cookie); rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/backups = %d\n%s", rec.Code, rec.Body.String())
	}

	rec := do(t, td, http.MethodPost, "/admin/backups", url.Values{}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/backups = %d\n%s", rec.Code, rec.Body.String())
	}

	list, err := svc.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("after taking one backup: %d archives, err %v", len(list), err)
	}
	name := list[0].Name

	// It shows up on the page, with its download link.
	rec = do(t, td, http.MethodGet, "/admin/backups", nil, cookie)
	body := rec.Body.String()
	if !strings.Contains(body, name) {
		t.Errorf("archive not listed:\n%s", body)
	}
	if !archiveNameRe.MatchString(body) {
		t.Errorf("page shows no archive name:\n%s", body)
	}

	// Download returns the bytes as an attachment.
	rec = do(t, td, http.MethodGet, "/admin/backups/"+name+"/download", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, name) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if rec.Body.Len() == 0 {
		t.Error("download was empty")
	}
	// The archive is private data and must not be cached anywhere.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q", cc)
	}

	// Lose the post.
	if err := os.Remove(post); err != nil {
		t.Fatal(err)
	}

	// A restore without the typed confirmation must do nothing.
	rec = do(t, td, http.MethodPost, "/admin/backups/"+name+"/restore", url.Values{"confirm": {"nope"}}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "err=") {
		t.Errorf("a wrong confirmation was not reported: %s", rec.Header().Get("Location"))
	}
	if _, err := os.Stat(post); !os.IsNotExist(err) {
		t.Fatal("the unconfirmed restore restored anyway")
	}

	// With the right one, the post comes back.
	confirm := list[0].Created.Local().Format("2006-01-02")
	rec = do(t, td, http.MethodPost, "/admin/backups/"+name+"/restore", url.Values{"confirm": {confirm}}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d\n%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "err=") {
		t.Fatalf("restore reported an error: %s", loc)
	}
	if _, err := os.Stat(post); err != nil {
		t.Errorf("post not restored: %v", err)
	}

	// And the state from immediately before is kept.
	after, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	var safety int
	for _, in := range after {
		if in.Kind == backup.KindPreRestore {
			safety++
		}
	}
	if safety != 1 {
		t.Errorf("expected one pre-restore archive, got %d of %d", safety, len(after))
	}

	// Delete removes it.
	rec = do(t, td, http.MethodPost, "/admin/backups/"+name+"/delete", url.Values{}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rec.Code)
	}
	final, _ := svc.List()
	for _, in := range final {
		if in.Name == name {
			t.Error("archive still listed after delete")
		}
	}
}

// TestBackupRoutesRequireAuth: these routes read and replace the whole site.
func TestBackupRoutesRequireAuth(t *testing.T) {
	td := newTestDepsWithAdmin(t)
	withBackups(t, td)

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/admin/backups"},
		{http.MethodPost, "/admin/backups"},
		{http.MethodGet, "/admin/backups/dtcom-20260101T000000Z-manual.tar.gz/download"},
		{http.MethodPost, "/admin/backups/dtcom-20260101T000000Z-manual.tar.gz/restore"},
		{http.MethodPost, "/admin/backups/dtcom-20260101T000000Z-manual.tar.gz/delete"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		td.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s %s: status %d, location %q — expected a redirect to login",
				tc.method, tc.path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// TestBackupDownloadRejectsTraversal: the name comes off the URL path.
func TestBackupDownloadRejectsTraversal(t *testing.T) {
	td := newTestDepsWithAdmin(t)
	withBackups(t, td)
	cookie := adminCookie(t, td)

	for _, name := range []string{
		"..%2f..%2fetc%2fpasswd",
		"dtcom-20260101T000000Z-manual.tar.gz%2f..%2f..%2fetc%2fpasswd",
		"site.yml",
	} {
		rec := do(t, td, http.MethodGet, "/admin/backups/"+name+"/download", nil, cookie)
		if rec.Code == http.StatusOK {
			t.Errorf("download of %q was served", name)
		}
	}
}
