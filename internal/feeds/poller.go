package feeds

import (
	"context"
	"fmt"
	"time"

	"davidtorcivia.com/dtcom/internal/siteconfig"
	"davidtorcivia.com/dtcom/internal/store"

	"github.com/mmcdole/gofeed"
)

type Poller struct {
	store *store.Store
	fp    *gofeed.Parser
}

func NewPoller(st *store.Store) *Poller {
	return &Poller{store: st, fp: gofeed.NewParser()}
}

// Poll fetches every enabled feed in site.RSSFeeds and upserts new items.
// Returns the count of newly imported items; items whose href was already
// present are counted as dedup (via store.UpsertRSSLink's inserted flag).
func (p *Poller) Poll(site *siteconfig.Config) (int, error) {
	imported := 0
	for _, f := range site.RSSFeeds {
		if !f.Enabled {
			continue
		}
		feed, err := p.fp.ParseURLWithContext(f.URL, context.Background())
		if err != nil {
			return imported, fmt.Errorf("parse %s: %w", f.URL, err)
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
				return imported, err
			}
			if inserted {
				imported++
			}
		}
	}
	return imported, nil
}

// Start runs Poll on an interval until ctx is cancelled.
func (p *Poller) Start(ctx context.Context, site func() *siteconfig.Config, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.Poll(site()); err != nil {
				// logged by caller; ignore here
				_ = err
			}
		}
	}
}
