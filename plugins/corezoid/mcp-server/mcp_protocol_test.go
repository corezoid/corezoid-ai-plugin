package main

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildTestBinary compiles the MCP server to a temp binary for protocol tests.
// It propagates this test binary's own Version so the subprocess reports the
// same serverInfo.version the in-process serverVersion() would.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	return buildTestBinaryWithVersion(t, Version)
}

// buildTestBinaryWithVersion compiles the MCP server with an explicit
// -ldflags-injected main.Version, mirroring how the release workflow builds it.
func buildTestBinaryWithVersion(t *testing.T, version string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "convctl-test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-ldflags", "-X main.Version="+version, "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build test binary: %v\n%s", err, out)
	}
	return bin
}

// initializeServerVersion starts bin, performs the MCP initialize handshake and
// returns the reported serverInfo.version.
func initializeServerVersion(t *testing.T, bin string) string {
	t.Helper()
	sess := newMCPSession(t, bin)
	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "0.0.1"},
		},
	})

	resp := sess.recv()
	if resp["error"] != nil {
		t.Fatalf("initialize returned error: %s", resp["error"])
	}
	var result struct {
		ServerInfo struct {
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("result parse: %v", err)
	}
	return result.ServerInfo.Version
}

// mcpSession wraps a running MCP server subprocess with helpers to send and receive JSON-RPC messages.
type mcpSession struct {
	t    *testing.T
	cmd  *exec.Cmd
	enc  *json.Encoder
	scan *bufio.Scanner
}

func newMCPSession(t *testing.T, bin string) *mcpSession {
	t.Helper()
	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return &mcpSession{
		t:    t,
		cmd:  cmd,
		enc:  json.NewEncoder(stdin),
		scan: bufio.NewScanner(stdout),
	}
}

func (s *mcpSession) send(msg interface{}) {
	s.t.Helper()
	if err := s.enc.Encode(msg); err != nil {
		s.t.Fatalf("send: %v", err)
	}
}

func (s *mcpSession) recv() map[string]json.RawMessage {
	s.t.Helper()
	for s.scan.Scan() {
		line := strings.TrimSpace(s.scan.Text())
		if line == "" {
			continue
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			s.t.Fatalf("recv parse: %v — %s", err, line)
		}
		return msg
	}
	s.t.Fatal("EOF before receiving a message")
	return nil
}

func TestMCPProtocol_Initialize(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "0.0.1"},
		},
	})

	resp := sess.recv()
	if resp["error"] != nil {
		t.Fatalf("initialize returned error: %s", resp["error"])
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("result parse: %v", err)
	}
	if result["protocolVersion"] == nil {
		t.Error("expected protocolVersion in initialize result")
	}
	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected serverInfo object in initialize result, got %#v", result["serverInfo"])
	}

	// serverInfo.version must track the ldflags-injected main.Version, never a
	// hardcoded constant. buildTestBinary injects this process's own Version,
	// so the two agree whether or not `go test` itself was built with -ldflags.
	got, _ := serverInfo["version"].(string)
	if got == "" {
		t.Error("expected a non-empty serverInfo.version")
	}
	if want := serverVersion(); got != want {
		t.Errorf("serverInfo.version = %q, want %q", got, want)
	}
}

// TestMCPProtocol_InitializeVersionFromLdflags is the end-to-end guard for the
// release pipeline: build the way the release workflow does and confirm the
// injected version surfaces in initialize, with the tag's "v" prefix stripped
// so it matches the plugin manifests.
func TestMCPProtocol_InitializeVersionFromLdflags(t *testing.T) {
	bin := buildTestBinaryWithVersion(t, "v9.9.9")
	if got := initializeServerVersion(t, bin); got != "9.9.9" {
		t.Errorf("serverInfo.version = %q, want %q", got, "9.9.9")
	}
}

// TestMCPProtocol_ServerDiscover covers the MCP 2026-07-28 era-detection probe
// over stdio. server/discover is sent *before* initialize on purpose: a modern
// client probes with no handshake, so answering must not depend on one.
func TestMCPProtocol_ServerDiscover(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params":  map[string]interface{}{},
	})
	resp := sess.recv()
	if resp["error"] != nil {
		t.Fatalf("server/discover returned error: %s", resp["error"])
	}
	var discover map[string]json.RawMessage
	if err := json.Unmarshal(resp["result"], &discover); err != nil {
		t.Fatalf("discover result parse: %v", err)
	}

	if got := string(discover["protocolVersion"]); got != `"2025-03-26"` {
		t.Errorf("protocolVersion = %s, want \"2025-03-26\"", got)
	}
	if got := string(discover["supportedProtocolVersions"]); got != `["2025-03-26"]` {
		t.Errorf("supportedProtocolVersions = %s, want [\"2025-03-26\"]", got)
	}
	if discover["capabilities"] == nil {
		t.Error("expected capabilities in server/discover result")
	}
	if discover["serverInfo"] == nil {
		t.Error("expected serverInfo in server/discover result")
	}

	// capabilities and serverInfo must be byte-identical to initialize's, so a
	// modern client that early-exits on discover sees exactly what a legacy
	// client would have negotiated.
	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "initialize",
		"params":  map[string]interface{}{"protocolVersion": "2025-03-26", "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
	})
	initResp := sess.recv()
	if initResp["error"] != nil {
		t.Fatalf("initialize returned error: %s", initResp["error"])
	}
	var initResult map[string]json.RawMessage
	if err := json.Unmarshal(initResp["result"], &initResult); err != nil {
		t.Fatalf("initialize result parse: %v", err)
	}
	for _, field := range []string{"protocolVersion", "capabilities", "serverInfo"} {
		if string(discover[field]) != string(initResult[field]) {
			t.Errorf("%s differs: discover=%s initialize=%s", field, discover[field], initResult[field])
		}
	}
	// initialize must stay legacy — no version list leaking into it.
	if _, ok := initResult["supportedProtocolVersions"]; ok {
		t.Error("initialize result must not contain supportedProtocolVersions")
	}
}

func TestMCPProtocol_ToolsList(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	// Initialize first.
	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{"protocolVersion": "2025-03-26", "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
	})
	sess.recv()

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	})
	resp := sess.recv()
	if resp["error"] != nil {
		t.Fatalf("tools/list returned error: %s", resp["error"])
	}
	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations *struct {
				ReadOnlyHint    *bool `json:"readOnlyHint"`
				DestructiveHint *bool `json:"destructiveHint"`
				IdempotentHint  *bool `json:"idempotentHint"`
				OpenWorldHint   *bool `json:"openWorldHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("tools/list result parse: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool in tools/list")
	}
	names := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, required := range []string{"login", "logout", "lint-process", "pull-process", "push-process", "pause-process", "resume-process", "move-process", "move-folder"} {
		if !names[required] {
			t.Errorf("expected tool %q in tools/list", required)
		}
	}

	// Safety annotations must survive the JSON-RPC round trip — clients read
	// them off the wire, not off the Go struct.
	wantHints := map[string]struct{ readOnly, destructive bool }{
		"delete-process": {readOnly: false, destructive: true},
		"pause-process":  {readOnly: false, destructive: true},
		"move-folder":    {readOnly: false, destructive: true},
		"list-variables": {readOnly: true, destructive: false},
	}
	for _, tool := range result.Tools {
		if tool.Annotations == nil {
			t.Errorf("tool %q has no annotations in tools/list", tool.Name)
			continue
		}
		want, ok := wantHints[tool.Name]
		if !ok {
			continue
		}
		if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != want.readOnly {
			t.Errorf("%s: readOnlyHint = %v, want %v", tool.Name, tool.Annotations.ReadOnlyHint, want.readOnly)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != want.destructive {
			t.Errorf("%s: destructiveHint = %v, want %v", tool.Name, tool.Annotations.DestructiveHint, want.destructive)
		}
		if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
			t.Errorf("%s: openWorldHint should be true", tool.Name)
		}
		if tool.Annotations.IdempotentHint == nil {
			t.Errorf("%s: idempotentHint missing", tool.Name)
		}
	}
	for name := range wantHints {
		if !names[name] {
			t.Errorf("annotation probe expected tool %q in tools/list", name)
		}
	}
}

func TestMCPProtocol_LintProcess_ValidSample(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{"protocolVersion": "2025-03-26", "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
	})
	sess.recv()

	// Use the sample file that ships with the test suite. Path-traversal
	// hardening rejects absolute paths, so pass the project-root-relative
	// form — which is also what real MCP clients would send.
	samplePath := "samples/valid_process.json"

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "lint-process",
			"arguments": map[string]interface{}{"process_path": samplePath},
		},
	})
	resp := sess.recv()
	if resp["error"] != nil {
		t.Fatalf("lint-process returned JSON-RPC error: %s", resp["error"])
	}
	// The tool result may report lint warnings but must not be a hard error.
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("result parse: %v", err)
	}
	if result.IsError {
		t.Errorf("lint-process reported isError=true for valid sample: %v", result.Content)
	}
}

func TestMCPProtocol_AuthRequired_NoCredentials(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{"protocolVersion": "2025-03-26", "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
	})
	sess.recv()

	// pull-process requires auth — should return an isError result, not a JSON-RPC error.
	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "pull-process",
			"arguments": map[string]interface{}{"process_id": "123"},
		},
	})
	resp := sess.recv()
	// We expect either a JSON-RPC error or an isError tool result — but NOT a success.
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if resp["error"] == nil {
		if err := json.Unmarshal(resp["result"], &result); err != nil {
			t.Fatalf("result parse: %v", err)
		}
		if !result.IsError {
			t.Error("expected isError=true for pull-process without credentials")
		}
	}
	// If there's a JSON-RPC error, that's also acceptable — the point is no silent success.
}
