package backup

// Downloading.
//
// What is stored is an archive without its images, plus a pool they are shared
// from. What leaves the machine has to be the whole thing: a download is the
// copy that survives this disk, and one that needs a pool left behind on the
// disk it is escaping would be no copy at all.
//
// So the archive is reassembled on the way out — its own entries streamed
// through, then the images it names appended from the pool. The result is
// byte-for-byte the kind of file the first version of this package wrote, and
// restoring from it needs nothing else.

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

// Download writes the complete archive to w.
//
// Streamed rather than built in a temp file: the whole point of the pool is not
// to keep second copies of the images around, and staging one on every download
// would be doing it again with extra steps.
func (s *Service) Download(name string, w io.Writer) error {
	if err := validName(name); err != nil {
		return err
	}
	man, err := s.manifestOf(name)
	if err != nil {
		return err
	}
	rc, _, err := s.dest.Get(name)
	if err != nil {
		return err
	}
	defer rc.Close()

	gzIn, err := gzip.NewReader(rc)
	if err != nil {
		return err
	}
	defer gzIn.Close()

	gzOut := gzip.NewWriter(w)
	tw := tar.NewWriter(gzOut)

	// Everything the stored archive holds, unchanged.
	tr := tar.NewReader(gzIn)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}

	// Then the images it names, out of the pool. An archive written by the old
	// format names none and already carries them.
	for _, img := range man.PooledImages() {
		if err := s.appendPooled(tw, img); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gzOut.Close()
}

func (s *Service) appendPooled(tw *tar.Writer, img string) error {
	rc, size, err := s.dest.GetObject(poolDir + "/" + img)
	if err != nil {
		// A pooled image that has gone missing must not silently produce an
		// archive that looks complete and is not.
		return err
	}
	defer rc.Close()
	if err := tw.WriteHeader(&tar.Header{
		Name:   "data/images/" + img,
		Mode:   0o644,
		Size:   size,
		Format: tar.FormatPAX,
	}); err != nil {
		return err
	}
	_, err = io.Copy(tw, rc)
	return err
}

// DownloadSize is what the assembled archive will roughly weigh, for the
// admin page. Roughly, because the images are already compressed and gzip's
// framing of them is not known until it has run.
func (s *Service) DownloadSize(in Info) int64 {
	man, err := s.manifestOf(in.Name)
	if err != nil {
		return in.Size
	}
	total := in.Size
	for _, img := range man.PooledImages() {
		if st, err := s.objectStat(img); err == nil {
			total += st
		}
	}
	return total
}

func (s *Service) objectStat(img string) (int64, error) {
	if l, ok := s.dest.(*Local); ok {
		p, err := l.objectPath(poolDir + "/" + img)
		if err != nil {
			return 0, err
		}
		st, err := os.Stat(p)
		if err != nil {
			return 0, err
		}
		return st.Size(), nil
	}
	rc, size, err := s.dest.GetObject(poolDir + "/" + img)
	if err != nil {
		return 0, err
	}
	rc.Close()
	return size, nil
}

// stagePooledImages writes the images an archive names into dir, which is what
// the restore path copies from when the archive itself carries none.
func (s *Service) stagePooledImages(man Manifest, dir string) error {
	for _, img := range man.PooledImages() {
		if err := s.stageObject(img, filepath.Join(dir, filepath.FromSlash(img))); err != nil {
			return err
		}
	}
	return nil
}
