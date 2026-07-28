package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// testPasswordHash is bcrypt of the literal string "password".
//
// It is generated at test startup rather than hardcoded because the
// well-known fixture "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
// used in the task spec does not validate against "password" under the
// current golang.org/x/crypto/bcrypt (CompareHashAndPassword rejects it).
// Per the task's explicit fallback instruction, we generate a fresh hash
// with bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
// so the test is self-contained and always validates.
var testPasswordHash = func() string {
	h, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}()

const testSecret = "JBSWY3DPEHPK3PXP" // a valid base32 TOTP secret

func newTestAuth() *Auth {
	return New(Options{
		SessionKey:   strings.Repeat("k", 32),
		PasswordHash: testPasswordHash,
		TOTPSecret:   testSecret,
		SecureCookie: true,
	})
}

func TestPasswordAndTOTP(t *testing.T) {
	a := newTestAuth()
	code, err := a.GenerateTOTP(time.Now())
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	if !a.CheckPasswordAndTOTP("password", code) {
		t.Error("expected valid password+TOTP to succeed")
	}
	if a.CheckPasswordAndTOTP("wrongpassword", code) {
		t.Error("expected wrong password to fail")
	}
}

// A TOTP code is valid for a whole 30s step. Accepting it more than once would
// let an intercepted code be replayed inside that window.
func TestTOTPCodeCannotBeReplayed(t *testing.T) {
	a := newTestAuth()
	code, err := a.GenerateTOTP(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !a.CheckPasswordAndTOTP("password", code) {
		t.Fatal("first login should succeed")
	}
	if a.CheckPasswordAndTOTP("password", code) {
		t.Error("replaying the same TOTP code should be rejected")
	}
}

// The used-code map must not grow without bound across long uptimes.
func TestUsedTOTPMapIsPruned(t *testing.T) {
	a := newTestAuth()
	base := time.Now()
	for i := range 100 {
		a.consumeTOTP(base.Add(time.Duration(i) * totpPeriod * time.Second))
	}
	a.mu.Lock()
	n := len(a.usedTOTP)
	a.mu.Unlock()
	if n > 8 {
		t.Errorf("usedTOTP retained %d entries, want a small bounded set", n)
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	a := newTestAuth()
	rec := httptest.NewRecorder()
	if err := a.SetSession(rec, "admin"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	sub, ok := a.SessionUser(req)
	if !ok || sub != "admin" {
		t.Errorf("SessionUser = %q ok=%v", sub, ok)
	}
}

func TestSessionRejectsTamperedCookie(t *testing.T) {
	a := newTestAuth()
	for _, value := range []string{
		"admin|9999999999.badsignature",
		"admin|9999999999",
		"",
		".",
		"|9999999999." + hmacHex([]byte(strings.Repeat("k", 32)), "|9999999999"),
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: value})
		if _, ok := a.SessionUser(req); ok {
			t.Errorf("cookie %q was accepted", value)
		}
	}
}

// A non-numeric expiry must fail closed rather than parse to zero by accident.
func TestSessionRejectsMalformedExpiry(t *testing.T) {
	a := newTestAuth()
	payload := "admin|not-a-number"
	value := payload + "." + hmacHex(a.sessionKey, payload)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: value})
	if _, ok := a.SessionUser(req); ok {
		t.Error("cookie with a malformed expiry was accepted")
	}
}

func TestSessionExpires(t *testing.T) {
	a := newTestAuth()
	payload := "admin|1"
	value := payload + "." + hmacHex(a.sessionKey, payload)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: value})
	if _, ok := a.SessionUser(req); ok {
		t.Error("expired cookie was accepted")
	}
}

// Over plain http a Secure cookie is dropped by the browser, so a dev run must
// be able to turn it off.
func TestSecureCookieFollowsOption(t *testing.T) {
	for _, secure := range []bool{true, false} {
		a := New(Options{SessionKey: strings.Repeat("k", 32), PasswordHash: testPasswordHash, TOTPSecret: testSecret, SecureCookie: secure})
		rec := httptest.NewRecorder()
		if err := a.SetSession(rec, "admin"); err != nil {
			t.Fatal(err)
		}
		if got := rec.Result().Cookies()[0].Secure; got != secure {
			t.Errorf("Secure = %v, want %v", got, secure)
		}
	}
}

// The clearing cookie must carry the same attributes as the one it replaces,
// or the browser may keep the original alongside it.
func TestClearSessionMatchesAttributes(t *testing.T) {
	a := newTestAuth()
	rec := httptest.NewRecorder()
	a.ClearSession(rec)
	c := rec.Result().Cookies()[0]
	if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("clear cookie = %+v, want empty value and negative MaxAge", c)
	}
	if !c.HttpOnly || !c.Secure || c.Path != "/" {
		t.Errorf("clear cookie attributes = %+v, want HttpOnly+Secure+Path=/", c)
	}
}
