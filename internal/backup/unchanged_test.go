package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestNoBackupWhenNothingChanged is the rule: a copy of a state that is already
// saved protects nothing, and under a keep-the-last-N policy it actively harms,
// because it pushes a genuinely different state off the end.
func TestNoBackupWhenNothingChanged(t *testing.T) {
	f := newFixture(t)

	first, err := f.svc.Create(KindManual)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}

	// Nothing has happened since.
	got, err := f.svc.Create(KindManual)
	if !errors.Is(err, ErrUnchanged) {
		t.Fatalf("second backup returned %v, want ErrUnchanged", err)
	}
	if got.Name != first.Name {
		t.Errorf("ErrUnchanged pointed at %s, want the archive that covers it (%s)", got.Name, first.Name)
	}
	list, err := f.svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("%d archives after an unchanged backup, want 1", len(list))
	}
}

// TestBackupWhenSomethingChanged walks each thing the fingerprint covers, one
// at a time, and checks that each one on its own is enough to justify a copy.
func TestBackupWhenSomethingChanged(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f *fixture)
	}{
		{"a post is edited", func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.content, "posts", "one.md"), "# One, rewritten")
		}},
		{"a post is added", func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.content, "posts", "new.md"), "# New")
		}},
		{"a post is deleted", func(t *testing.T, f *fixture) {
			if err := os.Remove(filepath.Join(f.content, "posts", "one.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"site.yml is edited", func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.content, "site.yml"), "title: A renamed site")
		}},
		{"an image is uploaded", func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.images, "eeee.png"), "a new picture")
		}},
		{"an image is deleted", func(t *testing.T, f *fixture) {
			if err := os.Remove(filepath.Join(f.images, "aaaa.png")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a link is added to the database", func(t *testing.T, f *fixture) {
			f.db.content = "database with one more link"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.svc.Create(KindManual); err != nil {
				t.Fatal(err)
			}
			tc.change(t, f)
			if _, err := f.svc.Create(KindManual); err != nil {
				t.Errorf("no backup taken after %s: %v", tc.name, err)
			}
			list, _ := f.svc.List()
			if len(list) != 2 {
				t.Errorf("%d archives after %s, want 2", len(list), tc.name)
			}
		})
	}
}

// TestRenditionsDoNotJustifyABackup: generated files are not in an archive, so
// regenerating them is not a reason to write one.
func TestRenditionsDoNotJustifyABackup(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(KindManual); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.images, "aaaa.w768.png"), "a rendition made since")
	write(t, filepath.Join(f.images, "aaaa.w1080.webp"), "and another")

	if _, err := f.svc.Create(KindManual); !errors.Is(err, ErrUnchanged) {
		t.Errorf("a backup was taken for regenerated renditions: %v", err)
	}
}

// TestPreRestoreAlwaysWrites: the copy taken before a restore is what makes the
// restore reversible, so it is never skipped — least of all on the reasoning
// that nothing has changed, which the restore is about to falsify.
func TestPreRestoreAlwaysWrites(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(KindManual); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(KindPreRestore); err != nil {
		t.Fatalf("pre-restore backup was skipped: %v", err)
	}
	list, _ := f.svc.List()
	if len(list) != 2 {
		t.Errorf("%d archives, want 2", len(list))
	}
}

// TestDownloadIsSelfContained is the promise the pool must not break: what
// leaves the machine has to hold the images, because it is the copy that
// outlives the disk the pool is on.
func TestDownloadIsSelfContained(t *testing.T) {
	f := newFixture(t)
	info, err := f.svc.Create(KindManual)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := f.svc.Download(info.Name, &buf); err != nil {
		t.Fatalf("Download: %v", err)
	}

	// Write it out and read it back the way a person restoring elsewhere would.
	elsewhere := t.TempDir()
	downloaded := filepath.Join(elsewhere, info.Name)
	if err := os.WriteFile(downloaded, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	members := archiveMembers(t, downloaded)

	for _, want := range []string{
		manifestName,
		"content/site.yml",
		"content/posts/one.md",
		"data/dtcom.db",
		"data/images/aaaa.png",
		"data/images/bbbb.jpg",
	} {
		if _, ok := members[want]; !ok {
			t.Errorf("downloaded archive is missing %s (has %v)", want, keys(members))
		}
	}
	if got := members["data/images/aaaa.png"]; got != "master png" {
		t.Errorf("pooled image came back as %q", got)
	}
	// Still no generated files.
	for name := range members {
		if strings.Contains(name, ".w480.") || strings.HasSuffix(name, "aaaa.webp") {
			t.Errorf("downloaded archive holds a generated file: %s", name)
		}
	}
}

// TestRestoreFromDownloadedArchive: a file that came back from a browser has
// its images inside and must restore without the pool it was assembled from.
func TestRestoreFromDownloadedArchive(t *testing.T) {
	f := newFixture(t)
	info, err := f.svc.Create(KindManual)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.svc.Download(info.Name, &buf); err != nil {
		t.Fatal(err)
	}

	// A second site, with nothing in common and no pool.
	g := newFixture(t)
	if err := os.Remove(filepath.Join(g.images, "aaaa.png")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(g.content, "posts", "one.md"), "# Somebody else's post")
	name := archiveName(time.Now().UTC(), KindManual)
	write(t, filepath.Join(g.backups, name), "")
	if err := os.WriteFile(filepath.Join(g.backups, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := g.svc.Restore(name); err != nil {
		t.Fatalf("restore from a downloaded archive: %v", err)
	}
	if got := read(t, filepath.Join(g.content, "posts", "one.md")); got != "# One" {
		t.Errorf("post not restored: %q", got)
	}
	if got := read(t, filepath.Join(g.images, "aaaa.png")); got != "master png" {
		t.Errorf("image not restored from the downloaded archive: %q", got)
	}
}

// TestRestoreWithoutPooledImageFailsEarly: if the pool has lost an image, that
// has to surface before anything live is touched.
func TestRestoreWithoutPooledImageFailsEarly(t *testing.T) {
	f := newFixture(t)
	info, err := f.svc.Create(KindManual)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.backups, "images", "aaaa.png")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.content, "posts", "one.md"), "# Edited since")

	if _, err := f.svc.Restore(info.Name); err == nil {
		t.Fatal("restore succeeded with an image missing from the pool")
	}
	if got := read(t, filepath.Join(f.content, "posts", "one.md")); got != "# Edited since" {
		t.Errorf("content was modified by a restore that could not complete: %q", got)
	}
}

// TestRestoreVersion1Archive pins backward compatibility with the format the
// first release wrote: images inside the tar, no fingerprint, and an "images"
// key holding a count rather than a list.
//
// This exists because that compatibility was broken once, by reusing the
// "images" key for the list of names — every archive already on disk became
// unreadable, which is the one failure a backup format is not allowed to have.
func TestRestoreVersion1Archive(t *testing.T) {
	f := newFixture(t)
	name := archiveName(time.Now().UTC().Add(-time.Hour), KindScheduled)
	writeV1Archive(t, filepath.Join(f.backups, name), map[string]string{
		"content/site.yml":     "title: The old site",
		"content/posts/one.md": "# One, as it was",
		"data/dtcom.db":        "database from the old format",
		"data/images/aaaa.png": "master png",
		"data/images/old.png":  "a picture only the old archive has",
	})

	// The site has moved on since.
	write(t, filepath.Join(f.content, "posts", "one.md"), "# One, rewritten")

	res, err := f.svc.Restore(name)
	if err != nil {
		t.Fatalf("restore a version 1 archive: %v", err)
	}
	if res.Manifest.Version != 1 {
		t.Errorf("manifest version = %d, want 1", res.Manifest.Version)
	}
	if got := read(t, filepath.Join(f.content, "posts", "one.md")); got != "# One, as it was" {
		t.Errorf("post not restored: %q", got)
	}
	// Its images came from inside the archive, not from a pool that never had them.
	if got := read(t, filepath.Join(f.images, "old.png")); got != "a picture only the old archive has" {
		t.Errorf("image not restored from inside the archive: %q", got)
	}
}

// writeV1Archive writes an archive in the original format: a manifest with an
// integer "images" count, and the image files carried inside.
func writeV1Archive(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var images int
	for name := range files {
		if strings.HasPrefix(name, "data/images/") {
			images++
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	blob := fmt.Sprintf(`{"version":1,"created":%q,"kind":"scheduled","posts":1,"images":%d,"db_bytes":12}`,
		time.Now().UTC().Format(time.RFC3339Nano), images)
	if err := writeTarBytes(tw, manifestName, []byte(blob), time.Now()); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeTarBytes(tw, name, []byte(files[name]), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
