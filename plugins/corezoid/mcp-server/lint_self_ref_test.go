package main

import (
	"strings"
	"testing"
)

// TestLintProcess_SelfReferenceCopy verifies that lint detects api_copy nodes
// whose conv_id equals the process's own obj_id.
func TestLintProcess_SelfReferenceCopy(t *testing.T) {
	result, err := lintProcess("samples/api_copy_to_itself.json")
	if err != nil {
		t.Fatalf("unexpected lint error: %v", err)
	}
	if len(result.SelfReferenceCopies) != 1 {
		t.Errorf("expected 1 SelfReferenceCopy finding, got %d", len(result.SelfReferenceCopies))
	}
	if len(result.SelfReferenceCopies) > 0 {
		sr := result.SelfReferenceCopies[0]
		if sr.NodeType != "api_copy" {
			t.Errorf("expected NodeType=api_copy, got %q", sr.NodeType)
		}
		if sr.NodeTitle != "Copy to Self" {
			t.Errorf("expected NodeTitle=%q, got %q", "Copy to Self", sr.NodeTitle)
		}
		if !strings.Contains(sr.Issue, "force=true") {
			t.Errorf("issue message should mention force=true: %s", sr.Issue)
		}
	}
}

// TestFindSelfReferenceCopies_ProcessIDZero verifies that processID=0 produces
// no findings (cannot check when process ID is unknown).
func TestFindSelfReferenceCopies_ProcessIDZero(t *testing.T) {
	nodes := []processNode{
		{
			id:    "aabbccddaabbccddaabb0001",
			title: "Self Copy",
			logics: []map[string]interface{}{
				{"type": "api_copy", "conv_id": float64(999)},
			},
		},
	}
	result := findSelfReferenceCopies(nodes, 0)
	if len(result) != 0 {
		t.Errorf("expected 0 findings for processID=0, got %d", len(result))
	}
}

// TestFindSelfReferenceCopies_NoMatch verifies no finding when conv_id != processID.
func TestFindSelfReferenceCopies_NoMatch(t *testing.T) {
	nodes := []processNode{
		{
			id:    "aabbccddaabbccddaabb0001",
			title: "Copy to Other",
			logics: []map[string]interface{}{
				{"type": "api_copy", "conv_id": float64(456)},
			},
		},
	}
	result := findSelfReferenceCopies(nodes, 999)
	if len(result) != 0 {
		t.Errorf("expected 0 findings, got %d", len(result))
	}
}

// TestFindSelfReferenceCopies_ApiRpc verifies api_rpc self-reference is also detected.
func TestFindSelfReferenceCopies_ApiRpc(t *testing.T) {
	nodes := []processNode{
		{
			id:    "aabbccddaabbccddaabb0001",
			title: "RPC to Self",
			logics: []map[string]interface{}{
				{"type": "api_rpc", "conv_id": float64(777)},
			},
		},
	}
	result := findSelfReferenceCopies(nodes, 777)
	if len(result) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result))
	}
	if result[0].NodeType != "api_rpc" {
		t.Errorf("expected NodeType=api_rpc, got %q", result[0].NodeType)
	}
	if !strings.Contains(result[0].Issue, "Call a Process") {
		t.Errorf("issue should mention 'Call a Process': %s", result[0].Issue)
	}
}
