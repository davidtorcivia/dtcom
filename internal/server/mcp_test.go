package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPRequiresToken(t *testing.T) {
	d := newTestDeps(t)
	body := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req := httptest.NewRequest(http.MethodPost, "/mcp", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// TestMCPGetSiteTool exercises a real tool call through the Streamable HTTP
// transport end-to-end. It performs the initialize handshake, then calls
// get_site and asserts the response carries the site title. This is the
// strongest signal that all 16 tools are wired correctly without enumerating
// each one.
func TestMCPGetSiteTool(t *testing.T) {
	d := newTestDeps(t)

	doPost := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(payload)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+d.apiToken)
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		return rec
	}

	// 1. initialize handshake. The streamable server returns a session id in
	// the Mcp-Session-Id header that must be echoed on subsequent requests.
	initRec := doPost(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`)
	if initRec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body: %s", initRec.Code, initRec.Body.String())
	}
	sessionID := initRec.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize did not return a Mcp-Session-Id header")
	}

	doPostWithSession := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(payload)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+d.apiToken)
		req.Header.Set("Mcp-Session-Id", sessionID)
		rec := httptest.NewRecorder()
		d.mux.ServeHTTP(rec, req)
		return rec
	}

	// 2. notifications/initialized notification (no response expected).
	doPostWithSession(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	// 3. call the get_site tool.
	callRec := doPostWithSession(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_site","arguments":{}}}`)
	if callRec.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d, body: %s", callRec.Code, callRec.Body.String())
	}
	body := callRec.Body.String()
	// The site title from the test fixture is "DT".
	if !containsStr(body, "DT") {
		t.Errorf("get_site response missing site title; body:\n%s", body)
	}
}

func containsStr(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
