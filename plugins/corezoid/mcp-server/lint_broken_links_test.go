package main

import "testing"

// A static to_node_id / err_node_id pointing at a node that is absent from the
// process is a broken link (the server rejects the deploy). Both are flagged.
func TestBrokenLinks_DanglingStaticTargetsFlagged(t *testing.T) {
	missing := "ffffffffffffffffffffffff" // 24-hex, not in the node set
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call", 0, []map[string]interface{}{
			{"type": "api_code", "err_node_id": missing},
			{"type": "go", "to_node_id": missing},
		}),
	}
	got := findBrokenLinks(nodes)
	if len(got) != 2 {
		t.Fatalf("expected 2 broken links (err_node_id + to_node_id), got %d: %+v", len(got), got)
	}
	for _, bl := range got {
		if bl.Target != missing || bl.ID != nA {
			t.Fatalf("unexpected finding: %+v", bl)
		}
	}
}

// A count semaphor's esc_node_id and a time semaphor's to_node_id are real node
// links too — a dangling static target there is equally broken.
func TestBrokenLinks_SemaphorTargetsChecked(t *testing.T) {
	missing := "aaaaaaaaaaaaaaaaaaaaaaaf"
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "gate", 0, []map[string]interface{}{lgGo(nFin)},
			map[string]interface{}{"type": "count", "value": float64(5), "esc_node_id": missing}),
		lintNode(nFin, "Final", 2, nil),
	}
	got := findBrokenLinks(nodes)
	if len(got) != 1 || got[0].Field != "esc_node_id" || got[0].Target != missing {
		t.Fatalf("expected the esc_node_id dangling link flagged, got %+v", got)
	}
}

// Valid links (targets exist) and dynamic references ({{...}} / @alias) must
// never be flagged — the latter resolve at deploy time.
func TestBrokenLinks_ValidAndDynamicNotFlagged(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "call", 0, []map[string]interface{}{
			{"type": "api_rpc", "conv_id": "{{env_var[@target]}}", "err_node_id": nFin},
			lgGo(nFin),
		}),
		lintNode(nFin, "Final", 2, nil),
	}
	if got := findBrokenLinks(nodes); len(got) != 0 {
		t.Fatalf("valid + dynamic links must not be flagged, got %+v", got)
	}
	// a non-hex, non-24-char target (e.g. an alias name) is not a static id
	nodes2 := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{{"type": "go", "to_node_id": "@some-alias"}}),
	}
	if got := findBrokenLinks(nodes2); len(got) != 0 {
		t.Fatalf("non-static targets must not be flagged, got %+v", got)
	}
}
