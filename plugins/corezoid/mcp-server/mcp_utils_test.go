package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- intArg ----------------------------------------------------------------

func TestIntArg_Float64(t *testing.T) {
	args := map[string]interface{}{"n": float64(42)}
	v, err := intArg(args, "n")
	if err != nil || v != 42 {
		t.Errorf("got (%d, %v), want (42, nil)", v, err)
	}
}

func TestIntArg_Int(t *testing.T) {
	args := map[string]interface{}{"n": 7}
	v, err := intArg(args, "n")
	if err != nil || v != 7 {
		t.Errorf("got (%d, %v), want (7, nil)", v, err)
	}
}

func TestIntArg_StringNumeric(t *testing.T) {
	args := map[string]interface{}{"n": "99"}
	v, err := intArg(args, "n")
	if err != nil || v != 99 {
		t.Errorf("got (%d, %v), want (99, nil)", v, err)
	}
}

func TestIntArg_StringInvalid(t *testing.T) {
	args := map[string]interface{}{"n": "abc"}
	_, err := intArg(args, "n")
	if err == nil {
		t.Error("expected error for non-numeric string, got nil")
	}
}

func TestIntArg_Missing(t *testing.T) {
	_, err := intArg(map[string]interface{}{}, "n")
	if err == nil {
		t.Error("expected error for missing key, got nil")
	}
}

func TestIntArg_WrongType(t *testing.T) {
	args := map[string]interface{}{"n": []int{1, 2}}
	_, err := intArg(args, "n")
	if err == nil {
		t.Error("expected error for unexpected type, got nil")
	}
}

// ---- strArg ----------------------------------------------------------------

func TestStrArg_OK(t *testing.T) {
	args := map[string]interface{}{"k": "hello"}
	v, err := strArg(args, "k")
	if err != nil || v != "hello" {
		t.Errorf("got (%q, %v), want (\"hello\", nil)", v, err)
	}
}

func TestStrArg_Missing(t *testing.T) {
	_, err := strArg(map[string]interface{}{}, "k")
	if err == nil {
		t.Error("expected error for missing key, got nil")
	}
}

func TestStrArg_WrongType(t *testing.T) {
	args := map[string]interface{}{"k": 42}
	_, err := strArg(args, "k")
	if err == nil {
		t.Error("expected error for non-string value, got nil")
	}
}

// ---- optStrArg -------------------------------------------------------------

func TestOptStrArg_Present(t *testing.T) {
	args := map[string]interface{}{"k": "val"}
	if got := optStrArg(args, "k"); got != "val" {
		t.Errorf("got %q, want %q", got, "val")
	}
}

func TestOptStrArg_Absent(t *testing.T) {
	if got := optStrArg(map[string]interface{}{}, "k"); got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestOptStrArg_WrongType(t *testing.T) {
	args := map[string]interface{}{"k": 123}
	if got := optStrArg(args, "k"); got != "" {
		t.Errorf("got %q, want empty string for non-string value", got)
	}
}

// ---- resolveDirPath --------------------------------------------------------

func TestResolveDirPath_Empty(t *testing.T) {
	if got := resolveDirPath(map[string]interface{}{}, "path"); got != "." {
		t.Errorf("got %q, want \".\"", got)
	}
}

func TestResolveDirPath_Provided(t *testing.T) {
	// chdir into a tempdir so a relative path is unambiguous and inside cwd.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	args := map[string]interface{}{"path": "sub/foo"}
	if got := resolveDirPath(args, "path"); got != "sub/foo" {
		t.Errorf("got %q, want %q", got, "sub/foo")
	}
}

func TestResolveDirPath_RejectsEscape(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	// "../" escape must fall back to "." (and log a warning).
	args := map[string]interface{}{"path": "../../etc"}
	if got := resolveDirPath(args, "path"); got != "." {
		t.Errorf("expected escape to fall back to \".\", got %q", got)
	}
}

// ---- resolveFolderIDFromDir ------------------------------------------------

func TestResolveFolderIDFromDir_Found(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "12345_my-stage.stage.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := resolveFolderIDFromDir(dir)
	if err != nil || id != 12345 {
		t.Errorf("got (%d, %v), want (12345, nil)", id, err)
	}
}

func TestResolveFolderIDFromDir_FolderJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "999_my-folder.folder.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := resolveFolderIDFromDir(dir)
	if err != nil || id != 999 {
		t.Errorf("got (%d, %v), want (999, nil)", id, err)
	}
}

func TestResolveFolderIDFromDir_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveFolderIDFromDir(dir)
	if err == nil {
		t.Error("expected error when no folder/stage json present, got nil")
	}
}

func TestResolveFolderIDFromDir_BadDir(t *testing.T) {
	_, err := resolveFolderIDFromDir("/nonexistent_dir_xyz_abc")
	if err == nil {
		t.Error("expected error for non-existent directory, got nil")
	}
}

// ---- resolveProcessPath ----------------------------------------------------

func TestResolveProcessPath_ExplicitArg(t *testing.T) {
	// chdir into a tempdir so the relative path is provably inside cwd.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	args := map[string]interface{}{"process_path": "sub/path.conv.json"}
	p, err := resolveProcessPath(args, "process_path")
	if err != nil || p != "sub/path.conv.json" {
		t.Errorf("got (%q, %v)", p, err)
	}
}

func TestResolveProcessPath_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	// "../" escape must produce a typed error, not silently read the file.
	args := map[string]interface{}{"process_path": "../../etc/passwd"}
	_, err := resolveProcessPath(args, "process_path")
	if err == nil {
		t.Error("expected error for path-traversal arg, got nil")
	}
}

func TestResolveProcessPath_RejectsAbsoluteEscape(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	args := map[string]interface{}{"process_path": "/etc/passwd"}
	_, err := resolveProcessPath(args, "process_path")
	if err == nil {
		t.Error("expected error for absolute path outside cwd, got nil")
	}
}

func TestConfineToWorkdir_Allows(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	cases := []string{"", "foo.json", "sub/bar.conv.json", "./qux"}
	for _, c := range cases {
		if _, err := confineToWorkdir(c); err != nil {
			t.Errorf("confineToWorkdir(%q) = err %v, want nil", c, err)
		}
	}
}

func TestConfineToWorkdir_AllowsAbsoluteInsideCwd(t *testing.T) {
	// Absolute paths pointing inside cwd are accepted and rewritten to the
	// relative form. On macOS t.TempDir() lives under /var/folders/... which
	// is a symlink to /private/var/folders/... — exactly the case that made a
	// lexical comparison unsound — so this test exercises the EvalSymlinks
	// resolution for real.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(dir, "ok.conv.json"):          "ok.conv.json",
		filepath.Join(dir, "sub", "in.conv.json"):   filepath.Join("sub", "in.conv.json"),
	}
	for abs, want := range cases {
		got, err := confineToWorkdir(abs)
		if err != nil {
			t.Errorf("confineToWorkdir(%q) = err %v, want nil", abs, err)
			continue
		}
		if got != want {
			t.Errorf("confineToWorkdir(%q) = %q, want %q", abs, got, want)
		}
	}
}

func TestConfineToWorkdir_RejectsAbsoluteOutsideCwd(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	outside := t.TempDir() // sibling temp dir — exists, but not under cwd
	for _, abs := range []string{filepath.Join(outside, "x.conv.json"), "/etc/passwd"} {
		if _, err := confineToWorkdir(abs); err == nil {
			t.Errorf("confineToWorkdir(%q) = nil, want error", abs)
		}
	}
}

func TestConfineToWorkdir_Rejects(t *testing.T) {
	cases := []string{"../escape", "../../etc/passwd", "/etc/passwd", "/var/log/system.log", ".."}
	for _, c := range cases {
		if _, err := confineToWorkdir(c); err == nil {
			t.Errorf("confineToWorkdir(%q) = nil, want error", c)
		}
	}
}

func TestResolveProcessPath_AutoDiscoverSingle(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	os.WriteFile(filepath.Join(dir, "123_proc.conv.json"), []byte("{}"), 0644) //nolint:errcheck
	p, err := resolveProcessPath(map[string]interface{}{}, "process_path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != "123_proc.conv.json" {
		t.Errorf("got %q, want %q", p, "123_proc.conv.json")
	}
}

func TestResolveProcessPath_AutoDiscoverMultiple(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	os.WriteFile(filepath.Join(dir, "1_a.conv.json"), []byte("{}"), 0644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "2_b.conv.json"), []byte("{}"), 0644) //nolint:errcheck
	_, err := resolveProcessPath(map[string]interface{}{}, "process_path")
	if err == nil {
		t.Error("expected error for multiple .conv.json files, got nil")
	}
}

func TestResolveProcessPath_AutoDiscoverNone(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	_, err := resolveProcessPath(map[string]interface{}{}, "process_path")
	if err == nil {
		t.Error("expected error when no .conv.json present, got nil")
	}
}
