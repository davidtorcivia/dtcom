package feeds

import (
	"context"
	"log/slog"
	"time"

	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"

	"github.com/mmcdole/gofeed"
)

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
	return &Poller{store: st, fp: gofeed.NewParser()}
}

// Poll fetches every enabled feed in site.RSSFeeds and upserts new items.
// Per-feed errors are logged via slog.Warn and skipped — one dead feed does
// not block the others. Returns the count of newly imported items. Items whose
// href was already present are counted as dedup (via store.UpsertRSSLink's
// inserted flag). Items with disallowed href schemes (javascript:/data:) are
// silently dropped by UpsertRSSLink.
func (p *Poller) Poll(site *siteconfig.Config) int {
	imported := 0
	for _, f := range site.RSSFeeds {
		if !f.Enabled {
			continue
		}
		feed, err := p.fp.ParseURLWithContext(f.URL, context.Background())
		if err != nil {
			slog.Warn("rss feed parse failed (skipping)", "url", f.URL, "err", err)
			continue
		}
		for _, item := range feed.Items {
			sortDate := time.Now()
			if item.PublishedParsed != nil {
				sortDate = *item.PublishedParsed
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
	}
	if p.OnPoll != nil {
		p.OnPoll(imported)
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
			p.Poll(site())
		}
	}
}
