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
	if id := e.CreateEmptyConv(685228, "t", "", "process"); id != 777 {
		t.Fatalf("CreateEmptyConv returned %d, want 777", id)
	}
	if got, ok := gotFolderID.(float64); !ok || int(got) != 685228 {
		t.Errorf("folder_id on the wire = %v, want 685228", gotFolderID)
	}
}
