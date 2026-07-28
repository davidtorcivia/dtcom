package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// writeJSON encodes v as JSON with the given status and content type.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits a generic {error: ...} body for the given status and logs
// the underlying error (if any) for debugging.
func writeError(w http.ResponseWriter, status int, err error) {
	if err != nil {
		slog.Error("handler error", "status", status, "err", err)
	}
	writeJSON(w, status, map[string]string{"error": http.StatusText(status)})
}

// decodeJSON reads and decodes the request body into v, capping the body at
// 10 MB to prevent memory exhaustion and rejecting unknown fields so typos in
// client payloads surface as 400s instead of silent drops. A body exceeding
// the cap surfaces as a 400 via MaxBytesReader's decoding error.
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	// 10 MB: plenty for any reasonable article body, bounded so a hostile
	// (even authed) client can't OOM the server by streaming a huge body.
	r.Body = http.MaxBytesReader(nil, r.Body, 10<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// checkBearer returns true iff the request carries "Authorization: Bearer
// <token>" matching the configured API token.
func checkBearer(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(v, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

// botUAKeywords are lowercased substrings that mark a User-Agent as automated.
var botUAKeywords = []string{
	"bot", "crawler", "spider", "slurp", "ia_archiver",
	"googlebot", "bingbot", "duckduckbot", "gptbot", "claudebot",
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

// hashIP returns a short hex digest of a remote address. Views are deduped per
// (path, day, ipHash); the hash is one-way and not reversible to an address,
// so the views table never stores personally identifying IP data.
func hashIP(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

// todayUTC returns the current UTC day as YYYY-MM-DD, the granularity at which
// view dedup is bucketed.
func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}
