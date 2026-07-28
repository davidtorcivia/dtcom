// Package feeds renders the outbound RSS feed and polls inbound feeds.
package feeds

import (
	"bytes"
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

// xmlEscape escapes the characters that are unsafe in XML text content.
// text/template does not auto-escape, so we apply this to author-controlled
// strings (title, description) to keep the generated feed well-formed.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

const rssTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
<channel>
<title>{{.Site.Title | xmlEscape}}</title>
<link>{{.Site.BaseURL}}</link>
<description>{{.Site.Description | xmlEscape}}</description>
<atom:link href="{{.Site.BaseURL}}/feed.xml" rel="self" type="application/rss+xml" />
{{range .Articles}}<item>
<title>{{.Title | xmlEscape}}</title>
<link>{{$.Site.BaseURL}}/posts/{{.Slug}}</link>
<guid>{{$.Site.BaseURL}}/posts/{{.Slug}}</guid>
<pubDate>{{.Date.Format "Mon, 02 Jan 2006 15:04:05 -0700"}}</pubDate>
<description>{{.Description | xmlEscape}}</description>
</item>
{{end}}
</channel>
</rss>`

func RenderFeed(site *siteconfig.Config, arts []Article) (string, error) {
	tmpl, err := template.New("feed").Funcs(template.FuncMap{"xmlEscape": xmlEscape}).Parse(rssTmpl)
	if err != nil {
		return "", fmt.Errorf("parse feed template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"Site": site, "Articles": arts}); err != nil {
		return "", fmt.Errorf("render feed: %w", err)
	}
	return buf.String(), nil
}
