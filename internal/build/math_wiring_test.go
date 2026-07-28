package build

import (
	"strings"
	"testing"
)

// KaTeX is ~600 KB of script and fonts, so it is loaded per-page rather than
// site-wide. This pins both halves of that: a post with math pulls it in, and
// one without must not.
func TestKatexIsLoadedOnlyWherePostsHaveMath(t *testing.T) {
	te := newTestEngine(t)
	te.writePost(t, "2026-02-01-math.md",
		"---\ntitle: Math\ndate: 2026-02-01\ndescription: d\n---\n\n$$\n"+
			`L = \begin{cases} a \ b \end{cases}`+"\n$$\n\n```go\nfunc x() {}\n```\n")
	te.writePost(t, "2026-01-31-plain.md",
		"---\ntitle: Plain\ndate: 2026-01-31\ndescription: d\n---\n\nJust prose, no formulas.\n")
	if err := te.engine.Rebuild(); err != nil {
		t.Fatal(err)
	}

	mathHTML := te.mustRead(t, "posts", "math", "index.html")
	for _, want := range []string{
		"katex.min.css",
		"katex.min.js",
		"math.js",
		`class="math display"`,
		`\begin{cases}`, // the LaTeX itself, unmangled, for KaTeX to read
		"chroma-",       // the fenced Go block, highlighted
	} {
		if !strings.Contains(mathHTML, want) {
			t.Errorf("math post is missing %q", want)
		}
	}

	plainHTML := te.mustRead(t, "posts", "plain", "index.html")
	if strings.Contains(plainHTML, "katex") {
		t.Error("a post with no math still pulls in katex")
	}
}
