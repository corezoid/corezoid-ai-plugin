package main

import "testing"

// lgSetParam builds a set_param logic with an optional err_node_id.
func lgSetParam(err string) map[string]interface{} {
	m := map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"step": "x"}}
	if err != "" {
		m["err_node_id"] = err
	}
	return m
}

// A node mixing set_param with go_if_const is old format: the UI converter
// splits it into an action node plus a condition node.
func TestOldFormat_MixedActionConditionFlagged(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "finalize step 4", 0, []map[string]interface{}{
			lgSetParam(nReply), lgIf(nB), lgGo(nFin)}),
		lintNode(nB, "dispatch", 0, []map[string]interface{}{lgGo(nFin)}),
		lintNode(nReply, "Reply: failed", 3, []map[string]interface{}{lgGo(nErr1)}),
		lintNode(nErr1, "Error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	got := findOldFormatNodes(nodes)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].ID != nA {
		t.Errorf("expected mixed node %s flagged, got %+v", nA, got[0])
	}
}

// err_node_id pointing at an obj_type:0 condition node (retry IF) is old
// format: escalation targets must be obj_type:3.
func TestOldFormat_ErrTargetObjType0Flagged(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "provider: call", 0, []map[string]interface{}{lgCode(nIF), lgGo(nFin)}),
		lintNode(nIF, "provider: retry?", 0, []map[string]interface{}{lgIf(nDelay), lgGo(nReply)}),
		lintNode(nDelay, "retry pause", 0, []map[string]interface{}{lgGo(nA)},
			map[string]interface{}{"type": "time", "value": float64(60)}),
		lintNode(nReply, "Reply: failed", 3, []map[string]interface{}{lgGo(nErr1)}),
		lintNode(nErr1, "Error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	got := findOldFormatNodes(nodes)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].ID != nIF {
		t.Errorf("expected err-target %s flagged, got %+v", nIF, got[0])
	}
}

// The same retry IF with obj_type:3 is the correct new format — no finding,
// and it must NOT be reported as a passthrough escalation either (it routes
// via conditions, it does not pass through).
func TestOldFormat_ErrTargetObjType3CleanAndNotPassthrough(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "provider: call", 0, []map[string]interface{}{lgCode(nIF), lgGo(nFin)}),
		lintNode(nIF, "provider: retry?", 3, []map[string]interface{}{lgIf(nDelay), lgGo(nReply)}),
		lintNode(nDelay, "retry pause", 0, []map[string]interface{}{lgGo(nA)},
			map[string]interface{}{"type": "time", "value": float64(60)}),
		lintNode(nReply, "Reply: failed", 3, []map[string]interface{}{
			{"type": "api_rpc_reply", "res_data": map[string]interface{}{"result": "error"}}, lgGo(nErr1)}),
		lintNode(nErr1, "Error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	if got := findOldFormatNodes(nodes); len(got) != 0 {
		t.Fatalf("obj_type:3 retry IF is correct format, got %+v", got)
	}
	if got := findPassthroughEscalations(nodes); len(got) != 0 {
		t.Fatalf("condition escalation must not be passthrough, got %+v", got)
	}
}

// A timer escalation (obj_type:3 Delay with a semaphor and a bare go) routes
// through time, not through nothing — not a passthrough.
func TestPassthroughEscalation_TimerEscalationNotFlagged(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call", 0, []map[string]interface{}{lgCode(nDelay), lgGo(nFin)}),
		lintNode(nDelay, "retry pause", 3, []map[string]interface{}{lgGo(nA)},
			map[string]interface{}{"type": "time", "value": float64(60), "to_node_id": nA}),
		lintNode(nFin, "Final", 2, nil),
	}
	if got := findPassthroughEscalations(nodes); len(got) != 0 {
		t.Fatalf("timer escalation must not be passthrough, got %+v", got)
	}
}

// A business-flow condition reached via go keeps obj_type:0 — the converter
// itself creates split-off condition nodes as obj_type:0. No finding.
func TestOldFormat_BusinessConditionObjType0Clean(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nIF)}),
		lintNode(nIF, "dispatch: provider?", 0, []map[string]interface{}{lgIf(nA), lgGo(nB)}),
		lintNode(nA, "provider-1", 0, []map[string]interface{}{lgSetParam(nReply), lgGo(nFin)}),
		lintNode(nB, "provider-2", 0, []map[string]interface{}{lgSetParam(nReply), lgGo(nFin)}),
		lintNode(nReply, "Reply: failed", 3, []map[string]interface{}{lgGo(nErr1)}),
		lintNode(nErr1, "Error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	if got := findOldFormatNodes(nodes); len(got) != 0 {
		t.Fatalf("business condition with obj_type:0 is fine, got %+v", got)
	}
}
