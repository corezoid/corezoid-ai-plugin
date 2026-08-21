package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A directory this server materializes must be usable as a target for the next
// operation. create-process mirrors the Corezoid tree on disk (see
// mirroredDirForFolder), and before ensureFolderMarkers those mirrored
// directories carried no <id>_<name>.folder.json — so the very next
// create-process run from the directory that had just been created failed with
// "no <id>_<name>.folder.json file found", the folder ID being unresolvable.
// The whole chain is exercised here: create A (mirrored dir born), create B
// inside that directory with no folder_id, then pull and push both.

// markerTestSampleConv reads the lint-clean sample and completes it into the
// shape push-process accepts, for a process that lives in folder 42.
func markerTestSampleConv(t *testing.T, raw []byte, objID int, title string) map[string]interface{} {
	t.Helper()
	var conv map[string]interface{}
	if err := json.Unmarshal(raw, &conv); err != nil {
		t.Fatal(err)
	}
	conv["obj_id"] = float64(objID)
	conv["parent_id"] = float64(42)
	conv["title"] = title
	conv["status"] = "active"
	conv["ref_mask"] = true
	conv["conv_type"] = "process"
	scheme, _ := conv["scheme"].(map[string]interface{})
	if scheme == nil {
		t.Fatal("sample has no scheme")
	}
	scheme["web_settings"] = []interface{}{}
	for _, rawNode := range scheme["nodes"].([]interface{}) {
		node := rawNode.(map[string]interface{})
		if x, _ := node["x"].(float64); x == 0 {
			node["x"] = float64(100)
		}
		if y, _ := node["y"].(float64); y == 0 {
			node["y"] = float64(100)
		}
	}
	return conv
}

func TestCreateProcess_MirroredDirIsUsableForFollowUpOperations(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)

	// The sample has to be read before the test moves cwd into the workspace.
	sampleRaw, err := os.ReadFile(filepath.Join("samples", "valid_process.json"))
	if err != nil {
		t.Fatal(err)
	}

	root := tmpHomeAndCWD(t)
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWD) }) //nolint:errcheck
	writeTestStageMarker(t, root, 900, 800, "dev")
	if err := UpdateCurrent(func(f *Folder) { f.RootPath = root }); err != nil {
		t.Fatalf("persist root: %v", err)
	}

	// Corezoid side: stage 900 holds folder 42 "billing"; two processes get
	// created in it, 501 then 502.
	titles := map[int]string{501: "alpha", 502: "beta"}
	newConvIDs := []int{501, 502}
	created := 0
	// The handler needs its own base URL for export download links, which only
	// exists after the server starts.
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/download/") {
			id, _ := strconv.Atoi(path.Base(r.URL.Path))
			body, err := json.Marshal([]interface{}{markerTestSampleConv(t, sampleRaw, id, titles[id])})
			if err != nil {
				t.Error(err)
				return
			}
			w.Write(body) //nolint:errcheck
			return
		}
		var body struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		json.NewDecoder(r.Body).Decode(&body)          //nolint:errcheck
		json.NewEncoder(w).Encode(func() interface{} { //nolint:errcheck
			if len(body.Ops) == 0 {
				return wrapOp(map[string]interface{}{"proc": "ok"})
			}
			op := body.Ops[0]
			typ, _ := op["type"].(string)
			obj, _ := op["obj"].(string)
			objID, _ := op["obj_id"].(float64)
			switch {
			case obj == "folder" && typ == "show":
				switch int(objID) {
				case 900: // the stage itself: where the chain walk stops
					return wrapOp(map[string]interface{}{
						"proc": "ok", "obj_id": float64(900), "title": "dev",
						"obj_type": float64(10), "parent_obj_id": float64(800),
					})
				case 42:
					return wrapOp(map[string]interface{}{
						"proc": "ok", "obj_id": float64(42), "title": "billing",
						"obj_type": float64(0), "parent_obj_id": float64(900),
					})
				}
				return wrapOp(map[string]interface{}{"proc": "error", "description": "unknown folder"})
			case obj == "conv" && typ == "create":
				if created >= len(newConvIDs) {
					t.Errorf("unexpected extra conv create: %v", op)
					return wrapOp(map[string]interface{}{"proc": "error"})
				}
				id := newConvIDs[created]
				created++
				return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(id)})
			case obj == "obj_scheme":
				return wrapOp(map[string]interface{}{
					"proc":         "ok",
					"download_url": baseURL + "/download/" + strconv.Itoa(int(objID)),
				})
			case obj == "conv" && typ == "list":
				return wrapOp(map[string]interface{}{
					"proc":    "ok",
					"obj_id":  objID,
					"commits": map[string]interface{}{"version": float64(5)},
					"list": []interface{}{map[string]interface{}{
						"obj_type": float64(1),
						"obj_id":   "bbccddaabbccddaabbcc0001",
						"title":    "Start",
					}},
				})
			case obj == "snapshots" || obj == "snapshot" || obj == snapshotProbeUnknownObj:
				// This environment has no snapshot object: push proceeds with
				// a warning instead of being blocked (see snapshot_support.go).
				if typ == "create" {
					t.Error("snapshot must not be created on an environment without the object")
				}
				return wrapOp(map[string]interface{}{"proc": "error", "description": "bad object"})
			case obj == "commits" && typ == "list":
				return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{
					map[string]interface{}{"conv_id": objID, "version": float64(5)},
				}})
			case obj == "node" && typ == "create":
				results := make([]interface{}, len(body.Ops))
				for i, nodeOp := range body.Ops {
					localID, _ := nodeOp["id"].(string)
					results[i] = map[string]interface{}{"proc": "ok", "id": localID, "obj_id": localID}
				}
				return map[string]interface{}{"request_proc": "ok", "ops": results}
			}
			return okResponse(body.Ops)
		}())
	}))
	t.Cleanup(srv.Close)
	baseURL = srv.URL
	setProjectAuth(t, srv.URL)
	origAccount := accountURL
	accountURL = srv.URL
	t.Cleanup(func() { accountURL = origAccount })
	stageID = 900
	cachedProjectID = 800

	mirrored := filepath.Join(root, "42_billing")

	// 1. create-process pinned to Corezoid folder 42 with no local path: the
	//    mirrored directory is created here.
	res, isErr := handleToolCall(context.Background(), "create-process", map[string]interface{}{
		"folder_id":    float64(42),
		"process_name": "alpha",
	})
	if isErr {
		t.Fatalf("create alpha failed: %s", res)
	}
	if _, err := os.Stat(filepath.Join(mirrored, "501_alpha.conv.json")); err != nil {
		t.Fatalf("alpha must land in the mirrored folder: %v (result: %s)", err, res)
	}

	// The marker is the whole point: without it the directory just created is
	// a dead end for every folder-resolving operation.
	markerPath := filepath.Join(mirrored, "42_billing.folder.json")
	// Not fatal on purpose: step 2 below is the behaviour the marker exists for,
	// and seeing both failures at once says whether the marker is missing or
	// merely wrong.
	if markerRaw, err := os.ReadFile(markerPath); err != nil {
		t.Errorf("mirrored directory must carry its folder marker: %v", err)
	} else {
		var marker folderMarkerContent
		if err := json.Unmarshal(markerRaw, &marker); err != nil {
			t.Errorf("folder marker is not valid JSON: %v", err)
		} else if marker.ObjID != 42 || marker.ParentID != 900 || marker.Title != "billing" || marker.ObjType != 0 {
			t.Errorf("marker = %+v, want obj_id 42, parent 900, title billing, obj_type 0", marker)
		}
	}

	// 2. create-process from the directory that step 1 created, with no
	//    folder_id — the folder ID has to come from the marker.
	res, isErr = handleToolCall(context.Background(), "create-process", map[string]interface{}{
		"folder_path":  "42_billing",
		"process_name": "beta",
	})
	if isErr {
		t.Fatalf("create beta from the freshly created directory failed: %s", res)
	}
	if !strings.Contains(res, "42_billing.folder.json") {
		t.Errorf("beta's target must be resolved from the marker, got: %s", res)
	}
	if !strings.Contains(res, "folder #42") {
		t.Errorf("beta must be created in folder 42, got: %s", res)
	}
	if _, err := os.Stat(filepath.Join(mirrored, "502_beta.conv.json")); err != nil {
		t.Fatalf("beta must land in the same mirrored folder: %v (result: %s)", err, res)
	}

	// 3. pull both: placement must agree with create, i.e. no second copy.
	for _, id := range newConvIDs {
		res, isErr = handleToolCall(context.Background(), "pull-process", map[string]interface{}{
			"process_id": float64(id),
		})
		if isErr {
			t.Fatalf("pull %d failed: %s", id, res)
		}
	}
	for _, name := range []string{"501_alpha.conv.json", "502_beta.conv.json"} {
		if _, err := os.Stat(filepath.Join(mirrored, name)); err != nil {
			t.Errorf("pull must reuse create's placement for %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			t.Errorf("%s must not also exist at the workspace root", name)
		}
	}

	// 4. push both from the mirrored tree.
	for _, name := range []string{"501_alpha.conv.json", "502_beta.conv.json"} {
		res, isErr = handleToolCall(context.Background(), "push-process", map[string]interface{}{
			"process_path": filepath.Join("42_billing", name),
		})
		if isErr {
			t.Fatalf("push %s failed: %s", name, res)
		}
	}
}

// ---- ensureFolderMarkers ---------------------------------------------------

func TestEnsureFolderMarkers_WritesEveryLevel(t *testing.T) {
	root := t.TempDir()
	segments := []folderPathSegment{
		{ID: 10, Title: "billing ops", SafeName: "billing_ops", ParentID: 900},
		{ID: 11, Title: "v2", SafeName: "v2", ParentID: 10},
	}
	lvl1 := filepath.Join(root, "10_billing_ops")
	lvl2 := filepath.Join(lvl1, "11_v2")

	if err := ensureFolderMarkers(mirroredPlacement{Dir: lvl2, StageRoot: root, Segments: segments}); err != nil {
		t.Fatalf("ensureFolderMarkers: %v", err)
	}

	// Every intermediate level must be resolvable, not just the leaf: a user
	// running create-process from the middle of the tree has to work too.
	for dir, want := range map[string]folderMarkerContent{
		lvl1: {ObjID: 10, ParentID: 900, Title: "billing ops"},
		lvl2: {ObjID: 11, ParentID: 10, Title: "v2"},
	} {
		id, name, err := resolveFolderIDFromDir(dir)
		if err != nil {
			t.Fatalf("%s must resolve to a folder ID: %v", dir, err)
		}
		if id != want.ObjID {
			t.Errorf("%s resolved to folder %d, want %d", dir, id, want.ObjID)
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var got folderMarkerContent
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("marker %s is not valid JSON: %v", name, err)
		}
		if got != want {
			t.Errorf("marker %s = %+v, want %+v", name, got, want)
		}
	}
}

// A marker that came from the server carries a real description this code
// cannot reconstruct, so an existing marker is never rewritten.
func TestEnsureFolderMarkers_KeepsExistingMarker(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "10_billing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pulled := filepath.Join(dir, "10_billing.folder.json")
	body := []byte(`{"description":"pulled from the server","obj_id":10,"obj_type":0,"parent_id":900,"title":"billing"}`)
	if err := os.WriteFile(pulled, body, 0644); err != nil {
		t.Fatal(err)
	}

	segments := []folderPathSegment{{ID: 10, Title: "billing", SafeName: "billing", ParentID: 900}}
	if err := ensureFolderMarkers(mirroredPlacement{Dir: dir, StageRoot: root, Segments: segments}); err != nil {
		t.Fatalf("ensureFolderMarkers: %v", err)
	}

	got, err := os.ReadFile(pulled)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("existing marker was rewritten:\n got %s\nwant %s", got, body)
	}
	// A second marker would make the directory ambiguous and break resolution
	// just as thoroughly as having none (see resolveFolderIDFromDir).
	if _, _, err := resolveFolderIDFromDir(dir); err != nil {
		t.Errorf("directory must still resolve to a single folder: %v", err)
	}
}

func TestEnsureFolderMarkers_RejectsStaleMarker(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "10_billing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "37_old.folder.json")
	if err := os.WriteFile(stale, []byte(`{"obj_id":37,"obj_type":0,"parent_id":900,"title":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ensureFolderMarkers(mirroredPlacement{
		Dir: dir, StageRoot: root,
		Segments: []folderPathSegment{{ID: 10, Title: "billing", SafeName: "billing", ParentID: 900}},
	})
	if err == nil || !strings.Contains(err.Error(), "expected folder 10") {
		t.Fatalf("expected stale marker error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "10_billing.folder.json")); !os.IsNotExist(err) {
		t.Fatalf("must not create a second marker after stale marker error, stat err=%v", err)
	}
}

// Only a marker's NAME decides which folder a directory resolves to
// (resolveFolderIDFromDir never parses the body). Markers inside a pulled
// workspace come out of a server ZIP export, so an unfamiliar body must not
// stop mirroring — the correctly-named marker is kept and the directory still
// resolves. Bodies the exporter may legitimately emit are covered here: an
// empty object, a parent_id we cannot corroborate, and outright corruption.
func TestEnsureFolderMarkers_KeepsCorrectlyNamedMarkerWithUnexpectedBody(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":       `{not-json`,
		"empty-object":    `{}`,
		"no-parent-id":    `{"obj_id":10,"obj_type":0,"title":"billing"}`,
		"other-parent":    `{"obj_id":10,"obj_type":0,"parent_id":37,"title":"billing"}`,
		"obj-id-mismatch": `{"obj_id":77,"obj_type":0,"parent_id":900,"title":"billing"}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "10_billing")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "10_billing.folder.json")
			if err := os.WriteFile(marker, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ensureFolderMarkers(mirroredPlacement{
				Dir: dir, StageRoot: root,
				Segments: []folderPathSegment{{ID: 10, Title: "billing", SafeName: "billing", ParentID: 900}},
			}); err != nil {
				t.Fatalf("an unexpected marker body must not fail mirroring: %v", err)
			}
			got, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != body {
				t.Errorf("marker was rewritten:\n got %s\nwant %s", got, body)
			}
			id, _, err := resolveFolderIDFromDir(dir)
			if err != nil || id != 10 {
				t.Errorf("resolveFolderIDFromDir = (%d, %v), want (10, nil)", id, err)
			}
		})
	}
}

// A marker whose NAME points at another folder is the case that actually
// misroutes a later create/push, so it stays fatal.
func TestEnsureFolderMarkers_RejectsAmbiguousMarkerSet(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "10_billing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"10_billing.folder.json", "37_old.folder.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := ensureFolderMarkers(mirroredPlacement{
		Dir: dir, StageRoot: root,
		Segments: []folderPathSegment{{ID: 10, Title: "billing", SafeName: "billing", ParentID: 900}},
	})
	if err == nil || !strings.Contains(err.Error(), "markers") {
		t.Fatalf("expected ambiguous marker set to be rejected, got %v", err)
	}
}

// Nothing to anchor at (no local stage root) and nothing to create (the target
// IS the stage root) must both be no-ops rather than writes into the void.
func TestEnsureFolderMarkers_NoopCases(t *testing.T) {
	if err := ensureFolderMarkers(mirroredPlacement{}); err != nil {
		t.Errorf("empty placement must be a no-op, got %v", err)
	}
	root := t.TempDir()
	if err := ensureFolderMarkers(mirroredPlacement{Dir: root, StageRoot: root}); err != nil {
		t.Errorf("stage-root target must be a no-op, got %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("nothing must be written for a stage-root target, got %v", entries)
	}
}
