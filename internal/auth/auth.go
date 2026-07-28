// Package auth implements password + TOTP verification and HMAC-signed session cookies.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

type Auth struct {
	sessionKey   []byte
	passwordHash []byte
	totpSecret   string
}

func New(sessionKey, passwordHash, totpSecret string) *Auth {
	return &Auth{
		sessionKey:   []byte(sessionKey),
		passwordHash: []byte(passwordHash),
		totpSecret:   totpSecret,
	}
}

// CheckPasswordAndTOTP verifies both factors.
func (a *Auth) CheckPasswordAndTOTP(password, code string) bool {
	if bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) != nil {
		return false
	}
	return totp.Validate(code, a.totpSecret)
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
	expires := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d", sub, expires)
	sig := hmacHex(a.sessionKey, payload)
	value := payload + "." + sig
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: value, Path: "/",
		Expires: time.Now().Add(sessionTTL), HttpOnly: true,
		Secure: true, SameSite: http.SameSiteLaxMode,
	})
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
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return "", false
	}
	var expires int64
	fmt.Sscanf(parts[1], "%d", &expires)
	if time.Now().Unix() > expires {
		return "", false
	}
	return parts[0], true
}

func (a *Auth) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
}

func hmacHex(key []byte, msg string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}
