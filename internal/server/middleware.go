package server

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Client IP
// ---------------------------------------------------------------------------

// clientIP returns the address to attribute a request to, for view dedup and
// rate limiting.
//
// r.RemoteAddr is "host:port", and the port differs on every connection — using
// it verbatim (as an earlier version did) made every page view look unique and
// made per-IP rate limiting useless. Behind a reverse proxy it is also always
// the proxy's own address, so the forwarded headers are consulted instead —
// but only when DTCOM_TRUST_PROXY says a proxy is really in front, since a
// directly-reachable server must never believe a client-supplied address.
func (d *Deps) clientIP(r *http.Request) string {
	if d.Cfg != nil && d.Cfg.TrustProxyHeaders {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
			return v
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			// Left-most entry is the original client; the rest are proxies.
			if first, _, _ := strings.Cut(v, ","); strings.TrimSpace(first) != "" {
				return strings.TrimSpace(first)
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------------------------------------------------------------------------
// Security headers
// ---------------------------------------------------------------------------

// contentSecurityPolicy locks the pages down to same-origin assets.
//
// script-src is strict: every script is a same-origin file under /static, and
// no page executes an inline <script>. That is the part that matters here,
// since posts are rendered from markdown with raw HTML enabled.
//
// 'unsafe-inline' is kept for style-src because the templates carry inline
// style="" attributes, and the Google Fonts hosts are allowed because
// style.css @imports its typography from them. Self-hosting the fonts would
// let both exceptions go away.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"img-src 'self' data: https:; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders applies the response headers every route should carry.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Same-origin enforcement for cookie-authenticated writes
// ---------------------------------------------------------------------------

// sameOrigin reports whether a state-changing request came from this site.
//
// The session cookie is SameSite=Lax, which already blocks cross-site form
// POSTs in current browsers; this is the belt to that suspenders, and it also
// covers clients that ignore SameSite. Requests carrying neither header (curl,
// same-origin fetches in older browsers) are allowed — Lax remains the primary
// control, and rejecting header-less requests would break legitimate tooling.
func sameOrigin(r *http.Request, baseURL string) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "same-site", "none":
		return true
	case "cross-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return true // no Origin sent: fall back to the SameSite cookie attribute
	}
	if strings.EqualFold(origin, baseURL) {
		return true
	}
	// Also accept an Origin whose host matches the request's own Host, so a
	// deployment reached over a hostname other than BaseURL (localhost during
	// development, a preview domain) still works.
	if u := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://"); u == r.Host {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

// statusRecorder captures the response status (and byte count) for the log
// line, and remembers whether anything was written so the panic handler knows
// if it can still emit an error page.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
		s.ResponseWriter.WriteHeader(code)
	}
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.written += int64(n)
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logging emits one structured line per request, including the response status
// and how long it took — without that, the log can't distinguish a served page
// from a 404 or a 500.
func (d *Deps) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		slog.Info("request",
			"method", r.Method,
			// Path only, never RawQuery: search terms are visitor input and
			// don't belong in an operational log.
			"path", r.URL.Path,
			"status", status,
			"bytes", rec.written,
			"dur_ms", time.Since(start).Milliseconds(),
			"ip", d.clientIP(r),
		)
	})
}

// recoverer turns panics in handlers into 500s so one bad request can't kill
// the whole process.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec) // the server's own signal for a deliberate abort
				}
				slog.Error("panic", "rec", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
				// If the handler already started writing, headers are flushed
				// and appending an error page would corrupt the body; the
				// truncated response is the best available outcome.
				if sr, ok := w.(*statusRecorder); ok && (sr.status != 0 || sr.written > 0) {
					return
				}
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Compression
// ---------------------------------------------------------------------------

var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz         *gzip.Writer
	status     int
	compress   bool
	headerDone bool
}

// compressible reports whether a content type is worth gzipping. Images and
// other pre-compressed payloads only get bigger.
func compressible(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch ct {
	case "text/html", "text/css", "text/plain", "text/markdown", "text/xml",
		"application/javascript", "text/javascript", "application/json",
		"application/xml", "application/rss+xml", "image/svg+xml":
		return true
	}
	return false
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.headerDone {
		return
	}
	g.headerDone = true
	g.status = code
	h := g.ResponseWriter.Header()
	// Decide once, after the handler has set Content-Type (http.ServeFile
	// sniffs it), whether this response body is worth compressing.
	if compressible(h.Get("Content-Type")) && h.Get("Content-Encoding") == "" {
		g.compress = true
		h.Del("Content-Length") // length changes once compressed
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		g.gz = gzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.headerDone {
		g.WriteHeader(http.StatusOK)
	}
	if g.compress {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipResponseWriter) Close() {
	if g.gz != nil {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// compression gzips text responses for clients that accept it. Every page this
// server emits is HTML, CSS, JS, XML, or JSON, and the largest of them (an
// article page plus its stylesheet) compresses to roughly a fifth of its size.
func compression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		// Range requests are served uncompressed: the byte offsets a client
		// asks for refer to the identity encoding.
		if r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}

// ---------------------------------------------------------------------------
// Caching
// ---------------------------------------------------------------------------

// cacheControl sets a Cache-Control header before delegating. Static assets get
// a long TTL; generated HTML gets a short one so an edit shows up promptly.
func cacheControl(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

const (
	// Static asset URLs carry a content hash (see internal/assets), so a given
	// URL's bytes never change and it can be cached indefinitely. An edit
	// produces a new URL, which the freshly-rendered HTML points at.
	staticCacheControl = "public, max-age=31536000, immutable"
	// Uploaded images are content-addressed for the same reason.
	imageCacheControl = "public, max-age=31536000, immutable"
	// Pages are cached but revalidated on every use. The whole premise here is
	// that an edit publishes within about half a second; a max-age would put a
	// stale window in front of that, while no-cache still gets a cheap 304
	// from the Last-Modified http.ServeFile sends.
	pageCacheControl = "public, no-cache"
)
