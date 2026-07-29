// Package build is the static site rebuild engine.
package build

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	nativewebp "github.com/HugoSmits86/nativewebp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode-only support for .webp uploads
)

// MaxImagePixels bounds the decoded size of an uploaded image.
//
// A small file can decode to an enormous bitmap (a "decompression bomb"): a
// few hundred KB of PNG can claim 30000x30000 pixels, which is 3.6 GB of RGBA.
// DecodeConfig reads only the header, so the dimensions are checked before any
// pixel memory is allocated. 50 MP is well past any photograph a post needs.
const MaxImagePixels = 50 << 20

// MaxImageDim is the longest side kept for a stored image.
//
// This is the master: the file the lightbox opens when a reader wants to look
// closely, and the one every smaller rendition is cut from. It is deliberately
// larger than any column the site has — a phone at 3x shows a 2560px picture
// at full resolution only once it is zoomed to a third of the screen — and
// deliberately not larger than that, because a master is measured in megabytes
// and someone on a train pays for the difference.
//
// Nothing but the lightbox ever loads it. Pages get the renditions below.
const MaxImageDim = 2560

// VariantWidths are the rendition widths generated for every stored image.
//
// Chosen against the layout rather than as round numbers: 480 and 768 cover
// phones at 1x and 2x, 1080 is the widest the reading column ever gets, and
// 1440 and 2048 are that column on a 2x display and a little beyond. A width
// at or above the master's own is skipped — there is nothing to cut.
var VariantWidths = []int{480, 768, 1080, 1440, 2048}

// jpegQuality is used for photographic renditions. 85 is the usual sweet spot;
// the master is the copy to go back to, so a rendition does not have to be
// archival.
const jpegQuality = 85

// ErrUnsupportedImage means the bytes weren't a decodable image in a format we
// serve.
var ErrUnsupportedImage = fmt.Errorf("unsupported image format")

// ProcessedImage is the result of normalizing an upload.
type ProcessedImage struct {
	Data   []byte
	Ext    string // ".jpg" or ".png"
	Width  int
	Height int
	Alpha  bool
}

// ProcessImage decodes an uploaded image, rejects implausible dimensions,
// downscales it to at most maxDim on its longest side, and re-encodes it.
//
// Re-encoding is the point: it drops EXIF (which carries GPS coordinates and
// camera serial numbers), and it guarantees the bytes served are a real image
// in a format we chose, not whatever the client claimed to upload.
func ProcessImage(r io.Reader, maxDim int) (*ProcessedImage, error) {
	if maxDim <= 0 {
		maxDim = MaxImageDim
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrUnsupportedImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxImagePixels {
		return nil, fmt.Errorf("image is %dx%d, over the %d-pixel limit", cfg.Width, cfg.Height, MaxImagePixels)
	}

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrUnsupportedImage
	}
	src = scaleLongestSide(src, maxDim)
	b := src.Bounds()

	// Two things put a picture on the lossless path, and either is enough.
	//
	// Transparency is the one that cannot be got wrong, and the decoded pixels
	// decide it, not the format the decoder named: a WebP or a TIFF carries an
	// alpha channel just as a PNG does, and asking the name rather than the
	// image is how a cut-out ends up flattened onto black by the JPEG encoder.
	//
	// The other is the author's own choice. Someone who saved a chart as PNG
	// decided its text and flat colour were worth the bytes, and re-encoding it
	// as a JPEG would ring every edge they were protecting.
	out := &ProcessedImage{Width: b.Dx(), Height: b.Dy(), Alpha: hasAlpha(src)}
	var buf bytes.Buffer
	if out.Alpha || format == "png" || format == "gif" {
		out.Ext = ".png"
		err = encodePNG(&buf, src)
	} else {
		// The master is what a reader zooms into, so it is kept above the
		// quality a rendition gets: this is the copy there is no going back to.
		out.Ext = ".jpg"
		err = jpeg.Encode(&buf, src, &jpeg.Options{Quality: 92})
	}
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", out.Ext, err)
	}
	out.Data = buf.Bytes()
	return out, nil
}

// hasAlpha reports whether any pixel is less than fully opaque.
//
// The concrete type answers it outright for the formats that cannot carry
// transparency, which is most photographs; only the types that can are scanned,
// and that scan stops at the first pixel it finds.
func hasAlpha(im image.Image) bool {
	switch im.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		return false
	}
	if o, ok := im.(interface{ Opaque() bool }); ok {
		// image.Opaque is exact for the standard types and cheaper than the
		// loop below, which is here for anything that does not implement it.
		return !o.Opaque()
	}
	b := im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := im.At(x, y).RGBA(); a != 0xffff {
				return true
			}
		}
	}
	return false
}

// encodePNG writes a full-colour PNG. The compression level is the encoder's
// best: these files are written once and served for years, so the trade is
// entirely in favour of the reader.
//
// Deliberately never paletted. Quantising to 256 colours would shrink a chart
// nicely and would also reduce a soft anti-aliased edge over a transparent
// background to a hard one, which is the visible half of "we broke the
// transparency".
func encodePNG(w io.Writer, im image.Image) error {
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	return enc.Encode(w, im)
}

// encodeWebP writes a lossless WebP.
//
// Lossless is not a preference here, it is the requirement: this path exists
// for pictures with transparency, and a lossy WebP would resample the colour
// under every partly-transparent pixel. Verified bit-exact against the
// resampled source, alpha included, by TestVariantsPreserveTransparency.
//
// The encoder is handed non-premultiplied pixels, which is the form WebP
// stores. Passing an *image.RGBA — where colour is already multiplied by alpha
// — would darken every soft edge towards black.
func encodeWebP(w io.Writer, im image.Image) error {
	return nativewebp.Encode(w, toNRGBA(im), &nativewebp.Options{})
}

// toNRGBA converts to straight (non-premultiplied) alpha, leaving an image
// that is already in that form alone.
func toNRGBA(im image.Image) *image.NRGBA {
	if n, ok := im.(*image.NRGBA); ok {
		return n
	}
	out := image.NewNRGBA(im.Bounds())
	draw.Draw(out, out.Bounds(), im, im.Bounds().Min, draw.Src)
	return out
}

// scaleLongestSide returns src downscaled so its longest side is at most
// maxDim. Images already within the limit are returned untouched.
func scaleLongestSide(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return src
	}
	scale := float64(maxDim) / float64(max(w, h))
	return resample(src, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale)))
}

// scaleToWidth returns src downscaled to exactly w pixels wide, keeping its
// aspect ratio. Used for the renditions, which are chosen by width because
// that is what a browser matches a srcset against.
func scaleToWidth(src image.Image, w int) image.Image {
	b := src.Bounds()
	if b.Dx() <= w {
		return src
	}
	h := max(1, int(float64(b.Dy())*float64(w)/float64(b.Dx())))
	return resample(src, w, h)
}

// resample does the actual work, in premultiplied RGBA.
//
// Premultiplied is the correct space to resample in: averaging straight
// colour across a transparent edge pulls in whatever hue happens to be stored
// under the invisible pixels, which is how a dark halo appears around cut-out
// artwork. The encoders convert back on the way out.
func resample(src image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// ResizeImage decodes srcPath, resizes so its longest side is maxDim pixels
// (preserving aspect ratio), and writes to outPath. Images already smaller
// than maxDim are copied as-is. Format is inferred from the file extension.
func ResizeImage(srcPath, outPath string, maxDim int) (string, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("decode config: %w", err)
	}
	if cfg.Width*cfg.Height > MaxImagePixels {
		return "", fmt.Errorf("image is %dx%d, over the %d-pixel limit", cfg.Width, cfg.Height, MaxImagePixels)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	b := src.Bounds()
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if b.Dx() <= maxDim && b.Dy() <= maxDim {
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return "", err
		}
		return outPath, os.WriteFile(outPath, data, 0o644)
	}
	dst := scaleLongestSide(src, maxDim)

	out, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer out.Close()
	switch strings.ToLower(filepath.Ext(outPath)) {
	case ".png":
		err = encodePNG(out, dst)
	case ".jpg", ".jpeg":
		err = jpeg.Encode(out, dst, &jpeg.Options{Quality: jpegQuality})
	case ".gif":
		err = gif.Encode(out, dst, nil)
	default:
		err = fmt.Errorf("unsupported output extension %s", filepath.Ext(outPath))
	}
	return outPath, err
}
