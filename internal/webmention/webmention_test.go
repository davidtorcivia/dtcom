package webmention

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/store"
)

func TestDiscoveryAndBacklinkParsing(t *testing.T) {
	base, _ := url.Parse("https://example.com/post")
	page := []byte(`<html><head><title>A mention</title><link rel="webmention" href="/wm"></head><body><a href="https://mine.example/posts/x">x</a></body></html>`)
	endpoint := endpointFromHTML(page, base)
	if endpoint == nil || endpoint.String() != "https://example.com/wm" {
		t.Fatalf("endpoint = %v", endpoint)
	}
	links, title := pageLinks(page, base)
	if title != "A mention" || len(links) != 1 || links[0].String() != "https://mine.example/posts/x" {
		t.Fatalf("links=%v title=%q", links, title)
	}
	if got := endpointFromLinkHeader(`<https://example.com/endpoint>; rel="webmention"`, base); got == nil || got.String() != "https://example.com/endpoint" {
		t.Fatalf("Link endpoint = %v", got)
	}
}

func TestReceiveVerifiesPublishedTarget(t *testing.T) {
	posts := t.TempDir()
	if err := os.WriteFile(filepath.Join(posts, "2026-01-01-x.md"), []byte("---\ntitle: X\ndate: 2026-01-01\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "mentions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	target := "https://mine.example/posts/x"
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<title>Linked</title><a href="` + target + `">post</a>`))
	}))
	defer source.Close()
	svc := New(st, "https://mine.example", posts)
	svc.client = source.Client()
	if _, err := svc.Receive(context.Background(), source.URL, target); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		items, err := st.ListWebmentions(10)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 1 && items[0].Status == "verified" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("mention was not verified")
}
