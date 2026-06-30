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

// TestFormatLintResult_Golden compares FormatLintResult output against a golden file.
// Run with -update to regenerate golden files.
func TestFormatLintResult_Golden(t *testing.T) {
	cases := []struct {
		name    string
		sample  string
		golden  string
	}{
		{"clean", "samples/valid_process.json", "testdata/golden/lint_clean.txt"},
		{"orphaned", "samples/orphaned_node.json", "testdata/golden/lint_orphaned.txt"},
		{"noop", "samples/noop_condition.json", "testdata/golden/lint_noop.txt"},
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
