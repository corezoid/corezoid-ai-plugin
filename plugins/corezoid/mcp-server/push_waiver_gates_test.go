package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The push handler owns four independent safety gates — lint, concurrency,
// Stub Mode and snapshot — and each one has its own waiver. These tests cover
// the properties that hold ACROSS the gates:
//
//   - one waiver never stands in for another (force was doing double duty as
//     the lint override and the concurrent-overwrite authorisation, so a force
//     passed for a lint finding pre-authorised dropping a server change that
//     had not happened yet and was therefore never reported);
//   - a waived gate is named in the tool RESULT, not only on stderr, because a
//     host that surfaces just the returned content would otherwise show
//     "deployed successfully" as the whole record of a lost update;
//   - the two waivers that together make a push irreversible cannot both be
//     reached by accident.

// changedServerProcess is what `list conv` answers for a process someone else
// committed after our pull: a newer change_time than the baseline, and a node
// the local file does not have.
func changedServerProcess() map[string]interface{} {
	return map[string]interface{}{
		"proc":                   "ok",
		"obj_id":                 float64(123),
		"change_time":            float64(9000),
		"last_confirmed_version": float64(9),
		"commits": map[string]interface{}{
			"version": float64(9),
			"list": []interface{}{
				map[string]interface{}{"change_time": float64(9000), "nick": "Alice"},
			},
		},
		"list": []interface{}{
			map[string]interface{}{"obj_type": float64(1), "obj_id": "bbccddaabbccddaabbcc0001", "title": "Start"},
			map[string]interface{}{"obj_type": float64(0), "obj_id": "bbccddaabbccddaabbcc0009", "title": "Alices-node"},
		},
	}
}

// writeTestPullBaseline records the sidecar a pull would have written, at a
// version older than what changedServerProcess reports.
func writeTestPullBaseline(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(cwd, 123, baselineEntry{ChangeTime: 100, Version: 5, Source: baselineSourceDetail}); err != nil {
		t.Fatal(err)
	}
}

// snapshotCapableServer answers every op the push path needs on an environment
// where snapshots work, delegating the process read to procOp.
func snapshotCapableServer(t *testing.T, procOp func() map[string]interface{}) {
	t.Helper()
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "list" && obj == "snapshots":
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
		case typ == "create" && obj == "snapshot":
			return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(77), "version": float64(6)})
		case typ == "list" && obj == "conv":
			return wrapOp(procOp())
		case typ == "create" && obj == "node":
			results := make([]interface{}, len(ops))
			for i, nodeOp := range ops {
				localID, _ := nodeOp["id"].(string)
				results[i] = map[string]interface{}{"proc": "ok", "id": localID, "obj_id": localID}
			}
			return map[string]interface{}{"request_proc": "ok", "ops": results}
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10
}

// force is the lint override. It must not carry a push past the concurrency
// gate: the whole point of that gate is that the user sees WHAT they are about
// to drop before authorising it, and a force set for an unrelated lint finding
// is authorisation given before the conflict existed.
func TestHandlePushProcess_ForceDoesNotWaiveTheConcurrencyGate(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	writeTestPullBaseline(t)
	snapshotCapableServer(t, changedServerProcess)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": name,
		"force":        true,
	})
	if !isErr {
		t.Fatalf("force=true must not deploy over a concurrent server change, got:\n%s", result)
	}
	for _, want := range []string{
		"changed on the server",
		"Alice",
		"overwrite_server_change=true",
		// Anything written against the old contract arrives here with force
		// already set; the report has to say why it did nothing, or the split
		// reads as a broken flag.
		"force=true does NOT waive this gate",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("the block report must name %q, got:\n%s", want, result)
		}
	}
}

// neverDeployedServerProcess is `list conv` for a process that exists as an
// object but was never committed: no confirmed version, no nodes — and a
// change_time past the baseline, which creating or moving the object alone is
// enough to produce.
func neverDeployedServerProcess() map[string]interface{} {
	return map[string]interface{}{
		"proc":                   "ok",
		"obj_id":                 float64(123),
		"change_time":            float64(9000),
		"last_confirmed_version": float64(0),
		"commits":                map[string]interface{}{"version": float64(0)},
		"list":                   []interface{}{},
	}
}

// The irreversibility gate must not fire for a process that has nothing to
// lose. A pulled-but-never-deployed process reaches it — its change_time moves
// on its own, so the concurrency gate reports a divergence, and CreateSnapshot
// refuses a process with no committed version, so no rollback point is taken.
// Both halves of "unreconciled and unrecoverable" are technically true and both
// are vacuous: there is no previous version. Demanding allow_no_snapshot there
// would ask for a waiver to protect something that does not exist.
func TestHandlePushProcess_NeverDeployedOverwriteNeedsNoSnapshotWaiver(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	writeTestPullBaseline(t)

	deployed := false
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "list" && obj == "snapshots":
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
		case typ == "create" && obj == "snapshot":
			// What the real API answers for a process with no committed version.
			return wrapOp(map[string]interface{}{"proc": "error", "description": "conv has no confirmed version"})
		case typ == "list" && obj == "conv":
			return wrapOp(neverDeployedServerProcess())
		case typ == "create" && obj == "node":
			deployed = true
			results := make([]interface{}, len(ops))
			for i, nodeOp := range ops {
				localID, _ := nodeOp["id"].(string)
				results[i] = map[string]interface{}{"proc": "ok", "id": localID, "obj_id": localID}
			}
			return map[string]interface{}{"request_proc": "ok", "ops": results}
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":            name,
		"overwrite_server_change": true,
	})
	if isErr {
		t.Fatalf("a never-deployed process has no version to lose — the push must not need allow_no_snapshot:\n%s", result)
	}
	if !deployed {
		t.Error("the process was never deployed")
	}
	if strings.Contains(result, "allow_no_snapshot=true in addition") {
		t.Errorf("the irreversibility gate must not fire for a never-deployed process:\n%s", result)
	}
	// The waiver was still used, so it is still reported — only the second flag
	// is not demanded.
	for _, want := range []string{
		"Process deployed successfully",
		"WARNING: overwrite_server_change=true was used",
		"no deployed version yet",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("the result must still record %q, got:\n%s", want, result)
		}
	}
}

// overwrite_server_change does authorise it — and the authorisation, the
// divergence and the impact all come back in the result. Before the split this
// path returned an empty message and wrote one line to stderr, so the tool
// result of a lost update was indistinguishable from a clean deploy.
func TestHandlePushProcess_OverwriteServerChangeIsReportedInTheResult(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	writeTestPullBaseline(t)
	snapshotCapableServer(t, changedServerProcess)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":            name,
		"overwrite_server_change": true,
	})
	if isErr {
		t.Fatalf("overwrite_server_change=true must deploy, got:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"Snapshot created before push",
		"WARNING: overwrite_server_change=true was used",
		"those server changes were DROPPED",
		"Alice",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("the deploy result must record %q, got:\n%s", want, result)
		}
	}
}

// The one combination nothing can undo: the comparison was waived, so the
// previous content was never even reported, and no snapshot exists to restore
// it from. Each waiver alone is a judgement call; together they are
// irreversible, so reaching that state takes both flags deliberately. This is
// also the only path by which a misclassified snapshot capability can turn into
// data loss, which is why the check keys on the snapshot outcome rather than on
// the reason it is missing.
func TestHandlePushProcess_UnreconciledOverwriteWithoutSnapshotBlocks(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	badObject := map[string]interface{}{"proc": "error", "description": "bad object"}
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case obj == "snapshots" || obj == "snapshot" || obj == snapshotProbeUnknownObj:
			return wrapOp(badObject)
		case typ == "list" && obj == "commits":
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{
				map[string]interface{}{"conv_id": float64(123), "version": float64(1787315841)},
			}})
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
		case typ == "create" && obj == "node":
			t.Error("nothing may be deployed once the push is refused")
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10

	// adopt_existing: no baseline, so the live version is overwritten without
	// being compared to anything — and this environment has no snapshots.
	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if !isErr {
		t.Fatalf("an unreconciled overwrite with no rollback point must be refused, got:\n%s", result)
	}
	for _, want := range []string{
		"adopt_existing=true waived the comparison",
		"no pre-push snapshot exists",
		"irreversible",
		"allow_no_snapshot=true",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("the refusal must explain %q, got:\n%s", want, result)
		}
	}
}

// Lint findings that force waived used to be reported under the heading
// "Lint (non-blocking, deployed anyway)" — findings present, audit trail
// wrong. The heading has to say the push overrode a gate.
func TestHandlePushProcess_ForcedLintFindingsAreLabelledAsOverridden(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	addShortTimer(t, name)
	snapshotCapableServer(t, deployedProcessOp)

	// Without force the finding blocks.
	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if !isErr || !strings.Contains(result, "re-run with force=true") {
		t.Fatalf("an overridable lint finding must block by default, got isErr=%v:\n%s", isErr, result)
	}

	result, isErr = handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
		"force":          true,
	})
	if isErr {
		t.Fatalf("force=true must waive an overridable lint finding, got:\n%s", result)
	}
	for _, want := range []string{
		"WARNING: force=true was used",
		"block a push by default were overridden",
		"BLOCKING finding(s) overridden with force=true",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("the overridden findings must be labelled as overridden (%q), got:\n%s", want, result)
		}
	}
	if strings.Contains(result, "Lint (non-blocking, deployed anyway)") {
		t.Errorf("overridden blocking findings must not be reported as non-blocking:\n%s", result)
	}
}

// addShortTimer gives the fixture one overridable lint finding: a time semaphor
// under the 30s server minimum.
func addShortTimer(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var conv map[string]interface{}
	if err := json.Unmarshal(raw, &conv); err != nil {
		t.Fatal(err)
	}
	nodes := conv["scheme"].(map[string]interface{})["nodes"].([]interface{})
	start := nodes[0].(map[string]interface{})
	final := nodes[len(nodes)-1].(map[string]interface{})
	// Semaphors live under condition, which is where the linter reads them.
	start["condition"].(map[string]interface{})["semaphors"] = []interface{}{map[string]interface{}{
		"type":       "time",
		"value":      float64(5),
		"dimension":  "sec",
		"to_node_id": final["id"],
	}}
	patched, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".", name), patched, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The handler reads args by name, but unknown argument names are rejected before
// it ever runs (unknownArgsError), so a waiver the schema does not declare is a
// waiver nobody can pass over MCP. The handler tests above call the handler
// directly and would not notice; this one closes that gap, and checks the two
// flags describe themselves as separate.
func TestPushProcessSchema_DeclaresTheSeparatedWaivers(t *testing.T) {
	args := map[string]interface{}{
		"process_path":            "x.conv.json",
		"force":                   true,
		"overwrite_server_change": true,
		"adopt_existing":          true,
		"allow_no_snapshot":       true,
		"allow_active_stub_mode":  true,
		"merge":                   true,
	}
	if msg := unknownArgsError("push-process", args); msg != "" {
		t.Fatalf("every waiver the handler reads must be declared in the schema: %s", msg)
	}
	// Declared as boolean so the CLI path coerces "true" instead of silently
	// handing the handler a string it type-asserts away.
	for _, arg := range []string{"force", "overwrite_server_change"} {
		toolAllowedArgsOnce.Do(buildToolAllowedArgs)
		if got := toolArgTypes["push-process"][arg]; got != "boolean" {
			t.Errorf("%s must be declared boolean, got %q", arg, got)
		}
	}

	var push *mcpTool
	for i := range toolRegistry {
		if toolRegistry[i].Name == "push-process" {
			push = &toolRegistry[i]
		}
	}
	if push == nil {
		t.Fatal("push-process is not registered")
	}
	props := push.InputSchema.(map[string]interface{})["properties"].(map[string]interface{})
	forceDesc := props["force"].(map[string]interface{})["description"].(string)
	if !strings.Contains(forceDesc, "LINT ONLY") || !strings.Contains(forceDesc, "overwrite_server_change") {
		t.Errorf("force must document that it is the lint override only and point at the concurrency waiver, got:\n%s", forceDesc)
	}
	overwriteDesc := props["overwrite_server_change"].(map[string]interface{})["description"].(string)
	if !strings.Contains(overwriteDesc, "allow_no_snapshot") {
		t.Errorf("overwrite_server_change must document the snapshot pairing, got:\n%s", overwriteDesc)
	}
}
