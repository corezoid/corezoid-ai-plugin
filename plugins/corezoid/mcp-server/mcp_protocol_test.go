package main

import (
	"bufio"
	"encoding/json"
	"os"
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
	return newMCPSessionInDir(t, bin, "")
}

func newMCPSessionInDir(t *testing.T, bin, dir string) *mcpSession {
	t.Helper()
	cmd := exec.Command(bin)
	if dir != "" {
		cmd.Dir = dir
	}
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

func (s *mcpSession) callTool(id int, name string, arguments map[string]interface{}) (string, bool) {
	s.t.Helper()
	s.send(map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": arguments},
	})
	resp := s.recv()
	if resp["error"] != nil {
		s.t.Fatalf("%s returned JSON-RPC error: %s", name, resp["error"])
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		s.t.Fatalf("%s result parse: %v", name, err)
	}
	if len(result.Content) != 1 {
		s.t.Fatalf("%s returned %d content blocks", name, len(result.Content))
	}
	return result.Content[0].Text, result.IsError
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
	if result["serverInfo"] == nil {
		t.Error("expected serverInfo in initialize result")
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
	for _, required := range []string{
		"login", "logout", "lint-process", "pull-process", "push-process",
		"show-project-policy", "configure-project-policy",
	} {
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

func TestMCPProtocol_ProjectPolicyLifecycle(t *testing.T) {
	bin := buildTestBinary(t)
	project := t.TempDir()
	sess := newMCPSessionInDir(t, bin, project)
	sess.send(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{"protocolVersion": "2025-03-26", "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "test", "version": "0"}},
	})
	sess.recv()

	shown, isErr := sess.callTool(2, "show-project-policy", map[string]interface{}{})
	if isErr || !strings.Contains(shown, "Cycle safety: off") || !strings.Contains(shown, "Process contracts: off") {
		t.Fatalf("unexpected default policy over MCP: %s", shown)
	}
	configured, isErr := sess.callTool(3, "configure-project-policy", map[string]interface{}{
		"cycle_safety": "strict", "process_contracts": "strict",
		"contract_dependency_scope": "project", "max_cycle_iterations": 7,
	})
	if isErr || !strings.Contains(configured, "Cycle safety: strict (maximum 7 iterations") || !strings.Contains(configured, "Process contracts: strict") {
		t.Fatalf("unexpected configured policy over MCP: %s", configured)
	}
	policyData, err := os.ReadFile(filepath.Join(project, ".corezoid", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(policyData) {
		t.Fatalf("MCP wrote invalid policy JSON: %s", policyData)
	}
	paused, isErr := sess.callTool(4, "configure-project-policy", map[string]interface{}{"cycle_safety": "off"})
	if !isErr || !strings.Contains(paused, "confirm_policy_downgrade=true") {
		t.Fatalf("MCP downgrade was not paused: %s", paused)
	}
	shown, isErr = sess.callTool(5, "show-project-policy", map[string]interface{}{})
	if isErr || !strings.Contains(shown, "Cycle safety: strict") {
		t.Fatalf("paused MCP downgrade changed the policy: %s", shown)
	}
}
