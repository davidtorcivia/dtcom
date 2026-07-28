package markdown

import (
	"strings"
	"testing"
)

func TestRenderBasics(t *testing.T) {
	in := "# Title\n\nA _bold_ **strong** paragraph with `code`.\n\n> Quote\n"
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantSubstrings := []string{"<h1", "<em>bold</em>", "<strong>strong</strong>", "<code>code</code>", "<blockquote>"}
	for _, s := range wantSubstrings {
		if !strings.Contains(html, s) {
			t.Errorf("output missing %q\n%s", s, html)
		}
	}
}

func TestRenderHighlight(t *testing.T) {
	in := "Text with ==highlighted== words."
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "<mark>highlighted</mark>") {
		t.Errorf("expected <mark>, got:\n%s", html)
	}
}

// The fence's language has to reach the output. It used to do that as
// goldmark's plain class="language-go"; Chroma now consumes the language and
// emits per-token classes instead, which is the same fact expressed more
// usefully — "go" was never styled by anything, whereas these are.
func TestRenderCodeBlockLang(t *testing.T) {
	in := "```go\npackage main\n```\n"
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// chroma-kn is the keyword-namespace token: proof the block was lexed as
	// Go rather than dumped as plain text.
	if !strings.Contains(html, "chroma-kn") {
		t.Errorf("expected the fence language to drive highlighting, got:\n%s", html)
	}
	if !strings.Contains(html, "package") {
		t.Errorf("code text was lost:\n%s", html)
	}
}

func TestRenderTable(t *testing.T) {
	in := "| A | B |\n|---|---|\n| 1 | 2 |\n"
	html, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "<table>") {
		t.Errorf("expected <table>, got:\n%s", html)
	}
}

// Math must survive markdown untouched. Left to the ordinary inline parser a
// `\` row separator collapses to a single `\` and a subscript underscore
// becomes <em>, either of which turns a valid formula into a broken one.
func TestMathIsProtectedFromMarkdown(t *testing.T) {
	src := "$$\n" +
		`L = \begin{cases}` + "\n" +
		`V / 12.92 & \text{if } V \leq 0.04045 \` + "\n" +
		`\left(\frac{V + 0.055}{1.055}\right)^{2.4} & \text{if } V > 0.04045` + "\n" +
		`\end{cases}` + "\n$$\n"
	out, err := Render(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="math display"`) {
		t.Fatalf("display math not recognised:\n%s", out)
	}
	for _, want := range []string{
		`\begin{cases}`,
		`\`, // the row separator, still doubled
		`\left(\frac{V + 0.055}{1.055}\right)`,
		`\end{cases}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("math lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<br") || strings.Contains(out, "<em>") {
		t.Errorf("markdown rewrote the math:\n%s", out)
	}
	if !HasMath(out) {
		t.Error("HasMath did not detect the formula")
	}
}

// Two inline formulas in one paragraph is the case that made an earlier
// candidate library hang outright, so it is pinned here.
func TestTwoInlineFormulas(t *testing.T) {
	out, err := Render("First $a+b$ then $c+d$ end.")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, `class="math inline"`); n != 2 {
		t.Errorf("got %d inline formulas, want 2:\n%s", n, out)
	}
}

// Prose with no math must not pull in KaTeX.
func TestHasMathIsFalseForPlainProse(t *testing.T) {
	out, err := Render("It cost 5 dollars and change.\n")
	if err != nil {
		t.Fatal(err)
	}
	if HasMath(out) {
		t.Errorf("plain prose reported as containing math:\n%s", out)
	}
}

// Fenced code carries language classes so the stylesheet can colour it.
func TestCodeBlockIsHighlighted(t *testing.T) {
	out, err := Render("```go\nfunc main() {}\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chroma-") {
		t.Fatalf("code block was not highlighted:\n%s", out)
	}
	// Classes, not inline styles: the colours live in style.css so a block can
	// follow the light/dark theme instead of being fixed to one palette.
	if strings.Contains(out, `style="color:`) {
		t.Errorf("highlighting was baked into inline styles:\n%s", out)
	}
	if !strings.Contains(out, "func") || !strings.Contains(out, "main") {
		t.Errorf("code text was lost:\n%s", out)
	}
}

// An unknown or absent language must still render, just without colour.
func TestCodeBlockWithoutLanguage(t *testing.T) {
	for _, src := range []string{"```\nplain text\n```\n", "```notalanguage\nx = 1\n```\n"} {
		out, err := Render(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if !strings.Contains(out, "<pre") {
			t.Errorf("%q produced no code block:\n%s", src, out)
		}
	}
}
