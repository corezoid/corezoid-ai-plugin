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
