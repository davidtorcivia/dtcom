package backup

// "Has anything actually changed?"
//
// A backup of a site identical to the one already saved protects nothing, and
// under a "keep the last N" policy it does harm: it pushes a genuinely
// different state off the end of the list. So each archive records what the
// site looked like, and the next one compares before writing.
//
// The fingerprint covers the posts and site.yml by content, the image masters
// by name — their names are their content hashes, so a name is a digest — and
// the authored rows of the database. It deliberately does not cover view
// counts: those tick up whenever anybody reads the site, and letting them
// decide would mean an archive every night of an unchanged site.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"davidtorcivia.com/dtcom/internal/build"
)

// fingerprint reads the current state and summarises it.
func (s *Service) fingerprint() (string, error) {
	contentFiles, err := listTree(s.cfg.ContentDir, func(string) bool { return true })
	if err != nil {
		return "", err
	}
	imageFiles, err := listTree(s.cfg.ImagesDir, build.IsMasterImage)
	if err != nil {
		return "", err
	}
	return s.fingerprintOf(contentFiles, imageFiles)
}

// fingerprintOf summarises a state whose file lists have already been read, so
// the create path does not walk the directories twice.
func (s *Service) fingerprintOf(contentFiles, imageFiles []string) (string, error) {
	h := sha256.New()
	for _, rel := range contentFiles {
		sum, err := hashFile(filepath.Join(s.cfg.ContentDir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "content\x00%s\x00%s\n", rel, sum)
	}
	for _, rel := range imageFiles {
		// The name is the SHA-256 of the file, so hashing the bytes again would
		// be doing the same arithmetic twice.
		fmt.Fprintf(h, "image\x00%s\n", rel)
	}
	dbPrint, err := s.db.ContentFingerprint()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(h, "db\x00%s\n", dbPrint)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// manifestOf reads one archive's manifest without unpacking the rest of it.
//
// The manifest is written first into the tar for exactly this reason: reading
// it costs one gzip block, not a whole archive, and both the change check and
// the pool sweep do it for every archive held.
func (s *Service) manifestOf(name string) (Manifest, error) {
	var man Manifest
	rc, _, err := s.dest.Get(name)
	if err != nil {
		return man, err
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return man, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return man, fmt.Errorf("no %s in %s", manifestName, name)
		}
		if err != nil {
			return man, err
		}
		if hdr.Name != manifestName {
			continue
		}
		blob, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return man, err
		}
		if err := json.Unmarshal(blob, &man); err != nil {
			return man, err
		}
		return man, nil
	}
}
