package backup

// The image pool.
//
// An uploaded picture's name is the SHA-256 of its own bytes, and nothing ever
// rewrites one: storeUploadedImage skips the write when the name already
// exists. A master is therefore immutable, and putting a copy of it inside
// every archive was storing the same bytes over and over — on this site, 96% of
// each archive was images, and the same ten files appeared in every one of
// them.
//
// So the images live once, beside the archives, and an archive names the ones
// it needs. Thirty archives now cost about what one did.
//
// Two things this must not break, and does not:
//
//   - A downloaded archive is still self-contained. The download path streams
//     the named images back into the tar on the way out, so what lands in the
//     browser is the same complete file it always was, and restoring from it
//     elsewhere needs nothing else. See assemble in download.go.
//   - An archive written by the old format still restores. Those carry their
//     images inside; the restore path uses what it finds there and only reaches
//     for the pool when the archive has none.
//
// On one filesystem the pool costs no space at all: entries are hard links to
// the live image, so the bytes exist once and stay alive as long as either name
// does. That is safe precisely because masters are immutable — there is no
// writer to surprise the other link. Across filesystems it falls back to a copy.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// poolDir is the subdirectory of the destination that holds the images.
const poolDir = "images"

// putPool copies (or links) one image into the pool if it is not there already.
func (s *Service) putPool(name, srcPath string) error {
	return s.dest.PutObject(poolDir+"/"+name, srcPath)
}

// referencedImages collects the image names every archive currently held still
// needs, by reading each one's manifest.
//
// The manifest is written first into the tar precisely so this can stop after
// one entry rather than decompressing whole archives.
func (s *Service) referencedImages() (map[string]bool, error) {
	list, err := s.dest.List()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, in := range list {
		man, err := s.manifestOf(in.Name)
		if err != nil {
			// An unreadable archive is not a licence to delete the images it
			// might have named. Fail the sweep instead.
			return nil, fmt.Errorf("read manifest of %s: %w", in.Name, err)
		}
		for _, n := range man.PooledImages() {
			out[n] = true
		}
	}
	return out, nil
}

// sweepPool deletes pooled images no remaining archive names.
func (s *Service) sweepPool() (int, error) {
	referenced, err := s.referencedImages()
	if err != nil {
		return 0, err
	}
	objects, err := s.dest.ListObjects(poolDir + "/")
	if err != nil {
		return 0, err
	}
	var removed int
	for _, obj := range objects {
		name := strings.TrimPrefix(obj.Name, poolDir+"/")
		if referenced[name] {
			continue
		}
		// Just-written images are not yet named by any listed archive.
		if time.Since(obj.Modified) < unusedSince {
			continue
		}
		if err := s.dest.DeleteObject(obj.Name); err != nil {
			continue
		}
		removed++
	}
	return removed, nil
}

// PoolSize reports how many images the pool holds and what they occupy, for
// the admin page.
func (s *Service) PoolSize() (int, int64) {
	objects, err := s.dest.ListObjects(poolDir + "/")
	if err != nil {
		return 0, 0
	}
	var total int64
	for _, o := range objects {
		total += o.Size
	}
	return len(objects), total
}

// stageObject writes a pooled image to a temporary file, for the restore path
// which needs a plain file to copy into place.
func (s *Service) stageObject(name, dst string) error {
	rc, _, err := s.dest.GetObject(poolDir + "/" + name)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
