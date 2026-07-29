package markdown

import (
	"strings"
	"testing"
)

// An image alone in its paragraph becomes a figure, and its alt text becomes a
// visible caption. Markdown has no caption syntax, so alt is the only place an
// author can put one.
func TestStandaloneImageGetsACaption(t *testing.T) {
	out, err := Render("![A caption here](/images/x.jpg)\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<figure>",
		`<img src="/images/x.jpg" alt="A caption here">`,
		"<figcaption>A caption here</figcaption>",
		"</figure>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// The wrapping paragraph must be gone — a <figure> inside a <p> is invalid
	// and browsers silently close the paragraph, which breaks the spacing.
	if strings.Contains(out, "<p><figure") || strings.Contains(out, "<p></p>") {
		t.Errorf("figure left inside a paragraph:\n%s", out)
	}
	// alt stays put; the caption is in addition to it, not instead of it.
	if !strings.Contains(out, `alt="A caption here"`) {
		t.Errorf("alt attribute was dropped:\n%s", out)
	}
}

// An image with no alt text is decorative or simply undescribed. It still gets
// the figure so its spacing matches, but an empty caption box would be worse
// than none.
func TestImageWithoutAltGetsNoCaption(t *testing.T) {
	out, err := Render("![](/images/x.jpg)\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<figure>") {
		t.Errorf("expected a figure:\n%s", out)
	}
	if strings.Contains(out, "figcaption") {
		t.Errorf("empty alt produced a caption:\n%s", out)
	}
}

// Whitespace-only alt is the same as none — a caption of three spaces is a gap.
func TestWhitespaceAltGetsNoCaption(t *testing.T) {
	out, err := Render("![   ](/images/x.jpg)\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "figcaption") {
		t.Errorf("whitespace alt produced a caption:\n%s", out)
	}
}

// An image used mid-sentence is part of the prose. Promoting it would drop a
// block element into the middle of a line.
func TestInlineImageIsNotCaptioned(t *testing.T) {
	for _, src := range []string{
		"Text with ![inline](/images/x.jpg) inside.\n",
		"![Link](/images/x.jpg) and [text](/y)\n",
	} {
		out, err := Render(src)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "<figure") || strings.Contains(out, "figcaption") {
			t.Errorf("inline image was captioned for %q:\n%s", src, out)
		}
	}
}

// The title attribute is a separate thing from alt and must survive.
func TestImageTitleIsPreserved(t *testing.T) {
	out, err := Render("![Cap](/images/x.jpg \"a title\")\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `title="a title"`) {
		t.Errorf("title attribute lost:\n%s", out)
	}
	if !strings.Contains(out, "<figcaption>Cap</figcaption>") {
		t.Errorf("caption missing:\n%s", out)
	}
}

// Alt text arrives already escaped for an attribute, and is reused verbatim as
// element content. Anything that could close the tag or inject markup has to
// still be inert.
func TestCaptionCannotInjectMarkup(t *testing.T) {
	out, err := Render("![a <b>bold</b> & \"quoted\" caption](/images/x.jpg)\n")
	if err != nil {
		t.Fatal(err)
	}
	caption := out
	if i := strings.Index(caption, "<figcaption>"); i >= 0 {
		caption = caption[i+len("<figcaption>"):]
		if j := strings.Index(caption, "</figcaption>"); j >= 0 {
			caption = caption[:j]
		}
	}
	// goldmark builds alt text from the AST's plain text, so embedded tags are
	// dropped outright rather than escaped — a stronger guarantee than escaping,
	// and the reason a caption cannot carry emphasis either.
	if strings.Contains(caption, "<") || strings.Contains(caption, ">") {
		t.Errorf("raw markup reached the caption: %q", caption)
	}
	if !strings.Contains(caption, "bold") {
		t.Errorf("the text inside the tags was lost: %q", caption)
	}
	// Characters that survive as text must still be entity-escaped, or a stray
	// ampersand would start an entity in the caption.
	if !strings.Contains(caption, "&amp;") {
		t.Errorf("ampersand was not escaped: %q", caption)
	}
	if !strings.Contains(caption, "&quot;") {
		t.Errorf("quotes were not escaped: %q", caption)
	}
}

// A caption is plain text by construction: goldmark flattens alt to its text
// content, so markdown emphasis inside it does not become markup. Worth
// pinning so nobody files it as a bug later.
func TestCaptionIsPlainText(t *testing.T) {
	out, err := Render("![with *emphasis* inside](/images/x.jpg)\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<figcaption>with emphasis inside</figcaption>") {
		t.Errorf("expected flattened caption text:\n%s", out)
	}
}

// Several figures in one document must each be wrapped, not just the first.
func TestMultipleFiguresInOneDocument(t *testing.T) {
	out, err := Render("![One](/images/a.jpg)\n\nProse between.\n\n![Two](/images/b.jpg)\n")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "<figure>"); n != 2 {
		t.Errorf("got %d figures, want 2:\n%s", n, out)
	}
	for _, want := range []string{"<figcaption>One</figcaption>", "<figcaption>Two</figcaption>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "<p>Prose between.</p>") {
		t.Errorf("prose between figures was disturbed:\n%s", out)
	}
}

// Two images tagged #light and #dark collapse into one figure holding both,
// with a single caption. CSS paints whichever matches the theme.
func TestThemedImagePairBecomesOneFigure(t *testing.T) {
	out, err := Render("![A chart](/images/fig-light.png#light)\n![A chart](/images/fig-dark.png#dark)\n")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "<figure>"); n != 1 {
		t.Fatalf("got %d figures, want one holding both images:\n%s", n, out)
	}
	if n := strings.Count(out, "<img "); n != 2 {
		t.Errorf("expected both images in the figure, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, `class="theme-light"`) || !strings.Contains(out, `class="theme-dark"`) {
		t.Errorf("theme classes missing:\n%s", out)
	}
	if n := strings.Count(out, "<figcaption>"); n != 1 {
		t.Errorf("expected exactly one caption, got %d:\n%s", n, out)
	}
	// The marker has done its job in the renderer; leaving it would send a
	// fragment along on every image request.
	if strings.Contains(out, "#light") || strings.Contains(out, "#dark") {
		t.Errorf("theme marker left in the src:\n%s", out)
	}
	if !strings.Contains(out, `src="/images/fig-light.png"`) || !strings.Contains(out, `src="/images/fig-dark.png"`) {
		t.Errorf("cleaned srcs missing:\n%s", out)
	}
}

// Order should not matter — dark first is just as valid.
func TestThemedPairInEitherOrder(t *testing.T) {
	out, err := Render("![A chart](/images/d.png#dark)\n![A chart](/images/l.png#light)\n")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "<figure>"); n != 1 {
		t.Errorf("dark-first pair did not combine:\n%s", out)
	}
}

// A lone tagged image is legitimate: show this one only in dark mode. It gets
// its class and its caption, and no partner is invented.
func TestSingleThemedImage(t *testing.T) {
	out, err := Render("![Only in dark](/images/d.png#dark)\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="theme-dark"`) {
		t.Errorf("theme class missing:\n%s", out)
	}
	if !strings.Contains(out, "<figcaption>Only in dark</figcaption>") {
		t.Errorf("caption missing:\n%s", out)
	}
	if strings.Contains(out, "#dark") {
		t.Errorf("marker left in the src:\n%s", out)
	}
}

// Two ordinary images on consecutive lines are one markdown paragraph, but they
// are not a themed pair and must not be silently merged into a figure.
func TestTwoUntaggedImagesAreNotAPair(t *testing.T) {
	out, err := Render("![One](/images/a.png)\n![Two](/images/b.png)\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<figure") {
		t.Errorf("untagged images were merged into a figure:\n%s", out)
	}
}

// Two images tagged the same way are not a pair either.
func TestTwoSameThemeImagesAreNotAPair(t *testing.T) {
	out, err := Render("![One](/images/a.png#dark)\n![Two](/images/b.png#dark)\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<figure") {
		t.Errorf("two dark images were treated as a pair:\n%s", out)
	}
}

// The caption falls back to whichever image carries alt text, so tagging only
// one of the pair still produces a caption.
func TestThemedPairCaptionFallsBack(t *testing.T) {
	out, err := Render("![](/images/l.png#light)\n![The chart](/images/d.png#dark)\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<figcaption>The chart</figcaption>") {
		t.Errorf("caption did not fall back to the second image:\n%s", out)
	}
}

// A theme marker on an inline image is not a figure and must not gain a class
// or a caption.
func TestInlineThemedImageIsLeftAlone(t *testing.T) {
	out, err := Render("Text ![x](/images/a.png#dark) more.\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<figure") || strings.Contains(out, "theme-dark") {
		t.Errorf("inline themed image was promoted:\n%s", out)
	}
}

// A fenced block that happens to contain image markup is code, not an image.
func TestImageMarkupInCodeIsNotCaptioned(t *testing.T) {
	out, err := Render("```md\n![Not a caption](/images/x.jpg)\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<figure") || strings.Contains(out, "figcaption") {
		t.Errorf("code sample was turned into a figure:\n%s", out)
	}
}
