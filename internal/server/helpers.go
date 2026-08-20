package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"davidtorcivia.com/dtcom/internal/store"
)

// writeJSON encodes v as JSON with the given status and content type.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already out, so this can only be logged. Most
		// often it means the client hung up mid-response.
		slog.Debug("write json response", "err", err)
	}
}

// writeError emits a generic {error: ...} body for the given status and logs
// the underlying error (if any) for debugging. The client never sees the
// internal message — error strings here carry filesystem paths and SQL text.
func writeError(w http.ResponseWriter, status int, err error) {
	if err != nil {
		slog.Error("handler error", "status", status, "err", err)
	}
	writeJSON(w, status, map[string]string{"error": http.StatusText(status)})
}

// maxJSONBody caps request bodies on the authed API: plenty for any reasonable
// article, bounded so a hostile (even authed) client can't OOM the server by
// streaming a huge body.
const maxJSONBody = 10 << 20 // 10 MB

// decodeJSON reads and decodes the request body into v, capping the body and
// rejecting unknown fields so typos in client payloads surface as 400s instead
// of silent drops. A body exceeding the cap surfaces as a 400 via
// MaxBytesReader's decoding error.
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// wantsJSON reports whether the caller asked for a JSON answer rather than a
// page. The admin UI is served as HTML to a browser navigating to it and as
// JSON to the editor's own fetch calls, and this is what tells the two apart.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// bearerToken extracts the credential from an Authorization header.
func bearerToken(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	// The scheme is case-insensitive per RFC 7235.
	if len(v) < 7 || !strings.EqualFold(v[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(v[7:])
}

// checkBearer returns true iff the request carries "Authorization: Bearer
// <token>" matching the configured API token.
func checkBearer(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	provided := bearerToken(r)
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

// authorizeToken accepts either the bootstrap credential from the environment
// or any active token minted through the admin UI.
//
// The environment token is kept as a permanent fallback on purpose: it is how
// the deployment is configured, and it means locking yourself out by revoking
// the wrong managed token is always recoverable.
func (d *Deps) authorizeToken(r *http.Request) bool {
	if checkBearer(r, d.Cfg.APIToken) {
		return true
	}
	provided := bearerToken(r)
	if provided == "" || d.Store == nil {
		return false
	}
	t, err := d.Store.LookupAPIToken(provided)
	if err != nil {
		if !errors.Is(err, store.ErrTokenNotFound) {
			slog.Error("api token lookup", "err", err)
		}
		return false
	}
	d.touchToken(t)
	return true
}

// touchToken records a token's use, at most once a minute per token — the
// last-used column is for the admin page, and a write on every API request
// would be pure contention on a single-writer database.
func (d *Deps) touchToken(t *store.APIToken) {
	now := time.Now()
	d.tokenTouchMu.Lock()
	last, seen := d.tokenTouched[t.ID]
	if seen && now.Sub(last) < time.Minute {
		d.tokenTouchMu.Unlock()
		return
	}
	if d.tokenTouched == nil {
		d.tokenTouched = map[int64]time.Time{}
	}
	d.tokenTouched[t.ID] = now
	d.tokenTouchMu.Unlock()

	if err := d.Store.TouchAPIToken(t.ID); err != nil {
		slog.Debug("touch api token", "id", t.ID, "err", err)
	}
}

// validSlug reports whether s is safe to use as a path segment when resolving a
// generated file. Slugs normally come from slugify ([a-z0-9-]), but a
// hand-named markdown file can produce anything, so the check is permissive
// about the character set and strict about the parts that enable traversal.
func validSlug(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	if strings.Contains(s, "..") || strings.HasPrefix(s, ".") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// botUAKeywords are lowercased substrings that mark a User-Agent as automated.
var botUAKeywords = []string{
	"bot", "crawler", "spider", "slurp", "ia_archiver",
	"googlebot", "bingbot", "duckduckbot", "gptbot", "claudebot",
	"headlesschrome", "python-requests", "curl/", "wget", "libwww-perl",
}

// isBot reports whether a User-Agent looks automated. An empty UA is treated
// as a bot (suspicious) so beacons from curl-style clients don't inflate views.
func isBot(ua string) bool {
	ua = strings.ToLower(ua)
	if ua == "" {
		return true
	}
	for _, k := range botUAKeywords {
		if strings.Contains(ua, k) {
			return true
		}
	}
	return false
}

// hashIP returns a short keyed digest of a client address. Views are deduped
// per (path, day, ipHash).
//
// A real HMAC, not secret-prefix SHA-256: the IPv4 space is small enough to
// enumerate, so an unkeyed (or forgeable) hash of an address is reversible by
// anyone who obtains the database. Keyed, the stored value is meaningless
// without the secret.
func (d *Deps) hashIP(addr string) string {
	var key string
	if d.Cfg != nil {
		key = d.Cfg.SessionKey
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("view|" + addr))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// today returns the current day as YYYY-MM-DD, the granularity at which view
// dedup is bucketed. Local time, so a day boundary falls at the site owner's
// midnight rather than at 8pm — set TZ to say which zone that is (see
// docker-compose.yml); Go falls back to UTC when it is unset.
func today() string {
	return time.Now().Format("2006-01-02")
}
