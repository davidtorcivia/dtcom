package markdown

import (
	"regexp"
	"strings"
)

// Figures: captions, and light/dark image pairs.
//
// A picture on its own line, ![Caption text](/images/x.jpg), becomes a <figure>
// with the alt text repeated as a visible <figcaption>. Markdown has no caption
// syntax, and alt text is the only place an author can put a description
// without inventing one.
//
// An image whose URL ends in #light or #dark is themed. Give both and they
// collapse into one figure that swaps with the site theme:
//
//	![The sRGB transfer function](/images/fig2-light.png#light)
//	![The sRGB transfer function](/images/fig2-dark.png#dark)
//
// The swap is CSS, keyed on the same data-theme attribute the rest of the site
// uses. A <picture> element with media="(prefers-color-scheme: dark)" would
// have been the obvious construct and is wrong here: it follows the operating
// system only, so it would ignore the theme toggle in the header and strand a
// reader who picked dark on a light desktop.
//
// Only images that are the whole of their paragraph are promoted. An image used
// mid-sentence is part of the prose and gets nothing — captioning it would drop
// a block element into the middle of a line.
//
// Done on the rendered HTML rather than in the AST, matching how ==highlight==
// is handled in Render. The input is not arbitrary HTML: it is this renderer's
// own output, where goldmark has already escaped every attribute value, so
// `[^>]*` cannot run past the tag it is matching.

// imageOnlyParagraph matches a paragraph whose entire content is one or more
// <img> tags and whitespace. Any text, link, or other element in the paragraph
// fails to match, which is the intent.
var imageOnlyParagraph = regexp.MustCompile(`<p>((?:\s*<img [^>]*>)+\s*)</p>`)

var (
	imgTag   = regexp.MustCompile(`<img ([^>]*)>`)
	altAttr  = regexp.MustCompile(`(^|\s)alt="([^"]*)"`)
	srcAttr  = regexp.MustCompile(`(^|\s)src="([^"]*)"`)
	themeTag = regexp.MustCompile(`#(light|dark)$`)
)

type figureImage struct {
	tag   string // the full <img …> as goldmark wrote it
	attrs string
	alt   string
	theme string // "light", "dark", or "" for untagged
}

func captionImages(html string) string {
	return imageOnlyParagraph.ReplaceAllStringFunc(html, func(m string) string {
		inner := imageOnlyParagraph.FindStringSubmatch(m)
		if inner == nil {
			return m
		}
		imgs := parseImages(inner[1])
		switch len(imgs) {
		case 1:
			return figure(imgs, imgs[0].alt)
		case 2:
			// Exactly one light and one dark is a themed pair. Two untagged
			// images on consecutive lines are just two images, and are left as
			// the paragraph they were.
			a, b := imgs[0], imgs[1]
			if (a.theme == "light" && b.theme == "dark") || (a.theme == "dark" && b.theme == "light") {
				caption := a.alt
				if caption == "" {
					caption = b.alt
				}
				return figure(imgs, caption)
			}
		}
		return m
	})
}

func parseImages(fragment string) []figureImage {
	var out []figureImage
	for _, m := range imgTag.FindAllStringSubmatch(fragment, -1) {
		fi := figureImage{tag: m[0], attrs: m[1]}
		if a := altAttr.FindStringSubmatch(fi.attrs); a != nil {
			fi.alt = strings.TrimSpace(a[2])
		}
		if s := srcAttr.FindStringSubmatch(fi.attrs); s != nil {
			if t := themeTag.FindStringSubmatch(s[2]); t != nil {
				fi.theme = t[1]
				// Strip the marker from the URL it was carried on. It has done
				// its job here, and leaving it would send a fragment to the
				// server on every image request.
				clean := strings.TrimSuffix(s[2], "#"+fi.theme)
				fi.tag = strings.Replace(fi.tag, `src="`+s[2]+`"`, `src="`+clean+`"`, 1)
			}
		}
		out = append(out, fi)
	}
	return out
}

// figure wraps the images and, when there is caption text, appends it.
func figure(imgs []figureImage, caption string) string {
	var b strings.Builder
	b.WriteString("<figure>")
	for _, img := range imgs {
		tag := img.tag
		if img.theme != "" {
			// The class drives the CSS swap; see .theme-light / .theme-dark in
			// style.css.
			tag = strings.Replace(tag, "<img ", `<img class="theme-`+img.theme+`" `, 1)
		}
		b.WriteString(tag)
	}
	if caption != "" {
		// alt stays on each <img>. It is the author's own words either way, and
		// dropping it would leave the image undescribed anywhere the caption
		// does not render. The cost is that a screen reader meets the text
		// twice, which is the conventional trade for markdown-authored figures.
		b.WriteString("<figcaption>" + caption + "</figcaption>")
	}
	b.WriteString("</figure>")
	return b.String()
}
