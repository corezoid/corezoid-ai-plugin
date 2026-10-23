package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The HTTP transport is stateless (MCP 2026-07-28): no Mcp-Session-Id is
// minted or required, and client identity travels in each request's _meta.
// These tests replace the former session-store tests (mint at initialize,
// 404 on an unknown session, DELETE to end one) — that behaviour was removed
// on purpose. The invariant those tests guarded, that concurrent clients are
// never cross-attributed, is kept below.

// modernMeta is the per-request _meta a 2026-07-28 client sends.
func modernMeta(name, version string, caps map[string]interface{}) map[string]interface{} {
	if caps == nil {
		caps = map[string]interface{}{}
	}
	m := map[string]interface{}{
		metaKeyProtocolVersion:    mcpModernProtocolVersion,
		metaKeyClientCapabilities: caps,
	}
	if name != "" {
		m[metaKeyClientInfo] = map[string]interface{}{"name": name, "version": version}
	}
	return m
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

	ctx := context.WithValue(context.Background(), clientIdentityContextKey, clientIdentity{Name: "Request Client", Version: "2.0.0"})
	name, version := clientIdentityFor(ctx)
	if name != "Request Client" || version != "2.0.0" {
		t.Errorf("expected context identity to win over global, got name=%q version=%q", name, version)
	}
}

// ---- end-to-end: concurrent clients must not cross-attribute ---------------

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

// httpModernCall sends one modern tools/call to the spy tool, with no
// handshake and no session, and returns the identity it observed.
func httpModernCall(t *testing.T, client *http.Client, serverURL, clientLabel string) (observedIdentity string, resp *http.Response) {
	t.Helper()
	callBody, _ := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params: mustMarshal(t, map[string]interface{}{
			"name":      spyIdentityToolName,
			"arguments": map[string]interface{}{},
			"_meta":     modernMeta(clientLabel, "v-"+clientLabel, nil),
		}),
	})
	req, err := http.NewRequest(http.MethodPost, serverURL, bytes.NewReader(callBody))
	if err != nil {
		t.Fatalf("building tools/call request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", mcpModernProtocolVersion)
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", spyIdentityToolName)
	callResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("tools/call request failed: %v", err)
	}
	defer callResp.Body.Close()

	var out mcpResponse
	if err := json.NewDecoder(callResp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding tools/call response: %v", err)
	}
	if out.Error != nil {
		t.Fatalf("tools/call error: %+v", out.Error)
	}
	resultRaw, _ := json.Marshal(out.Result)
	var result mcpToolResult
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		t.Fatalf("decoding tool result: %v", err)
	}
	if result.ResultType != resultTypeComplete {
		t.Errorf("resultType = %q, want %q", result.ResultType, resultTypeComplete)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty tool result content")
	}
	return result.Content[0].Text, callResp
}

func TestHTTPStateless_ConcurrentClientsAttributedCorrectly(t *testing.T) {
	withSpyIdentityTool(t)

	srv := httptest.NewServer(http.HandlerFunc(corsWrap(httpMCPEndpoint)))
	defer srv.Close()

	const numClients = 20
	const roundsPerClient = 5

	var wg sync.WaitGroup
	errCh := make(chan string, numClients*roundsPerClient)
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := &http.Client{}
			label := fmt.Sprintf("Client-%d", i)
			want := label + "|v-" + label
			for r := 0; r < roundsPerClient; r++ {
				got, resp := httpModernCall(t, client, srv.URL, label)
				if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
					errCh <- fmt.Sprintf("client %d: server minted Mcp-Session-Id %q; the transport is stateless", i, sid)
				}
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

func TestHTTPToolsCall_LegacyWithoutMetaFallsBackToGlobal(t *testing.T) {
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

// ---- no session semantics ---------------------------------------------------

func postMCP(t *testing.T, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	httpHandlePost(w, req)
	return w
}

func TestHTTPHandlePost_SessionHeaderIgnored(t *testing.T) {
	// A stale or invented Mcp-Session-Id is neither rejected nor echoed.
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	w := postMCP(t, body, map[string]string{"Mcp-Session-Id": "session-that-was-never-registered", "Last-Event-ID": "42"})
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 regardless of a session header, got %d", w.Code)
	}
	if got := w.Header().Get("Mcp-Session-Id"); got != "" {
		t.Errorf("server echoed Mcp-Session-Id %q", got)
	}
}

func TestHTTPHandlePost_Initialize_MintsNoSession(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`
	w := postMCP(t, body, nil)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for legacy initialize, got %d", w.Code)
	}
	if got := w.Header().Get("Mcp-Session-Id"); got != "" {
		t.Errorf("initialize minted Mcp-Session-Id %q; sessions were removed", got)
	}
}

func TestHTTPHandlePost_Notification_Returns202(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	w := postMCP(t, body, nil)
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 for a notification, got %d", w.Code)
	}
}

func TestHTTPHandlePost_ServerDiscover_NoHandshakeRequired(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`
	w := postMCP(t, body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for server/discover, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"supportedVersions"`) {
		t.Errorf("expected supportedVersions in body, got %s", w.Body.String())
	}
}

// ---- 2026-07-28 routing headers and status codes ----------------------------

func modernBody(t *testing.T, method string, params map[string]interface{}) string {
	t.Helper()
	if params == nil {
		params = map[string]interface{}{}
	}
	params["_meta"] = modernMeta("test", "0", nil)
	b, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return string(b)
}

func decodeRecorderError(t *testing.T, w *httptest.ResponseRecorder) *mcpError {
	t.Helper()
	var resp struct {
		Error *mcpError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v — %s", err, w.Body.String())
	}
	return resp.Error
}

func TestHTTPHandlePost_Modern_OK(t *testing.T) {
	w := postMCP(t, modernBody(t, "tools/list", nil), map[string]string{
		"MCP-Protocol-Version": mcpModernProtocolVersion,
		"Mcp-Method":           "tools/list",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if string(resp.Result["resultType"]) != `"complete"` {
		t.Errorf("resultType = %s", resp.Result["resultType"])
	}
	if string(resp.Result["cacheScope"]) != `"public"` || resp.Result["ttlMs"] == nil {
		t.Errorf("expected ttlMs/cacheScope, got ttlMs=%s cacheScope=%s", resp.Result["ttlMs"], resp.Result["cacheScope"])
	}
}

func TestHTTPHandlePost_Modern_MissingVersionHeader_HeaderMismatch(t *testing.T) {
	w := postMCP(t, modernBody(t, "tools/list", nil), nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if e := decodeRecorderError(t, w); e == nil || e.Code != errCodeHeaderMismatch {
		t.Errorf("expected HeaderMismatch (-32020), got %+v", e)
	}
}

func TestHTTPHandlePost_Modern_MethodHeaderMismatch(t *testing.T) {
	w := postMCP(t, modernBody(t, "tools/list", nil), map[string]string{
		"MCP-Protocol-Version": mcpModernProtocolVersion,
		"Mcp-Method":           "prompts/list",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if e := decodeRecorderError(t, w); e == nil || e.Code != errCodeHeaderMismatch {
		t.Errorf("expected HeaderMismatch (-32020), got %+v", e)
	}
}

func TestHTTPHandlePost_Modern_NameHeaderBase64(t *testing.T) {
	body := modernBody(t, "prompts/get", map[string]interface{}{"name": "pull-workspace"})
	// "pull-workspace" base64-encoded.
	w := postMCP(t, body, map[string]string{
		"MCP-Protocol-Version": mcpModernProtocolVersion,
		"Mcp-Method":           "prompts/get",
		"Mcp-Name":             "=?base64?cHVsbC13b3Jrc3BhY2U=?=",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a matching base64 Mcp-Name, got %d: %s", w.Code, w.Body.String())
	}
	w = postMCP(t, body, map[string]string{
		"MCP-Protocol-Version": mcpModernProtocolVersion,
		"Mcp-Name":             "edit-process",
	})
	if e := decodeRecorderError(t, w); w.Code != http.StatusBadRequest || e == nil || e.Code != errCodeHeaderMismatch {
		t.Errorf("expected 400/-32020 for a mismatched Mcp-Name, got %d %+v", w.Code, e)
	}
}

func TestHTTPHandlePost_LegacyBodyWithModernHeader_HeaderMismatch(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	w := postMCP(t, body, map[string]string{"MCP-Protocol-Version": mcpModernProtocolVersion})
	if e := decodeRecorderError(t, w); w.Code != http.StatusBadRequest || e == nil || e.Code != errCodeHeaderMismatch {
		t.Errorf("expected 400/-32020, got %d %+v", w.Code, e)
	}
}

func TestHTTPHandlePost_UnsupportedVersion_400(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`
	w := postMCP(t, body, map[string]string{"MCP-Protocol-Version": "2099-01-01"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	e := decodeRecorderError(t, w)
	if e == nil || e.Code != errCodeUnsupportedProtocolVersion {
		t.Fatalf("expected UnsupportedProtocolVersion (-32022), got %+v", e)
	}
	data, _ := json.Marshal(e.Data)
	if !strings.Contains(string(data), `"requested":"2099-01-01"`) || !strings.Contains(string(data), mcpModernProtocolVersion) {
		t.Errorf("error data = %s", data)
	}
}

func TestHTTPHandlePost_UnknownMethod_404(t *testing.T) {
	w := postMCP(t, modernBody(t, "ping", nil), map[string]string{"MCP-Protocol-Version": mcpModernProtocolVersion})
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for ping (removed in 2026-07-28), got %d", w.Code)
	}
	if e := decodeRecorderError(t, w); e == nil || e.Code != -32601 {
		t.Errorf("expected -32601, got %+v", e)
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
