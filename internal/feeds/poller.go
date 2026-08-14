package feeds

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"

	"github.com/mmcdole/gofeed"
)

// Timeouts and bounds for polling third-party feeds. These are somebody else's
// servers: a feed that hangs, redirects in a loop, or streams a gigabyte must
// not stall the poller (or the /api/v1/feeds/refresh request that triggered
// it) or exhaust memory.
const (
	// perFeedTimeout bounds one feed fetch end to end.
	perFeedTimeout = 20 * time.Second
	// maxFeedBytes caps a single feed response.
	maxFeedBytes = 8 << 20 // 8 MB
	// maxItemsPerFeed caps how many entries of one feed are imported per poll.
	maxItemsPerFeed = 100
	// maxFeeds caps how many feeds one poll will contact.
	maxFeeds = 50
)

// limitedTransport caps how many bytes a feed response can contribute, so a
// feed that streams without end (or a misconfigured URL pointing at a large
// file) can't exhaust memory. The cap applies after transparent decompression,
// which is where a "zip bomb" feed would otherwise expand.
type limitedTransport struct {
	base http.RoundTripper
	max  int64
}

func (t limitedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = limitedBody{Reader: io.LimitReader(resp.Body, t.max), closer: resp.Body}
	return resp, nil
}

type limitedBody struct {
	io.Reader
	closer io.Closer
}

func (b limitedBody) Close() error { return b.closer.Close() }

// feedTransport blocks fetches of private and link-local addresses. Feed
// URLs are configured by the site owner, but the API/MCP surface can write
// them with a bearer token — this keeps a leaked token from turning the
// poller into a scheduled internal-network probe (RFC1918 services, the
// link-local cloud metadata endpoint) whose findings land on /links.
// Loopback is allowed: a self-hosted local feed is a legitimate target, and
// the only thing on this host's loopback is dtcom's own auth-protected
// surface.
func feedTransport(max int64) http.RoundTripper {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	base := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if ip := net.ParseIP(host); ip != nil &&
				(ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
				return nil, fmt.Errorf("feed fetch to private address %s refused", addr)
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return limitedTransport{base: base, max: max}
}

type Poller struct {
	store *store.Store
	fp    *gofeed.Parser
	// OnPoll, if non-nil, is called after each Poll with the count of newly
	// imported items. main.go wires it to trigger a rebuild when imported > 0
	// so RSS-imported links surface on /links without waiting for some other
	// event to trigger one.
	OnPoll func(imported int)
}

func NewPoller(st *store.Store) *Poller {
	fp := gofeed.NewParser()
	// gofeed's zero-value client has no timeout at all, so a feed server that
	// accepts the connection and never responds would block the poller
	// forever.
	fp.Client = &http.Client{
		Timeout:   perFeedTimeout,
		Transport: feedTransport(maxFeedBytes),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				// An error, not ErrUseLastResponse: handing the 3xx body to
				// the parser would misreport a redirect loop as a parse
				// failure.
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		},
	}
	fp.UserAgent = "dtcom-feed-reader/1.0 (+https://github.com/dtorcivia)"
	return &Poller{store: st, fp: fp}
}

// Poll fetches every enabled feed in site.RSSFeeds and upserts new items.
// Per-feed errors are logged via slog.Warn and skipped — one dead feed does
// not block the others. Returns the count of newly imported items. Items whose
// href was already present are counted as dedup (via store.UpsertRSSLink's
// inserted flag). Items with disallowed href schemes (javascript:/data:) are
// silently dropped by UpsertRSSLink.
//
// ctx bounds the whole poll: cancelling it (process shutdown, a cancelled
// refresh request) stops the loop between feeds and aborts the in-flight fetch.
func (p *Poller) Poll(ctx context.Context, site *siteconfig.Config) int {
	imported := 0
	if site == nil {
		return 0
	}
	polled := 0
	for _, f := range site.RSSFeeds {
		if !f.Enabled || f.URL == "" {
			continue
		}
		if ctx.Err() != nil {
			slog.Info("rss poll cancelled", "imported", imported)
			break
		}
		if polled++; polled > maxFeeds {
			slog.Warn("rss poll truncated", "limit", maxFeeds, "configured", len(site.RSSFeeds))
			break
		}
		imported += p.pollOne(ctx, f)
	}
	if p.OnPoll != nil {
		p.OnPoll(imported)
	}
	return imported
}

// pollOne fetches and imports a single feed, returning the number of new items.
func (p *Poller) pollOne(ctx context.Context, f siteconfig.RSSFeed) int {
	fetchCtx, cancel := context.WithTimeout(ctx, perFeedTimeout)
	defer cancel()

	feed, err := p.fp.ParseURLWithContext(f.URL, fetchCtx)
	if err != nil {
		slog.Warn("rss feed parse failed (skipping)", "url", f.URL, "err", err)
		return 0
	}
	imported := 0
	for i, item := range feed.Items {
		if i >= maxItemsPerFeed {
			slog.Warn("rss feed truncated", "url", f.URL, "limit", maxItemsPerFeed, "items", len(feed.Items))
			break
		}
		if item.Link == "" {
			continue
		}
		sortDate := time.Now()
		if item.PublishedParsed != nil {
			sortDate = *item.PublishedParsed
		} else if item.UpdatedParsed != nil {
			sortDate = *item.UpdatedParsed
		}
		// A feed dated in the future would pin its items to the top of /links
		// indefinitely; clamp to now.
		if sortDate.After(time.Now()) {
			sortDate = time.Now()
		}
		_, inserted, err := p.store.UpsertRSSLink(store.Link{
			Label:    item.Title,
			Href:     item.Link,
			Note:     item.Description,
			SortDate: sortDate.Unix(),
			FeedURL:  f.URL,
		})
		if err != nil {
			slog.Warn("rss link upsert failed", "url", f.URL, "err", err)
			continue
		}
		if inserted {
			imported++
		}
	}
	return imported
}

// Start runs Poll on an interval until ctx is cancelled. Per-feed errors are
// logged via slog.Warn inside Poll; ctx cancellation is the only exit.
func (p *Poller) Start(ctx context.Context, site func() *siteconfig.Config, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Poll(ctx, site())
		}
	}
}
