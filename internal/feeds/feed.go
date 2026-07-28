// Package feeds renders the outbound RSS feed and polls inbound feeds.
package feeds

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"text/template"
	"time"

	"davidtorcivia.com/dtcom/internal/siteconfig"
)

// Article is a minimal article projection for feed rendering.
// (Deliberately separate from build.Article so this package doesn't import
// internal/build — feeds is a leaf rendering concern.)
type Article struct {
	Title       string
	Slug        string
	Date        time.Time
	Description string
}

// maxFeedItems caps the outbound feed. Readers only ever show the recent
// entries, and an unbounded feed grows without limit as the archive does.
const maxFeedItems = 50

// xmlEscape escapes a string for XML character data. text/template does not
// auto-escape, so this is applied to every interpolated value — including the
// base URL, which an operator could set with an ampersand in a query string
// and silently produce a malformed feed.
func xmlEscape(s string) string {
	var sb strings.Builder
	_ = xml.EscapeText(&sb, []byte(s))
	return sb.String()
}

// rfc822 formats a time the way RSS 2.0 requires.
func rfc822(t time.Time) string {
	return t.Format("Mon, 02 Jan 2006 15:04:05 -0700")
}

const rssTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
<channel>
<title>{{.Site.Title | xmlEscape}}</title>
<link>{{.BaseURL | xmlEscape}}</link>
<description>{{.Site.Description | xmlEscape}}</description>
<language>en</language>
<lastBuildDate>{{.BuildDate}}</lastBuildDate>
<atom:link href="{{.BaseURL | xmlEscape}}/feed.xml" rel="self" type="application/rss+xml" />
{{range .Articles}}<item>
<title>{{.Title | xmlEscape}}</title>
<link>{{$.BaseURL | xmlEscape}}/posts/{{.Slug | xmlEscape}}</link>
<guid isPermaLink="true">{{$.BaseURL | xmlEscape}}/posts/{{.Slug | xmlEscape}}</guid>
<pubDate>{{.Date | rfc822}}</pubDate>
<description>{{.Description | xmlEscape}}</description>
</item>
{{end}}</channel>
</rss>
`

var feedTmpl = template.Must(template.New("feed").
	Funcs(template.FuncMap{"xmlEscape": xmlEscape, "rfc822": rfc822}).
	Parse(rssTmpl))

// RenderFeed renders the RSS document for the given published articles, newest
// first as supplied by the caller.
func RenderFeed(site *siteconfig.Config, arts []Article) (string, error) {
	if site == nil {
		return "", fmt.Errorf("render feed: nil site config")
	}
	if len(arts) > maxFeedItems {
		arts = arts[:maxFeedItems]
	}
	// The feed's own timestamp is the newest post it carries, not time.Now():
	// a rebuild triggered by an RSS poll or a link edit must not make every
	// subscriber's reader think the feed changed.
	buildDate := time.Time{}
	for _, a := range arts {
		if a.Date.After(buildDate) {
			buildDate = a.Date
		}
	}
	if buildDate.IsZero() {
		buildDate = time.Now()
	}
	var buf bytes.Buffer
	err := feedTmpl.Execute(&buf, map[string]any{
		"Site":      site,
		"Articles":  arts,
		"BaseURL":   strings.TrimRight(site.BaseURL, "/"),
		"BuildDate": rfc822(buildDate),
	})
	if err != nil {
		return "", fmt.Errorf("render feed: %w", err)
	}
	return buf.String(), nil
}
