package build

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
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

// TestProcessImageKeepsTransparency: the master format is decided by the
// pixels. An upload with an alpha channel has to land on the lossless path
// whatever its container was — a WebP or a TIFF carries transparency as
// readily as a PNG, and sending one to the JPEG encoder composites it onto
// black without erroring.
func TestProcessImageKeepsTransparency(t *testing.T) {
	// Encoded as WebP, which is neither of the two extensions the old code
	// checked for, and decoded through the registered x/image/webp reader.
	src := transparentFixture(400, 300)
	var enc bytes.Buffer
	if err := encodeWebP(&enc, src); err != nil {
		t.Fatal(err)
	}

	got, err := ProcessImage(bytes.NewReader(enc.Bytes()), 2000)
	if err != nil {
		t.Fatalf("ProcessImage: %v", err)
	}
	if !got.Alpha {
		t.Error("transparency not detected")
	}
	if got.Ext != ".png" {
		t.Fatalf("stored as %s, want .png — a JPEG would have flattened it onto black", got.Ext)
	}
	im, _, err := image.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	var transparent int
	b := im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := im.At(x, y).RGBA(); a == 0 {
				transparent++
			}
		}
	}
	if transparent == 0 {
		t.Error("the stored master has no transparent pixels left")
	}
}

// TestProcessImageOpaquePNGStaysLossless: a chart saved as PNG without any
// transparency is still a chart. The author chose lossless; re-encoding it as
// a JPEG would ring every line of text on it.
func TestProcessImageOpaquePNGStaysLossless(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 500, 400))
	draw.Draw(im, im.Bounds(), &image.Uniform{color.RGBA{250, 250, 248, 255}}, image.Point{}, draw.Src)
	var enc bytes.Buffer
	if err := encodePNG(&enc, im); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessImage(bytes.NewReader(enc.Bytes()), 2000)
	if err != nil {
		t.Fatal(err)
	}
	if got.Alpha {
		t.Error("reported transparency it does not have")
	}
	if got.Ext != ".png" {
		t.Errorf("stored as %s, want .png", got.Ext)
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
