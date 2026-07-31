package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -update rewrites golden files with actual output; use when intentionally changing format.
var updateGolden = flag.Bool("update", false, "update golden files")

// TestLintProcess_Clean verifies a well-formed process reports no issues.
func TestLintProcess_Clean(t *testing.T) {
	result, err := lintProcess("samples/valid_process.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphanedNodes) != 0 {
		t.Errorf("expected 0 orphaned nodes, got %d", len(result.OrphanedNodes))
	}
	if len(result.NoopConditions) != 0 {
		t.Errorf("expected 0 noop conditions, got %d", len(result.NoopConditions))
	}
	if len(result.UnusedSetParams) != 0 {
		t.Errorf("expected 0 unused set_params, got %d", len(result.UnusedSetParams))
	}
}

// TestLintProcess_OrphanedNode verifies orphaned node detection.
func TestLintProcess_OrphanedNode(t *testing.T) {
	result, err := lintProcess("samples/orphaned_node.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphanedNodes) != 1 {
		t.Errorf("expected 1 orphaned node, got %d", len(result.OrphanedNodes))
	}
	if result.OrphanedNodes[0].Title != "Orphaned" {
		t.Errorf("expected orphaned node title 'Orphaned', got %q", result.OrphanedNodes[0].Title)
	}
}

// TestLintProcess_NoopCondition verifies noop condition detection.
func TestLintProcess_NoopCondition(t *testing.T) {
	result, err := lintProcess("samples/noop_condition.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.NoopConditions) != 1 {
		t.Errorf("expected 1 noop condition, got %d", len(result.NoopConditions))
	}
}

// TestLintProcess_PassthroughEscalation verifies passthrough escalation node detection.
func TestLintProcess_PassthroughEscalation(t *testing.T) {
	result, err := lintProcess("samples/passthrough_escalation.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.PassthroughEscalations) != 1 {
		t.Errorf("expected 1 passthrough escalation, got %d", len(result.PassthroughEscalations))
	}
	if result.PassthroughEscalations[0].TargetTitle != "rpc_error" {
		t.Errorf("expected target title 'rpc_error', got %q", result.PassthroughEscalations[0].TargetTitle)
	}
}

// TestLintProcess_LiteralReplyValues verifies detection of literal non-string
// values in api_rpc_reply res_data — the scheme shape that hangs the server
// commit ("no response from server") when pushed through the API.
func TestLintProcess_LiteralReplyValues(t *testing.T) {
	result, err := lintProcess("samples/reply_literal_values.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The fixture has two offending reply nodes: one via res_data, one via the
	// alternative extra/extra_type spelling (both are accepted by the platform
	// schema and both hang the commit the same way).
	if len(result.LiteralReplyValues) != 2 {
		t.Fatalf("expected 2 literal reply findings, got %d: %+v", len(result.LiteralReplyValues), result.LiteralReplyValues)
	}
	lr := result.LiteralReplyValues[0]
	if lr.Title != "Reply literals" {
		t.Errorf("expected node title 'Reply literals', got %q", lr.Title)
	}
	// links=[] and chars=0 are literals; note is a plain string and must not be flagged.
	if len(lr.Fields) != 2 {
		t.Errorf("expected 2 flagged fields, got %d: %v", len(lr.Fields), lr.Fields)
	}
	for _, f := range lr.Fields {
		if strings.Contains(f, "note") {
			t.Errorf("plain string value 'note' must not be flagged, got %v", lr.Fields)
		}
	}

	// Second node uses extra/extra_type: meta={} is a literal, status is a string.
	le := result.LiteralReplyValues[1]
	if le.Title != "Reply extra literal" {
		t.Errorf("expected node title 'Reply extra literal', got %q", le.Title)
	}
	if len(le.Fields) != 1 || !strings.Contains(le.Fields[0], "extra.meta") {
		t.Errorf("expected exactly [extra.meta ...] flagged, got %v", le.Fields)
	}
}

// TestLintProcess_LiteralReplyValues_CleanReply verifies that api_rpc_reply
// with only strings/templates in res_data reports nothing.
func TestLintProcess_LiteralReplyValues_CleanReply(t *testing.T) {
	result, err := lintProcess("samples/valid_process.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.LiteralReplyValues) != 0 {
		t.Errorf("expected 0 literal reply findings, got %d: %+v", len(result.LiteralReplyValues), result.LiteralReplyValues)
	}
}

// TestLintProcess_StubbedApiRpcExport verifies that the plugin accepts the
// Corezoid UI export shape for Call a Process stubs: obj_type:4 with
// condition.stub.logics. Stub conditions intentionally omit to_node_id because
// they select a mock api_rpc_reply instead of routing through the graph. The
// fixture covers both key_value replies and the Stub-specific keys shape where
// res_data is an array and res_data_type remains an object.
func TestLintProcess_StubbedApiRpcExport(t *testing.T) {
	result, err := lintProcess("samples/stubbed_api_rpc.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.SchemaValid {
		t.Fatalf("expected schema-valid stubbed api_rpc export, got: %s", result.SchemaError)
	}
	if len(result.OrphanedNodes) != 0 {
		t.Fatalf("expected no orphaned nodes, got %+v", result.OrphanedNodes)
	}
	if len(result.BrokenLinks) != 0 {
		t.Fatalf("expected no broken links, got %+v", result.BrokenLinks)
	}
	if len(result.StubModeNodes) != 1 {
		t.Fatalf("expected active Stub Mode finding, got %+v", result.StubModeNodes)
	}
	if result.StubModeNodes[0].Branches != 2 {
		t.Fatalf("expected 2 stub branches, got %+v", result.StubModeNodes[0])
	}
}

func TestLintProcess_InactiveStubConfigDoesNotWarn(t *testing.T) {
	data, err := os.ReadFile("samples/stubbed_api_rpc.json")
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"obj_type": 4`, `"obj_type": 0`, 1))

	f, err := os.CreateTemp("", "inactive-stub-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := lintProcess(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.SchemaValid {
		t.Fatalf("expected schema-valid inactive stub config, got: %s", result.SchemaError)
	}
	if len(result.StubModeNodes) != 0 {
		t.Fatalf("expected no active Stub Mode finding for obj_type:0, got %+v", result.StubModeNodes)
	}
}

func TestFormatLintResult_StubModeExplainsRisk(t *testing.T) {
	result, err := lintProcess("samples/stubbed_api_rpc.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := FormatLintResult(result)
	for _, want := range []string{
		"ACTIVE STUB MODE NODES",
		"temporary mock replies",
		"bypasses the real called process",
		"before production, disable Stub Mode",
		"allow_active_stub_mode=true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected Stub lint output to contain %q, got:\n%s", want, out)
		}
	}
}

// TestLintProcess_MalformedJSON verifies graceful error on invalid JSON.
func TestLintProcess_MalformedJSON(t *testing.T) {
	f, err := os.CreateTemp("", "bad-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("{not valid json")
	f.Close()

	_, err = lintProcess(f.Name())
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

// TestLintProcess_MissingScheme verifies graceful error when scheme is absent.
func TestLintProcess_MissingScheme(t *testing.T) {
	f, err := os.CreateTemp("", "no-scheme-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(`{"obj_type":1,"title":"No Scheme"}`)
	f.Close()

	_, err = lintProcess(f.Name())
	if err == nil {
		t.Error("expected error for missing scheme, got nil")
	}
}

// TestLintProcess_EmptyNodes verifies a process with no nodes doesn't panic.
func TestLintProcess_EmptyNodes(t *testing.T) {
	f, err := os.CreateTemp("", "empty-nodes-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(`{"obj_type":1,"title":"Empty","scheme":{"nodes":[]}}`)
	f.Close()

	result, err := lintProcess(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalNodes != 0 {
		t.Errorf("expected 0 nodes, got %d", result.TotalNodes)
	}
}

// TestLintProcess_RpcReplyMismatch verifies that api_rpc_reply nodes with
// res_data keys that have no matching res_data_type entry are reported with
// the exact key name so the author can fix it in one pass.
func TestLintProcess_RpcReplyMismatch(t *testing.T) {
	result, err := lintProcess("samples/api_rpc_reply_mismatch.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.RpcReplyMismatches) == 0 {
		t.Fatal("expected at least one RpcReplyMismatch, got none")
	}
	m := result.RpcReplyMismatches[0]
	if m.NodeTitle != "Reply Node" {
		t.Errorf("expected node title 'Reply Node', got %q", m.NodeTitle)
	}
	if m.DataField != "res_data" || m.TypeField != "res_data_type" {
		t.Errorf("unexpected field names: DataField=%q TypeField=%q", m.DataField, m.TypeField)
	}
	if !sliceContains(m.UntypedKeys, "status") {
		t.Errorf("expected untyped key 'status', got UntypedKeys=%v", m.UntypedKeys)
	}
	if sliceContains(m.UntypedKeys, "result") {
		t.Errorf("'result' has a type entry and must not appear in UntypedKeys; got %v", m.UntypedKeys)
	}
	if !strings.Contains(m.Issue, `"status"`) {
		t.Errorf("Issue message must name the offending key, got: %q", m.Issue)
	}
}

// TestLintProcess_RpcReplyClean verifies that a well-aligned api_rpc_reply
// node produces no RpcReplyMismatch entries.
func TestLintProcess_RpcReplyClean(t *testing.T) {
	result, err := lintProcess("samples/multi_action_logics.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.RpcReplyMismatches) != 0 {
		t.Errorf("expected 0 RpcReplyMismatches for aligned node, got %d: %+v",
			len(result.RpcReplyMismatches), result.RpcReplyMismatches)
	}
}

// sliceContains reports whether target appears in ss.
func sliceContains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

// TestFormatLintResult_Golden compares FormatLintResult output against a golden file.
// Run with -update to regenerate golden files.
func TestFormatLintResult_Golden(t *testing.T) {
	cases := []struct {
		name   string
		sample string
		golden string
	}{
		{"clean", "samples/valid_process.json", "testdata/golden/lint_clean.txt"},
		{"orphaned", "samples/orphaned_node.json", "testdata/golden/lint_orphaned.txt"},
		{"noop", "samples/noop_condition.json", "testdata/golden/lint_noop.txt"},
		{"reply_literals", "samples/reply_literal_values.json", "testdata/golden/lint_reply_literals.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := lintProcess(tc.sample)
			if err != nil {
				t.Fatalf("lintProcess(%s): %v", tc.sample, err)
			}
			got := FormatLintResult(result)

			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(tc.golden), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(tc.golden, []byte(got), 0644); err != nil {
					t.Fatalf("write golden %s: %v", tc.golden, err)
				}
				t.Logf("updated golden: %s", tc.golden)
				return
			}

			want, err := os.ReadFile(tc.golden)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to create)", tc.golden, err)
			}
			if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
				t.Errorf("FormatLintResult output differs from golden %s\n--- want ---\n%s\n--- got ---\n%s",
					tc.golden, string(want), got)
			}
		})
	}
}

// TestLintProcess_NodeExtraNull verifies that a node-level "extra": null passes
// schema validation. The platform itself emits null for some nodes, and
// pull-process writes it verbatim — so the plugin must accept its own pull
// output (previously: 'at /scheme/nodes/0/extra: got null, want string').
func TestLintProcess_NodeExtraNull(t *testing.T) {
	f, err := os.CreateTemp("", "extra-null-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(`{
		"obj_type": 1, "obj_id": 1, "conv_type": "process", "title": "extra-null", "status": "active",
		"params": [], "ref_mask": true,
		"scheme": {"web_settings": [[],[]], "nodes": [
			{"id": "aaaa0001513aa075cf400001", "obj_type": 1, "title": "Start", "x": 0, "y": 0,
			 "extra": null, "options": null,
			 "condition": {"logics": [{"type": "go", "to_node_id": "aaaa0002513aa075cf400002"}], "semaphors": []}},
			{"id": "aaaa0002513aa075cf400002", "obj_type": 2, "title": "Final", "x": 0, "y": 100,
			 "extra": "{}", "options": null,
			 "condition": {"logics": [], "semaphors": []}}
		]}
	}`)
	f.Close()

	result, err := lintProcess(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.SchemaValid {
		t.Errorf("extra:null must pass schema validation, got: %s", result.SchemaError)
	}
}
