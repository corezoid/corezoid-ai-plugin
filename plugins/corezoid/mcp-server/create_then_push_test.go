package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// create-process → push-process, issue #201. The server answers for the new
// conv in a shape that does not positively prove it empty (no commits object),
// and refuses to snapshot it because it has no versions. Before the fix the
// first push was blocked twice over — no pull baseline, then the failed
// snapshot — and the only way through was adopt_existing + allow_no_snapshot
// on the user's own empty process.

const freshChangeTime = float64(1790000000)

// freshConvServer mocks a Corezoid that has just created conv 123. changeTime
// is read on every `list conv`, so a test can move the server under the file.
func freshConvServer(t *testing.T, convJSON []byte, changeTime *float64, createSnapshots *int) {
	t.Helper()
	var conv interface{}
	if err := json.Unmarshal(convJSON, &conv); err != nil {
		t.Fatal(err)
	}
	export := []interface{}{conv}
	var srvURL string
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) == 0 {
			// The GET behind download_url.
			return export
		}
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "create" && obj == "conv":
			return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(123)})
		case obj == "obj_scheme":
			return wrapOp(map[string]interface{}{"proc": "ok", "download_url": srvURL + "/download"})
		case typ == "list" && obj == "conv":
			return wrapOp(map[string]interface{}{
				"proc":        "ok",
				"obj_id":      float64(123),
				"change_time": *changeTime,
				"list":        []interface{}{},
			})
		case typ == "list" && obj == "snapshots":
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
		case typ == "create" && obj == "snapshot":
			*createSnapshots++
			return wrapOp(map[string]interface{}{"proc": "error", "description": "conv has no versions"})
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
	srvURL = srv.URL
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10
}

func TestCreateThenPush_FirstPushNeedsNoWaivers(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	conv, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	changeTime := freshChangeTime
	var createSnapshots int
	freshConvServer(t, conv, &changeTime, &createSnapshots)

	res, isErr := handleCreateProcess(context.Background(), map[string]interface{}{
		"folder_path":  cwd,
		"folder_id":    float64(20),
		"process_name": "deployed",
	})
	if isErr {
		t.Fatalf("create-process failed: %s", res)
	}
	if strings.Contains(res, "Warning") {
		t.Errorf("create-process must record the baseline without a warning, got:\n%s", res)
	}
	base, ok, err := lookupBaseline(cwd, 123)
	if err != nil || !ok {
		t.Fatalf("create-process must record a baseline for the new process (ok=%v err=%v)", ok, err)
	}
	if !base.Created || base.ChangeTime != int64(freshChangeTime) || base.Source != baselineSourceDetail {
		t.Errorf("baseline = %+v, want a detail-sourced create-time baseline at change_time %d", base, int64(freshChangeTime))
	}
	if _, ok := readAncestorScheme(cwd, 123); !ok {
		t.Error("create-process must record the merge ancestor")
	}

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": name,
	})
	if isErr {
		t.Fatalf("the first push of a just-created process must not need any waiver, got:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"has not changed on the server since create-process",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected result to contain %q, got:\n%s", want, result)
		}
	}
	if createSnapshots != 1 {
		t.Errorf("the snapshot is still attempted first, got %d attempts", createSnapshots)
	}
	// The deploy refreshes the baseline, which ends the create-time exemption.
	if base, _, _ := lookupBaseline(cwd, 123); base.Created {
		t.Error("after a push the baseline must no longer be marked as create-time")
	}
}

// Once the server moved past the create-time baseline the process may hold a
// version someone else deployed, so the exemption must not apply.
func TestUntouchedSinceCreate(t *testing.T) {
	cases := []struct {
		name    string
		base    *baselineEntry
		current float64
		want    bool
	}{
		{"created, server unchanged", &baselineEntry{ChangeTime: 100, Source: baselineSourceDetail, Created: true}, 100, true},
		{"created, server moved", &baselineEntry{ChangeTime: 100, Source: baselineSourceDetail, Created: true}, 101, false},
		{"pulled, server unchanged", &baselineEntry{ChangeTime: 100, Source: baselineSourceDetail}, 100, false},
		{"no baseline", nil, 100, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.base != nil {
				if err := writeBaseline(dir, 123, *tc.base); err != nil {
					t.Fatal(err)
				}
			}
			_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
				return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(123), "change_time": tc.current})
			})
			store := fileConflictStore{dir: dir, filePath: filepath.Join(dir, "123_x.conv.json")}
			if got := untouchedSinceCreate(e, store, 123); got != tc.want {
				t.Errorf("untouchedSinceCreate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUntouchedSinceCreate_LookupFailure(t *testing.T) {
	dir := t.TempDir()
	if err := writeBaseline(dir, 123, baselineEntry{ChangeTime: 100, Source: baselineSourceDetail, Created: true}); err != nil {
		t.Fatal(err)
	}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})
	if untouchedSinceCreate(e, fileConflictStore{dir: dir}, 123) {
		t.Error("an unreachable API must not be read as untouched")
	}
}
