package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- ambiguous local markers --------------------------------------------------

func TestResolveFolderIDFromDir_AmbiguousMarkersRejected(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"681527_production.stage.json", "681528_develop.stage.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := resolveFolderIDFromDir(dir)
	if err == nil {
		t.Fatal("two markers in one directory must be an error, not a silent first pick")
	}
	if !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "folder_id") {
		t.Errorf("error must say the target is ambiguous and suggest folder_id, got: %v", err)
	}
}

// ---- explicit folder_id -------------------------------------------------------

func TestResolveCreateTarget_ExplicitFolderIDWins(t *testing.T) {
	dir := t.TempDir() // no marker files at all — explicit id must not need them
	id, how, err := resolveCreateTarget(map[string]interface{}{"folder_id": float64(685228)}, dir)
	if err != nil || id != 685228 {
		t.Fatalf("got (%d, %v), want (685228, nil)", id, err)
	}
	if !strings.Contains(how, "explicit") {
		t.Errorf("resolution description must say the id was explicit, got: %q", how)
	}
}

func TestResolveCreateTarget_ReportsMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "42_dev.folder.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	id, how, err := resolveCreateTarget(map[string]interface{}{}, dir)
	if err != nil || id != 42 {
		t.Fatalf("got (%d, %v), want (42, nil)", id, err)
	}
	if !strings.Contains(how, "42_dev.folder.json") {
		t.Errorf("resolution description must name the marker file, got: %q", how)
	}
}

// ---- folder_id reaches the wire ------------------------------------------------

func TestCreateEmptyConv_SendsGivenFolderID(t *testing.T) {
	var gotFolderID interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		gotFolderID = ops[0]["folder_id"]
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok", "obj_id": float64(777)}},
		}
	})
	if id, err := e.CreateEmptyConv(685228, "t", "", "process"); id != 777 || err != nil {
		t.Fatalf("CreateEmptyConv returned (%d, %v), want (777, nil)", id, err)
	}
	if got, ok := gotFolderID.(float64); !ok || int(got) != 685228 {
		t.Errorf("folder_id on the wire = %v, want 685228", gotFolderID)
	}
}

// ---- the server's reason reaches the tool result -------------------------------

// The server explains WHY a create failed ("Stage is immutable", access
// denied, ...). Burying that in mcp.log while the tool said only "failed to
// create" cost real field-debugging time — the reason must be in the result.
func TestCreateEmptyConv_SurfacesServerReason(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "error", "description": "Stage is immutable"}}}
	})
	id, err := e.CreateEmptyConv(685227, "t", "", "process")
	if id != 0 || err == nil {
		t.Fatalf("expected (0, err), got (%d, %v)", id, err)
	}
	if !strings.Contains(err.Error(), "Stage is immutable") {
		t.Errorf("error must carry the server's reason, got: %v", err)
	}
}

// ---- CLI boolean coercion -------------------------------------------------------

// CLI args arrive as strings, but handlers type-assert booleans — before the
// coercion, `deploy-stage apply=true` silently ran as a dry-run.
func TestCoerceCLIArgs_Booleans(t *testing.T) {
	args := map[string]interface{}{
		"apply":   "true",  // boolean in deploy-stage's schema
		"confirm": "true",  // string in the schema — must stay a string
		"company_id": "c1", // untouched
	}
	if err := coerceCLIArgs("deploy-stage", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["apply"] != true {
		t.Errorf(`apply = %v (%T), want true (bool)`, args["apply"], args["apply"])
	}
	if args["confirm"] != "true" {
		t.Errorf(`confirm = %v, must stay the string "true"`, args["confirm"])
	}
	if args["company_id"] != "c1" {
		t.Errorf("company_id changed: %v", args["company_id"])
	}
}

// Unparseable boolean strings must fail loudly — a boolean the handler cannot
// read is exactly how `apply=True` degraded to a silent dry-run.
func TestCoerceCLIArgs_CaseAndErrors(t *testing.T) {
	args := map[string]interface{}{"apply": "True"}
	if err := coerceCLIArgs("deploy-stage", args); err != nil || args["apply"] != true {
		t.Fatalf("mixed-case True must coerce, got err=%v apply=%v", err, args["apply"])
	}
	if err := coerceCLIArgs("deploy-stage", map[string]interface{}{"apply": "yep"}); err == nil {
		t.Fatal("unparseable boolean must be an error, not a silent string")
	}
}
