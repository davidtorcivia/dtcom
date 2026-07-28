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
		// Footnotes are NOT part of GFM in goldmark — that bundle is tables,
		// strikethrough, linkify and task lists only. Without this, [^1] markers
		// and their definitions rendered as literal text.
		extension.Footnote,
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
	// Line endings are normalised here rather than at the call sites. A browser
	// submits textarea content with CRLF, files on disk use LF, and the save
	// path already converted — but the admin preview did not, so it rendered
	// something subtly different from what publishing produced. Worse, the
	// line-oriented normalisation below matches on "$$" at end of line, which a
	// trailing \r silently defeats: one-line display math then slipped through
	// unfixed and swallowed the rest of the document. Doing it inside Render
	// means no future caller can forget.
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")

	var buf bytes.Buffer
	if err := md.Convert([]byte(normalizeDisplayMath(src)), &buf); err != nil {
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

// --- display-math normalisation ---------------------------------------------
//
// The math extension only handles a $$ block whose formula spans its own lines.
// Two forms it gets wrong, both silently:
//
//   - a formula written entirely on one line, "$$x = 1$$", renders EMPTY: the
//     content is dropped and only the delimiters survive.
//   - a formula on a single line between its own $$ markers has each doubled
//     backslash — LaTeX's row separator — collapsed to a single one, which
//     changes what the formula means.
//
// Both are rewritten here into the form that works: $$ on their own lines, with
// the formula broken at its row separators. LaTeX ignores the added newlines,
// so this changes how the source is parsed, not what it means.
//
// The two fixes have to ship together. Fixing only the first would turn a
// formula like $$a \\ b$$ from visibly empty into quietly wrong, and quietly
// wrong is the worse failure.

var oneLineDisplayMath = regexp.MustCompile(`^([ \t]*)\$\$(.+?)\$\$[ \t]*$`)

// splitMathRows puts each row of a LaTeX environment on its own line, breaking
// at the doubled backslashes that separate them.
func splitMathRows(body string) string {
	return strings.ReplaceAll(strings.TrimSpace(body), `\\`, "\\\\\n")
}

// normalizeDisplayMath rewrites the two broken forms. Fenced code is skipped
// outright: a shell snippet full of $$ is not math and must survive verbatim.
func normalizeDisplayMath(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))

	fence := ""           // the ``` or ~~~ run that opened the current code block
	mathOpen := false     // inside a $$ … $$ block
	var mathBody []string // its lines, held back until the closing $$

	flushMath := func(closing string) {
		out = append(out, "$$")
		joined := strings.TrimSpace(strings.Join(mathBody, "\n"))
		if joined != "" {
			out = append(out, strings.Split(splitMathRows(joined), "\n")...)
		}
		out = append(out, closing)
		mathBody = nil
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)

		if fence != "" {
			out = append(out, line)
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if mathOpen {
			if t == "$$" {
				flushMath(line)
				mathOpen = false
				continue
			}
			mathBody = append(mathBody, line)
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			out = append(out, line)
			continue
		}
		if t == "$$" {
			mathOpen = true
			continue
		}
		if m := oneLineDisplayMath.FindStringSubmatch(line); m != nil && strings.TrimSpace(m[2]) != "" {
			out = append(out, "$$")
			out = append(out, strings.Split(splitMathRows(m[2]), "\n")...)
			out = append(out, "$$")
			continue
		}
		out = append(out, line)
	}
	// An unclosed $$ is a typo, not a formula. Put the source back as written
	// rather than swallowing the rest of the post into a math block.
	if mathOpen {
		out = append(out, "$$")
		out = append(out, mathBody...)
	}
	return strings.Join(out, "\n")
}
