package build

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Social preview cards.
//
// Every page gets a 1200x630 PNG rendered at build time rather than sharing one
// static image, so a link to a post unfurls as that post instead of as the site
// in general. A post that sets `cover` in its frontmatter keeps using it —
// explicit author intent beats a generated card.
//
// The fonts are the latin subset of Inter as served by Google Fonts, embedded
// rather than read from disk so the renderer works in the distroless image,
// which has no system fonts at all. woff2 is not an option here: x/image can
// only parse raw TrueType. See static/fonts/OFL.txt for the licence.

//go:embed ogfonts/Inter-Bold.ttf
var interBoldTTF []byte

//go:embed ogfonts/Inter-Regular.ttf
var interRegularTTF []byte

// Card geometry. 1200x630 is the size Twitter, Facebook, LinkedIn, Slack and
// iMessage all crop from; anything else gets letterboxed by somebody.
const (
	ogWidth   = 1200
	ogHeight  = 630
	ogMargin  = 80
	ogMaxText = ogWidth - 2*ogMargin

	ogTitleLeading  = 78
	ogSubLeading    = 40
	ogMaxTitleLines = 3
	ogMaxSubLines   = 2

	// The lowest a subtitle baseline may sit before it would run into the
	// footer row.
	ogFooterGuard = 508
)

// ogTitleTop is the first title baseline, indexed by how many lines the title
// wrapped to. A taller title starts higher so the block as a whole stays
// between the brand mark and the footer instead of growing off the bottom.
var ogTitleTop = map[int]int{0: 240, 1: 240, 2: 212, 3: 186}

// The dark palette, matching the site's dark theme. Cards are viewed as
// thumbnails against whatever chrome the client uses, and the light theme's
// white field tends to disappear into it.
var (
	ogBG     = color.RGBA{0x0D, 0x0E, 0x10, 0xFF}
	ogText   = color.RGBA{0xE5, 0xE5, 0xE2, 0xFF}
	ogMuted  = color.RGBA{0x88, 0x88, 0x84, 0xFF}
	ogAccent = color.RGBA{0xFA, 0xCC, 0x15, 0xFF}
)

var (
	ogFontsOnce sync.Once
	ogBold      *sfnt.Font
	ogRegular   *sfnt.Font
	ogFontsErr  error
)

func ogFonts() (*sfnt.Font, *sfnt.Font, error) {
	ogFontsOnce.Do(func() {
		ogBold, ogFontsErr = sfnt.Parse(interBoldTTF)
		if ogFontsErr != nil {
			ogFontsErr = fmt.Errorf("parse Inter-Bold: %w", ogFontsErr)
			return
		}
		ogRegular, ogFontsErr = sfnt.Parse(interRegularTTF)
		if ogFontsErr != nil {
			ogFontsErr = fmt.Errorf("parse Inter-Regular: %w", ogFontsErr)
		}
	})
	return ogBold, ogRegular, ogFontsErr
}

// OGCard is the content of one social preview.
type OGCard struct {
	Title string
	// Subtitle is the post description, or the site tagline on the home card.
	Subtitle string
	// Kicker sits along the bottom, e.g. the site name.
	Kicker string
	// Meta sits opposite the kicker, e.g. the publication date.
	Meta string
}

func newFace(f *sfnt.Font, sizePx float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size: sizePx,
		DPI:  72, // at 72 DPI, Size is in pixels, which is how the layout below reads
		// Full hinting keeps stems even at the small sizes used for the
		// subtitle; the card is rasterised once and never scaled.
		Hinting: font.HintingFull,
	})
}

// RenderOGCard draws a card and returns it as PNG bytes.
func RenderOGCard(c OGCard) ([]byte, error) {
	boldFont, regularFont, err := ogFonts()
	if err != nil {
		return nil, err
	}

	img := image.NewRGBA(image.Rect(0, 0, ogWidth, ogHeight))
	draw.Draw(img, img.Bounds(), &image.Uniform{ogBG}, image.Point{}, draw.Src)

	titleFace, err := newFace(boldFont, 64)
	if err != nil {
		return nil, err
	}
	defer titleFace.Close()
	subFace, err := newFace(regularFont, 27)
	if err != nil {
		return nil, err
	}
	defer subFace.Close()
	footFace, err := newFace(boldFont, 19)
	if err != nil {
		return nil, err
	}
	defer footFace.Close()

	// Brand mark: the yellow square that opens the site header.
	fillRect(img, ogMargin, 70, 46, 46, ogAccent)

	// Title. Three lines is the ceiling — past that the type would have to
	// shrink far enough to stop reading as a headline in a thumbnail.
	title := strings.TrimSpace(c.Title)
	if title == "" {
		title = "Untitled"
	}
	lines := wrapText(titleFace, title, ogMaxText, ogMaxTitleLines)

	// The stack is laid out downward from a start that depends on how tall the
	// title turned out, so a three-line headline rises instead of pushing the
	// subtitle through the footer. Without this the two overlapped outright on
	// any long title.
	y := ogTitleTop[len(lines)]
	for _, line := range lines {
		drawText(img, titleFace, line, ogMargin, y, ogText)
		y += ogTitleLeading
	}

	// Accent rule under the title, then the subtitle beneath it.
	ruleY := y - ogTitleLeading + 40
	fillRect(img, ogMargin, ruleY, 132, 6, ogAccent)

	if sub := strings.TrimSpace(c.Subtitle); sub != "" {
		sy := ruleY + 56
		// Only as many subtitle lines as clear the footer. Belt and braces
		// against ogTitleTop and the leading drifting apart later.
		room := (ogFooterGuard - sy) / ogSubLeading
		if room > ogMaxSubLines {
			room = ogMaxSubLines
		}
		if room > 0 {
			for _, line := range wrapText(subFace, sub, ogMaxText, room) {
				drawText(img, subFace, line, ogMargin, sy, ogMuted)
				sy += ogSubLeading
			}
		}
	}

	// Footer: kicker left, meta right, both uppercase and letterspaced to match
	// the site's header and footer treatment.
	footY := ogHeight - 62
	if k := strings.TrimSpace(c.Kicker); k != "" {
		drawTracked(img, footFace, strings.ToUpper(k), ogMargin, footY, ogText, fixed.I(2))
	}
	if m := strings.TrimSpace(c.Meta); m != "" {
		text := strings.ToUpper(m)
		w := trackedWidth(footFace, text, fixed.I(2))
		drawTracked(img, footFace, text, ogWidth-ogMargin-w.Ceil(), footY, ogMuted, fixed.I(2))
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode og card: %w", err)
	}
	return buf.Bytes(), nil
}

func fillRect(dst *image.RGBA, x, y, w, h int, c color.Color) {
	draw.Draw(dst, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
}

func drawText(dst *image.RGBA, face font.Face, s string, x, y int, c color.Color) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  &image.Uniform{c},
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

// drawTracked draws a string with extra space between glyphs. font.Drawer has
// no letter-spacing, so the run is drawn a rune at a time with the tracking
// added to each advance.
func drawTracked(dst *image.RGBA, face font.Face, s string, x, y int, c color.Color, tracking fixed.Int26_6) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  &image.Uniform{c},
		Face: face,
		Dot:  fixed.P(x, y),
	}
	for _, r := range s {
		d.DrawString(string(r))
		d.Dot.X += tracking
	}
}

func trackedWidth(face font.Face, s string, tracking fixed.Int26_6) fixed.Int26_6 {
	w := font.MeasureString(face, s)
	if n := len([]rune(s)); n > 0 {
		w += tracking * fixed.Int26_6(n)
	}
	return w
}

// wrapText greedily breaks s into at most maxLines lines that fit maxWidth.
// The final line is ellipsised if there is text left over, so a long title
// degrades to something readable rather than being silently cut mid-word.
func wrapText(face font.Face, s string, maxWidth, maxLines int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	limit := fixed.I(maxWidth)
	var lines []string
	cur := ""
	for i := 0; i < len(words); i++ {
		candidate := words[i]
		if cur != "" {
			candidate = cur + " " + words[i]
		}
		if font.MeasureString(face, candidate) <= limit {
			cur = candidate
			continue
		}
		// The word does not fit on the current line.
		if cur == "" {
			// ...and does not fit on a line of its own either: hard-break it
			// rather than loop forever or overflow the card.
			head, tail := breakLongWord(face, words[i], limit)
			lines = append(lines, head)
			words[i] = tail
			i--
		} else {
			lines = append(lines, cur)
			cur = ""
			i--
		}
		if len(lines) == maxLines {
			return ellipsise(face, lines, limit)
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// ellipsise marks the last line as truncated, trimming characters until the
// ellipsis fits.
func ellipsise(face font.Face, lines []string, limit fixed.Int26_6) []string {
	last := lines[len(lines)-1]
	for last != "" {
		if font.MeasureString(face, last+"…") <= limit {
			break
		}
		last = strings.TrimRight(last[:len(last)-1], " ")
	}
	lines[len(lines)-1] = strings.TrimRight(last, " ") + "…"
	return lines
}

// breakLongWord splits a single unbreakable run at the last rune that fits.
func breakLongWord(face font.Face, word string, limit fixed.Int26_6) (head, tail string) {
	runes := []rune(word)
	for i := 1; i <= len(runes); i++ {
		if font.MeasureString(face, string(runes[:i])) > limit {
			if i == 1 {
				// Even one glyph overflows; emit it anyway to guarantee progress.
				return string(runes[:1]), string(runes[1:])
			}
			return string(runes[:i-1]), string(runes[i-1:])
		}
	}
	return word, ""
}
