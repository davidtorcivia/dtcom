package server

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Client IP
// ---------------------------------------------------------------------------

// clientIP returns the address used for view dedup and rate limiting.
// RemoteAddr is "host:port" — the port must be stripped. Forwarded headers
// are honored only when DTCOM_TRUST_PROXY says a proxy is in front; otherwise
// they are client-controlled.
func (d *Deps) clientIP(r *http.Request) string {
	if d.Cfg != nil && d.Cfg.TrustProxyHeaders {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
			return v
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			// Rightmost entry: a proxy that appends (the nginx/Cloudflare model)
			// puts the address it actually saw last, so a client cannot forge it.
			// The leftmost is client-supplied and only trustworthy when the proxy
			// overwrites the header wholesale — CF-Connecting-IP above covers that
			// case.
			parts := strings.Split(v, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if last != "" {
				return last
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
// style="" attributes.
//
// The fonts.googleapis.com and fonts.gstatic.com exceptions are gone: the
// typography is served from static/fonts/ now, so 'self' covers it. Adding a
// third-party font host back would mean reopening both.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders applies the response headers every route should carry.
//
// The CSP widens only for a configured analytics origin, and never on /admin:
// the tracker runs only on public pages, so the authenticated surface keeps
// the guarantee that it executes only its own code.
func (d *Deps) securityHeaders(next http.Handler) http.Handler {
	hsts := d.Cfg != nil && strings.HasPrefix(d.Cfg.BaseURL, "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// The proxy in front (e.g. Cloudflare Tunnel) does not add this.
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("Content-Security-Policy", d.policyFor(r))
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// policyFor returns the Content-Security-Policy for one request.
func (d *Deps) policyFor(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/admin") {
		return contentSecurityPolicy
	}
	site := d.Site()
	if site == nil {
		return contentSecurityPolicy
	}
	return d.csp.policy(site.Analytics.Origin())
}

// cspCache memoises the policy string for the configured analytics origin.
//
// Built once per origin rather than per request: this header goes out on every
// response including each static asset, and the origin changes only when
// site.yml is saved.
type cspCache struct {
	mu     sync.RWMutex
	origin string
	cached string
}

func (c *cspCache) policy(origin string) string {
	if origin == "" {
		return contentSecurityPolicy
	}
	c.mu.RLock()
	if c.origin == origin {
		p := c.cached
		c.mu.RUnlock()
		return p
	}
	c.mu.RUnlock()

	built := strings.Replace(contentSecurityPolicy, "script-src 'self'", "script-src 'self' "+origin, 1)
	built = strings.Replace(built, "connect-src 'self'", "connect-src 'self' "+origin, 1)

	c.mu.Lock()
	c.origin, c.cached = origin, built
	c.mu.Unlock()
	return built
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

// FlushError pushes the compressor's buffer out before flushing the connection.
//
// http.ResponseController walks the Unwrap chain, so without this a streaming
// handler's flush would reach the real writer directly and skip whatever the
// gzip writer is still holding — the bytes would arrive out of order, or not at
// all until the response ended. The MCP endpoint streams (its SSE responses are
// not a compressible type, so it takes the second branch), and any future
// streaming HTML or JSON would take the first.
func (g *gzipResponseWriter) FlushError() error {
	if !g.headerDone {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		if err := g.gz.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(g.ResponseWriter).Flush()
}

func (g *gzipResponseWriter) Close() {
	if g.gz != nil {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// acceptsGzip reports whether the client's Accept-Encoding permits gzip.
// A `gzip;q=0` entry explicitly refuses it and must not be served compressed.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
			if strings.EqualFold(strings.TrimSpace(k), "q") {
				q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

// compression gzips text responses for clients that accept it. Every page this
// server emits is HTML, CSS, JS, XML, or JSON, and the largest of them (an
// article page plus its stylesheet) compresses to roughly a fifth of its size.
func compression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
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

// svgUploadPolicy is the Content-Security-Policy served with an uploaded SVG.
//
// SVG is the one uploadable image format that is really a document: navigate
// straight to one and the browser parses it, scripts and external references
// and all. The site-wide policy already forbids inline script, but an upload
// has no business fetching anything at all, so it gets 'none' across the board
// with inline styles allowed because hand-authored SVG routinely carries them.
//
// This costs nothing when the file is used the normal way. A response CSP
// applies to a document, and an SVG rendered through <img> is not one — that
// path also disables scripting outright in every browser.
const svgUploadPolicy = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// svgPolicy replaces the site-wide policy with the stricter one above when the
// file being served is an SVG.
//
// It has to sit inside securityHeaders, not outside: that middleware writes the
// general policy on the way in, so only a handler running later can overwrite
// the header.
func svgPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.ToLower(r.URL.Path), ".svg") {
			w.Header().Set("Content-Security-Policy", svgUploadPolicy)
		}
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
