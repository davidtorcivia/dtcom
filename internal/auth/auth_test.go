package auth

import (
	"net/http"
	"net/http/httptest"
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

func TestPasswordAndTOTP(t *testing.T) {
	a := New("sessionkey", testPasswordHash, testSecret)
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

func TestSessionCookieRoundTrip(t *testing.T) {
	a := New("sessionkey", testPasswordHash, testSecret)
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
	a := New("sessionkey", testPasswordHash, testSecret)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "dtcom_session", Value: "admin|9999999999.badsignature"})
	if _, ok := a.SessionUser(req); ok {
		t.Error("expected tampered cookie to be rejected")
	}
}
