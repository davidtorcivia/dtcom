package markdown

// Responsive images.
//
// A post writes ![](/images/9f3c….png) and means "the picture". What should
// reach a phone is not the picture — it is the smallest rendition that still
// looks right on that screen. This pass turns each <img> into a tag that says
// what exists and lets the browser choose:
//
//	<picture>
//	  <source type="image/webp" srcset="…w480.webp 480w, …" sizes="…">
//	  <img src="…w1080.png" srcset="…w480.png 480w, …" sizes="…"
//	       width="2000" height="1111" decoding="async" loading="lazy"
//	       data-full="/images/9f3c….png" data-full-w="2000" data-full-h="1111">
//	</picture>
//
// The <picture> appears only when there are WebP renditions to offer, which is
// the lossless family — photographs get a plain <img> with a JPEG srcset,
// because lossless WebP of a photograph is several times the size of the JPEG
// and there is no lossy WebP in this pipeline. See internal/build/imageset.go
// for why.
//
// data-full is the master, and the lightbox reads it: what a page loads is a
// rendition, and zooming into a rendition would be zooming into a thumbnail.
// data-full-w travels with it because the lightbox has to know the master's
// true width before the master has finished loading — it is what decides how
// far a zoom can go while staying sharp.
//
// Applied after captionImages, and the order is load-bearing: figure detection
// matches paragraphs whose entire content is <img> tags, and a <picture>
// wrapper would stop every figure and every light/dark pair from being
// recognised.

import (
	"regexp"
	"strconv"
	"strings"
)

// sizesAttr tells the browser how wide the picture will be before any CSS has
// loaded, which is when it has to choose a rendition.
//
// It mirrors the layout exactly, and has to be revisited when that changes:
// .site-container is 1080px wide with 2rem of padding, tightening to 1rem of
// padding at 640px. So: the viewport less its padding, up to a 1016px measure.
const sizesAttr = "(max-width: 640px) calc(100vw - 2rem), (max-width: 1080px) calc(100vw - 4rem), 1016px"

// defaultSrcWidth is the rendition a browser without srcset support gets. The
// widest the column ever is, so it is right on a desktop and merely generous
// on a phone.
const defaultSrcWidth = 1080

// Rendition is one generated size of an image.
type Rendition struct {
	Width int
	URL   string
}

// ImageInfo is everything the renderer needs to know about one stored image.
// Built by internal/build from the images directory.
type ImageInfo struct {
	Master     string // "/images/9f3c….png" — full size, in the master's format
	MasterWebP string // the same picture as lossless WebP, "" if there is none
	Width      int    // the master's intrinsic size
	Height     int
	WebP       []Rendition // ascending by width, master included
	Fallback   []Rendition // ascending by width, master included
}

// ImageResolver looks up an image by the URL a post referred to it with.
// Returning false leaves the tag exactly as it was, which is what happens for
// remote images, SVGs, and anything not found on disk.
type ImageResolver func(src string) (*ImageInfo, bool)

var (
	imgTagFull  = regexp.MustCompile(`<img ([^>]*)>`)
	classAttr   = regexp.MustCompile(`(^|\s)class="([^"]*)"`)
	themeClass  = regexp.MustCompile(`\btheme-(light|dark)\b`)
	hasAttrExpr = func(attrs, name string) bool {
		return regexp.MustCompile(`(^|\s)` + name + `=`).MatchString(attrs)
	}
)

// applyResponsive rewrites every <img> the resolver recognises.
func applyResponsive(html string, resolve ImageResolver) string {
	if resolve == nil {
		return html
	}
	// The first picture in a post is usually the one a reader sees on arrival,
	// so it is fetched eagerly and at high priority; everything below it waits
	// until it is nearly in view. Getting this backwards costs the largest
	// contentful paint outright.
	first := true
	return imgTagFull.ReplaceAllStringFunc(html, func(tag string) string {
		m := imgTagFull.FindStringSubmatch(tag)
		if m == nil {
			return tag
		}
		attrs := m[1]
		src := attrValue(srcAttr, attrs)
		if src == "" {
			return tag
		}
		info, ok := resolve(src)
		if !ok || info == nil || info.Width <= 0 {
			return tag
		}
		eager := first
		first = false
		return buildTag(attrs, info, eager)
	})
}

func buildTag(attrs string, info *ImageInfo, eager bool) string {
	// The theme class moves to the <picture>, since that is the element the
	// light/dark rules have to show and hide once there is a wrapper. Without
	// the move, hiding the <img> would leave an empty <picture> holding the
	// figure's space open.
	class := attrValue(classAttr, attrs)
	theme := themeClass.FindString(class)
	if theme != "" {
		attrs = stripClass(attrs, theme)
	}

	var b strings.Builder
	b.WriteString("<img ")
	b.WriteString(strings.TrimSpace(attrs))
	b.WriteString(` src="`)
	b.WriteString(pickDefault(info))
	b.WriteString(`"`)
	if set := srcset(info.Fallback); set != "" && len(info.Fallback) > 1 {
		b.WriteString(` srcset="` + set + `" sizes="` + sizesAttr + `"`)
	}
	b.WriteString(` width="` + strconv.Itoa(info.Width) + `" height="` + strconv.Itoa(info.Height) + `"`)
	// Decoding off the main thread keeps a large picture from stalling the
	// scroll it is being scrolled into.
	b.WriteString(` decoding="async"`)
	if eager {
		b.WriteString(` fetchpriority="high"`)
	} else {
		b.WriteString(` loading="lazy"`)
	}
	b.WriteString(` data-full="` + info.Master + `"`)
	if info.MasterWebP != "" {
		b.WriteString(` data-full-webp="` + info.MasterWebP + `"`)
	}
	b.WriteString(` data-full-w="` + strconv.Itoa(info.Width) + `"`)
	b.WriteString(` data-full-h="` + strconv.Itoa(info.Height) + `">`)

	img := b.String()
	// The src attribute goldmark wrote is still in attrs; the one appended
	// above wins in every browser (last duplicate), but leaving both would be
	// sloppy markup, so the original goes.
	img = dedupeSrc(img)

	if len(info.WebP) == 0 {
		if theme != "" {
			return insertClass(img, theme)
		}
		return img
	}
	open := "<picture>"
	if theme != "" {
		open = `<picture class="` + theme + `">`
	}
	return open +
		`<source type="image/webp" srcset="` + srcset(info.WebP) + `" sizes="` + sizesAttr + `">` +
		img + "</picture>"
}

// pickDefault chooses the plain src: the rendition nearest the column width,
// falling back to the master when nothing smaller was generated.
func pickDefault(info *ImageInfo) string {
	best := info.Master
	bestDelta := -1
	for _, r := range info.Fallback {
		d := r.Width - defaultSrcWidth
		if d < 0 {
			d = -d
		}
		if bestDelta < 0 || d < bestDelta {
			best, bestDelta = r.URL, d
		}
	}
	return best
}

func srcset(rs []Rendition) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, r.URL+" "+strconv.Itoa(r.Width)+"w")
	}
	return strings.Join(parts, ", ")
}

func attrValue(re *regexp.Regexp, attrs string) string {
	if m := re.FindStringSubmatch(attrs); m != nil {
		return m[2]
	}
	return ""
}

// stripClass removes one class name, and the class attribute itself if that
// was all it held.
func stripClass(attrs, name string) string {
	return classAttr.ReplaceAllStringFunc(attrs, func(m string) string {
		sub := classAttr.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		var kept []string
		for _, c := range strings.Fields(sub[2]) {
			if c != name {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			return sub[1]
		}
		return sub[1] + `class="` + strings.Join(kept, " ") + `"`
	})
}

func insertClass(tag, name string) string {
	if classAttr.MatchString(tag) {
		return classAttr.ReplaceAllString(tag, `${1}class="`+name+` ${2}"`)
	}
	return strings.Replace(tag, "<img ", `<img class="`+name+`" `, 1)
}

// dedupeSrc drops every src but the last, which is the one this pass wrote.
func dedupeSrc(tag string) string {
	locs := srcAttr.FindAllStringIndex(tag, -1)
	if len(locs) < 2 {
		return tag
	}
	var b strings.Builder
	prev := 0
	for _, loc := range locs[:len(locs)-1] {
		b.WriteString(tag[prev:loc[0]])
		prev = loc[1]
	}
	b.WriteString(tag[prev:])
	return b.String()
}
