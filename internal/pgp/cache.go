package pgp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://keys.openpgp.org"
	DefaultTTL     = 24 * time.Hour
	maxKeyBytes    = 512 << 10
	fetchTimeout   = 10 * time.Second
)

// Record is the cached lookup of one contact address. Armored is empty when
// the keyserver had no verified key for Email: that negative is cached too, so
// a rebuild after an RSS poll does not become a keyserver round-trip.
type Record struct {
	Email       string    `json:"email"`
	FetchedAt   time.Time `json:"fetched_at"`
	Armored     string    `json:"armored"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

// Found reports whether this record carries a public key to publish.
func (r *Record) Found() bool {
	return r != nil && strings.Contains(r.Armored, beginPublic)
}

// FingerprintDisplay is the grouped fingerprint for the admin status line.
func (r *Record) FingerprintDisplay() string {
	if r == nil {
		return ""
	}
	return FormatFingerprint(r.Fingerprint)
}

// Cache persists a VKS lookup next to the rest of data/.
type Cache struct {
	Path    string
	BaseURL string
	TTL     time.Duration
	Client  *http.Client
	Now     func() time.Time

	mu sync.Mutex
}

// New returns a cache writing to path. Path empty means Resolve is a no-op,
// which is what tests that do not want a network call leave unset.
func New(path string) *Cache {
	return &Cache{
		Path:    path,
		BaseURL: DefaultBaseURL,
		TTL:     DefaultTTL,
		Client:  &http.Client{Timeout: fetchTimeout},
		Now:     time.Now,
	}
}

func (c *Cache) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Cache) ttl() time.Duration {
	if c != nil && c.TTL > 0 {
		return c.TTL
	}
	return DefaultTTL
}

func (c *Cache) baseURL() string {
	if c != nil && c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Cache) client() *http.Client {
	if c != nil && c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: fetchTimeout}
}

// Load reads the cache file. Missing is (nil, nil).
func (c *Cache) Load() (*Record, error) {
	if c == nil || c.Path == "" {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadLocked()
}

func (c *Cache) loadLocked() (*Record, error) {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// Resolve returns the public key for email, fetching from the keyserver when
// the cache is missing, stale, for a different address, or force is set.
//
// A keyserver or I/O error returns the last good record for this email (if
// any) alongside the error, so a rebuild can keep publishing what it had.
func (c *Cache) Resolve(email string, force bool) (*Record, error) {
	if c == nil || c.Path == "" {
		return nil, nil
	}
	email = CanonicalEmail(email)

	c.mu.Lock()
	defer c.mu.Unlock()

	cached, err := c.loadLocked()
	if err != nil {
		cached = nil
	}

	if email == "" {
		if cached != nil {
			_ = os.Remove(c.Path)
		}
		return nil, nil
	}

	if !force && cached != nil && cached.Email == email && c.fresh(cached) {
		return cached, nil
	}

	fetched, fetchErr := c.fetch(email)
	if fetchErr != nil {
		if cached != nil && cached.Email == email {
			return cached, fetchErr
		}
		return nil, fetchErr
	}
	if err := c.saveLocked(fetched); err != nil {
		return fetched, err
	}
	return fetched, nil
}

func (c *Cache) fresh(rec *Record) bool {
	if rec == nil || rec.FetchedAt.IsZero() {
		return false
	}
	return c.now().Sub(rec.FetchedAt) < c.ttl()
}

func (c *Cache) saveLocked(rec *Record) error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(c.Path, data)
}

func (c *Cache) fetch(email string) (*Record, error) {
	u := c.baseURL() + "/vks/v1/by-email/" + encodeEmail(email)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/pgp-keys")
	req.Header.Set("User-Agent", "dtcom-pgp/1.0 (+https://github.com/dtorcivia)")

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxKeyBytes {
		return nil, fmt.Errorf("keyserver response larger than %d bytes", maxKeyBytes)
	}

	now := c.now().UTC()
	switch resp.StatusCode {
	case http.StatusOK:
		armored := strings.TrimSpace(string(body))
		if _, err := DecodeArmor(armored); err != nil {
			return nil, fmt.Errorf("keyserver body: %w", err)
		}
		return &Record{
			Email:       email,
			FetchedAt:   now,
			Armored:     armored + "\n",
			Fingerprint: FingerprintFromArmor(armored),
		}, nil
	case http.StatusNotFound, http.StatusGone:
		return &Record{Email: email, FetchedAt: now}, nil
	default:
		return nil, fmt.Errorf("keyserver returned HTTP %d", resp.StatusCode)
	}
}

// CanonicalEmail lowercases and trims an address. Lookup and WKD both treat
// the local-part as case-insensitive; storing one form keeps the cache key
// stable across mailto: capitalisation.
func CanonicalEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func encodeEmail(email string) string {
	// PathEscape leaves @ intact; VKS wants a single path segment.
	return strings.ReplaceAll(url.PathEscape(email), "@", "%40")
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-pgp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
