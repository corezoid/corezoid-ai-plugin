package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// ---- processNeverDeployed --------------------------------------------------
//
// The pre-push snapshot gate blocks a push when CreateSnapshot fails, because
// without git for .conv.json files the previous server version is unrecoverable.
// A process that has never been deployed is the one case where that reasoning
// does not hold: there is no previous version to lose, and CreateSnapshot
// rejects it outright — so the gate must not read that rejection as a reason to
// block. Getting this wrong makes create-process -> push-process impossible for
// every brand-new process, which is why each branch below is pinned.

func TestProcessNeverDeployed_NoCommitNoNodes(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":    "ok",
				"obj_id":  float64(555),
				"commits": map[string]interface{}{"version": float64(0)},
				"list":    []interface{}{},
			}},
		}
	})
	if !processNeverDeployed(e, 555) {
		t.Error("a process with version 0 and no nodes has never been deployed")
	}
}

func TestProcessNeverDeployed_HasCommittedVersion(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":    "ok",
				"obj_id":  float64(555),
				"commits": map[string]interface{}{"version": float64(3)},
				"list":    []interface{}{},
			}},
		}
	})
	if processNeverDeployed(e, 555) {
		t.Error("version 3 is a deployed process — the snapshot gate must still block")
	}
}

// A draft with nodes but no commit still holds work that a snapshot would
// capture, so it must not take the exemption.
func TestProcessNeverDeployed_HasNodesButNoCommit(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":   "ok",
				"obj_id": float64(555),
				"list": []interface{}{
					map[string]interface{}{"id": "aaaaaaaaaaaaaaaaaaaaaaa1"},
				},
			}},
		}
	})
	if processNeverDeployed(e, 555) {
		t.Error("a process carrying nodes has state a snapshot would capture")
	}
}

// The whole point of the exemption is to unblock a push; a failed lookup must
// therefore fall back to the conservative answer, not the permissive one.
func TestProcessNeverDeployed_LookupFailureBlocks(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})
	if processNeverDeployed(e, 555) {
		t.Error("an unreachable API must not be read as never-deployed")
	}
}

// A response that does not answer the question must not be read as an answer.
// processNeverDeployed is the one thing that disarms the snapshot gate for a
// process that already exists on the server, so every partial, reshaped or
// truncated API response has to fall back to "block", not to "nothing to lose".
func TestProcessNeverDeployed_IncompleteResponseBlocks(t *testing.T) {
	cases := []struct {
		name string
		op   map[string]interface{}
	}{
		{
			name: "commits missing",
			op:   map[string]interface{}{"list": []interface{}{}},
		},
		{
			name: "commits.version missing",
			op: map[string]interface{}{
				"commits": map[string]interface{}{"list": []interface{}{}},
				"list":    []interface{}{},
			},
		},
		{
			name: "commits not an object",
			op: map[string]interface{}{
				"commits": "none",
				"list":    []interface{}{},
			},
		},
		{
			name: "commits.version not numeric",
			op: map[string]interface{}{
				"commits": map[string]interface{}{"version": "latest"},
				"list":    []interface{}{},
			},
		},
		{
			name: "commits.version null",
			op: map[string]interface{}{
				"commits": map[string]interface{}{"version": nil},
				"list":    []interface{}{},
			},
		},
		{
			name: "list missing",
			op: map[string]interface{}{
				"commits": map[string]interface{}{"version": float64(0)},
			},
		},
		{
			name: "list not an array",
			op: map[string]interface{}{
				"commits": map[string]interface{}{"version": float64(0)},
				"list":    map[string]interface{}{},
			},
		},
		{
			name: "list null",
			op: map[string]interface{}{
				"commits": map[string]interface{}{"version": float64(0)},
				"list":    nil,
			},
		},
		{
			name: "empty op",
			op:   map[string]interface{}{},
		},
		{
			// commits.version is 0 but the process carries a confirmed
			// deployed version — the veto field wins.
			name: "last_confirmed_version set",
			op: map[string]interface{}{
				"commits":                map[string]interface{}{"version": float64(0)},
				"last_confirmed_version": float64(7),
				"list":                   []interface{}{},
			},
		},
		{
			name: "last_confirmed_version not numeric",
			op: map[string]interface{}{
				"commits":                map[string]interface{}{"version": float64(0)},
				"last_confirmed_version": "7",
				"list":                   []interface{}{},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := map[string]interface{}{"proc": "ok", "obj_id": float64(555)}
			for k, val := range tc.op {
				op[k] = val
			}
			_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
				return map[string]interface{}{
					"request_proc": "ok",
					"ops":          []interface{}{op},
				}
			})
			if processNeverDeployed(e, 555) {
				t.Errorf("%s: an unconfirmed response must not disarm the snapshot gate", tc.name)
			}
		})
	}
}

// The numeric spellings a real response can use must still be recognised: the
// gate would otherwise block every genuinely new process whenever the decoder
// hands back json.Number instead of float64.
func TestProcessNeverDeployed_AcceptsNumericSpellings(t *testing.T) {
	cases := []struct {
		name    string
		version interface{}
		lcv     interface{}
	}{
		{name: "float64", version: float64(0)},
		{name: "json.Number", version: json.Number("0")},
		{name: "int", version: 0},
		{name: "numeric string", version: "0"},
		{name: "last_confirmed_version zero", version: float64(0), lcv: float64(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := map[string]interface{}{
				"proc":    "ok",
				"obj_id":  float64(555),
				"commits": map[string]interface{}{"version": tc.version},
				"list":    []interface{}{},
			}
			if tc.lcv != nil {
				op["last_confirmed_version"] = tc.lcv
			}
			_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
				return map[string]interface{}{
					"request_proc": "ok",
					"ops":          []interface{}{op},
				}
			})
			if !processNeverDeployed(e, 555) {
				t.Errorf("%s: version 0 with no nodes is a never-deployed process", tc.name)
			}
		})
	}
}

func TestProcessNeverDeployed_GuardsNilAndZero(t *testing.T) {
	if processNeverDeployed(nil, 555) {
		t.Error("nil executor must not be read as never-deployed")
	}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		t.Error("obj_id 0 must not reach the API")
		return okResponse(ops)
	})
	if processNeverDeployed(e, 0) {
		t.Error("obj_id 0 must not be read as never-deployed")
	}
}

// ---- mirroredDirForFolder -------------------------------------------------
//
// create-process used to write into the CWD while a later pull-process wrote the
// same object into the folder tree mirroring its parent_id, leaving two copies
// and split baseline sidecars. mirroredDirForFolder is what makes create agree
// with pull; every "" it returns is a fall back to the old CWD behaviour, so the
// negative cases matter as much as the positive one.

func TestMirroredDirForFolder_MirrorsPullPlacement(t *testing.T) {
	root := tmpHomeAndCWD(t)
	writeTestStageMarker(t, root, 900, 800, "stage")
	if err := UpdateCurrent(func(f *Folder) { f.RootPath = root }); err != nil {
		t.Fatalf("persist root: %v", err)
	}

	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		// One folder, 42_billing, whose parent is the stage itself.
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":          "ok",
				"obj_id":        float64(42),
				"title":         "billing",
				"obj_type":      float64(0),
				"parent_obj_id": float64(900),
			}},
		}
	})
	e.StageID = 900

	got := mirroredDirForFolder(e, 42)
	want := filepath.Join(root, "42_billing")
	if got.Dir != want {
		t.Errorf("mirrored dir = %q, want %q", got.Dir, want)
	}
	if got.StageRoot != root {
		t.Errorf("stage root = %q, want %q", got.StageRoot, root)
	}
	// The segments carry what a usable folder marker needs.
	if len(got.Segments) != 1 || got.Segments[0].ID != 42 ||
		got.Segments[0].Title != "billing" || got.Segments[0].ParentID != 900 {
		t.Errorf("segments = %+v, want one {42 billing parent 900}", got.Segments)
	}
}

// A registered root is deliberately present here: without the StageID guard
// findStageRootFromCWD(0) would happily return it and the folder would be
// resolved against a workspace that is not wired for stages. The mock asserts
// the API is never reached, so this test fails if the guard is dropped.
func TestMirroredDirForFolder_NoStageFallsBack(t *testing.T) {
	root := tmpHomeAndCWD(t)
	if err := UpdateCurrent(func(f *Folder) { f.RootPath = root }); err != nil {
		t.Fatalf("persist root: %v", err)
	}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		t.Error("no stage is known — the folder path must not be resolved")
		return okResponse(ops)
	})
	e.StageID = 0

	if got := mirroredDirForFolder(e, 42); got.Dir != "" {
		t.Errorf("want fall back to caller's path, got %q", got.Dir)
	}
}

func TestMirroredDirForFolder_APIErrorFallsBack(t *testing.T) {
	root := tmpHomeAndCWD(t)
	writeTestStageMarker(t, root, 900, 800, "stage")
	if err := UpdateCurrent(func(f *Folder) { f.RootPath = root }); err != nil {
		t.Fatalf("persist root: %v", err)
	}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})
	e.StageID = 900

	if got := mirroredDirForFolder(e, 42); got.Dir != "" {
		t.Errorf("an unresolvable folder must fall back, got %q", got.Dir)
	}
}

// Fully wired workspace — stage marker, registered root, reachable API — so the
// only thing standing between folderID 0 and a bogus resolve is the guard.
func TestMirroredDirForFolder_GuardsNilAndZero(t *testing.T) {
	if got := mirroredDirForFolder(nil, 42); got.Dir != "" {
		t.Errorf("nil executor must fall back, got %q", got.Dir)
	}
	root := tmpHomeAndCWD(t)
	writeTestStageMarker(t, root, 900, 800, "stage")
	if err := UpdateCurrent(func(f *Folder) { f.RootPath = root }); err != nil {
		t.Fatalf("persist root: %v", err)
	}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		t.Error("folder 0 must not reach the API")
		return okResponse(ops)
	})
	e.StageID = 900

	if got := mirroredDirForFolder(e, 0); got.Dir != "" {
		t.Errorf("folder 0 must fall back, got %q", got.Dir)
	}
}
