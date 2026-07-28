package server

import (
	"sync"
	"time"
)

// rateLimiter is a token-bucket limiter keyed by an arbitrary string (client IP
// for the public endpoints, a constant for the global bucket).
//
// It exists mainly for the admin login: bcrypt verification is deliberately
// expensive, and the second factor is a six-digit code, so an unthrottled
// /admin/login is both a CPU-exhaustion lever and a feasible TOTP brute force
// (10^6 codes, each valid for 30s). The same primitive throttles the
// unauthenticated search/track endpoints and bearer-token guessing.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	// refill is tokens added per second; burst is the bucket ceiling.
	refill    float64
	burst     float64
	lastSweep time.Time
	// now is injectable for tests; nil means time.Now.
	now func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(burst float64, per time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets:   make(map[string]*bucket),
		refill:    1 / per.Seconds(),
		burst:     burst,
		lastSweep: time.Now(),
	}
}

func (l *rateLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Allow consumes a token for key, reporting whether one was available.
func (l *rateLimiter) Allow(key string) bool {
	now := l.clock()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.refill
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets that have refilled to full, so the map tracks only
// clients that are currently near their limit. Without it, one request per
// unique IP would pin a map entry forever — an easy memory-growth lever on a
// public endpoint. Caller must hold l.mu.
func (l *rateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	full := time.Duration(l.burst/l.refill) * time.Second
	for k, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, k)
		}
	}
}

// limiters bundles every limiter the handlers consult.
type limiters struct {
	// login throttles per-IP admin login attempts: 5 immediately, then one
	// more every 30s. A human mistyping a TOTP code never notices; a brute
	// force gets ~2 guesses a minute.
	login *rateLimiter
	// loginGlobal bounds total bcrypt work regardless of source IP, so a
	// distributed attempt can't peg the CPU.
	loginGlobal *rateLimiter
	// bearer throttles failed API/MCP token guesses per IP.
	bearer *rateLimiter
	// search throttles the unauthenticated FTS endpoint (each call is a
	// SQLite query against the single write connection).
	search *rateLimiter
	// track throttles the unauthenticated view beacon, which writes a row.
	track *rateLimiter
}

func newLimiters() *limiters {
	return &limiters{
		login:       newRateLimiter(5, 30*time.Second),
		loginGlobal: newRateLimiter(20, time.Second),
		bearer:      newRateLimiter(10, 10*time.Second),
		search:      newRateLimiter(30, 200*time.Millisecond),
		track:       newRateLimiter(60, time.Second),
	}
}
