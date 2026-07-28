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

// md is built once and reused. goldmark.Markdown is safe for concurrent use,
// and constructing it per call re-registered every GFM extension on each
// article of every rebuild.
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		// Raw HTML is enabled deliberately: posts are written by the site's
		// sole author, and some of them embed markup the renderer has no
		// syntax for. It does mean rendered output must never be treated as
		// trusted by anything downstream — see stripMarkdown in the build
		// engine and the escaping in store.SearchArticles.
		html.WithUnsafe(),
	),
)

// Render converts markdown source to an HTML fragment.
func Render(src string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	out := buf.String()
	// ==highlight== → <mark>highlight</mark>
	// (post-render regex replace — simpler and more robust than a custom
	// goldmark inline parser. Safe because '==' does not otherwise appear
	// in our rendered HTML.)
	//
	// ReplaceAll is applied twice: the pattern consumes the character after
	// the closing "==", so two highlights separated by a single character
	// would leave the second unmatched on a single pass.
	out = highlightRe.ReplaceAllString(out, `${1}<mark>${2}</mark>${3}`)
	out = highlightRe.ReplaceAllString(out, `${1}<mark>${2}</mark>${3}`)
	return out, nil
}

var highlightRe = regexp.MustCompile(`(^|[^=])==([^=\n]+?)==([^=]|$)`)
