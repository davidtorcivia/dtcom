// Package build is the static site rebuild engine.
package build

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode-only support for .webp uploads
)

// MaxImagePixels bounds the decoded size of an uploaded image.
//
// A small file can decode to an enormous bitmap (a "decompression bomb"): a
// few hundred KB of PNG can claim 30000x30000 pixels, which is 3.6 GB of RGBA.
// DecodeConfig reads only the header, so the dimensions are checked before any
// pixel memory is allocated. 50 MP is well past any photograph a post needs.
const MaxImagePixels = 50 << 20

// MaxImageDim is the longest side kept for a stored image. Anything larger is
// downscaled — a 6000px original serves no one on a page whose content column
// is under 800px.
const MaxImageDim = 2000

// ErrUnsupportedImage means the bytes weren't a decodable image in a format we
// serve.
var ErrUnsupportedImage = fmt.Errorf("unsupported image format")

// ProcessedImage is the result of normalizing an upload.
type ProcessedImage struct {
	Data   []byte
	Ext    string // ".jpg" or ".png"
	Width  int
	Height int
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
	src = scaleToFit(src, maxDim)
	b := src.Bounds()

	// PNG and GIF may carry transparency, which a JPEG would flatten to black;
	// everything else becomes JPEG, which is far smaller for photographs.
	out := &ProcessedImage{Width: b.Dx(), Height: b.Dy()}
	var buf bytes.Buffer
	switch format {
	case "png", "gif":
		out.Ext = ".png"
		err = png.Encode(&buf, src)
	default:
		out.Ext = ".jpg"
		err = jpeg.Encode(&buf, src, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", out.Ext, err)
	}
	out.Data = buf.Bytes()
	return out, nil
}

// scaleToFit returns src downscaled so its longest side is at most maxDim.
// Images already within the limit are returned untouched.
func scaleToFit(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return src
	}
	scale := float64(maxDim) / float64(max(w, h))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
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
	dst := scaleToFit(src, maxDim)

	out, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer out.Close()
	switch strings.ToLower(filepath.Ext(outPath)) {
	case ".png":
		err = png.Encode(out, dst)
	case ".jpg", ".jpeg":
		err = jpeg.Encode(out, dst, &jpeg.Options{Quality: 85})
	case ".gif":
		err = gif.Encode(out, dst, nil)
	default:
		err = fmt.Errorf("unsupported output extension %s", filepath.Ext(outPath))
	}
	return outPath, err
}
