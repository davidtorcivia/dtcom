package feeds

import (
	"strings"
	"testing"
	"time"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

func TestRenderFeed(t *testing.T) {
	site := &siteconfig.Config{Title: "DT", BaseURL: "https://x.com", Description: "d"}
	arts := []Article{
		{Title: "A", Slug: "a", Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Description: "desc a", Content: "<p>Full body.</p>", Tags: []string{"essay"}},
	}
	out, err := RenderFeed(site, arts)
	if err != nil {
		t.Fatalf("RenderFeed: %v", err)
	}
	wantSubs := []string{"<rss", "<title>DT</title>", "<link>https://x.com/posts/a</link>", "<title>A</title>", "<content:encoded><![CDATA[<p>Full body.</p>]]>", "<category>essay</category>"}
	for _, s := range wantSubs {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
}
