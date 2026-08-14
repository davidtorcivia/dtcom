// Package auth implements password + TOTP verification and HMAC-signed session cookies.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// Options configures an Auth. SecureCookie should track whether the site is
// served over TLS: a Secure cookie is never sent back over plain http, so
// forcing it on in local dev makes admin login fail with no visible error.
type Options struct {
	SessionKey   string
	PasswordHash string
	TOTPSecret   string
	SecureCookie bool
}

type Auth struct {
	sessionKey   []byte
	passwordHash []byte
	totpSecret   string
	secureCookie bool

	// mu guards usedTOTP, which records the TOTP time-steps already spent on a
	// successful login. A TOTP code stays valid for its whole 30-second step
	// (plus skew), so without this a code captured from a shoulder-surf, a
	// proxy log, or a phishing page could be replayed within that window.
	mu       sync.Mutex
	usedTOTP map[int64]time.Time
}

func New(o Options) *Auth {
	return &Auth{
		sessionKey:   []byte(o.SessionKey),
		passwordHash: []byte(o.PasswordHash),
		totpSecret:   strings.TrimSpace(o.TOTPSecret),
		secureCookie: o.SecureCookie,
		usedTOTP:     make(map[int64]time.Time),
	}
}

// totpPeriod is the TOTP step length in seconds (the RFC 6238 default, and
// what totp.Validate assumes).
const totpPeriod = 30

// CheckPasswordAndTOTP verifies both factors. The TOTP branch runs (and burns
// its compare) even on a bad password so response time doesn't leak which
// factor failed. A code is accepted at most once.
func (a *Auth) CheckPasswordAndTOTP(password, code string) bool {
	passwordOK := bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
	code = strings.TrimSpace(code)
	codeOK := totp.Validate(code, a.totpSecret)
	if !passwordOK || !codeOK {
		return false
	}
	return a.consumeTOTP(time.Now(), code)
}

// consumeTOTP marks the step a code belongs to as spent. Skew of one step
// means a code validates across three wall-clock steps, so marking the
// time-of-use step (as an earlier version did) let a captured code replay
// across a step boundary. Returns false if the matching step was already used.
func (a *Auth) consumeTOTP(now time.Time, code string) bool {
	step := now.Unix() / totpPeriod
	a.mu.Lock()
	defer a.mu.Unlock()
	// Drop entries older than a few steps so the map can't grow unbounded.
	for s, seen := range a.usedTOTP {
		if now.Sub(seen) > 5*totpPeriod*time.Second {
			delete(a.usedTOTP, s)
		}
	}
	for _, s := range []int64{step - 1, step, step + 1} {
		want, err := totp.GenerateCode(a.totpSecret, time.Unix(s*totpPeriod, 0))
		if err != nil || want != code {
			continue
		}
		if _, used := a.usedTOTP[s]; used {
			return false
		}
		a.usedTOTP[s] = now
		return true
	}
	return false
}

// GenerateTOTP produces a current TOTP code (for testing / setup display).
func (a *Auth) GenerateTOTP(t time.Time) (string, error) {
	return totp.GenerateCode(a.totpSecret, t)
}

const (
	cookieName = "dtcom_session"
	sessionTTL = 7 * 24 * time.Hour
)

// SetSession writes a signed session cookie for the given subject.
func (a *Auth) SetSession(w http.ResponseWriter, sub string) error {
	if strings.ContainsAny(sub, "|.") {
		return fmt.Errorf("invalid session subject %q", sub)
	}
	expires := time.Now().Add(sessionTTL)
	payload := fmt.Sprintf("%s|%d", sub, expires.Unix())
	value := payload + "." + hmacHex(a.sessionKey, payload)
	http.SetCookie(w, a.cookie(value, expires, 0))
	return nil
}

// SessionUser returns the subject if the cookie is present, unexpired, and
// its signature verifies.
func (a *Auth) SessionUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	idx := strings.LastIndexByte(c.Value, '.')
	if idx < 0 {
		return "", false
	}
	payload, sig := c.Value[:idx], c.Value[idx+1:]
	if !hmac.Equal([]byte(sig), []byte(hmacHex(a.sessionKey, payload))) {
		return "", false
	}
	sub, expStr, ok := strings.Cut(payload, "|")
	if !ok || sub == "" {
		return "", false
	}
	// A malformed expiry must fail closed. The previous fmt.Sscanf spelling
	// ignored its error, so a cookie like "admin|nope" left expires at 0 —
	// which happened to expire, but only by accident.
	expires, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false
	}
	if time.Now().Unix() > expires {
		return "", false
	}
	return sub, true
}

// ClearSession expires the cookie. The attributes must match those used when
// setting it (path, secure, samesite) or browsers may keep the original.
func (a *Auth) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, a.cookie("", time.Unix(0, 0), -1))
}

func (a *Auth) cookie(value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: http.SameSiteLaxMode,
	}
}

func hmacHex(key []byte, msg string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}
