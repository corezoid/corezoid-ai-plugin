package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- httpJSONRPCError ------------------------------------------------------

func TestHTTPJSONRPCError_Fields(t *testing.T) {
	resp := httpJSONRPCError("req-1", -32600, "bad request")
	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", resp.JSONRPC)
	}
	if resp.Error == nil {
		t.Fatal("expected non-nil error")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("expected code -32600, got %d", resp.Error.Code)
	}
	if resp.Error.Message != "bad request" {
		t.Errorf("unexpected message: %q", resp.Error.Message)
	}
}

// ---- writeHTTPJSONRPC ------------------------------------------------------

func TestWriteHTTPJSONRPC_SetsContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeHTTPJSONRPC(w, map[string]string{"key": "val"})
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json content type, got %q", ct)
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ---- corsWrap --------------------------------------------------------------

func TestCORSWrap_SetsHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// wildcard mode
	t.Setenv("COREZOID_HTTP_ALLOWED_ORIGINS", "*")
	handler := corsWrap(inner)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("wildcard: expected *, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}

	// whitelist mode — matching origin
	t.Setenv("COREZOID_HTTP_ALLOWED_ORIGINS", "https://example.com")
	handler = corsWrap(inner)
	req = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Origin", "https://example.com")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Errorf("whitelist: expected https://example.com, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}

	// no env var — no CORS header
	t.Setenv("COREZOID_HTTP_ALLOWED_ORIGINS", "")
	handler = corsWrap(inner)
	req = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Origin", "https://evil.com")
	w = httptest.NewRecorder()
	handler(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("no env: expected no CORS header, got %q", got)
	}
}

func TestCORSWrap_Options(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // should not be called
	})
	handler := corsWrap(inner)

	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
}

// ---- httpDispatch ----------------------------------------------------------

func dispatchJSON(t *testing.T, method string, params interface{}) map[string]json.RawMessage {
	t.Helper()
	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		rawParams = json.RawMessage(`{}`)
	}
	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  rawParams,
	}
	result := httpDispatch(context.Background(), req)
	raw, _ := json.Marshal(result)
	var out map[string]json.RawMessage
	json.Unmarshal(raw, &out) //nolint:errcheck
	return out
}

func TestHTTPDispatch_Initialize(t *testing.T) {
	out := dispatchJSON(t, "initialize", map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]interface{}{},
	})
	if out["error"] != nil {
		t.Fatalf("unexpected error: %s", out["error"])
	}
	var result map[string]interface{}
	json.Unmarshal(out["result"], &result) //nolint:errcheck
	if result["protocolVersion"] == nil {
		t.Error("expected protocolVersion in initialize result")
	}
}

func TestHTTPDispatch_InitializeUnsupportedVersion(t *testing.T) {
	out := dispatchJSON(t, "initialize", map[string]interface{}{
		"protocolVersion": "1900-01-01",
		"capabilities":    map[string]interface{}{},
	})
	if out["result"] != nil {
		t.Fatalf("expected no result for an unsupported protocolVersion, got %s", out["result"])
	}
	var rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Supported []string `json:"supported"`
			Requested string   `json:"requested"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out["error"], &rpcErr); err != nil {
		t.Fatalf("error parse: %v", err)
	}
	if rpcErr.Code != mcpUnsupportedProtocolVersionCode {
		t.Errorf("error code = %d, want %d", rpcErr.Code, mcpUnsupportedProtocolVersionCode)
	}
	if rpcErr.Message != "Unsupported protocol version" {
		t.Errorf("error message = %q, want %q", rpcErr.Message, "Unsupported protocol version")
	}
	if len(rpcErr.Data.Supported) != 1 || rpcErr.Data.Supported[0] != mcpProtocolVersion {
		t.Errorf("data.supported = %v, want [%q]", rpcErr.Data.Supported, mcpProtocolVersion)
	}
	if rpcErr.Data.Requested != "1900-01-01" {
		t.Errorf("data.requested = %q, want %q", rpcErr.Data.Requested, "1900-01-01")
	}
}

func TestHTTPDispatch_InitializeMissingVersion(t *testing.T) {
	out := dispatchJSON(t, "initialize", map[string]interface{}{
		"capabilities": map[string]interface{}{},
	})
	if out["error"] != nil {
		t.Fatalf("initialize without protocolVersion returned error: %s", out["error"])
	}
	var result map[string]interface{}
	json.Unmarshal(out["result"], &result) //nolint:errcheck
	if got := result["protocolVersion"]; got != mcpProtocolVersion {
		t.Errorf("protocolVersion = %v, want %q", got, mcpProtocolVersion)
	}
}

func TestHTTPDispatch_ServerDiscover(t *testing.T) {
	out := dispatchJSON(t, "server/discover", nil)
	if out["error"] != nil {
		t.Fatalf("unexpected error: %s", out["error"])
	}
	var result struct {
		ProtocolVersion           string   `json:"protocolVersion"`
		SupportedProtocolVersions []string `json:"supportedProtocolVersions"`
		Capabilities              struct {
			Tools     map[string]interface{} `json:"tools"`
			Resources map[string]interface{} `json:"resources"`
			Prompts   map[string]interface{} `json:"prompts"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	json.Unmarshal(out["result"], &result) //nolint:errcheck
	if result.ProtocolVersion != mcpProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, mcpProtocolVersion)
	}
	if len(result.SupportedProtocolVersions) != 1 || result.SupportedProtocolVersions[0] != mcpProtocolVersion {
		t.Errorf("supportedProtocolVersions = %v, want [%q]", result.SupportedProtocolVersions, mcpProtocolVersion)
	}
	if result.Capabilities.Tools == nil || result.Capabilities.Resources == nil || result.Capabilities.Prompts == nil {
		t.Errorf("expected tools/resources/prompts capabilities, got %+v", result.Capabilities)
	}
	if result.ServerInfo.Name != "convctl-mcp" || result.ServerInfo.Version != mcpServerVersion {
		t.Errorf("serverInfo = %+v, want {convctl-mcp %s}", result.ServerInfo, mcpServerVersion)
	}
}

func TestHTTPDispatch_ToolsList(t *testing.T) {
	out := dispatchJSON(t, "tools/list", nil)
	if out["error"] != nil {
		t.Fatalf("unexpected error: %s", out["error"])
	}
	var result struct {
		Tools []struct{ Name string `json:"name"` } `json:"tools"`
	}
	json.Unmarshal(out["result"], &result) //nolint:errcheck
	if len(result.Tools) == 0 {
		t.Error("expected non-empty tools list")
	}
}

func TestHTTPDispatch_PromptsList(t *testing.T) {
	out := dispatchJSON(t, "prompts/list", nil)
	if out["error"] != nil {
		t.Fatalf("unexpected error: %s", out["error"])
	}
	var result struct {
		Prompts []struct{ Name string `json:"name"` } `json:"prompts"`
	}
	json.Unmarshal(out["result"], &result) //nolint:errcheck
	if len(result.Prompts) == 0 {
		t.Error("expected non-empty prompts list")
	}
}

func TestHTTPDispatch_PromptsGet(t *testing.T) {
	out := dispatchJSON(t, "prompts/get", map[string]interface{}{
		"name":      "pull-workspace",
		"arguments": map[string]string{},
	})
	if out["error"] != nil {
		t.Fatalf("unexpected error: %s", out["error"])
	}
}

func TestHTTPDispatch_PromptsGet_Unknown(t *testing.T) {
	out := dispatchJSON(t, "prompts/get", map[string]interface{}{
		"name":      "nonexistent",
		"arguments": map[string]string{},
	})
	if out["error"] == nil {
		t.Error("expected error for unknown prompt, got nil")
	}
}

func TestHTTPDispatch_ResourcesList(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir) //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	out := dispatchJSON(t, "resources/list", nil)
	if out["error"] != nil {
		t.Fatalf("unexpected error: %s", out["error"])
	}
}

func TestHTTPDispatch_Notifications_ReturnNil(t *testing.T) {
	req := mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  json.RawMessage(`{}`),
	}
	if result := httpDispatch(context.Background(), req); result != nil {
		t.Errorf("expected nil for notification, got %v", result)
	}

	req.Method = "notifications/cancelled"
	if result := httpDispatch(context.Background(), req); result != nil {
		t.Errorf("expected nil for notification, got %v", result)
	}
}

func TestHTTPDispatch_UnknownMethod(t *testing.T) {
	out := dispatchJSON(t, "unknown/method", nil)
	if out["error"] == nil {
		t.Error("expected error for unknown method, got nil")
	}
}

func TestHTTPDispatch_ToolsCall_LintProcess(t *testing.T) {
	// Use the valid sample that already exists in testdata.
	samplePath := "samples/valid_process.json"
	if _, err := os.Stat(samplePath); err != nil {
		t.Skip("valid_process.json not found")
	}
	absPath, _ := os.Getwd()
	absPath = absPath + "/" + samplePath

	out := dispatchJSON(t, "tools/call", map[string]interface{}{
		"name":      "lint-process",
		"arguments": map[string]interface{}{"process_path": absPath},
	})
	if out["error"] != nil {
		t.Fatalf("unexpected JSON-RPC error: %s", out["error"])
	}
}

func TestHTTPDispatch_ToolsCall_InvalidParams(t *testing.T) {
	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`not-json`),
	}
	result := httpDispatch(context.Background(), req)
	raw, _ := json.Marshal(result)
	var out map[string]json.RawMessage
	json.Unmarshal(raw, &out) //nolint:errcheck
	if out["error"] == nil {
		t.Error("expected error for invalid params, got nil")
	}
}

// ---- httpMCPEndpoint (method routing) ---------------------------------------

func TestHTTPMCPEndpoint_Delete(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	w := httptest.NewRecorder()
	httpMCPEndpoint(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for DELETE, got %d", w.Code)
	}
}

func TestHTTPMCPEndpoint_Unsupported(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/mcp", nil)
	w := httptest.NewRecorder()
	httpMCPEndpoint(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHTTPMCPEndpoint_Post_Notification_Returns202(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	httpMCPEndpoint(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 for notification, got %d", w.Code)
	}
}

func TestHTTPDispatch_ResourcesRead_OK(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir) //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	procDir := filepath.Join(dir, ".processes")
	os.MkdirAll(procDir, 0755)                                                               //nolint:errcheck
	os.WriteFile(filepath.Join(procDir, "1_test.conv.json"), []byte(`{"ok":true}`), 0644) //nolint:errcheck

	out := dispatchJSON(t, "resources/read", map[string]interface{}{
		"uri": resourceURIPrefix + "1_test.conv.json",
	})
	if out["error"] != nil {
		t.Fatalf("unexpected error: %s", out["error"])
	}
}

func TestHTTPDispatch_ResourcesRead_NotFound(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir) //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	os.MkdirAll(filepath.Join(dir, ".processes"), 0755) //nolint:errcheck

	out := dispatchJSON(t, "resources/read", map[string]interface{}{
		"uri": resourceURIPrefix + "missing.conv.json",
	})
	if out["error"] == nil {
		t.Error("expected error for missing resource, got nil")
	}
}

func TestHTTPMCPEndpoint_Post_BadJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	httpMCPEndpoint(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with error body, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "parse error") {
		t.Errorf("expected parse error in body, got %q", body)
	}
}
