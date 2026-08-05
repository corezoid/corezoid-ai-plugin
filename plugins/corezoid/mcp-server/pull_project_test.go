package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---- fixMojibake -----------------------------------------------------------

func TestFixMojibake_Cyrillic(t *testing.T) {
	// "погода" double-encoded as Latin-1 bytes re-read as UTF-8
	mojibake := "Ð¿Ð¾Ð³Ð¾Ð´Ð°"
	got := fixMojibake(mojibake)
	if got != "погода" {
		t.Errorf("fixMojibake(%q) = %q, want \"погода\"", mojibake, got)
	}
}

func TestFixMojibake_ASCII(t *testing.T) {
	s := "hello-world"
	if got := fixMojibake(s); got != s {
		t.Errorf("fixMojibake(%q) = %q, want unchanged", s, got)
	}
}

func TestFixMojibake_NonLatin1(t *testing.T) {
	// Contains a rune > 0xFF — should be returned unchanged.
	s := "日本語"
	if got := fixMojibake(s); got != s {
		t.Errorf("fixMojibake(%q) should be unchanged for non-Latin-1 runes", s)
	}
}

func TestFixMojibake_AlreadyUTF8(t *testing.T) {
	// Valid UTF-8 that encodes identically — unchanged.
	s := "simple"
	if got := fixMojibake(s); got != s {
		t.Errorf("fixMojibake(%q) should be unchanged", s)
	}
}

// ---- unzipFile -------------------------------------------------------------

func makeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content)) //nolint:errcheck
	}
	w.Close()
	f.Close()
	return zipPath
}

func TestUnzipFile_Basic(t *testing.T) {
	zipPath := makeZip(t, map[string]string{
		"hello.txt":    "world",
		"sub/deep.txt": "content",
	})
	dest := t.TempDir()
	if err := unzipFile(zipPath, dest); err != nil {
		t.Fatalf("unzipFile error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	if err != nil || string(data) != "world" {
		t.Errorf("hello.txt: got %q, err %v", data, err)
	}
	data2, err := os.ReadFile(filepath.Join(dest, "sub", "deep.txt"))
	if err != nil || string(data2) != "content" {
		t.Errorf("sub/deep.txt: got %q, err %v", data2, err)
	}
}

func TestUnzipFile_ZipSlip(t *testing.T) {
	// Manually craft a zip with a path traversal entry.
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "evil.zip")
	f, _ := os.Create(zipPath)
	w := zip.NewWriter(f)
	fw, _ := w.Create("../escape.txt")
	fw.Write([]byte("evil")) //nolint:errcheck
	w.Close()
	f.Close()

	dest := t.TempDir()
	err := unzipFile(zipPath, dest)
	if err == nil {
		t.Error("expected zip-slip error, got nil")
	}
}

func TestUnzipFile_NotFound(t *testing.T) {
	err := unzipFile("/nonexistent.zip", t.TempDir())
	if err == nil {
		t.Error("expected error for missing zip, got nil")
	}
}

// ---- findStageDir / walkDepth ----------------------------------------------

func TestFindStageDir_Found(t *testing.T) {
	root := t.TempDir()
	stageDir := filepath.Join(root, "12345_myproject.stage")
	os.MkdirAll(stageDir, 0755) //nolint:errcheck

	got, err := findStageDir(root, 2)
	if err != nil {
		t.Fatalf("findStageDir error: %v", err)
	}
	if got != stageDir {
		t.Errorf("got %q, want %q", got, stageDir)
	}
}

func TestFindStageDir_Nested(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "outer", "99_inner.stage")
	os.MkdirAll(nested, 0755) //nolint:errcheck

	got, err := findStageDir(root, 3)
	if err != nil {
		t.Fatalf("findStageDir error: %v", err)
	}
	if got != nested {
		t.Errorf("got %q, want %q", got, nested)
	}
}

func TestFindStageDir_TooDeep(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "42_deep.stage")
	os.MkdirAll(deep, 0755) //nolint:errcheck

	got, err := findStageDir(root, 1) // max depth 1 — won't reach depth 3
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty result for too-deep dir, got %q", got)
	}
}

func TestFindStageDir_NotFound(t *testing.T) {
	root := t.TempDir()
	got, err := findStageDir(root, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestFindStageDir_SkipsHiddenDirs(t *testing.T) {
	// Simulates $HOME layout: a .Trash sibling next to the real stage dir.
	// findStageDir must skip the hidden dir and still find the stage.
	root := t.TempDir()
	hiddenDir := filepath.Join(root, ".Trash")
	if err := os.MkdirAll(hiddenDir, 0755); err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(root, "12345_myproject.stage")
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := findStageDir(root, 2)
	if err != nil {
		t.Fatalf("findStageDir returned unexpected error: %v", err)
	}
	if got != stageDir {
		t.Errorf("got %q, want %q", got, stageDir)
	}
}

func TestWalkDepth_SkipsPermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — chmod 000 has no effect")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0755) }) //nolint:errcheck

	// walkDepth must not return an error when it encounters a permission-denied dir.
	var visited []string
	err := walkDepth(root, 0, 2, func(path string, d os.DirEntry) bool {
		visited = append(visited, path)
		return false
	})
	if err != nil {
		t.Fatalf("walkDepth returned unexpected error: %v", err)
	}
	// "locked" itself should appear (we see it via the parent's ReadDir)
	// but no error should propagate from trying to descend into it.
}

// ---- hoistZipWrapperDirs ---------------------------------------------------

// A fresh Corezoid export always lands as filePath/stage_<id>_<ts>.zip/<id>_<name>.stage/…
// (the ".zip" here is a directory suffix, not a file). If a stale <id>_<name>.stage
// from an earlier pull already exists at root, findStageDir/hoist below prefer the
// stale one and leave the wrapper behind — so re-pulls quietly grow a pile of
// stage_*.zip wrapper directories at root. hoistZipWrapperDirs must unwrap it and
// overwrite the stale stage with the fresh content.
func TestHoistZipWrapperDirs_ReplacesStaleStage(t *testing.T) {
	root := t.TempDir()

	staleStage := filepath.Join(root, "42_demo.stage")
	if err := os.MkdirAll(staleStage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleStage, "old.marker"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	wrapper := filepath.Join(root, "stage_42_1700000000000.zip")
	freshStage := filepath.Join(wrapper, "42_demo.stage")
	if err := os.MkdirAll(freshStage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(freshStage, "new.marker"), []byte("fresh"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := hoistZipWrapperDirs(root); err != nil {
		t.Fatalf("hoistZipWrapperDirs error: %v", err)
	}

	if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
		t.Errorf("wrapper %s should be gone, stat err=%v", wrapper, err)
	}
	if _, err := os.Stat(filepath.Join(root, "42_demo.stage", "new.marker")); err != nil {
		t.Errorf("fresh marker should have replaced the stale stage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "42_demo.stage", "old.marker")); !os.IsNotExist(err) {
		t.Errorf("stale marker should have been overwritten, stat err=%v", err)
	}
}

// TestHoistZipWrapperDirs_UnwrapsWorkspaceRootWrappers covers the wrappers
// emitted for a No-Project (workspace-root) pull: one per top-level object,
// named after its obj_type — folder_/conv_/dashboard_. All three must be
// hoisted so their contents land directly under root, or the user ends up
// with an extra layer per object.
func TestHoistZipWrapperDirs_UnwrapsWorkspaceRootWrappers(t *testing.T) {
	root := t.TempDir()

	// folder wrapper contains an inner "<id>_<name>" dir with a .folder.json inside.
	folderInner := filepath.Join(root, "folder_687287_1785852544313.zip", "687287_et")
	if err := os.MkdirAll(folderInner, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folderInner, "687287_et.folder.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// conv wrapper contains a single .conv.json at its root.
	convDir := filepath.Join(root, "conv_1571296_1785852544373.zip")
	if err := os.MkdirAll(convDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "1571296_Graph_Maker.conv.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// dashboard wrapper.
	dashDir := filepath.Join(root, "dashboard_136538_1785852544843.zip")
	if err := os.MkdirAll(dashDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dashDir, "136538_Test.dashboard.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := hoistZipWrapperDirs(root); err != nil {
		t.Fatalf("hoistZipWrapperDirs error: %v", err)
	}

	// All three wrappers should be gone; their content should be at root.
	for _, want := range []string{
		"687287_et/687287_et.folder.json",
		"1571296_Graph_Maker.conv.json",
		"136538_Test.dashboard.json",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("expected %s at root: %v", want, err)
		}
	}
	for _, wrapper := range []string{
		"folder_687287_1785852544313.zip",
		"conv_1571296_1785852544373.zip",
		"dashboard_136538_1785852544843.zip",
	} {
		if _, err := os.Stat(filepath.Join(root, wrapper)); !os.IsNotExist(err) {
			t.Errorf("wrapper %s should be gone, stat err=%v", wrapper, err)
		}
	}
}

func TestHoistZipWrapperDirs_NoWrapperNoop(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "7_x.stage")
	if err := os.MkdirAll(stage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := hoistZipWrapperDirs(root); err != nil {
		t.Fatalf("hoistZipWrapperDirs error: %v", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Errorf("stage should be untouched, got: %v", err)
	}
}

// ---- moveContents ----------------------------------------------------------

func TestMoveContents(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("aa"), 0644) //nolint:errcheck
	os.WriteFile(filepath.Join(src, "b.txt"), []byte("bb"), 0644) //nolint:errcheck

	if err := moveContents(src, dst); err != nil {
		t.Fatalf("moveContents error: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("expected %s in dst, got error: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(src, name)); err == nil {
			t.Errorf("expected %s to be gone from src", name)
		}
	}
}

// ---- formatJSON ------------------------------------------------------------

func TestFormatJSON_RemovesUUID(t *testing.T) {
	dir := t.TempDir()
	data := map[string]interface{}{
		"uuid":  "should-be-removed",
		"title": "test",
		"scheme": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{
					"id":   "aabbcc",
					"uuid": "node-uuid-gone",
					"name": "start",
				},
			},
		},
	}
	raw, _ := json.Marshal(data)
	os.WriteFile(filepath.Join(dir, "process.json"), raw, 0644) //nolint:errcheck

	if err := formatJSON(dir); err != nil {
		t.Fatalf("formatJSON error: %v", err)
	}

	result, _ := os.ReadFile(filepath.Join(dir, "process.json"))
	var out map[string]interface{}
	json.Unmarshal(result, &out) //nolint:errcheck
	if _, ok := out["uuid"]; ok {
		t.Error("expected top-level uuid to be removed")
	}
	nodes := out["scheme"].(map[string]interface{})["nodes"].([]interface{})
	if _, ok := nodes[0].(map[string]interface{})["uuid"]; ok {
		t.Error("expected uuid in node to be removed")
	}
}

func TestFormatJSON_NonJSONSkipped(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("text file"), 0644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "valid.json"), []byte(`{"key":"val"}`), 0644) //nolint:errcheck

	if err := formatJSON(dir); err != nil {
		t.Fatalf("formatJSON error: %v", err)
	}
}

// ---- renameFiles2Folders ---------------------------------------------------

func TestRenameFiles2Folders_RenamesFolderDir(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "123_myproc.folder")
	newDir := filepath.Join(root, "123_myproc")
	os.MkdirAll(oldDir, 0755) //nolint:errcheck

	if err := renameFiles2Folders(root); err != nil {
		t.Fatalf("renameFiles2Folders error: %v", err)
	}

	if _, err := os.Stat(newDir); err != nil {
		t.Errorf("expected renamed dir %q to exist: %v", newDir, err)
	}
	if _, err := os.Stat(oldDir); err == nil {
		t.Errorf("expected old dir %q to be gone", oldDir)
	}
}

// TestRenameFiles2Folders_StripsAllSuffixes verifies pull-folder's layout
// invariant: after flattening, every top-level Corezoid export suffix
// (".stage", ".folder", ".project") is stripped from directory names —
// nested subfolders that survive at the workspace root should carry the
// plain "<id>_<name>" form.
func TestRenameFiles2Folders_StripsAllSuffixes(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		"671255_develop.stage":   "671255_develop",
		"555_orders.folder":      "555_orders",
		"999_billing.project":    "999_billing",
		"777_plain":              "777_plain",
	}
	for src := range cases {
		if err := os.MkdirAll(filepath.Join(root, src), 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := renameFiles2Folders(root); err != nil {
		t.Fatalf("renameFiles2Folders error: %v", err)
	}

	for src, want := range cases {
		if src != want {
			if _, err := os.Stat(filepath.Join(root, src)); err == nil {
				t.Errorf("original %q must be renamed", src)
			}
		}
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("expected %q after rename: %v", want, err)
		}
	}
}
