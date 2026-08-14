package backup

// The image pool.
//
// Masters are immutable and content-addressed, so putting a copy inside every
// archive stored the same bytes over and over (on this site, 96% of each
// archive was images). Instead the images live once beside the archives and
// each archive names the ones it needs.
//
// Two invariants hold:
//   - A downloaded archive is self-contained — the download path streams the
//     named images back into the tar (see assemble in download.go).
//   - Old-format archives that carry their images still restore.
//
// On one filesystem the pool costs nothing: entries are hard links to the
// live image, safe because masters are never rewritten. Across filesystems it
// falls back to a copy.

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
