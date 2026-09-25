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

// Non-final nodes must end their logics with a bare go — the server rejects
// the deploy otherwise ("must always have a logic with type go at the end").
func TestMissingDefaultGo(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "queue: park", 0, []map[string]interface{}{
			{"type": "api_queue", "capacity": float64(100)}}),
		lintNode(nFin, "done", 2, nil),
	}
	got := findMissingDefaultGo(nodes)
	if len(got) != 1 || got[0].ID != nA {
		t.Fatalf("expected queue node flagged, got %+v", got)
	}
	nodes[1].logics = append(nodes[1].logics, lgGo(nFin))
	if got := findMissingDefaultGo(nodes); len(got) != 0 {
		t.Fatalf("trailing go must be clean, got %+v", got)
	}
}

// Time semaphors under the 30-second server minimum are rejected at deploy.
// The check converts the dimension, parses numeric strings, and leaves
// templates for runtime.
func TestShortTimers(t *testing.T) {
	mk := func(v interface{}, dim string) []processNode {
		return []processNode{
			lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
			lintNode(nA, "pause", 0, []map[string]interface{}{lgGo(nFin)},
				map[string]interface{}{"type": "time", "value": v, "dimension": dim, "to_node_id": nFin}),
			lintNode(nFin, "done", 2, nil),
		}
	}
	if got := findShortTimers(mk(float64(5), "sec")); len(got) != 1 || got[0].ID != nA {
		t.Fatalf("expected 5s timer flagged, got %+v", got)
	}
	if got := findShortTimers(mk(float64(30), "sec")); len(got) != 0 {
		t.Fatalf("30s timer is legal, got %+v", got)
	}
	if got := findShortTimers(mk("{{delay}}", "sec")); len(got) != 0 {
		t.Fatalf("template timer must be left alone, got %+v", got)
	}
	// numeric string below the minimum is flagged
	if got := findShortTimers(mk("5", "sec")); len(got) != 1 {
		t.Fatalf("numeric-string 5s must be flagged, got %+v", got)
	}
	// 1 min = 60s is legal; 0 min = 0s is flagged (dimension conversion)
	if got := findShortTimers(mk(float64(1), "min")); len(got) != 0 {
		t.Fatalf("1 min is above the minimum, got %+v", got)
	}
	if got := findShortTimers(mk(float64(0), "min")); len(got) != 1 {
		t.Fatalf("0 min = 0s must be flagged, got %+v", got)
	}
	// a count semaphor (no dimension key at all) must not trip the time check
	countSem := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "gate", 0, []map[string]interface{}{lgGo(nFin)},
			map[string]interface{}{"type": "count", "value": float64(5), "esc_node_id": nFin}),
		lintNode(nFin, "done", 2, nil),
	}
	if got := findShortTimers(countSem); len(got) != 0 {
		t.Fatalf("count semaphor must not be treated as a timer, got %+v", got)
	}
}

// Regression for issue #176: a time semaphor on a node that DOES something is
// a call timeout, not a delay, and the server's 30-second floor does not apply
// to it. Flagging those blocked pushes of processes the server had already
// accepted and was running.
func TestShortTimers_TimeoutsOnActionNodesAreNotDelays(t *testing.T) {
	sem10s := map[string]interface{}{"type": "time", "value": float64(10), "dimension": "sec", "to_node_id": nFin}

	// Every node shape whose time semaphor is an escalation timeout.
	for _, lgType := range []string{"api_rpc", "api", "api_code", "db_call", "git_call", "api_copy", "api_callback", "set_param", "api_sum", "api_rpc_reply"} {
		nodes := []processNode{
			lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
			lintNode(nA, "call", 0, []map[string]interface{}{
				{"type": lgType, "err_node_id": nFin}, lgGo(nFin),
			}, sem10s),
			lintNode(nFin, "done", 2, nil),
		}
		if got := findShortTimers(nodes); len(got) != 0 {
			t.Errorf("%s timeout must not be flagged as a short delay, got %+v", lgType, got)
		}
	}

	// The Delay shape in the same process is still caught.
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "call", 0, []map[string]interface{}{
			{"type": "api_rpc", "err_node_id": nFin}, lgGo(nB),
		}, sem10s),
		lintNode(nB, "cooldown", 0, []map[string]interface{}{lgGo(nFin)}, sem10s),
		lintNode(nFin, "done", 2, nil),
	}
	got := findShortTimers(nodes)
	if len(got) != 1 || got[0].ID != nB {
		t.Fatalf("expected only the Delay node flagged, got %+v", got)
	}

	// A routing-only Condition node that holds the task is a delay too.
	cond := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "retry gate", 3, []map[string]interface{}{lgIf(nFin), lgGo(nFin)}, sem10s),
		lintNode(nFin, "done", 2, nil),
	}
	if got := findShortTimers(cond); len(got) != 1 {
		t.Fatalf("routing-only node with a hold timer must be flagged, got %+v", got)
	}
}

// Regression: the Corezoid UI's default Delay node shape puts the hold value
// directly on a `"type":"delay"` logic entry, not on a time semaphor (see
// docs/nodes/delay-node.md "Default Configuration"). That value must be
// checked against the same 30-second floor.
func TestShortTimers_DelayLogicType(t *testing.T) {
	lgDelay := func(v interface{}, dim string) map[string]interface{} {
		return map[string]interface{}{"type": "delay", "value": v, "dimension": dim, "to_node_id": nFin}
	}
	mk := func(v interface{}, dim string) []processNode {
		return []processNode{
			lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
			lintNode(nA, "Delay", 0, []map[string]interface{}{lgDelay(v, dim)}),
			lintNode(nFin, "done", 2, nil),
		}
	}
	if got := findShortTimers(mk(float64(5), "sec")); len(got) != 1 || got[0].ID != nA {
		t.Fatalf("expected 5s delay-logic timer flagged, got %+v", got)
	}
	if got := findShortTimers(mk(float64(30), "sec")); len(got) != 0 {
		t.Fatalf("30s delay-logic timer is legal, got %+v", got)
	}
	if got := findShortTimers(mk("{{delay}}", "sec")); len(got) != 0 {
		t.Fatalf("template delay-logic value must be left alone, got %+v", got)
	}
}
