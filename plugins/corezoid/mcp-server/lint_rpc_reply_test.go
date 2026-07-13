package main

import "testing"

func lgReply() map[string]interface{} {
	return map[string]interface{}{
		"type":     "api_rpc_reply",
		"res_data": map[string]interface{}{"result": "ok"},
	}
}

// A process that replies on its error path but ends the success path in a bare
// final leaves an RPC caller hanging on success — flagged.
func TestUnrepliedTerminals_SuccessPathWithoutReplyFlagged(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "work", 0, []map[string]interface{}{lgCode(nReply), lgGo(nFin)}),
		lintNode(nReply, "Reply: failed", 3, []map[string]interface{}{lgReply(), lgGo(nErr1)}),
		lintNode(nErr1, "work Error", 2, nil),
		lintNode(nFin, "done", 2, nil),
	}
	got := findUnrepliedTerminals(nodes)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].ID != nFin {
		t.Errorf("expected final %s flagged, got %+v", nFin, got[0])
	}
}

// Success path passing through a Reply node before the final — clean.
func TestUnrepliedTerminals_RepliedSuccessPathClean(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "work", 0, []map[string]interface{}{lgCode(nReply), lgGo(nB)}),
		lintNode(nB, "Reply: success", 0, []map[string]interface{}{lgReply(), lgGo(nFin)}),
		lintNode(nReply, "Reply: failed", 3, []map[string]interface{}{lgReply(), lgGo(nErr1)}),
		lintNode(nErr1, "work Error", 2, nil),
		lintNode(nFin, "done", 2, nil),
	}
	if got := findUnrepliedTerminals(nodes); len(got) != 0 {
		t.Fatalf("replied success path must be clean, got %+v", got)
	}
}

// A fire-and-forget process with no api_rpc_reply anywhere is not RPC-style —
// bare finals are fine.
func TestUnrepliedTerminals_FireAndForgetExempt(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "work", 0, []map[string]interface{}{lgCode(nErr1), lgGo(nFin)}),
		lintNode(nErr1, "work Error", 2, nil),
		lintNode(nFin, "done", 2, nil),
	}
	if got := findUnrepliedTerminals(nodes); len(got) != 0 {
		t.Fatalf("fire-and-forget process must be exempt, got %+v", got)
	}
}

// Retry loop (Delay semaphor back to the worker) must not blow up the walk,
// and the error final behind the retry Condition's Reply stays clean.
func TestUnrepliedTerminals_RetryLoopHandled(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "call", 0, []map[string]interface{}{lgCode(nIF), lgGo(nFin)}),
		lintNode(nIF, "retry?", 3, []map[string]interface{}{lgIf(nDelay), lgGo(nReply)}),
		lintNode(nDelay, "pause", 0, []map[string]interface{}{lgGo(nA)},
			map[string]interface{}{"type": "time", "value": float64(60), "to_node_id": nA}),
		lintNode(nReply, "Reply: failed", 3, []map[string]interface{}{lgReply(), lgGo(nErr1)}),
		lintNode(nErr1, "call Error", 2, nil),
		lintNode(nFin, "done", 2, nil),
	}
	got := findUnrepliedTerminals(nodes)
	if len(got) != 1 || got[0].ID != nFin {
		t.Fatalf("expected only the bare success final flagged, got %+v", got)
	}
}
