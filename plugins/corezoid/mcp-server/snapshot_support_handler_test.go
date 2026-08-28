package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two ends of the pre-push snapshot gate, exercised through the push
// handler itself:
//
//   - a snapshot API that exists and fails must BLOCK the push (there is a
//     previous server version that ProcessJSON is about to overwrite, and no
//     rollback point was created);
//   - an installation whose API has no snapshot object at all must let the push
//     through, saying so explicitly, and must never be asked to create one.
//
// Both run against the same process file, so the only difference is what the
// environment answers.

// writeDeployedConv drops a lint-clean sample process that already exists on
// the server (obj_id != 0) into a temp cwd, and returns its base name.
func writeDeployedConv(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("samples", "valid_process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var conv map[string]interface{}
	if err := json.Unmarshal(raw, &conv); err != nil {
		t.Fatal(err)
	}
	// The sample is a lint fixture, not a pushable file: complete it into the
	// shape push-process validates (an already-deployed process on a stage) so
	// the test exercises the snapshot gate, not the schema check.
	conv["obj_id"] = float64(123)
	conv["parent_id"] = float64(20)
	conv["status"] = "active"
	conv["ref_mask"] = true
	conv["conv_type"] = "process"
	scheme, _ := conv["scheme"].(map[string]interface{})
	if scheme == nil {
		t.Fatal("sample has no scheme")
	}
	scheme["web_settings"] = []interface{}{}
	// Every node carries coordinates, so push has no reason to re-lay-out or to
	// re-hydrate them from the server mid-test.
	for _, raw := range scheme["nodes"].([]interface{}) {
		node := raw.(map[string]interface{})
		if x, _ := node["x"].(float64); x == 0 {
			node["x"] = float64(100)
		}
		if y, _ := node["y"].(float64); y == 0 {
			node["y"] = float64(100)
		}
	}
	patched, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "123_deployed.conv.json")
	if err := os.WriteFile(p, patched, 0644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	return filepath.Base(p)
}

// deployedProcessOp is what `list conv` answers for an already-deployed process:
// a committed version and a start node, so ProcessJSON can map onto it and
// processNeverDeployed correctly reports "this has state to lose".
func deployedProcessOp() map[string]interface{} {
	return map[string]interface{}{
		"proc":    "ok",
		"obj_id":  float64(123),
		"commits": map[string]interface{}{"version": float64(5)},
		"list": []interface{}{
			map[string]interface{}{
				"obj_type": float64(1),
				"obj_id":   "bbccddaabbccddaabbcc0001",
				"title":    "Start",
			},
		},
	}
}

// A snapshot API that answers ordinary ops but fails the snapshot itself is the
// case the gate exists for: the push must stop, because after ProcessJSON the
// previous server version would be unrecoverable.
func TestHandlePushProcess_SnapshotAPIErrorBlocksPush(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	var createSnapshots int
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "list" && obj == "snapshots":
			// The feature exists here: the probe succeeds.
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
		case typ == "create" && obj == "snapshot":
			createSnapshots++
			return wrapOp(map[string]interface{}{"proc": "error", "description": "Internal server error"})
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": name,
		// These tests exercise the snapshot gate, not the baseline gate: the
		// fixture was never pulled, so adopt_existing carries it past the
		// missing-baseline block (see TestConflict_NoBaseline*).
		"adopt_existing": true,
	})
	if !isErr {
		t.Fatalf("a failed pre-push snapshot must block the push, got success:\n%s", result)
	}
	if createSnapshots != 1 {
		t.Errorf("expected exactly one snapshot attempt on an environment that supports them, got %d", createSnapshots)
	}
	for _, want := range []string{
		"Push blocked: the pre-push snapshot of process #123 failed",
		"Internal server error",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

// A CreateSnapshot API failure on a target that DID resolve (unlike the
// unresolved-target case below) must still be waivable with
// allow_no_snapshot=true, as long as the resolved stage is mutable and
// doesn't look production-like — otherwise a transient platform error on the
// snapshot endpoint would leave a brand-new, still-empty process permanently
// unpushable even though there is nothing of value on the server to lose.
func TestHandlePushProcess_SnapshotAPIErrorWaiverAllowsPushOnMutableStage(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	var createSnapshots int
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "list" && obj == "snapshots":
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
		case typ == "create" && obj == "snapshot":
			createSnapshots++
			return wrapOp(map[string]interface{}{"proc": "error", "description": "Internal server error"})
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
		case typ == "show" && obj == "stage":
			return wrapOp(map[string]interface{}{
				"proc":       "ok",
				"immutable":  false,
				"title":      "develop",
				"short_name": "dev",
			})
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

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":      name,
		"adopt_existing":    true,
		"allow_no_snapshot": true,
	})
	if isErr {
		t.Fatalf("allow_no_snapshot must waive a CreateSnapshot API failure on a resolved mutable stage, got:\n%s", result)
	}
	if createSnapshots != 1 {
		t.Errorf("expected exactly one snapshot attempt, got %d", createSnapshots)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"CreateSnapshot API call failed",
		"Internal server error",
		"NO ROLLBACK POINT EXISTS",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

// The waiver for a CreateSnapshot API failure must be refused on the same
// stages the resolution-failure waiver refuses: a transient API error is not
// license to push blind somewhere an irreversible overwrite is unrecoverable.
func TestHandlePushProcess_SnapshotAPIErrorWaiverRefusedOnImmutableStage(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	var createSnapshots int
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "list" && obj == "snapshots":
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
		case typ == "create" && obj == "snapshot":
			createSnapshots++
			return wrapOp(map[string]interface{}{"proc": "error", "description": "Internal server error"})
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
		case typ == "show" && obj == "stage":
			return wrapOp(map[string]interface{}{
				"proc":      "ok",
				"immutable": true,
				"title":     "production",
			})
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":      name,
		"adopt_existing":    true,
		"allow_no_snapshot": true,
	})
	if !isErr {
		t.Fatalf("allow_no_snapshot must not waive a CreateSnapshot API failure on an immutable stage, got:\n%s", result)
	}
	if createSnapshots != 1 {
		t.Errorf("expected exactly one snapshot attempt, got %d", createSnapshots)
	}
	if !strings.Contains(result, "not accepted here even with allow_no_snapshot=true") {
		t.Errorf("the refusal must say the waiver does not apply, got:\n%s", result)
	}
}

// An older installation with no snapshot object in its API — the shape
// dev.corezoid.com actually returns ("bad object", confirmed by the control
// ops) — must not have its pushes blocked: there is no snapshot to take. The
// push completes and says so, and `create snapshot` is never issued.
func TestHandlePushProcess_EnvironmentWithoutSnapshotsPushesWithWarning(t *testing.T) {
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
			if typ == "create" {
				t.Error("an environment without the snapshot object must never be asked to create a snapshot")
			}
			return wrapOp(badObject)
		case typ == "list" && obj == "commits":
			// Positive control: this conv is reachable and this build does keep
			// per-process versions — only the snapshot object is absent.
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{
				map[string]interface{}{"conv_id": float64(123), "version": float64(1787315841)},
			}})
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
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

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": name,
		// These tests exercise the snapshot gate, not the baseline gate: the
		// fixture was never pulled, so adopt_existing carries it past the
		// missing-baseline block (see TestConflict_NoBaseline*).
		"adopt_existing": true,
		// On an environment with no snapshots, an unreconciled overwrite plus no
		// rollback point is refused on purpose (irreversible), so the second waiver
		// is what keeps this a snapshot-gate test — see
		// TestHandlePushProcess_UnreconciledOverwriteWithoutSnapshotBlocks.
		"allow_no_snapshot": true,
	})
	if isErr {
		t.Fatalf("a missing snapshot feature must not block the push, got error:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"Auto-snapshot skipped: this Corezoid environment does not support snapshots",
		"keep the .conv.json under version control",
		// The two waivers together made this push unrecoverable; that has to be
		// stated, not left to be inferred from the flags that were passed.
		"allow_no_snapshot=true was combined with adopt_existing=true",
		"CANNOT be undone",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

// The third outcome of the snapshot gate: the target could not be resolved, so
// no snapshot could even be attempted. For a process that already has a
// deployed version this is not a "misconfigured environment, carry on" — it is
// an unknown safety configuration, and pushing anyway destroys the previous
// version with no way back. It must block.
func TestHandlePushProcess_UnresolvedTargetBlocksSnapshotlessPush(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		if typ == "create" && obj == "snapshot" {
			t.Error("no snapshot can be attempted when the target is unresolved")
		}
		if typ == "list" && obj == "conv" {
			return wrapOp(deployedProcessOp())
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	// Neither is configured: resolveAndCacheProjectID comes back empty.
	stageID = 0
	cachedProjectID = 0

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if !isErr {
		t.Fatalf("an unresolved target must block the push of a deployed process, got:\n%s", result)
	}
	for _, want := range []string{
		"no pre-push snapshot could be taken",
		"project_id/stage_id could not be resolved",
		"allow_no_snapshot=true",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

// The waiver is refused exactly where an irreversible overwrite is least
// recoverable: a target whose stage cannot be resolved or read at all. "I could
// not determine where this goes" must never be allowed to mean "so anything
// goes", so allow_no_snapshot does not help here.
func TestHandlePushProcess_NoSnapshotWaiverRefusedOnUnresolvedStage(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "create" && obj == "snapshot":
			t.Error("no snapshot can be attempted when the target is unresolved")
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
		case typ == "show" && obj == "folder":
			// The stage behind parent_id 20 cannot be read, so the stage policy
			// cannot clear this target as a mutable dev stage.
			return wrapOp(map[string]interface{}{"proc": "error", "description": "no access to folder"})
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 0
	cachedProjectID = 0

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":      name,
		"adopt_existing":    true,
		"allow_no_snapshot": true,
	})
	if !isErr {
		t.Fatalf("allow_no_snapshot must not be honoured on an unresolvable stage, got:\n%s", result)
	}
	if !strings.Contains(result, "not accepted here even with allow_no_snapshot=true") {
		t.Errorf("the refusal must say the waiver does not apply, got:\n%s", result)
	}
}

// A never-deployed process is the documented exception: there is no previous
// version, so an unresolved target costs nothing and the create-process →
// push-process flow keeps working without any waiver.
func TestHandlePushProcess_UnresolvedTargetAllowsNeverDeployedProcess(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "create" && obj == "snapshot":
			t.Error("a never-deployed process has no previous state to snapshot")
		case typ == "list" && obj == "conv":
			return wrapOp(map[string]interface{}{
				"proc":    "ok",
				"obj_id":  float64(123),
				"commits": map[string]interface{}{"version": float64(0)},
				"list":    []interface{}{},
			})
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
	stageID = 0
	cachedProjectID = 0

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": name,
	})
	if isErr {
		t.Fatalf("a never-deployed process must push without waivers, got:\n%s", result)
	}
	if !strings.Contains(result, "no deployed version yet") {
		t.Errorf("the reason must be stated, got:\n%s", result)
	}
}

// After a successful deploy the sidecars are refreshed so the next push starts
// current. That write can fail (permissions, full disk, a read-back error), and
// when it does the local concurrency state is stale while the deploy really did
// happen. Reporting a bare success there is what makes the next push either
// re-flag the user's own change or lose its 3-way ancestor, so the failure has
// to reach the caller and not just the log.
func TestHandlePushProcess_StaleConcurrencyStateIsReported(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

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
			return wrapOp(deployedProcessOp())
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

	// Make the post-deploy ancestor write fail without disturbing the pre-push
	// baseline read: writeAncestorScheme has to MkdirAll its directory, and a
	// regular file already sitting at that path stops it.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ancestorDirName), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if isErr {
		t.Fatalf("the deploy itself succeeded, so this must not be an error result:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"the local concurrency state was NOT updated",
		"re-pull this process before editing it again",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

// The combination that must NOT be blocked: an installation whose API has no
// snapshot object AND a target that could not be resolved. There was never a
// rollback point to take here, so blocking would make existing processes
// unpushable on those environments — and the block message would tell the user
// to configure snapshots that do not exist. The support question is therefore
// asked before the target-resolution question.
func TestHandlePushProcess_SnapshotlessEnvPushesWithUnresolvedTarget(t *testing.T) {
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
			if typ == "create" {
				t.Error("an environment without the snapshot object must never be asked to create a snapshot")
			}
			return wrapOp(badObject)
		case typ == "list" && obj == "commits":
			// Positive control: the conv is reachable and this build keeps
			// per-process versions. It is conv-scoped, so it still works with no
			// project/stage configured — which is what lets the probe conclude
			// anything at all in this scenario.
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{
				map[string]interface{}{"conv_id": float64(123), "version": float64(1787315841)},
			}})
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
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
	// Nothing resolved — the exact case that used to fall through to the
	// unresolved-target block without ever asking about snapshot support.
	stageID = 0
	cachedProjectID = 0

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
		// On an environment with no snapshots, an unreconciled overwrite plus no
		// rollback point is refused on purpose (irreversible), so the second waiver
		// is what keeps this a snapshot-gate test — see
		// TestHandlePushProcess_UnreconciledOverwriteWithoutSnapshotBlocks.
		"allow_no_snapshot": true,
	})
	if isErr {
		t.Fatalf("a snapshot-less environment must not be blocked by the unresolved-target gate, got:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"does not support snapshots",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected result to contain %q, got:\n%s", want, result)
		}
	}
	if strings.Contains(result, "project_id/stage_id could not be resolved") {
		t.Errorf("must not advise configuring snapshots that do not exist here:\n%s", result)
	}
}
