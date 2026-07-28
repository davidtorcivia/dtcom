package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"
)

func TestPollFeedImportsItems(t *testing.T) {
	// fake RSS server
	rssXML := `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Sub</title><item><title>Post 1</title><link>https://sub.com/p/1</link><pubDate>Mon, 02 Jan 2026 00:00:00 +0000</pubDate></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssXML))
	}))
	defer srv.Close()

	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	poller := NewPoller(st)
	site := &siteconfig.Config{RSSFeeds: []siteconfig.RSSFeed{{URL: srv.URL, Label: "Sub", Enabled: true}}}
	n, err := poller.Poll(site)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if n != 1 {
		t.Errorf("imported %d items, want 1", n)
	}
	// second poll: dedup
	n2, _ := poller.Poll(site)
	if n2 != 0 {
		t.Errorf("re-poll imported %d, want 0 (dedup)", n2)
	}
}

func TestPollSkipsDisabledFeeds(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel>
<title>Sub</title><item><title>X</title><link>https://sub.com/p/x</link><pubDate>Mon, 02 Jan 2026 00:00:00 +0000</pubDate></item>
</channel></rss>`))
	}))
	defer srv.Close()

	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	poller := NewPoller(st)
	site := &siteconfig.Config{RSSFeeds: []siteconfig.RSSFeed{{URL: srv.URL, Label: "Sub", Enabled: false}}}
	n, err := poller.Poll(site)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if n != 0 {
		t.Errorf("disabled feed imported %d, want 0", n)
	}
	if hits != 0 {
		t.Errorf("disabled feed was fetched %d times, want 0", hits)
	}
}

func TestStartStopsOnContextCancel(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	poller := NewPoller(st)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		poller.Start(ctx, func() *siteconfig.Config { return &siteconfig.Config{} }, 10*time.Millisecond)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// Start returned promptly after cancel — good.
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within 2s of context cancellation")
	}
}
