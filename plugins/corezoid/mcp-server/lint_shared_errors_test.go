package main

import "testing"

// lintNode builds a typed processNode for the shared-error-cluster tests.
func lintNode(id, title string, objType float64, logics []map[string]interface{}, sems ...map[string]interface{}) processNode {
	return processNode{id: id, title: title, objType: objType, logics: logics, sems: sems}
}

func lgGo(to string) map[string]interface{} {
	return map[string]interface{}{"type": "go", "to_node_id": to}
}
func lgIf(to string) map[string]interface{} {
	return map[string]interface{}{"type": "go_if_const", "to_node_id": to}
}
func lgCode(err string) map[string]interface{} {
	m := map[string]interface{}{"type": "api_code"}
	if err != "" {
		m["err_node_id"] = err
	}
	return m
}

const (
	nStart = "aaaaaaaaaaaaaaaaaaaaaaa1"
	nA     = "aaaaaaaaaaaaaaaaaaaaaaa2"
	nB     = "aaaaaaaaaaaaaaaaaaaaaaa3"
	nFin   = "aaaaaaaaaaaaaaaaaaaaaaa4"
	nIF    = "aaaaaaaaaaaaaaaaaaaaaaa5"
	nErr1  = "aaaaaaaaaaaaaaaaaaaaaaa6"
	nErr2  = "aaaaaaaaaaaaaaaaaaaaaaa7"
	nDelay = "aaaaaaaaaaaaaaaaaaaaaaa8"
	nReply = "aaaaaaaaaaaaaaaaaaaaaaa9"
)

// One node's error fanning through ITS OWN Condition (many branches) into one
// Error terminal is the allowed standard pattern — no finding.
func TestSharedErrorClusters_OwnConditionFanIsAllowed(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call", 0, []map[string]interface{}{lgCode(nIF), lgGo(nFin)}),
		lintNode(nIF, "error kind?", 0, []map[string]interface{}{
			lgIf(nErr1), lgIf(nErr1), lgIf(nErr1), lgIf(nErr1), lgIf(nErr1), lgGo(nErr1)}),
		lintNode(nErr1, "api call error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	if got := findSharedErrorClusters(nodes); len(got) != 0 {
		t.Fatalf("own-condition fan must be allowed, got %v", got)
	}
}

// A neighbouring node's err_node_id pointing at the SAME error node is the
// violation (a neighbour's error must never join another node's cluster).
func TestSharedErrorClusters_DirectNeighbourFanInFlagged(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call A", 0, []map[string]interface{}{lgCode(nErr1), lgGo(nB)}),
		lintNode(nB, "api call B", 0, []map[string]interface{}{lgCode(nErr1), lgGo(nFin)}),
		lintNode(nErr1, "shared error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	got := findSharedErrorClusters(nodes)
	if len(got) != 1 || got[0].ID != nErr1 || len(got[0].Sources) != 2 {
		t.Fatalf("direct fan-in must be flagged with both sources, got %v", got)
	}
}

// Two different escalation tails converging on one Error terminal is the same
// violation (a similar error from a neighbouring node is equally forbidden).
func TestSharedErrorClusters_ConvergingTailsFlagged(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call A", 0, []map[string]interface{}{lgCode(nIF), lgGo(nB)}),
		lintNode(nB, "api call B", 0, []map[string]interface{}{lgCode(nReply), lgGo(nFin)}),
		lintNode(nIF, "A error kind?", 0, []map[string]interface{}{lgGo(nErr1)}),
		lintNode(nReply, "B reply", 3, []map[string]interface{}{lgGo(nErr1)}),
		lintNode(nErr1, "shared terminal", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	got := findSharedErrorClusters(nodes)
	if len(got) != 1 || got[0].ID != nErr1 || len(got[0].Sources) != 2 {
		t.Fatalf("converging tails must flag the shared terminal, got %v", got)
	}
	// the per-source cluster entries (nIF, nReply) stay unflagged
	for _, sc := range got {
		if sc.ID == nIF || sc.ID == nReply {
			t.Fatalf("single-source cluster nodes must not be flagged: %v", got)
		}
	}
}

// The standard retry loop (err -> IF -> delay -> back to the node; IF -> error
// final) belongs to ONE node — no finding, even though the delay re-enters the
// main flow.
func TestSharedErrorClusters_RetryLoopIsOneCluster(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call", 0, []map[string]interface{}{lgCode(nIF), lgGo(nFin)}),
		lintNode(nIF, "retry?", 0, []map[string]interface{}{lgIf(nDelay), lgGo(nErr1)}),
		lintNode(nDelay, "pause 60s", 0, []map[string]interface{}{lgGo(nA)},
			map[string]interface{}{"type": "time", "to_node_id": nA}),
		lintNode(nErr1, "api call error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	if got := findSharedErrorClusters(nodes); len(got) != 0 {
		t.Fatalf("a single node's retry loop must not be flagged, got %v", got)
	}
}

// An err_node_id pointing back INTO the main flow (continue-on-error) is not
// an error cluster at all — skipped.
func TestSharedErrorClusters_ErrIntoMainFlowSkipped(t *testing.T) {
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call A", 0, []map[string]interface{}{lgCode(nB), lgGo(nB)}),
		lintNode(nB, "api call B", 0, []map[string]interface{}{lgCode(nB), lgGo(nFin)}),
		lintNode(nFin, "Final", 2, nil),
	}
	if got := findSharedErrorClusters(nodes); len(got) != 0 {
		t.Fatalf("continue-on-error must not be flagged, got %v", got)
	}
}

// An escalation that also has a business entry (a condition's fatal branch)
// is STILL a shared error cluster when several nodes' err paths feed it —
// error-ish nodes (obj_type 2/3) never count as business flow.
func TestSharedErrorClusters_BusinessBranchDoesNotWhitelist(t *testing.T) {
	nIF2 := "aaaaaaaaaaaaaaaaaaaaaa10"
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call A", 0, []map[string]interface{}{lgCode(nReply), lgGo(nB)}),
		lintNode(nB, "api call B", 0, []map[string]interface{}{lgCode(nReply), lgGo(nIF2)}),
		lintNode(nIF2, "verdict?", 0, []map[string]interface{}{lgIf(nReply), lgGo(nFin)}),
		lintNode(nReply, "Reply: error", 3, []map[string]interface{}{lgGo(nErr1)}),
		lintNode(nErr1, "payment failed", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	got := findSharedErrorClusters(nodes)
	if len(got) != 2 {
		t.Fatalf("exactly the reply and its terminal must be flagged, got %v", got)
	}
	ids := map[string]int{}
	for _, sc := range got {
		ids[sc.ID] = len(sc.Sources)
	}
	if ids[nReply] != 2 || ids[nErr1] != 2 {
		t.Fatalf("reply and its terminal must be flagged with 2 err sources despite the business branch, got %v", got)
	}
}

// A count semaphor escalates via esc_node_id, not err_node_id — two nodes'
// count escalations converging on one Reply/Error cluster is the same shared
// -cluster violation (symmetry with the err_node_id walks).
func TestSharedErrorClusters_EscNodeIdFanInFlagged(t *testing.T) {
	semEsc := func(esc string) map[string]interface{} {
		return map[string]interface{}{"type": "count", "value": float64(500), "esc_node_id": esc}
	}
	nodes := []processNode{
		lintNode(nStart, "Start", 1, []map[string]interface{}{lgGo(nA)}),
		lintNode(nA, "api call A", 0, []map[string]interface{}{{"type": "api"}, lgGo(nB)}, semEsc(nReply)),
		lintNode(nB, "api call B", 0, []map[string]interface{}{{"type": "api"}, lgGo(nFin)}, semEsc(nReply)),
		lintNode(nReply, "Reply: throttled", 3, []map[string]interface{}{
			{"type": "api_rpc_reply", "res_data": map[string]interface{}{"result": "error"}}, lgGo(nErr1)}),
		lintNode(nErr1, "shared throttle error", 2, nil),
		lintNode(nFin, "Final", 2, nil),
	}
	got := findSharedErrorClusters(nodes)
	if len(got) != 2 {
		t.Fatalf("esc_node_id fan-in must flag the shared reply and terminal, got %v", got)
	}
	ids := map[string]int{}
	for _, sc := range got {
		ids[sc.ID] = len(sc.Sources)
	}
	if ids[nReply] != 2 || ids[nErr1] != 2 {
		t.Fatalf("both count-semaphor sources must be attributed, got %v", got)
	}
}
