package backup

// Putting an archive back.
//
// The order matters, and it is: take a copy of what is there now, unpack the
// archive somewhere harmless and check it over, and only then touch anything
// live. Every failure before the last step leaves the site exactly as it was,
// and the one after it is covered by the copy taken in the first.
//
// Directories are updated in place rather than swapped. The obvious
// implementation — rename the old content/ aside, move the new one in — cannot
// work here: content/ and data/ are bind mounts inside the container, and a
// mount point cannot be renamed. So restore writes the archive's files over
// what is there and removes what the archive does not have.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
)

// maxArchiveBytes caps what a single archive may expand to. An archive is
// something this server wrote, but it is also something a person can upload
// over, and a tar that decompresses to the whole disk is a classic.
const maxArchiveBytes = 4 << 30 // 4 GB

// Result describes what a restore did.
type Result struct {
	From     Info
	Safety   Info // the archive taken of the previous state
	Manifest Manifest
	Files    int
	Removed  int
}

// Restore replaces the live content, database and image masters with the
// contents of one archive.
//
// It does not rebuild the site or regenerate renditions; the caller does that,
// because it owns the engine. Until it does, the site keeps serving the pages
// it had, which are the old ones — correct behaviour for the seconds involved,
// and better than serving a half-restored site.
func (s *Service) Restore(name string) (*Result, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.dest.List()
	if err != nil {
		return nil, err
	}
	var from Info
	for _, in := range all {
		if in.Name == name {
			from = in
			break
		}
	}
	if from.Name == "" {
		return nil, fmt.Errorf("no such backup: %s", name)
	}

	src, ok := localPath(s.dest, name)
	if !ok {
		return nil, errNoLocalCopy
	}

	// Unpack first, into a directory of our own. A corrupt or truncated
	// archive is found here, with nothing touched.
	work, err := os.MkdirTemp("", "dtcom-restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	man, err := extract(src, work)
	if err != nil {
		return nil, fmt.Errorf("unpack %s: %w", name, err)
	}
	if man.Version > manifestVersion {
		return nil, fmt.Errorf("%s was written by a newer version (%d) than this one understands (%d)",
			name, man.Version, manifestVersion)
	}
	stagedDB := filepath.Join(work, "data", "dtcom.db")
	if _, err := os.Stat(stagedDB); err != nil {
		return nil, fmt.Errorf("%s has no database in it", name)
	}

	// Everything checks out. Take the copy of the current state before
	// changing any of it — this is the one that makes the rest reversible.
	safety, err := s.create(KindPreRestore, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("safety backup before restore: %w", err)
	}

	res := &Result{From: from, Safety: safety, Manifest: man}

	written, removed, err := syncTree(filepath.Join(work, "content"), s.cfg.ContentDir, func(string) bool { return true })
	if err != nil {
		return res, fmt.Errorf("restore content: %w", err)
	}
	res.Files += written
	res.Removed += removed

	// Only masters are compared: the renditions in the images directory are not
	// in the archive and must not be taken for files the archive dropped. The
	// ones whose master is gone are cleaned up afterwards.
	written, removed, err = syncTree(filepath.Join(work, "data", "images"), s.cfg.ImagesDir, build.IsMasterImage)
	if err != nil {
		return res, fmt.Errorf("restore images: %w", err)
	}
	res.Files += written
	res.Removed += removed
	res.Removed += removeOrphanedRenditions(s.cfg.ImagesDir)

	// The database last. It is the step that briefly interrupts queries, so it
	// happens once everything else has already succeeded.
	if err := s.db.ReplaceWith(stagedDB); err != nil {
		return res, fmt.Errorf("restore database: %w", err)
	}
	res.Files++
	return res, nil
}

// extract unpacks an archive into dir, returning its manifest.
func extract(archivePath, dir string) (Manifest, error) {
	var man Manifest
	f, err := os.Open(archivePath)
	if err != nil {
		return man, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return man, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	var sawManifest bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return man, err
		}
		// Only regular files. A symlink or a device node in an archive has no
		// business in this site's content, and unpacking one is how an archive
		// writes outside the directory it was told to.
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return man, fmt.Errorf("unexpected entry %q in archive", hdr.Name)
		}
		rel, err := safeRelPath(hdr.Name)
		if err != nil {
			return man, err
		}
		total += hdr.Size
		if total > maxArchiveBytes {
			return man, fmt.Errorf("archive expands past the %d byte limit", int64(maxArchiveBytes))
		}

		if rel == manifestName {
			blob, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return man, err
			}
			if err := json.Unmarshal(blob, &man); err != nil {
				return man, fmt.Errorf("manifest: %w", err)
			}
			sawManifest = true
			continue
		}

		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return man, err
		}
		out, err := os.Create(dst)
		if err != nil {
			return man, err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, maxArchiveBytes)); err != nil {
			out.Close()
			return man, err
		}
		if err := out.Close(); err != nil {
			return man, err
		}
	}
	if !sawManifest {
		return man, fmt.Errorf("no %s — not an archive this server wrote", manifestName)
	}
	return man, nil
}

// safeRelPath rejects any archive member that would land outside the directory
// it is being unpacked into: absolute paths, and anything walking up through
// "..". This is the tar equivalent of the zip-slip bug, and it is worth being
// explicit about even for archives we wrote ourselves — the download button
// means one can come back edited.
func safeRelPath(name string) (string, error) {
	clean := path.Clean("/" + strings.TrimPrefix(filepath.ToSlash(name), "./"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("empty path in archive")
	}
	if strings.Contains(clean, "..") || path.IsAbs(clean) {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	// Only the trees this package writes are accepted, so an archive cannot
	// introduce a directory nobody expects.
	switch {
	case clean == manifestName,
		strings.HasPrefix(clean, "content/"),
		strings.HasPrefix(clean, "data/images/"),
		clean == "data/dtcom.db":
		return clean, nil
	}
	return "", fmt.Errorf("unexpected path in archive: %q", name)
}

// syncTree makes dst hold exactly what src holds, for the files the filter
// selects, and reports how many were written and removed.
func syncTree(src, dst string, manage func(name string) bool) (written, removed int, err error) {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil // the archive had none of these
		}
		return 0, 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, 0, err
	}

	wanted, err := listTree(src, manage)
	if err != nil {
		return 0, 0, err
	}
	have := map[string]bool{}
	for _, rel := range wanted {
		have[rel] = true
		to := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return written, removed, err
		}
		if err := copyInto(filepath.Join(src, filepath.FromSlash(rel)), to); err != nil {
			return written, removed, err
		}
		written++
	}

	existing, err := listTree(dst, manage)
	if err != nil {
		return written, removed, err
	}
	for _, rel := range existing {
		if have[rel] {
			continue
		}
		if err := os.Remove(filepath.Join(dst, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return written, removed, err
		}
		removed++
	}
	return written, removed, nil
}

// removeOrphanedRenditions deletes generated files whose master is no longer
// there — the leftovers of an image the restored state does not have. They
// would otherwise sit in the directory forever, since nothing else prunes it.
func removeOrphanedRenditions(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	masters := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && build.IsMasterImage(e.Name()) {
			masters[stem(e.Name())] = true
		}
	}
	var removed int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || build.IsMasterImage(name) || strings.HasPrefix(name, ".") {
			continue
		}
		if masters[stem(name)] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed
}

// stem is the content hash a file belongs to: "9f3c.w768.webp" → "9f3c".
func stem(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if i := strings.Index(base, "."); i >= 0 {
		return base[:i]
	}
	return base
}
