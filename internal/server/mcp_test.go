package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPRequiresToken(t *testing.T) {
	d := newTestDeps(t)
	body := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req := httptest.NewRequest(http.MethodPost, "/mcp", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// mcpPost sends one JSON-RPC message to /mcp with the given extra headers.
func mcpPost(t *testing.T, d *testDeps, payload string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+d.apiToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	return rec
}

// TestMCPGetSiteTool exercises a real tool call through the Streamable HTTP
// transport end-to-end, on the 2026-07-28 protocol: no handshake, no session,
// one self-contained POST. Calling get_site and asserting the response carries
// the site title is the strongest signal that all 17 tools are wired correctly
// without enumerating each one.
func TestMCPGetSiteTool(t *testing.T) {
	d := newTestDeps(t)

	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_site","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"test","version":"1.0"}}}}`,
		map[string]string{
			// The 2026-07-28 revision requires the routing headers to be
			// present and to agree with the body.
			"Mcp-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/call",
			"Mcp-Name":             "get_site",
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d, body: %s", rec.Code, rec.Body.String())
	}
	// The site title from the test fixture is "DT".
	if !containsStr(rec.Body.String(), "DT") {
		t.Errorf("get_site response missing site title; body:\n%s", rec.Body.String())
	}
}

// TestMCPStatelessNoSessionID guards the sessionless posture: the server must
// not mint an Mcp-Session-Id, because clients on the current spec no longer
// have anywhere to put one.
func TestMCPStatelessNoSessionID(t *testing.T) {
	d := newTestDeps(t)

	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{
			"Mcp-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/list",
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Mcp-Session-Id"); got != "" {
		t.Errorf("stateless server returned a session id: %q", got)
	}
	if !containsStr(rec.Body.String(), "list_articles") {
		t.Errorf("tools/list did not list the article tools; body:\n%s", rec.Body.String())
	}
}

// TestMCPDiscover covers the one RPC the 2026-07-28 spec says a server MUST
// implement: it is how a client learns which protocol versions this endpoint
// speaks, now that there is no handshake to negotiate one.
func TestMCPDiscover(t *testing.T) {
	d := newTestDeps(t)

	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{
			"Mcp-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "server/discover",
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("server/discover status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"2026-07-28", "2025-11-25", "dtcom"} {
		if !containsStr(body, want) {
			t.Errorf("server/discover response missing %q; body:\n%s", want, body)
		}
	}
}

// TestMCPLegacyHandshake pins the backward compatibility that made the
// upgrade safe to ship: a client still speaking the old initialize handshake
// gets served on the same endpoint.
func TestMCPLegacyHandshake(t *testing.T) {
	d := newTestDeps(t)

	initRec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		nil)
	if initRec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body: %s", initRec.Code, initRec.Body.String())
	}
	if !containsStr(initRec.Body.String(), "2025-11-25") {
		t.Errorf("initialize did not negotiate 2025-11-25; body:\n%s", initRec.Body.String())
	}

	// No session id to echo back — the old handshake still works, but the
	// server is sessionless either way.
	callRec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_site","arguments":{}}}`,
		map[string]string{"Mcp-Protocol-Version": "2025-11-25"})
	if callRec.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d, body: %s", callRec.Code, callRec.Body.String())
	}
	if !containsStr(callRec.Body.String(), "DT") {
		t.Errorf("get_site response missing site title; body:\n%s", callRec.Body.String())
	}
}

// TestMCPUpdateArticleOmittedVsEmpty pins the distinction the update_article
// schema is built around: a field the caller left out keeps its value, while a
// field sent as "" is genuinely cleared. Treating "" as "unchanged" — as an
// early version did — made clearing a description impossible.
func TestMCPUpdateArticleOmittedVsEmpty(t *testing.T) {
	d := newTestDeps(t)

	call := func(id int64, tool, args string) *httptest.ResponseRecorder {
		t.Helper()
		payload := `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tools/call","params":{"name":"` + tool +
			`","arguments":` + args +
			`,"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
		rec := mcpPost(t, d, payload, map[string]string{
			"Mcp-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/call",
			"Mcp-Name":             tool,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body: %s", tool, rec.Code, rec.Body.String())
		}
		return rec
	}

	call(1, "create_article", `{"title":"Held Note","body":"body text","description":"a summary","tags":["one"]}`)

	// Omitting description leaves it alone.
	call(2, "update_article", `{"slug":"held-note","title":"Held Note Revised"}`)
	got := call(3, "get_article", `{"slug":"held-note"}`).Body.String()
	if !containsStr(got, "a summary") {
		t.Errorf("omitted description was not preserved; body:\n%s", got)
	}
	if !containsStr(got, "Held Note Revised") {
		t.Errorf("title was not updated; body:\n%s", got)
	}

	// Sending it empty clears it, and an empty tag list empties the tags.
	call(4, "update_article", `{"slug":"held-note","description":"","tags":[]}`)
	got = call(5, "get_article", `{"slug":"held-note"}`).Body.String()
	if containsStr(got, "a summary") {
		t.Errorf("explicit empty description did not clear it; body:\n%s", got)
	}
	if containsStr(got, `"one"`) {
		t.Errorf("explicit empty tag list did not clear the tags; body:\n%s", got)
	}
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
