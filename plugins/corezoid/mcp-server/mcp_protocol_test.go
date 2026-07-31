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
func buildTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "convctl-test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build test binary: %v\n%s", err, out)
	}
	return bin
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
			"protocolVersion": mcpProtocolVersion,
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
	if result["serverInfo"] == nil {
		t.Error("expected serverInfo in initialize result")
	}
}

// TestMCPProtocol_ServerDiscover covers MCP 2026-07-28's era-detection probe
// over stdio: a modern client sends server/discover before anything else and
// classifies the server from the reply. It must succeed without a prior
// initialize and must report the protocol versions we can fall back to.
func TestMCPProtocol_ServerDiscover(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	// No initialize first — this is the whole point of the probe.
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
	var result struct {
		ProtocolVersion           string                 `json:"protocolVersion"`
		SupportedProtocolVersions []string               `json:"supportedProtocolVersions"`
		Capabilities              map[string]interface{} `json:"capabilities"`
		ServerInfo                struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("result parse: %v", err)
	}
	if result.ProtocolVersion != mcpProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, mcpProtocolVersion)
	}
	if len(result.SupportedProtocolVersions) != 1 || result.SupportedProtocolVersions[0] != mcpProtocolVersion {
		t.Errorf("supportedProtocolVersions = %v, want [%q]", result.SupportedProtocolVersions, mcpProtocolVersion)
	}
	for _, capability := range []string{"tools", "resources", "prompts"} {
		if _, ok := result.Capabilities[capability]; !ok {
			t.Errorf("expected capability %q in server/discover result", capability)
		}
	}
	if result.ServerInfo.Name != "convctl-mcp" {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, "convctl-mcp")
	}
	if result.ServerInfo.Version != mcpServerVersion {
		t.Errorf("serverInfo.version = %q, want %q", result.ServerInfo.Version, mcpServerVersion)
	}
}

// TestMCPProtocol_InitializeUnsupportedVersion asserts the
// UnsupportedProtocolVersionError contract: a client asking for a revision we
// don't speak gets -32022 with the supported list, not a silently-wrong success.
func TestMCPProtocol_InitializeUnsupportedVersion(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "1900-01-01",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "0"},
		},
	})

	resp := sess.recv()
	if resp["result"] != nil {
		t.Fatalf("expected no result for an unsupported protocolVersion, got %s", resp["result"])
	}
	var rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Supported []string `json:"supported"`
			Requested string   `json:"requested"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp["error"], &rpcErr); err != nil {
		t.Fatalf("error parse: %v", err)
	}
	if rpcErr.Code != errCodeUnsupportedProtocolVersion {
		t.Errorf("error.code = %d, want %d", rpcErr.Code, errCodeUnsupportedProtocolVersion)
	}
	if rpcErr.Message != msgUnsupportedProtocolVersion {
		t.Errorf("error.message = %q, want %q", rpcErr.Message, msgUnsupportedProtocolVersion)
	}
	if len(rpcErr.Data.Supported) != 1 || rpcErr.Data.Supported[0] != mcpProtocolVersion {
		t.Errorf("error.data.supported = %v, want [%q]", rpcErr.Data.Supported, mcpProtocolVersion)
	}
	if rpcErr.Data.Requested != "1900-01-01" {
		t.Errorf("error.data.requested = %q, want %q", rpcErr.Data.Requested, "1900-01-01")
	}
}

// TestMCPProtocol_InitializeMissingVersion pins the tolerant path: older clients
// routinely omit protocolVersion, and that must still hand back a normal
// handshake rather than the -32022 rejection.
func TestMCPProtocol_InitializeMissingVersion(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	sess.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"capabilities": map[string]interface{}{},
			"clientInfo":   map[string]interface{}{"name": "test", "version": "0"},
		},
	})

	resp := sess.recv()
	if resp["error"] != nil {
		t.Fatalf("initialize without protocolVersion returned error: %s", resp["error"])
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("result parse: %v", err)
	}
	if result.ProtocolVersion != mcpProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, mcpProtocolVersion)
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
		"params":  map[string]interface{}{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
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
			Name string `json:"name"`
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
	for _, required := range []string{"login", "logout", "lint-process", "pull-process", "push-process"} {
		if !names[required] {
			t.Errorf("expected tool %q in tools/list", required)
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
		"params":  map[string]interface{}{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
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
		"params":  map[string]interface{}{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
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
