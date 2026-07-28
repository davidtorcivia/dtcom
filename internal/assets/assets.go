// Package assets fingerprints static files so their URLs change when their
// contents do.
//
// Without this, /static/style.css is cached by its path alone. Serving it with
// a long max-age means an edit takes until expiry to reach a returning
// visitor; serving it with a short one throws away the cache on every visit.
// Appending a content hash resolves the tension: the URL is stable while the
// file is, and changes the moment it isn't.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Fingerprinter maps a site-relative asset path ("/static/style.css") to the
// same path with a version query appended ("/static/style.css?v=1f3c9a2b").
//
// It is safe for concurrent use: the site rebuild refreshes the table while
// request handlers read it.
type Fingerprinter struct {
	// root is the directory /static/ is served from.
	root string
	mu   sync.RWMutex
	tags map[string]string // "/static/style.css" -> "1f3c9a2b"
}

func New(staticDir string) *Fingerprinter {
	f := &Fingerprinter{root: staticDir, tags: map[string]string{}}
	f.Refresh()
	return f
}

// Refresh recomputes every hash. Called on each rebuild, which is also when a
// bind-mounted static file may have changed underneath a running container.
func (f *Fingerprinter) Refresh() {
	if f == nil || f.root == "" {
		return
	}
	tags := map[string]string{}
	// Errors are swallowed throughout: an asset that can't be read simply goes
	// unversioned, which is a degraded cache story, not a broken page.
	_ = filepath.WalkDir(f.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // keep walking past an unreadable entry
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr
		}
		rel, err := filepath.Rel(f.root, path)
		if err != nil {
			return nil //nolint:nilerr
		}
		sum := sha256.Sum256(data)
		tags["/static/"+filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])[:8]
		return nil
	})
	f.mu.Lock()
	f.tags = tags
	f.mu.Unlock()
}

// URL returns path with its content hash appended, or path unchanged when the
// file is unknown (so a typo degrades to an ordinary un-versioned URL rather
// than an error).
func (f *Fingerprinter) URL(path string) string {
	if f == nil {
		return path
	}
	// Ignore any query the caller already supplied.
	base, _, _ := strings.Cut(path, "?")
	f.mu.RLock()
	tag := f.tags[base]
	f.mu.RUnlock()
	if tag == "" {
		return path
	}
	return base + "?v=" + tag
}
