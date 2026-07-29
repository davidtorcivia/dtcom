package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDB stands in for the store. Snapshot writes a recognisable file and
// ReplaceWith records what it was handed, which is all this package needs to
// know about a database.
type fakeDB struct {
	content  string
	replaced string
	failNext bool
}

func (f *fakeDB) Snapshot(path string) error {
	if f.failNext {
		return os.ErrPermission
	}
	return os.WriteFile(path, []byte(f.content), 0o644)
}

func (f *fakeDB) ContentFingerprint() (string, error) {
	// Stands in for the authored rows of the database. Changing f.content is
	// how a test says "somebody edited a link".
	return f.content, nil
}

func (f *fakeDB) ReplaceWith(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f.replaced = string(b)
	f.content = string(b)
	return nil
}

type fixture struct {
	dir     string
	content string
	images  string
	backups string
	db      *fakeDB
	svc     *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		dir:     root,
		content: filepath.Join(root, "content"),
		images:  filepath.Join(root, "data", "images"),
		backups: filepath.Join(root, "data", "backups"),
		db:      &fakeDB{content: "database v1"},
	}
	mkdirAll(t, filepath.Join(f.content, "posts"), f.images, f.backups)
	write(t, filepath.Join(f.content, "site.yml"), "title: A site")
	write(t, filepath.Join(f.content, "posts", "one.md"), "# One")
	write(t, filepath.Join(f.content, "posts", "two.md"), "# Two")
	// A master and two of its renditions, so the exclusion rule is exercised.
	write(t, filepath.Join(f.images, "aaaa.png"), "master png")
	write(t, filepath.Join(f.images, "aaaa.w480.png"), "rendition")
	write(t, filepath.Join(f.images, "aaaa.w480.webp"), "rendition webp")
	write(t, filepath.Join(f.images, "aaaa.webp"), "master webp, also generated")
	write(t, filepath.Join(f.images, "bbbb.jpg"), "master jpg")

	f.svc = New(Config{
		ContentDir: f.content,
		ImagesDir:  f.images,
		DBPath:     filepath.Join(root, "data", "dtcom.db"),
	}, NewLocal(f.backups), f.db)
	return f
}

func mkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func archiveMembers(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		var sb strings.Builder
		if _, err := sb.Write(readAll(t, tr)); err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = sb.String()
	}
	return out
}

func readAll(t *testing.T, tr *tar.Reader) []byte {
	t.Helper()
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := tr.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

// TestCreateHoldsWhatCannotBeRemade is the contents rule: posts, site.yml, the
// database and the image masters go in; anything generated stays out.
func TestCreateHoldsWhatCannotBeRemade(t *testing.T) {
	f := newFixture(t)
	info, err := f.svc.Create(KindManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.Kind != KindManual || info.Size == 0 {
		t.Fatalf("unexpected info %+v", info)
	}

	members := archiveMembers(t, filepath.Join(f.backups, info.Name))
	for _, want := range []string{
		manifestName,
		"content/site.yml",
		"content/posts/one.md",
		"content/posts/two.md",
		"data/dtcom.db",
	} {
		if _, ok := members[want]; !ok {
			t.Errorf("archive is missing %s (has %v)", want, keys(members))
		}
	}
	// The images are named, not carried: they live in the pool, shared by
	// every archive that refers to them.
	for name := range members {
		if strings.HasPrefix(name, "data/images/") {
			t.Errorf("archive carries an image instead of naming it: %s", name)
		}
	}
	man := readManifest(t, filepath.Join(f.backups, info.Name))
	if got := man.PooledImages(); len(got) != 2 {
		t.Errorf("manifest names %v, want the two masters", got)
	}
	for _, want := range []string{"aaaa.png", "bbbb.jpg"} {
		if !contains(man.PooledImages(), want) {
			t.Errorf("manifest does not name %s: %v", want, man.PooledImages())
		}
	}
	for _, unwanted := range []string{"aaaa.w480.png", "aaaa.w480.webp", "aaaa.webp"} {
		if contains(man.PooledImages(), unwanted) {
			t.Errorf("manifest names a generated file: %s", unwanted)
		}
	}
	// And the pool holds exactly those.
	for _, want := range []string{"aaaa.png", "bbbb.jpg"} {
		if _, err := os.Stat(filepath.Join(f.backups, "images", want)); err != nil {
			t.Errorf("pool is missing %s: %v", want, err)
		}
	}
	if got := members["data/dtcom.db"]; got != "database v1" {
		t.Errorf("database contents = %q", got)
	}
	if got := members["content/posts/one.md"]; got != "# One" {
		t.Errorf("post contents = %q", got)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestRestorePutsItBack covers the whole round trip, including the three things
// a restore has to get right: files that changed are overwritten, files added
// since are removed, and files deleted since come back.
func TestRestorePutsItBack(t *testing.T) {
	f := newFixture(t)
	info, err := f.svc.Create(KindManual)
	if err != nil {
		t.Fatal(err)
	}

	// The site moves on: one post edited, one deleted, one added, an image
	// added, the database changed.
	write(t, filepath.Join(f.content, "posts", "one.md"), "# One, rewritten")
	if err := os.Remove(filepath.Join(f.content, "posts", "two.md")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.content, "posts", "three.md"), "# Three")
	write(t, filepath.Join(f.images, "cccc.png"), "a later picture")
	write(t, filepath.Join(f.images, "cccc.w480.png"), "its rendition")
	f.db.content = "database v2"

	res, err := f.svc.Restore(info.Name)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := read(t, filepath.Join(f.content, "posts", "one.md")); got != "# One" {
		t.Errorf("edited post not reverted: %q", got)
	}
	if got := read(t, filepath.Join(f.content, "posts", "two.md")); got != "# Two" {
		t.Errorf("deleted post not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.content, "posts", "three.md")); !os.IsNotExist(err) {
		t.Error("post added after the backup survived the restore")
	}
	if _, err := os.Stat(filepath.Join(f.images, "cccc.png")); !os.IsNotExist(err) {
		t.Error("image added after the backup survived the restore")
	}
	// Its renditions have no master any more and should have gone with it.
	if _, err := os.Stat(filepath.Join(f.images, "cccc.w480.png")); !os.IsNotExist(err) {
		t.Error("an orphaned rendition was left behind")
	}
	// Renditions of an image that is still there are left alone: they are
	// still correct, and regenerating them is not free.
	if _, err := os.Stat(filepath.Join(f.images, "aaaa.w480.png")); err != nil {
		t.Error("a live image's rendition was removed")
	}
	if f.db.replaced != "database v1" {
		t.Errorf("database not restored, got %q", f.db.replaced)
	}

	// And the safety copy holds the state from immediately before.
	if res.Safety.Kind != KindPreRestore {
		t.Fatalf("no pre-restore backup taken: %+v", res.Safety)
	}
	safety := archiveMembers(t, filepath.Join(f.backups, res.Safety.Name))
	if got := safety["content/posts/one.md"]; got != "# One, rewritten" {
		t.Errorf("safety copy does not hold the pre-restore state: %q", got)
	}
	if _, ok := safety["content/posts/three.md"]; !ok {
		t.Error("safety copy is missing a post that existed before the restore")
	}
	if got := safety["data/dtcom.db"]; got != "database v2" {
		t.Errorf("safety copy database = %q", got)
	}
}

// TestRestoreRefusesRubbish: nothing is touched by an archive that is not one.
func TestRestoreRefusesRubbish(t *testing.T) {
	f := newFixture(t)
	name := archiveName(time.Now().UTC(), KindManual)
	write(t, filepath.Join(f.backups, name), "not a gzip at all")

	before := read(t, filepath.Join(f.content, "posts", "one.md"))
	if _, err := f.svc.Restore(name); err == nil {
		t.Fatal("restored from a file that is not an archive")
	}
	if after := read(t, filepath.Join(f.content, "posts", "one.md")); after != before {
		t.Error("content changed despite the failed restore")
	}
	if f.db.replaced != "" {
		t.Error("database replaced despite the failed restore")
	}
}

// TestNamesOutsideTheDirectoryAreRefused: the archive name reaches this package
// from a URL path segment.
func TestNamesOutsideTheDirectoryAreRefused(t *testing.T) {
	f := newFixture(t)
	for _, bad := range []string{
		"../../etc/passwd",
		"/etc/passwd",
		"dtcom-20260101T000000Z-manual.tar.gz/../../x",
		"",
		"notes.txt",
		"dtcom-nonsense-manual.tar.gz",
	} {
		if _, _, err := f.svc.Open(bad); err == nil {
			t.Errorf("Open(%q) was allowed", bad)
		}
		if err := f.svc.Delete(bad); err == nil {
			t.Errorf("Delete(%q) was allowed", bad)
		}
		if _, err := f.svc.Restore(bad); err == nil {
			t.Errorf("Restore(%q) was allowed", bad)
		}
	}
}

// TestExtractRefusesEscapingPaths is the tar-slip guard, tested directly
// because a hand-edited archive is a thing a download button makes possible.
func TestExtractRefusesEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := writeTarBytes(tw, manifestName, []byte(`{"version":1}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escaped.md", "content/../../escaped.md", "/etc/passwd"} {
		_ = writeTarBytes(tw, name, []byte("owned"), time.Now())
	}
	tw.Close()
	gz.Close()
	f.Close()

	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := extract(archive, out); err == nil {
		t.Fatal("extract accepted an archive with escaping paths")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.md")); !os.IsNotExist(err) {
		t.Error("a file was written outside the extraction directory")
	}
}

// TestCreateLeavesNothingBehindOnFailure: a failed snapshot must not leave a
// half-written archive that a later restore could pick up.
func TestCreateLeavesNothingBehindOnFailure(t *testing.T) {
	f := newFixture(t)
	f.db.failNext = true
	if _, err := f.svc.Create(KindManual); err == nil {
		t.Fatal("Create succeeded with a failing snapshot")
	}
	list, err := f.svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("a failed backup was listed: %+v", list)
	}
}

func TestArchiveNameRoundTrip(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, kind := range []Kind{KindManual, KindScheduled, KindPreRestore} {
		name := archiveName(when, kind)
		got, gotKind, ok := parseArchiveName(name)
		if !ok {
			t.Fatalf("%s did not parse", name)
		}
		if !got.Equal(when) || gotKind != kind {
			t.Errorf("%s → %v %s, want %v %s", name, got, gotKind, when, kind)
		}
	}
	for _, bad := range []string{"backup.tar.gz", "dtcom-x-manual.tar.gz", "dtcom-20260304T050607Z-other.tar.gz"} {
		if _, _, ok := parseArchiveName(bad); ok {
			t.Errorf("%s parsed as an archive", bad)
		}
	}
}

func contains(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}

func readManifest(t *testing.T, path string) Manifest {
	t.Helper()
	var man Manifest
	blob := archiveMembers(t, path)[manifestName]
	if blob == "" {
		t.Fatalf("%s has no manifest", path)
	}
	if err := json.Unmarshal([]byte(blob), &man); err != nil {
		t.Fatal(err)
	}
	return man
}
