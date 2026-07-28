package markdown

import (
	"regexp"
	"strings"
)

// Image captions.
//
// A picture on its own line, ![Caption text](/images/x.jpg), becomes a <figure>
// with the alt text repeated as a visible <figcaption>. Markdown has no caption
// syntax, and alt text is the only place an author can put a description
// without inventing one.
//
// Only an image that is the whole of its paragraph is promoted. An image used
// mid-sentence is part of the prose and gets nothing — captioning it would drop
// a block element into the middle of a line.
//
// Done on the rendered HTML rather than in the AST, matching how ==highlight==
// is handled a few lines up. The input is not arbitrary HTML: it is this
// renderer's own output, where goldmark has already escaped every attribute
// value, so `[^>]*` cannot run past the tag it is matching.

// standaloneImage matches a paragraph whose entire content is one <img>.
// Anything else in the paragraph — text, a second image, a link — fails to
// match, which is the intent.
var standaloneImage = regexp.MustCompile(`<p>(<img ([^>]*)>)</p>`)

// altAttr pulls the alt value back out of the tag goldmark emitted.
var altAttr = regexp.MustCompile(`(^|\s)alt="([^"]*)"`)

func captionImages(html string) string {
	return standaloneImage.ReplaceAllStringFunc(html, func(m string) string {
		parts := standaloneImage.FindStringSubmatch(m)
		if parts == nil {
			return m
		}
		img, attrs := parts[1], parts[2]

		caption := ""
		if a := altAttr.FindStringSubmatch(attrs); a != nil {
			caption = strings.TrimSpace(a[2])
		}
		if caption == "" {
			// A decorative image, or one whose author gave no alt text. It
			// still becomes a figure so the spacing matches its captioned
			// neighbours, but an empty <figcaption> would be an empty box.
			return "<figure>" + img + "</figure>"
		}
		// alt stays on the <img>. It is the author's own words either way, and
		// dropping it would leave the image undescribed anywhere the caption
		// does not render. The cost is that a screen reader meets the text
		// twice, which is the conventional trade for markdown-authored figures.
		return "<figure>" + img + "<figcaption>" + caption + "</figcaption></figure>"
	})
}
