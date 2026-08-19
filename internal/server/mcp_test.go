package server

import (
	"bytes"
	"encoding/json"
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

// TestMCPToolAnnotations pins what each tool claims to do to the site. A client
// reads these to decide what it can call freely and what it should ask about
// first, and both destructiveHint and openWorldHint default to true when
// absent — so an unannotated tool is not neutral, it is alarming.
func TestMCPToolAnnotations(t *testing.T) {
	d := newTestDeps(t)

	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{"Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/list"})
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body: %s", rec.Code, rec.Body.String())
	}

	// The response is an SSE frame wrapping the JSON-RPC result.
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations *struct {
				Title           string `json:"title"`
				ReadOnlyHint    bool   `json:"readOnlyHint"`
				DestructiveHint *bool  `json:"destructiveHint"`
				IdempotentHint  bool   `json:"idempotentHint"`
				OpenWorldHint   *bool  `json:"openWorldHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(jsonRPCResult(t, rec.Body.String()), &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	byName := map[string]int{}
	for i, tool := range listed.Tools {
		byName[tool.Name] = i
		if tool.Annotations == nil {
			t.Errorf("tool %q has no annotations, so it reads as destructive and open-world", tool.Name)
			continue
		}
		if tool.Annotations.Title == "" {
			t.Errorf("tool %q has no display title", tool.Name)
		}
	}

	readOnly := []string{"list_articles", "get_article", "search_articles", "list_links", "get_site", "get_stats"}
	for _, name := range readOnly {
		a := listed.Tools[byName[name]].Annotations
		if a == nil || !a.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", name)
		}
	}

	destructive := []string{"delete_article", "remove_link"}
	for _, name := range destructive {
		a := listed.Tools[byName[name]].Annotations
		if a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s is not marked destructive", name)
		}
	}

	// Everything that writes but takes nothing away must say so explicitly,
	// or the default carries it into the destructive pile.
	safeWrites := []string{"create_article", "update_article", "add_link", "regenerate",
		"update_bio", "update_nav", "update_social", "update_rss_feeds"}
	for _, name := range safeWrites {
		a := listed.Tools[byName[name]].Annotations
		if a == nil || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s is not marked non-destructive", name)
		}
		if a != nil && a.ReadOnlyHint {
			t.Errorf("%s writes but claims to be read-only", name)
		}
	}
	// Replacing a section twice leaves the same site; adding a post twice does not.
	if a := listed.Tools[byName["update_bio"]].Annotations; a == nil || !a.IdempotentHint {
		t.Error("update_bio replaces a value and should be idempotent")
	}
	if a := listed.Tools[byName["create_article"]].Annotations; a != nil && a.IdempotentHint {
		t.Error("create_article adds a post and is not idempotent")
	}

	// refresh_feeds is the only tool that contacts anybody else.
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil {
			continue
		}
		open := *tool.Annotations.OpenWorldHint
		if want := tool.Name == "refresh_feeds"; open != want {
			t.Errorf("%s openWorldHint = %v, want %v", tool.Name, open, want)
		}
	}
}

// jsonRPCResult pulls the "result" object out of a Streamable HTTP response,
// which frames the JSON-RPC message as a server-sent event.
func jsonRPCResult(t *testing.T, body string) []byte {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
		if !ok {
			continue
		}
		var msg struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &msg); err != nil {
			continue
		}
		if len(msg.Error) > 0 {
			t.Fatalf("JSON-RPC error: %s", msg.Error)
		}
		if len(msg.Result) > 0 {
			return msg.Result
		}
	}
	t.Fatalf("no JSON-RPC result in response:\n%s", body)
	return nil
}

// TestMCPToolOutputValidates calls every tool that reads and checks the answer
// comes back as structuredContent.
//
// The point is the validation, not the values. Declaring an output schema means
// the SDK validates each result against it, and a mismatch is a protocol error
// rather than a tool error — the client gets no result at all. It is also easy
// to trip over: a nil Go map is not a JSON object, which is exactly how
// get_site broke the first time these schemas were turned on.
// Every outputSchema has to describe an object. MCP defines structuredContent
// as an object, so a tool whose schema is a top-level array is not merely
// unusual: Claude Desktop rejects the tools/list response with "Invalid result
// for tools/list", naming the offending tool by its index, and refuses to load
// the server at all. The Go SDK derives the schema from the handler's return
// type, which makes this a one-character mistake away at every list-shaped
// tool, so it is checked for all of them at once.
func TestMCPOutputSchemasAreObjects(t *testing.T) {
	d := newTestDeps(t)

	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{"Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/list"})
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var listed struct {
		Tools []struct {
			Name         string          `json:"name"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(jsonRPCResult(t, rec.Body.String()), &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("no tools listed")
	}
	for i, tool := range listed.Tools {
		if len(tool.OutputSchema) == 0 {
			continue // a tool may legitimately declare no structured output
		}
		var schema struct {
			Type any `json:"type"`
		}
		if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
			t.Errorf("tool %d (%s): outputSchema is not an object: %v", i, tool.Name, err)
			continue
		}
		// The SDK writes ["null","array"] for a slice return, so the check has
		// to reject a list of types as well as a plain wrong one.
		if s, ok := schema.Type.(string); !ok || s != "object" {
			t.Errorf("tool %d (%s): outputSchema.type = %v, want \"object\"; a bare array cannot be structuredContent",
				i, tool.Name, schema.Type)
		}
	}
}

func TestMCPToolOutputValidates(t *testing.T) {
	d := newTestDeps(t)

	// A site with nothing in it: empty lists, unset maps, no views recorded.
	// That is the shape most likely to fall foul of a schema.
	readOnly := map[string]string{
		"list_articles":   `{}`,
		"get_article":     `{"slug":"hello"}`,
		"search_articles": `{"query":"nothing matches this"}`,
		"list_links":      `{}`,
		"get_site":        `{}`,
		"get_stats":       `{}`,
	}
	for name, args := range readOnly {
		rec := mcpPost(t, d,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+
				`,"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
			map[string]string{
				"Mcp-Protocol-Version": "2026-07-28",
				"Mcp-Method":           "tools/call",
				"Mcp-Name":             name,
			})
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, body: %s", name, rec.Code, rec.Body.String())
			continue
		}
		var res struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
			IsError           bool            `json:"isError"`
		}
		if err := json.Unmarshal(jsonRPCResult(t, rec.Body.String()), &res); err != nil {
			t.Errorf("%s: decode result: %v", name, err)
			continue
		}
		if res.IsError {
			t.Errorf("%s: returned a tool error: %s", name, rec.Body.String())
			continue
		}
		if len(res.StructuredContent) == 0 {
			t.Errorf("%s: no structuredContent — the output schema is not being applied", name)
		}
	}

	// And every tool advertises the schema its answer is validated against.
	var listed struct {
		Tools []struct {
			Name         string          `json:"name"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}
	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{"Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/list"})
	if err := json.Unmarshal(jsonRPCResult(t, rec.Body.String()), &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	for _, tool := range listed.Tools {
		if len(tool.OutputSchema) == 0 {
			t.Errorf("tool %q has no output schema", tool.Name)
		}
	}
}

// mcpCall runs one tool and fails the test if it does not succeed.
func mcpCall(t *testing.T, d *testDeps, tool, args string) {
	t.Helper()
	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+args+
			`,"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{
			"Mcp-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/call",
			"Mcp-Name":             tool,
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, body: %s", tool, rec.Code, rec.Body.String())
	}
	if containsStr(rec.Body.String(), `"isError":true`) {
		t.Fatalf("%s: tool error: %s", tool, rec.Body.String())
	}
}

type listedResources struct {
	Resources []struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
		MIMEType    string `json:"mimeType"`
	} `json:"resources"`
}

func mcpListResources(t *testing.T, d *testDeps) listedResources {
	t.Helper()
	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{"Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": "resources/list"})
	if rec.Code != http.StatusOK {
		t.Fatalf("resources/list status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var out listedResources
	if err := json.Unmarshal(jsonRPCResult(t, rec.Body.String()), &out); err != nil {
		t.Fatalf("decode resources/list: %v", err)
	}
	return out
}

// TestMCPArticleResources covers posts offered as resources rather than as tool
// calls: they are listed, they can be read, and — the part that needed care —
// the list follows the posts rather than whatever was on disk at startup.
func TestMCPArticleResources(t *testing.T) {
	d := newTestDeps(t)

	listed := mcpListResources(t, d)
	if len(listed.Resources) != 1 {
		t.Fatalf("expected the one fixture post, got %d: %+v", len(listed.Resources), listed.Resources)
	}
	got := listed.Resources[0]
	if got.URI != "dtcom://article/hello" {
		t.Errorf("resource URI = %q", got.URI)
	}
	if got.Title != "Hello" || got.MIMEType != "text/markdown" {
		t.Errorf("resource metadata = %+v", got)
	}

	// Reading one gives the markdown source, frontmatter and all.
	rec := mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dtcom://article/hello","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{
			"Mcp-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "resources/read",
			"Mcp-Name":             "dtcom://article/hello",
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("resources/read status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"title: Hello", "Body text."} {
		if !containsStr(body, want) {
			t.Errorf("resource body missing %q:\n%s", want, body)
		}
	}

	// A post written since the list was last built shows up, and one deleted
	// since drops off. A list fixed at startup would get both wrong.
	mcpCall(t, d, "create_article", `{"title":"Second Post","body":"more words","description":"the second"}`)
	listed = mcpListResources(t, d)
	if len(listed.Resources) != 2 {
		t.Fatalf("a new post did not appear: %+v", listed.Resources)
	}

	mcpCall(t, d, "delete_article", `{"slug":"hello"}`)
	listed = mcpListResources(t, d)
	if len(listed.Resources) != 1 {
		t.Fatalf("a deleted post did not drop off: %+v", listed.Resources)
	}
	if listed.Resources[0].URI != "dtcom://article/second-post" {
		t.Errorf("wrong post survived: %+v", listed.Resources[0])
	}

	// Reading a post that is gone is a not-found, not a crash or an empty file.
	rec = mcpPost(t, d,
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dtcom://article/hello","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{
			"Mcp-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "resources/read",
			"Mcp-Name":             "dtcom://article/hello",
		})
	if !containsStr(rec.Body.String(), "error") {
		t.Errorf("reading a deleted post did not fail:\n%s", rec.Body.String())
	}
}

// TestMCPArticleResourceURIsAreChecked: the slug in a resource URI reaches the
// filesystem, so it gets the same check every other slug-taking path gets.
func TestMCPArticleResourceURIsAreChecked(t *testing.T) {
	for _, uri := range []string{
		"dtcom://article/../../etc/passwd",
		"dtcom://article/",
		"dtcom://article/a/b",
		"file:///etc/passwd",
		"dtcom://other/hello",
	} {
		if _, ok := articleSlugFromURI(uri); ok {
			t.Errorf("%q was accepted as an article URI", uri)
		}
	}
	if slug, ok := articleSlugFromURI("dtcom://article/hello"); !ok || slug != "hello" {
		t.Errorf("a valid URI was rejected: %q %v", slug, ok)
	}
}
