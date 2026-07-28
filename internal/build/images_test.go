package build

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestResizeImage(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// make a 3000x2000 solid jpeg
	img := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for y := 0; y < 2000; y++ {
		for x := 0; x < 3000; x++ {
			img.Set(x, y, color.RGBA{R: 255})
		}
	}
	srcPath := filepath.Join(src, "big.jpg")
	f, _ := os.Create(srcPath)
	_ = jpeg.Encode(f, img, nil)
	f.Close()

	outPath, err := ResizeImage(srcPath, filepath.Join(dst, "big.jpg"), 1600)
	if err != nil {
		t.Fatalf("ResizeImage: %v", err)
	}
	g, err := decodeImage(outPath)
	if err != nil {
		t.Fatal(err)
	}
	b := g.Bounds()
	if b.Dx() != 1600 {
		t.Errorf("width = %d, want 1600", b.Dx())
	}
}

func decodeImage(p string) (image.Image, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
