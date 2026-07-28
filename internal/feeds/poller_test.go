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
	n := poller.Poll(context.Background(), site)
	if n != 1 {
		t.Errorf("imported %d items, want 1", n)
	}
	// second poll: dedup
	n2 := poller.Poll(context.Background(), site)
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
	n := poller.Poll(context.Background(), site)
	if n != 0 {
		t.Errorf("disabled feed imported %d, want 0", n)
	}
	if hits != 0 {
		t.Errorf("disabled feed was fetched %d times, want 0", hits)
	}
}

// TestPollOnPollCallback verifies the OnPoll callback fires with the import
// count after a successful Poll. main.go wires this to trigger a rebuild when
// imported > 0; the contract that needs to hold here is: it fires, and the
// value equals the count of newly imported items.
func TestPollOnPollCallback(t *testing.T) {
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

	called := 0
	poller := NewPoller(st)
	poller.OnPoll = func(n int) { called += n }
	site := &siteconfig.Config{RSSFeeds: []siteconfig.RSSFeed{{URL: srv.URL, Label: "Sub", Enabled: true}}}
	poller.Poll(context.Background(), site)
	if called != 1 {
		t.Errorf("OnPoll total = %d, want 1", called)
	}
	// second poll: dedup — OnPoll fires with 0 imports.
	poller.Poll(context.Background(), site)
	if called != 1 {
		t.Errorf("OnPoll total after dedup = %d, want still 1", called)
	}
}

// TestPollContinuesPastDeadFeed verifies a single dead feed does not block the
// others — the live feed's items should still import even though the first
// feed's URL returns garbage.
func TestPollContinuesPastDeadFeed(t *testing.T) {
	// dead server: returns 500
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()

	// live server: one item
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel>
	<title>Live</title><item><title>Live Post</title><link>https://live.com/p/1</link><pubDate>Mon, 02 Jan 2026 00:00:00 +0000</pubDate></item>
</channel></rss>`))
	}))
	defer live.Close()

	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	poller := NewPoller(st)
	site := &siteconfig.Config{RSSFeeds: []siteconfig.RSSFeed{
		{URL: dead.URL, Label: "Dead", Enabled: true},
		{URL: live.URL, Label: "Live", Enabled: true},
	}}
	n := poller.Poll(context.Background(), site)
	if n != 1 {
		t.Errorf("imported %d, want 1 (dead feed must not block live feed)", n)
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
