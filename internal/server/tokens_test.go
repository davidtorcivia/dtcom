package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// apiGet issues an API request with the given bearer credential.
func (d *testDeps) apiGet(token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/articles", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// A distinct address per call keeps the failure limiter out of the way.
	req.RemoteAddr = "203.0.113." + token[:min(2, len(token))] + ":1000"
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec
}

// newTokenRe pulls the freshly-minted token out of the page that shows it once.
var newTokenRe = regexp.MustCompile(`<code class="token token-new">([0-9a-f]{64})</code>`)

// A token created in the admin UI has to actually authenticate against the
// API, and revoking it has to stop working immediately.
func TestAPITokenLifecycle(t *testing.T) {
	d := newTestDepsWithAdmin(t)

	rec := d.adminPost(t, "/admin/tokens", url.Values{"name": {"Claude Desktop"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create token = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	m := newTokenRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("created token not shown in the page:\n%s", rec.Body.String())
	}
	raw := m[1]

	// It authenticates.
	if got := d.apiGet(raw); got.Code != http.StatusOK {
		t.Fatalf("new token = %d, want 200", got.Code)
	}
	// The environment's bootstrap token still works alongside it.
	if got := d.apiGet(d.apiToken); got.Code != http.StatusOK {
		t.Errorf("bootstrap token = %d, want 200", got.Code)
	}
	// A near-miss does not.
	if got := d.apiGet(raw[:len(raw)-1] + "0"); got.Code == http.StatusOK {
		t.Error("a modified token authenticated")
	}

	// It appears in the list, and its raw value does not.
	list := d.adminGet(t, "/admin/integrations")
	if !strings.Contains(list.Body.String(), "Claude Desktop") {
		t.Error("token missing from the list")
	}
	if strings.Contains(list.Body.String(), raw) {
		t.Error("the list page redisplays the raw token; only a hash is stored, so it must not")
	}

	// Revoke it.
	tokens, err := d.deps.Store.ListAPITokens()
	if err != nil || len(tokens) != 1 {
		t.Fatalf("ListAPITokens = %v, %v", tokens, err)
	}
	rec = d.adminPost(t, "/admin/tokens/"+itoa(tokens[0].ID)+"/revoke", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	if got := d.apiGet(raw); got.Code == http.StatusOK {
		t.Error("a revoked token still authenticates")
	}
	// And the bootstrap credential is untouched — that is the way back in.
	if got := d.apiGet(d.apiToken); got.Code != http.StatusOK {
		t.Errorf("bootstrap token after revoke = %d, want 200", got.Code)
	}
}

func TestAPITokenRevokeIsIdempotentAndBounded(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	d.adminPost(t, "/admin/tokens", url.Values{"name": {"one"}})
	tokens, _ := d.deps.Store.ListAPITokens()
	id := itoa(tokens[0].ID)

	if rec := d.adminPost(t, "/admin/tokens/"+id+"/revoke", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("first revoke = %d", rec.Code)
	}
	// Revoking twice reports the situation rather than pretending it worked.
	if rec := d.adminPost(t, "/admin/tokens/"+id+"/revoke", nil); rec.Code == http.StatusSeeOther {
		t.Error("second revoke reported success")
	}
	// A nonsense id must not 500.
	for _, bad := range []string{"0", "-1", "abc", "99999"} {
		rec := d.adminPost(t, "/admin/tokens/"+bad+"/revoke", nil)
		if rec.Code >= 500 {
			t.Errorf("revoke %q = %d", bad, rec.Code)
		}
	}
}

// Multiple tokens coexist, and revoking one leaves the others working — the
// whole point of having more than one.
func TestMultipleTokensAreIndependent(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	var raws []string
	for _, name := range []string{"laptop", "phone", "ci"} {
		rec := d.adminPost(t, "/admin/tokens", url.Values{"name": {name}})
		m := newTokenRe.FindStringSubmatch(rec.Body.String())
		if m == nil {
			t.Fatalf("no token minted for %q", name)
		}
		raws = append(raws, m[1])
	}
	for i, raw := range raws {
		if got := d.apiGet(raw); got.Code != http.StatusOK {
			t.Fatalf("token %d = %d, want 200", i, got.Code)
		}
	}

	tokens, _ := d.deps.Store.ListAPITokens()
	var phoneID int64
	for _, tk := range tokens {
		if tk.Name == "phone" {
			phoneID = tk.ID
		}
	}
	d.adminPost(t, "/admin/tokens/"+itoa(phoneID)+"/revoke", nil)

	if got := d.apiGet(raws[1]); got.Code == http.StatusOK {
		t.Error("the revoked token still works")
	}
	for _, i := range []int{0, 2} {
		if got := d.apiGet(raws[i]); got.Code != http.StatusOK {
			t.Errorf("token %d = %d after revoking a different one", i, got.Code)
		}
	}
}

// Token management is a destructive, cookie-authenticated action.
func TestTokenRoutesRequireAuth(t *testing.T) {
	d := newTestDepsWithAdmin(t)
	for _, path := range []string{"/admin/tokens", "/admin/tokens/1/revoke"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("name=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		if rec.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s unauthenticated → %d (%s)", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	if n, _ := d.deps.Store.CountActiveAPITokens(); n != 0 {
		t.Errorf("%d tokens created without a session", n)
	}
}

// The scheme name is case-insensitive per RFC 7235.
func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	d := newTestDeps(t)
	for _, prefix := range []string{"Bearer ", "bearer ", "BEARER "} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/articles", nil)
		req.Header.Set("Authorization", prefix+d.apiToken)
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%q → %d, want 200", prefix, rec.Code)
		}
	}
}

// adminGet issues an authenticated GET.
func (d *testDeps) adminGet(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	sess := httptest.NewRecorder()
	if err := d.deps.Auth.SetSession(sess, "admin"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(sess.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
