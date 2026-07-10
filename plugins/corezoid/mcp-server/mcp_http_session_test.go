package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// withCleanHTTPSessions clears httpSessions before and after t, so tests
// don't see leftover entries from other tests in the same package run.
func withCleanHTTPSessions(t *testing.T) {
	t.Helper()
	httpSessionsMu.Lock()
	httpSessions = map[string]httpClientIdentity{}
	httpSessionsMu.Unlock()
	t.Cleanup(func() {
		httpSessionsMu.Lock()
		httpSessions = map[string]httpClientIdentity{}
		httpSessionsMu.Unlock()
	})
}

// ---- registerHTTPSession / httpSessionIdentity / endHTTPSession -----------

func TestRegisterHTTPSession_StoreAndRetrieve(t *testing.T) {
	withCleanHTTPSessions(t)

	registerHTTPSession("sess-1", "Claude Code", "1.2.3")

	id, ok := httpSessionIdentity("sess-1")
	if !ok {
		t.Fatal("expected session to be found")
	}
	if id.Name != "Claude Code" || id.Version != "1.2.3" {
		t.Errorf("got name=%q version=%q, want name=%q version=%q", id.Name, id.Version, "Claude Code", "1.2.3")
	}
}

func TestHTTPSessionIdentity_UnknownSessionMisses(t *testing.T) {
	withCleanHTTPSessions(t)

	if _, ok := httpSessionIdentity("never-registered"); ok {
		t.Error("expected miss for unregistered session ID")
	}
	if _, ok := httpSessionIdentity(""); ok {
		t.Error("expected miss for empty session ID")
	}
}

func TestRegisterHTTPSession_EmptyIDIsNoOp(t *testing.T) {
	withCleanHTTPSessions(t)

	registerHTTPSession("", "Claude Code", "1.2.3")

	httpSessionsMu.Lock()
	n := len(httpSessions)
	httpSessionsMu.Unlock()
	if n != 0 {
		t.Errorf("expected registerHTTPSession(\"\", ...) to be a no-op, got %d stored sessions", n)
	}
}

func TestHTTPSessionIdentity_RefreshesLastSeen(t *testing.T) {
	withCleanHTTPSessions(t)

	registerHTTPSession("sess-1", "Claude Code", "1.2.3")
	httpSessionsMu.Lock()
	httpSessions["sess-1"] = httpClientIdentity{Name: "Claude Code", Version: "1.2.3", LastSeen: time.Now().Add(-2 * time.Hour)}
	httpSessionsMu.Unlock()

	if _, ok := httpSessionIdentity("sess-1"); !ok {
		t.Fatal("expected session to be found")
	}

	httpSessionsMu.Lock()
	lastSeen := httpSessions["sess-1"].LastSeen
	httpSessionsMu.Unlock()
	if time.Since(lastSeen) > time.Minute {
		t.Errorf("expected httpSessionIdentity to refresh LastSeen to ~now, got %v ago", time.Since(lastSeen))
	}
}

func TestEndHTTPSession_RemovesEntry(t *testing.T) {
	withCleanHTTPSessions(t)

	registerHTTPSession("sess-1", "Claude Code", "1.2.3")
	endHTTPSession("sess-1")

	if _, ok := httpSessionIdentity("sess-1"); ok {
		t.Error("expected session to be removed after endHTTPSession")
	}
}

func TestEndHTTPSession_EmptyIDIsNoOp(t *testing.T) {
	withCleanHTTPSessions(t)

	registerHTTPSession("sess-1", "Claude Code", "1.2.3")
	endHTTPSession("") // must not panic or affect other sessions

	if _, ok := httpSessionIdentity("sess-1"); !ok {
		t.Error("expected unrelated session to survive endHTTPSession(\"\")")
	}
}

// ---- pruneIdleHTTPSessions --------------------------------------------------

func TestPruneIdleHTTPSessions_EvictsOnlyOlderThanCutoff(t *testing.T) {
	withCleanHTTPSessions(t)

	now := time.Now()
	httpSessionsMu.Lock()
	httpSessions["stale"] = httpClientIdentity{Name: "Old", Version: "0.1", LastSeen: now.Add(-2 * time.Hour)}
	httpSessions["fresh"] = httpClientIdentity{Name: "New", Version: "9.9", LastSeen: now}
	httpSessionsMu.Unlock()

	pruneIdleHTTPSessions(now.Add(-time.Hour))

	if _, ok := httpSessionIdentity("stale"); ok {
		t.Error("expected stale session to be evicted")
	}
	if _, ok := httpSessionIdentity("fresh"); !ok {
		t.Error("expected fresh session to survive the prune")
	}
}

// ---- clientIdentityFor fallback --------------------------------------------

func TestClientIdentityFor_FallsBackToGlobalWithoutContextValue(t *testing.T) {
	prevName, prevVersion := clientName, clientVersion
	t.Cleanup(func() { clientName, clientVersion = prevName, prevVersion })
	clientName, clientVersion = "Fallback Client", "0.0.1"

	name, version := clientIdentityFor(context.Background())
	if name != "Fallback Client" || version != "0.0.1" {
		t.Errorf("expected fallback to global identity, got name=%q version=%q", name, version)
	}
}

func TestClientIdentityFor_PrefersContextValue(t *testing.T) {
	prevName, prevVersion := clientName, clientVersion
	t.Cleanup(func() { clientName, clientVersion = prevName, prevVersion })
	clientName, clientVersion = "Global Client", "1.0.0"

	ctx := context.WithValue(context.Background(), clientIdentityContextKey, clientIdentity{Name: "Session Client", Version: "2.0.0"})
	name, version := clientIdentityFor(ctx)
	if name != "Session Client" || version != "2.0.0" {
		t.Errorf("expected context identity to win over global, got name=%q version=%q", name, version)
	}
}

// ---- end-to-end: concurrent HTTP sessions must not cross-attribute --------

// spyIdentityToolName is a test-only tool registered/unregistered per test so
// we can observe exactly what identity handleToolCall resolved for this call,
// round-tripped through the real MCP tool-result content.
const spyIdentityToolName = "test-identity-spy"

func spyIdentityHandler(ctx context.Context, _ map[string]interface{}) (string, bool) {
	name, version := clientIdentityFor(ctx)
	return name + "|" + version, false
}

func withSpyIdentityTool(t *testing.T) {
	t.Helper()
	toolHandlers[spyIdentityToolName] = spyIdentityHandler
	noAuthTools[spyIdentityToolName] = struct{}{}
	t.Cleanup(func() {
		delete(toolHandlers, spyIdentityToolName)
		delete(noAuthTools, spyIdentityToolName)
	})
}

// httpInitializeAndCall drives one initialize -> tools/call round trip
// against a real httptest server using a real http.Client, exactly the path
// a real MCP client takes. Returns the Mcp-Session-Id the server minted and
// the identity string the spy tool observed.
func httpInitializeAndCall(t *testing.T, client *http.Client, serverURL, clientLabel string) (sessionID, observedIdentity string) {
	t.Helper()

	initBody, _ := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params: mustMarshal(t, map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": clientLabel, "version": "v-" + clientLabel},
		}),
	})
	initResp, err := client.Post(serverURL, "application/json", bytes.NewReader(initBody))
	if err != nil {
		t.Fatalf("initialize request failed: %v", err)
	}
	defer initResp.Body.Close()
	sessionID = initResp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("expected a non-empty Mcp-Session-Id on the initialize response")
	}

	callBody, _ := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params: mustMarshal(t, map[string]interface{}{
			"name":      spyIdentityToolName,
			"arguments": map[string]interface{}{},
		}),
	})
	req, err := http.NewRequest(http.MethodPost, serverURL, bytes.NewReader(callBody))
	if err != nil {
		t.Fatalf("building tools/call request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	callResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("tools/call request failed: %v", err)
	}
	defer callResp.Body.Close()

	var out mcpResponse
	if err := json.NewDecoder(callResp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding tools/call response: %v", err)
	}
	resultRaw, _ := json.Marshal(out.Result)
	var result mcpToolResult
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		t.Fatalf("decoding tool result: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty tool result content")
	}
	return sessionID, result.Content[0].Text
}

func TestHTTPSessions_ConcurrentClientsAttributedCorrectly(t *testing.T) {
	withCleanHTTPSessions(t)
	withSpyIdentityTool(t)

	srv := httptest.NewServer(http.HandlerFunc(corsWrap(httpMCPEndpoint)))
	defer srv.Close()

	const numClients = 20
	const roundsPerClient = 5

	var wg sync.WaitGroup
	errCh := make(chan string, numClients*roundsPerClient)
	seenSessionIDs := make([]string, numClients)
	var seenMu sync.Mutex

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := &http.Client{}
			label := fmt.Sprintf("Client-%d", i)
			want := label + "|v-" + label

			for r := 0; r < roundsPerClient; r++ {
				sid, got := httpInitializeAndCall(t, client, srv.URL, label)

				seenMu.Lock()
				if r == 0 {
					seenSessionIDs[i] = sid
				} else if seenSessionIDs[i] == sid {
					errCh <- fmt.Sprintf("client %d: expected a fresh session ID each round (initialize always mints one), got repeat %q", i, sid)
				}
				seenMu.Unlock()

				if got != want {
					errCh <- fmt.Sprintf("client %d round %d: got identity %q, want %q (cross-attribution)", i, r, got, want)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}

func TestHTTPToolsCall_NoSessionFallsBackToGlobal(t *testing.T) {
	withCleanHTTPSessions(t)
	withSpyIdentityTool(t)

	prevName, prevVersion := clientName, clientVersion
	t.Cleanup(func() { clientName, clientVersion = prevName, prevVersion })
	clientName, clientVersion = "Global Fallback", "3.3.3"

	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params: mustMarshal(t, map[string]interface{}{
			"name":      spyIdentityToolName,
			"arguments": map[string]interface{}{},
		}),
	}
	// No Mcp-Session-Id in context — reqCtx.Value lookup misses, so
	// httpDispatch's tools/call case must not attach a context identity.
	result := httpDispatch(context.Background(), req)
	resp, ok := result.(mcpResponse)
	if !ok {
		t.Fatalf("expected mcpResponse, got %T", result)
	}
	resultRaw, _ := json.Marshal(resp.Result)
	var toolResult mcpToolResult
	if err := json.Unmarshal(resultRaw, &toolResult); err != nil {
		t.Fatalf("decoding tool result: %v", err)
	}
	if got, want := toolResult.Content[0].Text, "Global Fallback|3.3.3"; got != want {
		t.Errorf("expected fallback to global identity %q, got %q", want, got)
	}
}

// ---- 404 on an unrecognized (but present) Mcp-Session-Id -------------------

func postMCP(t *testing.T, body string, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	w := httptest.NewRecorder()
	httpHandlePost(w, req)
	return w
}

func TestHTTPHandlePost_UnrecognizedSessionID_Returns404(t *testing.T) {
	withCleanHTTPSessions(t)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	w := postMCP(t, body, "session-that-was-never-registered")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unrecognized session ID, got %d", w.Code)
	}
}

func TestHTTPHandlePost_EvictedSessionID_Returns404(t *testing.T) {
	withCleanHTTPSessions(t)

	registerHTTPSession("sess-evicted", "Claude Code", "1.0.0")
	pruneIdleHTTPSessions(time.Now().Add(time.Hour)) // cutoff in the future: prune everything

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	w := postMCP(t, body, "sess-evicted")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for an evicted session ID, got %d", w.Code)
	}
}

func TestHTTPHandlePost_NoSessionID_NotRejected(t *testing.T) {
	withCleanHTTPSessions(t)

	// A client that never implemented the session protocol at all (no
	// Mcp-Session-Id header whatsoever) must NOT be rejected — this is the
	// existing graceful-fallback-to-global-identity path, unrelated to the
	// "session ID present but unrecognized" case that 404s.
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	w := postMCP(t, body, "")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for a request with no session header, got %d", w.Code)
	}
}

func TestHTTPHandlePost_Initialize_ExemptFromSessionCheck(t *testing.T) {
	withCleanHTTPSessions(t)

	// initialize always mints a fresh session regardless of what the client
	// sends — even a stray/unrecognized Mcp-Session-Id header must not 404.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`
	w := postMCP(t, body, "some-id-the-server-has-never-seen")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for initialize regardless of session header, got %d", w.Code)
	}
	if w.Header().Get("Mcp-Session-Id") == "" {
		t.Error("expected initialize to mint a fresh Mcp-Session-Id")
	}
}

func TestHTTPHandlePost_Notification_ExemptFromSessionCheck(t *testing.T) {
	withCleanHTTPSessions(t)

	body := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	w := postMCP(t, body, "unrecognized-session-id")

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 for a notification regardless of session header, got %d", w.Code)
	}
}

func TestHTTPHandlePost_RecognizedSessionID_NotRejected(t *testing.T) {
	withCleanHTTPSessions(t)

	registerHTTPSession("sess-good", "Claude Code", "1.0.0")

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	w := postMCP(t, body, "sess-good")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for a recognized session ID, got %d", w.Code)
	}
}

// mustMarshal is a small json.RawMessage helper to keep the request-building
// code above readable.
func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
