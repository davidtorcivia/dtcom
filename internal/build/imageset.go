package build

// Responsive renditions.
//
// A stored image is a master — up to MaxImageDim on its longest side, in the
// format that keeps it honest. What a page loads is never that file. Beside
// each master sit renditions at the widths in VariantWidths, named after it:
//
//	9f3c….png            the master, opened by the lightbox
//	9f3c….w768.png       a rendition, in the master's own format
//	9f3c….w768.webp      the same rendition as lossless WebP
//	9f3c….webp           the master as lossless WebP
//
// The name carries the width so a browser can be told about every rendition in
// one srcset and pick for itself, and so this package can tell which files
// exist by reading the directory rather than keeping a database of them. The
// hash prefix is the master's content, so a rendition's URL is as immutable as
// the master's and inherits the same year-long cache header.
//
// Which formats a master gets is decided by the master:
//
//   - PNG masters — anything with transparency, and anything the author chose
//     to keep lossless — get PNG renditions and lossless WebP renditions. The
//     WebP is the smaller of the two for every picture measured, by around a
//     fifth, and is bit-identical to the PNG. Lossy WebP is not used at all: it
//     resamples the colour beneath every partly-transparent pixel.
//   - JPEG masters — photographs — get JPEG renditions and nothing else.
//     Lossless WebP of a photograph runs about seven times the size of the
//     JPEG, so offering it would be a pessimisation dressed as a modern format.
//
// SVG is skipped entirely. It is a drawing, not a raster: it is already every
// size at once.

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"davidtorcivia.com/dtcom/internal/markdown"
)

// variantSuffix builds the ".w768" infix used in a rendition's name.
func variantSuffix(w int) string { return ".w" + strconv.Itoa(w) }

// masterName reports whether name is a master (as opposed to one of its
// renditions) and strips the extension.
func masterName(name string) (base, ext string, ok bool) {
	ext = strings.ToLower(filepath.Ext(name))
	base = strings.TrimSuffix(name, filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg":
	default:
		return "", "", false
	}
	// A rendition ends in ".w<number>"; so does a master whose hash somehow
	// ended that way, which cannot happen — a hash is hex.
	if i := strings.LastIndex(base, ".w"); i >= 0 {
		if _, err := strconv.Atoi(base[i+2:]); err == nil {
			return "", "", false
		}
	}
	return base, ext, true
}

// GenerateVariants writes any renditions of one master that are not on disk
// yet, and returns the names it wrote.
//
// Doing nothing is the common case and has to be cheap: every master is
// offered to this function on every startup. A rendition that already exists is
// left alone, and a master small enough to need none is answered from its
// header without decoding a pixel.
func GenerateVariants(dir, name string) ([]string, error) {
	base, ext, ok := masterName(name)
	if !ok {
		return nil, nil
	}
	path := filepath.Join(dir, name)

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxImagePixels {
		return nil, fmt.Errorf("%s is %dx%d", name, cfg.Width, cfg.Height)
	}
	lossless := ext == ".png"

	// Work out what is missing before decoding, so a directory that is already
	// complete costs one header read per image.
	type job struct {
		name  string
		width int // 0 means the master's own size
		webp  bool
	}
	var todo []job
	missing := func(j job) bool {
		_, err := os.Stat(filepath.Join(dir, j.name))
		return err != nil
	}
	for _, w := range VariantWidths {
		// A rendition is only worth making if it is meaningfully smaller than
		// the master; one within a few per cent would be a second copy.
		if w >= cfg.Width*95/100 {
			continue
		}
		j := job{name: base + variantSuffix(w) + ext, width: w}
		if missing(j) {
			todo = append(todo, j)
		}
		if lossless {
			j := job{name: base + variantSuffix(w) + ".webp", width: w, webp: true}
			if missing(j) {
				todo = append(todo, j)
			}
		}
	}
	if lossless {
		// The master as WebP, for the lightbox: same pixels, fewer bytes.
		j := job{name: base + ".webp", webp: true}
		if missing(j) {
			todo = append(todo, j)
		}
	}
	if len(todo) == 0 {
		return nil, nil
	}

	src, err := decodeFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	written := make([]string, 0, len(todo))
	for _, j := range todo {
		im := src
		if j.width > 0 {
			im = scaleToWidth(src, j.width)
		}
		var buf []byte
		buf, err = encodeVariant(im, j.webp, ext)
		if err != nil {
			return written, fmt.Errorf("%s: %w", j.name, err)
		}
		if err := writeImageAtomic(filepath.Join(dir, j.name), buf); err != nil {
			return written, err
		}
		written = append(written, j.name)
	}
	return written, nil
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	im, _, err := image.Decode(f)
	return im, err
}

func encodeVariant(im image.Image, webp bool, ext string) ([]byte, error) {
	var b bytes.Buffer
	var err error
	switch {
	case webp:
		err = encodeWebP(&b, im)
	case ext == ".png":
		err = encodePNG(&b, im)
	default:
		err = jpeg.Encode(&b, im, &jpeg.Options{Quality: jpegQuality})
	}
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// writeImageAtomic writes through a temp file in the same directory, so a
// rebuild reading the directory never lists a rendition that is still being
// written — the name appears only once the bytes are all there.
func writeImageAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ImageIndex answers "what renditions does this image have, and how big is it"
// for the renderer, by reading the images directory.
//
// The answers are cached: a rebuild renders every article, and several of them
// share pictures. Refresh drops the cache, and is called at the top of each
// rebuild so an image uploaded since the last one is seen.
type ImageIndex struct {
	dir string

	mu    sync.Mutex
	cache map[string]*markdown.ImageInfo // by file name
}

func NewImageIndex(dir string) *ImageIndex {
	return &ImageIndex{dir: dir, cache: map[string]*markdown.ImageInfo{}}
}

// Refresh empties the cache. Cheap, and correct: the next lookup rereads.
func (ix *ImageIndex) Refresh() {
	if ix == nil {
		return
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.cache = map[string]*markdown.ImageInfo{}
}

// Backfill generates the renditions for every master in the directory,
// returning how many files it wrote. Idempotent, so it is safe to run on every
// startup: images uploaded before this pipeline existed get their renditions on
// the next boot, and everything else costs a header read.
func (ix *ImageIndex) Backfill() (int, error) {
	if ix == nil {
		return 0, nil
	}
	entries, err := os.ReadDir(ix.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var wrote int
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names, err := GenerateVariants(ix.dir, e.Name())
		if err != nil && firstErr == nil {
			firstErr = err
		}
		wrote += len(names)
	}
	if wrote > 0 {
		ix.Refresh()
	}
	return wrote, firstErr
}

// Resolve implements markdown.ImageResolver. src is the URL as written in the
// post, e.g. "/images/9f3c….png".
func (ix *ImageIndex) Resolve(src string) (*markdown.ImageInfo, bool) {
	if ix == nil {
		return nil, false
	}
	name, ok := imageFileName(src)
	if !ok {
		return nil, false
	}

	ix.mu.Lock()
	if info, hit := ix.cache[name]; hit {
		ix.mu.Unlock()
		return info, info != nil
	}
	ix.mu.Unlock()

	info := ix.build(name)

	ix.mu.Lock()
	ix.cache[name] = info
	ix.mu.Unlock()
	return info, info != nil
}

// imageFileName pulls the file name out of a local /images/ URL, rejecting
// anything that points elsewhere or tries to climb out of the directory.
func imageFileName(src string) (string, bool) {
	const prefix = "/images/"
	if !strings.HasPrefix(src, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(src, prefix)
	if name == "" || strings.ContainsAny(name, "/?#") || strings.Contains(name, "..") {
		return "", false
	}
	return name, true
}

// build reads one master's dimensions and lists the renditions beside it.
// Returns nil for anything that is not a raster master this package made — an
// SVG, or an image whose file has gone missing — which the renderer treats as
// "leave this tag alone".
func (ix *ImageIndex) build(name string) *markdown.ImageInfo {
	base, ext, ok := masterName(name)
	if !ok {
		return nil
	}
	f, err := os.Open(filepath.Join(ix.dir, name))
	if err != nil {
		return nil
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil
	}

	info := &markdown.ImageInfo{
		Master: "/images/" + name,
		Width:  cfg.Width,
		Height: cfg.Height,
	}
	exists := func(n string) bool {
		st, err := os.Stat(filepath.Join(ix.dir, n))
		return err == nil && st.Size() > 0
	}
	for _, w := range VariantWidths {
		if w >= cfg.Width {
			continue
		}
		if n := base + variantSuffix(w) + ext; exists(n) {
			info.Fallback = append(info.Fallback, markdown.Rendition{Width: w, URL: "/images/" + n})
		}
		if n := base + variantSuffix(w) + ".webp"; exists(n) {
			info.WebP = append(info.WebP, markdown.Rendition{Width: w, URL: "/images/" + n})
		}
	}
	// The master closes both lists, so a wide display is not left choosing the
	// largest rendition when the full picture is right there.
	if n := base + ".webp"; exists(n) {
		info.MasterWebP = "/images/" + n
		info.WebP = append(info.WebP, markdown.Rendition{Width: cfg.Width, URL: info.MasterWebP})
	}
	info.Fallback = append(info.Fallback, markdown.Rendition{Width: cfg.Width, URL: info.Master})

	sort.Slice(info.WebP, func(i, j int) bool { return info.WebP[i].Width < info.WebP[j].Width })
	sort.Slice(info.Fallback, func(i, j int) bool { return info.Fallback[i].Width < info.Fallback[j].Width })
	return info
}
