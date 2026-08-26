package pgp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testCache(t *testing.T, srv *httptest.Server) *Cache {
	t.Helper()
	c := New(filepath.Join(t.TempDir(), "pgp.json"))
	c.BaseURL = srv.URL
	c.Client = srv.Client()
	c.Now = func() time.Time {
		return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	}
	return c
}

func keyserver(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestResolveFetchesAndCaches(t *testing.T) {
	var hits atomic.Int32
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/vks/v1/by-email/a@b.c" {
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	})
	c := testCache(t, srv)

	rec, err := c.Resolve("A@B.C", false)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Found() {
		t.Fatal("expected a key")
	}
	if rec.Email != "a@b.c" {
		t.Errorf("email = %q", rec.Email)
	}
	if rec.Fingerprint != "0956E8AAE5152D9450B1CA3F4B88B196041350BC" {
		t.Errorf("fingerprint = %q", rec.Fingerprint)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}

	again, err := c.Resolve("a@b.c", false)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Found() {
		t.Fatal("cache lost the key")
	}
	if hits.Load() != 1 {
		t.Fatalf("fresh cache hit the network; hits = %d", hits.Load())
	}
}

func TestResolveForceRefetches(t *testing.T) {
	var hits atomic.Int32
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	})
	c := testCache(t, srv)
	if _, err := c.Resolve("a@b.c", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resolve("a@b.c", true); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("force should refetch; hits = %d", hits.Load())
	}
}

func TestResolveTTLExpiry(t *testing.T) {
	var hits atomic.Int32
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	})
	c := testCache(t, srv)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }

	if _, err := c.Resolve("a@b.c", false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(23 * time.Hour)
	if _, err := c.Resolve("a@b.c", false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("23h should still be fresh; hits = %d", hits.Load())
	}
	now = now.Add(2 * time.Hour)
	if _, err := c.Resolve("a@b.c", false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("TTL expiry should refetch; hits = %d", hits.Load())
	}
}

func TestResolveEmailChangeRefetches(t *testing.T) {
	var lastPath string
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	})
	c := testCache(t, srv)
	if _, err := c.Resolve("a@b.c", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resolve("other@b.c", false); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/vks/v1/by-email/other@b.c" {
		t.Fatalf("path = %s", lastPath)
	}
	got, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "other@b.c" {
		t.Errorf("cached email = %q", got.Email)
	}
}

func TestResolveNotFoundIsCached(t *testing.T) {
	var hits atomic.Int32
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	})
	c := testCache(t, srv)
	rec, err := c.Resolve("nobody@b.c", false)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Found() {
		t.Fatal("404 should not be a key")
	}
	if _, err := c.Resolve("nobody@b.c", false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("negative cache missed; hits = %d", hits.Load())
	}
}

func TestResolveKeepsCacheOnError(t *testing.T) {
	var ok atomic.Bool
	ok.Store(true)
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		if !ok.Load() {
			http.Error(w, "nope", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	})
	c := testCache(t, srv)
	if _, err := c.Resolve("a@b.c", false); err != nil {
		t.Fatal(err)
	}
	ok.Store(false)
	rec, err := c.Resolve("a@b.c", true)
	if err == nil {
		t.Fatal("expected the 502")
	}
	if !rec.Found() {
		t.Fatal("error should still return the last good key")
	}
}

func TestResolveEmptyEmailClearsCache(t *testing.T) {
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pgp-keys")
		_, _ = w.Write([]byte(testArmored))
	})
	c := testCache(t, srv)
	if _, err := c.Resolve("a@b.c", false); err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve("", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %+v", got)
	}
	if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
		t.Fatalf("cache file still there: %v", err)
	}
}

func TestResolveNilCache(t *testing.T) {
	var c *Cache
	rec, err := c.Resolve("a@b.c", false)
	if rec != nil || err != nil {
		t.Fatalf("got %v %v", rec, err)
	}
}

func TestResolveRejectsPrivateKey(t *testing.T) {
	srv := keyserver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nd2tkLWJpbmFyeS1ib2R5\n-----END PGP PRIVATE KEY BLOCK-----\n"))
	})
	c := testCache(t, srv)
	rec, err := c.Resolve("a@b.c", false)
	if err == nil {
		t.Fatal("accepted a private key")
	}
	if rec != nil {
		t.Fatalf("stored a private key: %+v", rec)
	}
}

func TestCanonicalEmail(t *testing.T) {
	if got := CanonicalEmail("  David@Example.COM "); got != "david@example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestEncodeEmail(t *testing.T) {
	if got := encodeEmail("a@b.c"); got != "a%40b.c" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(encodeEmail("a+tag@b.c"), "%40") {
		t.Fatal("plus-address lost the at-sign encoding")
	}
}
