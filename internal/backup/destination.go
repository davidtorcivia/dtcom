package backup

// Where archives are kept.
//
// One implementation today: a directory on disk. The interface exists because
// there will be a second — an S3-compatible bucket, R2 in particular — and the
// operations a remote one needs are exactly these four. Everything above this
// file works in terms of names and readers, so adding that destination is a
// matter of writing it and choosing it at startup, not of reworking the service.
//
// A note for whoever writes the remote one: Put takes a path rather than a
// reader so that the local implementation can rename a finished temp file into
// place instead of copying it through memory. A remote implementation opens
// that path and streams it, which is what an uploader wants anyway — it needs
// the length, and a retry needs to seek back to the start.
//
// Mirroring to two destinations at once (write here, upload there; delete both
// when pruning) is the shape the eventual R2 work takes: a Destination that
// holds two others and fans out, so the service above stays unaware. Fanning
// out means deciding what a partial failure means — an archive that made it to
// disk but not to the bucket is still a backup, and should be reported rather
// than rolled back.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Destination interface {
	// Put stores the file at srcPath under name. The source is a finished
	// archive; the implementation may move it.
	Put(name, srcPath string) error

	// Get opens an archive for reading, with its size.
	Get(name string) (io.ReadCloser, int64, error)

	// Delete removes an archive. Removing one that is not there is not an error:
	// the point of the call is that it should be gone.
	Delete(name string) error

	// List returns the archives it holds, newest first.
	List() ([]Info, error)

	// --- the image pool ---
	//
	// Objects are the shared, immutable images archives refer to rather than
	// carry. Kept under a prefix so a bucket implementation can use the same
	// namespace as the archives; see pool.go for what they are for.

	// PutObject stores srcPath under key, and does nothing if the key already
	// exists — the content is addressed by its own hash, so a key that is
	// present is by definition already correct.
	PutObject(key, srcPath string) error

	// GetObject opens one, with its size.
	GetObject(key string) (io.ReadCloser, int64, error)

	// DeleteObject removes one.
	DeleteObject(key string) error

	// ListObjects returns everything under a key prefix.
	ListObjects(prefix string) ([]Object, error)

	// Describe names this destination for the admin page.
	Describe() string
}

// Object is one pooled file.
type Object struct {
	Name     string
	Size     int64
	Modified time.Time
}

// Local is a directory on the same machine.
//
// Worth being clear about what that does and does not protect against: it
// survives a mistake — a post deleted, a site.yml mangled, an image replaced —
// and it does not survive the disk. Until there is a second destination, the
// download button is the copy that leaves the building.
type Local struct{ Dir string }

func NewLocal(dir string) *Local { return &Local{Dir: dir} }

func (l *Local) Describe() string { return l.Dir }

func (l *Local) path(name string) string { return filepath.Join(l.Dir, name) }

func (l *Local) Put(name, srcPath string) error {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	dst := l.path(name)
	if err := os.Rename(srcPath, dst); err == nil {
		return os.Chmod(dst, 0o644)
	}
	// Rename fails across filesystems, and the archive is built in the system
	// temp directory, which is often on another one.
	return copyInto(srcPath, dst)
}

func copyInto(srcPath, dst string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	// Written under a temp name and renamed, so a listing never shows a
	// half-copied archive as though it were a whole one.
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

func (l *Local) Get(name string) (io.ReadCloser, int64, error) {
	if err := validName(name); err != nil {
		return nil, 0, err
	}
	f, err := os.Open(l.path(name))
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (l *Local) Delete(name string) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := os.Remove(l.path(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Local) List() ([]Info, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no backups taken yet is not a failure
		}
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		created, kind, ok := parseArchiveName(e.Name())
		if !ok {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{Name: e.Name(), Kind: kind, Created: created, Size: st.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// --- the pool, on disk ---

func (l *Local) objectPath(key string) (string, error) {
	// Keys are built here, never taken from a request, but they do end up as
	// paths, so they are checked anyway.
	clean := filepath.Clean("/" + filepath.ToSlash(key))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || strings.Contains(clean, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return filepath.Join(l.Dir, filepath.FromSlash(clean)), nil
}

func (l *Local) PutObject(key, srcPath string) error {
	dst, err := l.objectPath(key)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		// Content-addressed: a key that exists holds the right bytes already.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// A hard link costs no space and no time, and is safe because the images it
	// points at are never rewritten in place — a new picture gets a new name.
	// It also means the pooled copy survives the original being deleted, which
	// is the entire point of having it.
	if err := os.Link(srcPath, dst); err == nil {
		return nil
	}
	// Different filesystem, or a filesystem without links.
	return copyInto(srcPath, dst)
}

func (l *Local) GetObject(key string) (io.ReadCloser, int64, error) {
	p, err := l.objectPath(key)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (l *Local) DeleteObject(key string) error {
	p, err := l.objectPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Local) ListObjects(prefix string) ([]Object, error) {
	dir, err := l.objectPath(strings.TrimSuffix(prefix, "/"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Object
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Object{
			Name:     strings.TrimSuffix(prefix, "/") + "/" + e.Name(),
			Size:     st.Size(),
			Modified: st.ModTime(),
		})
	}
	return out, nil
}

// localPath is the escape hatch for the restore path, which needs a file on
// disk to extract from. A remote destination will need to stage a download
// somewhere first; this is where that will hook in.
func localPath(d Destination, name string) (string, bool) {
	l, ok := d.(*Local)
	if !ok {
		return "", false
	}
	return l.path(name), true
}

var errNoLocalCopy = fmt.Errorf("this destination cannot be restored from directly")
