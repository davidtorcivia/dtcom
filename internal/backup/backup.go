// Package backup archives the two things on this machine that cannot be
// reconstructed, and puts them back.
//
// Neither content/ nor data/ is in git, so a post, an uploaded picture, and a
// view count exist in exactly one place; this package makes a second one.
// An archive is a gzipped tar holding:
//
//	manifest.json     what this is, when it was taken, and what was in it
//	content/…         posts and site.yml, byte for byte
//	data/dtcom.db     a VACUUM INTO snapshot, consistent as of the moment
//	data/images/…     the uploaded masters
//
// Renditions and public/ are deliberately excluded — both are derived and
// regenerated (renditions at startup, public/ on every rebuild); archiving
// them would multiply storage roughly fourfold to protect reproducible files.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"davidtorcivia.com/dtcom/internal/build"
)

// manifestVersion is written into every archive. A restore refuses a version it
// does not understand rather than half-applying it.
// Version 2 moved the images out of the archive and into a shared pool it names
// instead, and added the fingerprint. Version 1 archives still restore: they
// carry their images, and the restore path uses what it finds.
const manifestVersion = 2

const manifestName = "manifest.json"

// Kind records why an archive was taken, which is the difference between "the
// nightly one" and "the one from just before I overwrote everything".
type Kind string

const (
	KindScheduled  Kind = "scheduled"
	KindManual     Kind = "manual"
	KindPreRestore Kind = "pre-restore"
)

func (k Kind) valid() bool {
	switch k {
	case KindScheduled, KindManual, KindPreRestore:
		return true
	}
	return false
}

// Manifest is the archive's own description of itself.
type Manifest struct {
	Version int       `json:"version"`
	Created time.Time `json:"created"`
	Kind    Kind      `json:"kind"`
	Posts   int       `json:"posts"`
	DBBytes int64     `json:"db_bytes"`

	// Images is how many masters this archive covers. Version 1 wrote this key
	// and nothing else about them, so it keeps its meaning — reusing it for the
	// list below would make every archive written by the previous release
	// unreadable, which is the one thing a backup format must never do.
	Images int `json:"images"`

	// ImageFiles names those masters. Version 2 keeps them in the shared pool
	// rather than inside the tar, so the archive has to say which it needs. A
	// version 1 archive carries them and leaves this empty.
	ImageFiles []string `json:"image_files,omitempty"`

	// Fingerprint is what the site looked like when this was taken — the posts,
	// site.yml, the image names, and the authored rows of the database. Two
	// archives with the same fingerprint hold the same site, which is how the
	// next backup knows there is nothing to do.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// PooledImages is the list of masters this archive expects to find in the pool,
// which is empty for a version 1 archive because it carries its own.
func (m Manifest) PooledImages() []string { return m.ImageFiles }

// Info is one archive as the admin page lists it.
type Info struct {
	Name    string
	Kind    Kind
	Created time.Time
	Size    int64
}

// Age is how long ago this archive was taken.
func (i Info) Age() time.Duration { return time.Since(i.Created) }

// DB is the part of the store this package needs: a consistent copy out, a
// file swapped in, and a summary of the parts of it a person changed.
type DB interface {
	Snapshot(path string) error
	ReplaceWith(path string) error
	ContentFingerprint() (string, error)
}

// Config is where everything lives.
type Config struct {
	ContentDir string
	ImagesDir  string
	DBPath     string

	// Interval between scheduled archives. Zero disables them; manual ones
	// still work.
	Interval time.Duration

	// Retention. See prune.go.
	Keep Policy
}

// Service takes, lists, prunes and restores archives.
type Service struct {
	cfg  Config
	dest Destination
	db   DB

	// One archive operation at a time. Two concurrent restores would fight
	// over the same directories, and two concurrent creates would each
	// snapshot the same database for no reason.
	mu sync.Mutex
}

func New(cfg Config, dest Destination, db DB) *Service {
	if cfg.Keep == (Policy{}) {
		cfg.Keep = DefaultPolicy
	}
	return &Service{cfg: cfg, dest: dest, db: db}
}

// Interval reports the scheduled cadence, zero meaning "only when asked".
func (s *Service) Interval() time.Duration { return s.cfg.Interval }

// Where names the destination archives are written to, for display.
func (s *Service) Where() string { return s.dest.Describe() }

// Policy reports the retention rule in force, for display.
func (s *Service) Policy() Policy { return s.cfg.Keep }

// List returns every archive, newest first.
func (s *Service) List() ([]Info, error) { return s.dest.List() }

// Open returns the bytes of one archive, for download.
func (s *Service) Open(name string) (io.ReadCloser, int64, error) {
	if err := validName(name); err != nil {
		return nil, 0, err
	}
	return s.dest.Get(name)
}

// Stat finds one archive by name.
//
// Worth having as its own call because the download path streams: once a byte
// has gone out, the status code is spent, so "does this exist and is it one of
// ours" has to be answered before any header is written.
func (s *Service) Stat(name string) (Info, error) {
	if err := validName(name); err != nil {
		return Info{}, err
	}
	list, err := s.dest.List()
	if err != nil {
		return Info{}, err
	}
	for _, in := range list {
		if in.Name == name {
			return in, nil
		}
	}
	return Info{}, fmt.Errorf("no such backup: %s", name)
}

// Delete removes one archive.
func (s *Service) Delete(name string) error {
	if err := validName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dest.Delete(name)
}

// ErrUnchanged is returned by Create when the site is identical to the newest
// archive, which is not a failure — it is the answer.
var ErrUnchanged = errors.New("nothing has changed since the last backup")

// Create writes a new archive and returns it.
//
// If the site is byte-for-byte what the newest archive already holds, nothing
// is written and ErrUnchanged comes back with the archive that already covers
// it. A copy of a state that is already saved protects nothing and costs a
// place in the retention count.
//
// A pre-restore archive is always written, whatever the fingerprint says: it is
// taken to make an irreversible thing reversible, and skipping it because
// "nothing changed" would be trusting the very comparison the restore is about
// to invalidate.
//
// Pruning is the caller's next call, not this one's: taking a copy and throwing
// copies away are different decisions, and a failure in the second should not
// look like a failure in the first.
func (s *Service) Create(kind Kind) (Info, error) {
	if !kind.valid() {
		return Info{}, fmt.Errorf("unknown backup kind %q", kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if kind != KindPreRestore {
		same, existing, err := s.unchanged()
		if err != nil {
			// Not being able to tell is a reason to take the backup, not to
			// skip it.
			slog.Warn("backup change check", "err", err)
		} else if same {
			return existing, ErrUnchanged
		}
	}
	return s.create(kind, time.Now().UTC())
}

// unchanged reports whether the site matches the newest archive's fingerprint.
func (s *Service) unchanged() (bool, Info, error) {
	list, err := s.dest.List()
	if err != nil || len(list) == 0 {
		return false, Info{}, err
	}
	newest := list[0] // List is newest first
	man, err := s.manifestOf(newest.Name)
	if err != nil {
		return false, Info{}, err
	}
	if man.Fingerprint == "" {
		// An archive from before fingerprints existed. Nothing to compare
		// against, so take a new one — after which there will be.
		return false, Info{}, nil
	}
	now, err := s.fingerprint()
	if err != nil {
		return false, Info{}, err
	}
	return now == man.Fingerprint, newest, nil
}

func (s *Service) create(kind Kind, now time.Time) (Info, error) {
	work, err := os.MkdirTemp("", "dtcom-backup-")
	if err != nil {
		return Info{}, err
	}
	defer os.RemoveAll(work)

	// The database is snapshotted to a file first: VACUUM INTO writes where
	// SQLite chooses to write, and it cannot write into a tar stream.
	dbCopy := filepath.Join(work, "dtcom.db")
	if err := s.db.Snapshot(dbCopy); err != nil {
		return Info{}, fmt.Errorf("snapshot database: %w", err)
	}
	dbStat, err := os.Stat(dbCopy)
	if err != nil {
		return Info{}, err
	}

	tmp := filepath.Join(work, "archive.tar.gz")
	f, err := os.Create(tmp)
	if err != nil {
		return Info{}, err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	man := Manifest{
		Version: manifestVersion,
		Created: now,
		Kind:    kind,
		DBBytes: dbStat.Size(),
	}
	// The manifest goes in first but is only complete once everything has been
	// counted, so it is written last into a reserved place — which a tar cannot
	// do. Instead the counting happens up front, from the same walk that will
	// write the files.
	contentFiles, err := listTree(s.cfg.ContentDir, func(string) bool { return true })
	if err != nil {
		f.Close()
		return Info{}, fmt.Errorf("read content: %w", err)
	}
	imageFiles, err := listTree(s.cfg.ImagesDir, build.IsMasterImage)
	if err != nil {
		f.Close()
		return Info{}, fmt.Errorf("read images: %w", err)
	}
	for _, rel := range contentFiles {
		if strings.HasSuffix(rel, ".md") {
			man.Posts++
		}
	}
	man.Images = len(imageFiles)
	man.ImageFiles = imageFiles
	if man.Fingerprint, err = s.fingerprintOf(contentFiles, imageFiles); err != nil {
		f.Close()
		return Info{}, err
	}

	fail := func(err error) (Info, error) {
		tw.Close()
		gz.Close()
		f.Close()
		return Info{}, err
	}

	blob, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := writeTarBytes(tw, manifestName, blob, now); err != nil {
		return fail(err)
	}
	if err := writeTarTree(tw, s.cfg.ContentDir, "content", contentFiles); err != nil {
		return fail(fmt.Errorf("archive content: %w", err))
	}
	if err := writeTarFile(tw, dbCopy, "data/dtcom.db"); err != nil {
		return fail(fmt.Errorf("archive database: %w", err))
	}
	// The images are not written into the tar. They go to the pool, which the
	// archive names — see pool.go. The download path puts them back on the way
	// out, so what a person receives is still one complete file.

	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		return fail(err)
	}
	// Flushed to the platter before the archive is named: a backup that exists
	// only in the page cache is not a backup.
	if err := f.Sync(); err != nil {
		f.Close()
		return Info{}, err
	}
	if err := f.Close(); err != nil {
		return Info{}, err
	}

	// The images go to the pool before the archive is named, so an archive is
	// never listed while something it refers to is still missing.
	for _, rel := range imageFiles {
		if err := s.putPool(rel, filepath.Join(s.cfg.ImagesDir, filepath.FromSlash(rel))); err != nil {
			return Info{}, fmt.Errorf("pool %s: %w", rel, err)
		}
	}

	// Measured before it is handed over: Put may move the file, and a stat
	// afterwards would be asking about a name that is no longer there.
	st, err := os.Stat(tmp)
	if err != nil {
		return Info{}, err
	}
	// A name is the moment it was taken, to the second, which is readable and
	// sorts correctly — and collides when two archives are taken inside the
	// same second. That happens: a restore writes its safety copy and the
	// operator's manual backup can land in the same tick. Rather than lose one
	// of them silently to an overwrite, the later one moves forward a second
	// until its name is free.
	name, at := s.freeName(now, kind)
	if err := s.dest.Put(name, tmp); err != nil {
		return Info{}, err
	}
	return Info{Name: name, Kind: kind, Created: at, Size: st.Size()}, nil
}

func (s *Service) freeName(now time.Time, kind Kind) (string, time.Time) {
	taken := map[string]bool{}
	if list, err := s.dest.List(); err == nil {
		for _, in := range list {
			taken[in.Name] = true
		}
	}
	at := now
	for i := 0; i < 120; i++ {
		name := archiveName(at, kind)
		if !taken[name] {
			return name, at
		}
		at = at.Add(time.Second)
	}
	return archiveName(at, kind), at
}

// archiveName encodes the moment and the reason, in that order, so a plain
// sort by name is a sort by time.
func archiveName(t time.Time, kind Kind) string {
	return fmt.Sprintf("dtcom-%s-%s.tar.gz", t.UTC().Format("20060102T150405Z"), kind)
}

// parseArchiveName is the inverse. Anything that does not parse is not one of
// ours and is left alone — the destination may hold other files.
func parseArchiveName(name string) (time.Time, Kind, bool) {
	if !strings.HasPrefix(name, "dtcom-") || !strings.HasSuffix(name, ".tar.gz") {
		return time.Time{}, "", false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(name, "dtcom-"), ".tar.gz")
	i := strings.Index(middle, "-")
	if i < 0 {
		return time.Time{}, "", false
	}
	t, err := time.Parse("20060102T150405Z", middle[:i])
	if err != nil {
		return time.Time{}, "", false
	}
	kind := Kind(middle[i+1:])
	if !kind.valid() {
		return time.Time{}, "", false
	}
	return t.UTC(), kind, true
}

// validName rejects anything that is not a plain archive file name. Every
// caller of this package's name-taking methods is a request parameter away
// from a path, and "../../etc/passwd" is a name.
func validName(name string) error {
	if name == "" || name != filepath.Base(name) || name != path.Base(name) ||
		strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid backup name %q", name)
	}
	if _, _, ok := parseArchiveName(name); !ok {
		return fmt.Errorf("not a backup archive: %q", name)
	}
	return nil
}

// listTree returns paths under root, relative to it and slash-separated, for
// every file the filter accepts. Directories, symlinks and anything else that
// is not a regular file are skipped: an archive of this site holds documents
// and pictures, and nothing that could point somewhere else on restore.
func listTree(root string, keep func(name string) bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return nil // nothing stored yet
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil // editor leftovers, .tmp-* from an interrupted write
		}
		if !keep(name) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mod time.Time) error {
	if err := tw.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: mod,
		Format:  tar.FormatPAX,
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarFile(tw *tar.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    st.Size(),
		ModTime: st.ModTime(),
		Format:  tar.FormatPAX,
	}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func writeTarTree(tw *tar.Writer, root, prefix string, rels []string) error {
	for _, rel := range rels {
		if err := writeTarFile(tw, filepath.Join(root, filepath.FromSlash(rel)), prefix+"/"+rel); err != nil {
			return err
		}
	}
	return nil
}
