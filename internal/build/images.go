// Package build is the static site rebuild engine.
package build

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

// ResizeImage decodes srcPath, resizes so its longest side is maxDim pixels
// (preserving aspect ratio), and writes to outPath. Images already smaller
// than maxDim are copied as-is. Format is inferred from the file extension.
func ResizeImage(srcPath, outPath string, maxDim int) (string, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return "", err
		}
		return outPath, os.WriteFile(outPath, data, 0o644)
	}
	scale := float64(maxDim) / float64(max(w, h))
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer out.Close()
	ext := strings.ToLower(filepath.Ext(outPath))
	switch ext {
	case ".png":
		err = png.Encode(out, dst)
	case ".jpg", ".jpeg":
		err = jpeg.Encode(out, dst, &jpeg.Options{Quality: 85})
	default:
		err = fmt.Errorf("unsupported output extension %s", ext)
	}
	return outPath, err
}
