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
	})
	if isErr {
		t.Fatalf("a missing snapshot feature must not block the push, got error:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"Auto-snapshot skipped: this Corezoid environment does not support snapshots",
		"keep the .conv.json under version control",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}
