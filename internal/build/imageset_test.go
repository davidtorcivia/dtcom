package build

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nativewebp "github.com/HugoSmits86/nativewebp"
)

// transparentFixture is a picture of the kind this pipeline exists to protect:
// a fully transparent background, opaque marks on it, and a band of partly
// transparent pixels between them — the soft edge that a careless encode turns
// into a dark fringe.
func transparentFixture(w, h int) *image.NRGBA {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x%97 < 20 && y%89 < 20:
				// opaque, saturated, and different in every channel so a
				// channel swap or a chroma shift cannot hide
				im.Set(x, y, color.NRGBA{R: uint8(x % 251), G: uint8(y % 241), B: 200, A: 255})
			case x%97 < 26 && y%89 < 26:
				// the anti-aliased rim
				im.Set(x, y, color.NRGBA{R: 240, G: 30, B: 90, A: uint8(40 + (x+y)%160)})
			default:
				im.Set(x, y, color.NRGBA{}) // transparent
			}
		}
	}
	return im
}

func writePNG(t *testing.T, path string, im image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(f, im); err != nil {
		t.Fatal(err)
	}
}

// TestVariantsPreserveTransparency is the guarantee the whole lossless path
// exists for: every rendition of a picture with an alpha channel carries that
// alpha through exactly, and the WebP rendition is bit-identical to the PNG one
// it sits beside.
//
// Deliberately run against GenerateVariants rather than the encoders directly.
// Feeding an encoder a hand-built image proves the encoder works; it does not
// prove the pipeline hands it the right thing, and premultiplied pixels reaching
// a WebP encoder is exactly the mistake that would slip through such a test.
func TestVariantsPreserveTransparency(t *testing.T) {
	dir := t.TempDir()
	master := transparentFixture(1300, 800)
	writePNG(t, filepath.Join(dir, "abc.png"), master)

	written, err := GenerateVariants(dir, "abc.png")
	if err != nil {
		t.Fatalf("GenerateVariants: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("no renditions written")
	}

	var checkedWebP, checkedPNG int
	for _, name := range written {
		got, err := decodeImage(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b := got.Bounds()

		// Alpha must survive: both the fully transparent field and the soft rim.
		var transparent, partial, opaque int
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				_, _, _, a := got.At(x, y).RGBA()
				switch {
				case a == 0:
					transparent++
				case a == 0xffff:
					opaque++
				default:
					partial++
				}
			}
		}
		if transparent == 0 {
			t.Errorf("%s: no fully transparent pixels — the background was filled in", name)
		}
		if partial == 0 {
			t.Errorf("%s: no partly transparent pixels — the soft edges were hardened", name)
		}
		if opaque == 0 {
			t.Errorf("%s: nothing opaque left", name)
		}

		if strings.HasSuffix(name, ".webp") {
			checkedWebP++
			// The WebP rendition and the PNG rendition of the same width are
			// the same picture, so they must agree pixel for pixel.
			twin := strings.TrimSuffix(name, ".webp") + ".png"
			if _, err := os.Stat(filepath.Join(dir, twin)); err != nil {
				continue // the master's own WebP has no ".png" twin by that name
			}
			ref, err := decodeImage(filepath.Join(dir, twin))
			if err != nil {
				t.Fatal(err)
			}
			if diff := comparePixels(ref, got); diff != "" {
				t.Errorf("%s differs from %s: %s", name, twin, diff)
			}
		} else {
			checkedPNG++
		}
	}
	if checkedWebP == 0 || checkedPNG == 0 {
		t.Fatalf("expected both PNG and WebP renditions, got %d and %d", checkedPNG, checkedWebP)
	}
}

// TestWebPEncodeIsExact pins the property the format choice rests on: the
// lossless encoder returns precisely what it was given, alpha included. If a
// future dependency bump quietly starts approximating, this fails rather than
// the site quietly losing fidelity.
func TestWebPEncodeIsExact(t *testing.T) {
	src := transparentFixture(320, 200)
	var buf bytes.Buffer
	if err := encodeWebP(&buf, src); err != nil {
		t.Fatalf("encodeWebP: %v", err)
	}
	got, err := nativewebp.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if diff := comparePixels(src, got); diff != "" {
		t.Errorf("lossless round-trip changed the image: %s", diff)
	}
}

// TestVariantsFromPremultipliedSource covers the resampled path specifically:
// renditions are produced by scaling, which happens in premultiplied RGBA, and
// what reaches the encoder must have been converted back. A miss here shows up
// as darkened soft edges rather than as an error.
func TestVariantsFromPremultipliedSource(t *testing.T) {
	// A red disc fading to transparent — premultiplication error is most
	// visible where alpha is low and the colour is bright.
	const size = 900
	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x-size/2), float64(y-size/2))
			a := 255 - int(d)
			if a < 0 {
				a = 0
			}
			im.Set(x, y, color.NRGBA{R: 255, G: 40, B: 40, A: uint8(a)})
		}
	}
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "disc.png"), im)
	if _, err := GenerateVariants(dir, "disc.png"); err != nil {
		t.Fatalf("GenerateVariants: %v", err)
	}

	got, err := decodeImage(filepath.Join(dir, "disc.w480.webp"))
	if err != nil {
		t.Fatal(err)
	}
	// Sample the low-alpha rim. Straight colour must still be the red it was;
	// if premultiplied pixels reached the encoder it will have slid to black.
	n := toNRGBA(got)
	var samples, dark int
	b := n.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := n.PixOffset(x, y)
			a := n.Pix[i+3]
			if a == 0 || a > 60 {
				continue
			}
			samples++
			if n.Pix[i] < 128 { // red channel, which should be ~255 everywhere
				dark++
			}
		}
	}
	if samples == 0 {
		t.Fatal("no low-alpha pixels to check")
	}
	if dark > samples/50 {
		t.Errorf("%d of %d low-alpha pixels lost their colour — premultiplied pixels reached the encoder",
			dark, samples)
	}
}

// TestGenerateVariantsIsIdempotent: startup offers every image to the generator
// on every boot, so the second run has to do nothing at all.
func TestGenerateVariantsIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "abc.png"), transparentFixture(1200, 700))

	first, err := GenerateVariants(dir, "abc.png")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("first run wrote nothing")
	}
	second, err := GenerateVariants(dir, "abc.png")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("second run rewrote %v", second)
	}
	// And a rendition is never mistaken for a master of its own.
	if again, err := GenerateVariants(dir, "abc.w480.png"); err != nil || len(again) != 0 {
		t.Errorf("renditions of a rendition: %v, %v", again, err)
	}
}

// TestPhotographsGetNoWebP: lossless WebP of a photograph is several times the
// size of the JPEG, so the JPEG path must not generate one.
func TestPhotographsGetNoWebP(t *testing.T) {
	dir := t.TempDir()
	im := image.NewRGBA(image.Rect(0, 0, 1400, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1400; x++ {
			im.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8((x * y) % 256), A: 255})
		}
	}
	f, err := os.Create(filepath.Join(dir, "photo.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := encodeVariantToFile(f, im); err != nil {
		t.Fatal(err)
	}
	f.Close()

	written, err := GenerateVariants(dir, "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("no renditions written")
	}
	for _, n := range written {
		if strings.HasSuffix(n, ".webp") {
			t.Errorf("photograph got a WebP rendition: %s", n)
		}
	}
}

func encodeVariantToFile(f *os.File, im image.Image) error {
	b, err := encodeVariant(im, false, ".jpg")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return err
}

// comparePixels returns a description of the first meaningful difference, or
// "" when the two images match. Compared in straight alpha, since that is the
// form the difference matters in: premultiplied rounding is not a defect.
func comparePixels(want, got image.Image) string {
	if want.Bounds() != got.Bounds() {
		return "bounds " + got.Bounds().String() + " want " + want.Bounds().String()
	}
	a := toNRGBA(want)
	b := toNRGBA(got)
	var colourDiff, alphaDiff int
	for i := 0; i < len(a.Pix); i += 4 {
		if a.Pix[i+3] != b.Pix[i+3] {
			alphaDiff++
			continue
		}
		if a.Pix[i+3] == 0 {
			// Colour under a fully transparent pixel is not visible and not
			// defined; encoders are free to store anything there.
			continue
		}
		if a.Pix[i] != b.Pix[i] || a.Pix[i+1] != b.Pix[i+1] || a.Pix[i+2] != b.Pix[i+2] {
			colourDiff++
		}
	}
	if alphaDiff > 0 || colourDiff > 0 {
		return itoa(alphaDiff) + " alpha and " + itoa(colourDiff) + " colour samples differ"
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestIndexBuildsRenditionLists checks the shape handed to the renderer: both
// lists ascend, both end at the master, and the master's own WebP is offered.
func TestIndexBuildsRenditionLists(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "abc.png"), transparentFixture(1600, 900))
	if _, err := GenerateVariants(dir, "abc.png"); err != nil {
		t.Fatal(err)
	}

	ix := NewImageIndex(dir)
	info, ok := ix.Resolve("/images/abc.png")
	if !ok {
		t.Fatal("master not resolved")
	}
	if info.Width != 1600 || info.Height != 900 {
		t.Errorf("dimensions = %dx%d, want 1600x900", info.Width, info.Height)
	}
	if info.MasterWebP == "" {
		t.Error("no WebP master offered")
	}
	if n := len(info.WebP); n < 2 {
		t.Errorf("WebP renditions = %d, want several", n)
	}
	if n := len(info.Fallback); n < 2 {
		t.Errorf("fallback renditions = %d, want several", n)
	}
	last := info.Fallback[len(info.Fallback)-1]
	if last.Width != 1600 || last.URL != "/images/abc.png" {
		t.Errorf("largest fallback = %+v, want the master", last)
	}
	for i := 1; i < len(info.WebP); i++ {
		if info.WebP[i].Width <= info.WebP[i-1].Width {
			t.Errorf("WebP renditions not ascending: %+v", info.WebP)
			break
		}
	}

	// Things that are not masters of ours resolve to nothing, and the renderer
	// leaves those tags alone.
	for _, src := range []string{"/images/nope.png", "https://example.com/x.png", "/images/../secret", "/static/app.js"} {
		if _, ok := ix.Resolve(src); ok {
			t.Errorf("%s resolved, want not found", src)
		}
	}
}
