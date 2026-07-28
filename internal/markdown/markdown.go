// Package markdown renders GFM markdown to HTML with a ==highlight== extension.
package markdown

import (
	"bytes"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// Render converts markdown source to an HTML fragment.
func Render(src string) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	out := buf.String()
	// ==highlight== → <mark>highlight</mark>
	// (post-render regex replace — simpler and more robust than a custom
	// goldmark inline parser. Safe because '==' does not otherwise appear
	// in our rendered HTML.)
	out = highlightRe.ReplaceAllString(out, `${1}<mark>${2}</mark>${3}`)
	return out, nil
}

var highlightRe = regexp.MustCompile(`(^|[^=])==([^=\n]+?)==([^=]|$)`)
