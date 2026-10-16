package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordPulledAncestor stores the file as it is now as the scheme a pull
// recorded, i.e. what is already deployed.
func recordPulledAncestor(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAncestorScheme(".", 123, string(raw)); err != nil {
		t.Fatal(err)
	}
}

// addRpcReplyMismatch routes Start through a reply node whose res_data carries
// a key res_data_type does not type: a caller-contract finding the server
// accepts, so a version that has it is genuinely deployed.
func addRpcReplyMismatch(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var conv map[string]interface{}
	if err := json.Unmarshal(raw, &conv); err != nil {
		t.Fatal(err)
	}
	scheme := conv["scheme"].(map[string]interface{})
	nodes := scheme["nodes"].([]interface{})
	start := nodes[0].(map[string]interface{})
	final := nodes[len(nodes)-1].(map[string]interface{})
	const replyID = "bbccddaabbccddaabbcc00a1"
	start["condition"].(map[string]interface{})["logics"] = []interface{}{
		map[string]interface{}{"type": "go", "to_node_id": replyID},
	}
	scheme["nodes"] = append(nodes, map[string]interface{}{
		"id":       replyID,
		"obj_type": float64(0),
		"title":    "Reply",
		"x":        float64(100),
		"y":        float64(200),
		"extra":    `{"modeForm":"expand","icon":""}`,
		"options":  nil,
		"condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{
					"type":            "api_rpc_reply",
					"mode":            "key_value",
					"res_data":        map[string]interface{}{"result": "ok", "untyped": "x"},
					"res_data_type":   map[string]interface{}{"result": "string"},
					"throw_exception": false,
				},
				map[string]interface{}{"type": "go", "to_node_id": final["id"]},
			},
			"semaphors": []interface{}{},
		},
	})
	patched, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".", name), patched, 0o644); err != nil {
		t.Fatal(err)
	}
}

// An edit to a process whose deployed version already had a caller-contract
// finding must not need force: the finding is not part of the change. Before
// the fix every such push was rejected, and force became the default.
func TestHandlePushProcess_InheritedLintFindingDoesNotBlock(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	addRpcReplyMismatch(t, name)
	recordPulledAncestor(t, name)
	snapshotCapableServer(t, deployedProcessOp)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if isErr {
		t.Fatalf("a finding the pulled version already had must not block, got:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"already in the pulled version",
		"untyped", // the finding is still reported
	} {
		if !strings.Contains(strings.ToUpper(result), strings.ToUpper(want)) {
			t.Errorf("result must contain %q, got:\n%s", want, result)
		}
	}
	if strings.Contains(result, "force=true was used") {
		t.Errorf("no waiver was used, none may be reported:\n%s", result)
	}
}

// A short timer is rejected by the server on every deploy, so the pulled
// version having it does not make this push deployable: it must still block,
// not pass the gate and fail on the server.
func TestHandlePushProcess_InheritedServerRejectedFindingStillBlocks(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	addShortTimer(t, name)
	recordPulledAncestor(t, name)
	snapshotCapableServer(t, deployedProcessOp)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if !isErr || !strings.Contains(result, "re-run with force=true") {
		t.Fatalf("a finding the server rejects must block even when inherited, got isErr=%v:\n%s", isErr, result)
	}
	if strings.Contains(result, "already in the pulled version") {
		t.Errorf("a server-rejected finding must not be reported as inherited:\n%s", result)
	}
}

// The same finding introduced by the edit still blocks, and the block report
// lists only what blocks.
func TestHandlePushProcess_IntroducedLintFindingStillBlocks(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	recordPulledAncestor(t, name)
	addShortTimer(t, name)
	snapshotCapableServer(t, deployedProcessOp)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if !isErr || !strings.Contains(result, "introduced by this push") || !strings.Contains(result, "re-run with force=true") {
		t.Fatalf("a finding the edit introduced must block, got isErr=%v:\n%s", isErr, result)
	}
	if strings.Contains(result, "already in the pulled version") {
		t.Errorf("nothing was inherited, got:\n%s", result)
	}
}

func TestSplitInheritedLint(t *testing.T) {
	cur := &LintResult{
		ShortTimers:        []ShortTimer{{ID: "a"}, {ID: "b"}},
		RpcReplyMismatches: []RpcReplyMismatch{{NodeID: "r", UntypedKeys: []string{"x", "y"}}},
		UnrepliedTerminals: []UnrepliedTerminal{{ID: "u"}},
	}
	base := &LintResult{
		ShortTimers:        []ShortTimer{{ID: "a"}},
		RpcReplyMismatches: []RpcReplyMismatch{{NodeID: "r", UntypedKeys: []string{"x"}}},
		UnrepliedTerminals: []UnrepliedTerminal{{ID: "u"}},
	}
	introduced, inherited := splitInheritedLint(cur, base)
	// Short timers are rejected by the server, so both count as introduced
	// even though base had timer a.
	if _, n, _ := categorizeLintForPush(introduced); n != 3 {
		t.Errorf("want 3 introduced (timers a and b, reply node with a new untyped key), got %d: %+v", n, introduced)
	}
	if _, n, _ := categorizeLintForPush(inherited); n != 1 {
		t.Errorf("want 1 inherited (terminal u), got %d: %+v", n, inherited)
	}

	introduced, inherited = splitInheritedLint(cur, nil)
	if _, n, _ := categorizeLintForPush(introduced); n != 4 {
		t.Errorf("without a baseline every finding is introduced, got %d", n)
	}
	if _, n, _ := categorizeLintForPush(inherited); n != 0 {
		t.Errorf("without a baseline nothing is inherited, got %d", n)
	}
}
