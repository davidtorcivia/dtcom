// Package markdown renders GFM markdown to HTML with a ==highlight==
// extension, syntax-highlighted code blocks, and LaTeX math.
package markdown

import (
	"bytes"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	mathjax "github.com/litao91/goldmark-mathjax"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// md is built once and reused. goldmark.Markdown is safe for concurrent use,
// and constructing it per call re-registered every GFM extension on each
// article of every rebuild.
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		// Code blocks are highlighted here, at build time, rather than by a
		// script in the browser: the output is a static site, so there is no
		// reason to ship a highlighter and re-run it on every visit.
		highlighting.NewHighlighting(
			// Emit class names instead of inline style attributes. The colours
			// then live in style.css, where they can follow the light/dark
			// theme — a baked-in Chroma theme cannot, and every block would be
			// stuck on one palette.
			highlighting.WithFormatOptions(
				chromahtml.WithClasses(true),
				chromahtml.ClassPrefix("chroma-"),
			),
		),
		// Math is *protected*, not rendered, here. The extension parses $…$ and
		// $$…$$ into their own nodes and emits the LaTeX verbatim inside a
		// <span class="math">, which is the whole point: left to ordinary
		// markdown, `\\` row separators collapse to `\` and underscores turn
		// into <em>. KaTeX renders the result in the browser — see
		// static/math.js.
		mathjax.MathJax,
	),
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

// HasMath reports whether rendered HTML contains math for KaTeX to typeset.
//
// The build uses this to decide whether a page loads KaTeX at all. It is ~280
// KB of JavaScript plus fonts, and most posts have no math in them; loading it
// unconditionally would be the single heaviest thing on the site.
func HasMath(renderedHTML string) bool {
	return strings.Contains(renderedHTML, `class="math`)
}

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
