package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
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
	id, marker, err := resolveFolderIDFromDir(dir)
	if err != nil || id != 12345 {
		t.Errorf("got (%d, %v), want (12345, nil)", id, err)
	}
	if marker != "12345_my-stage.stage.json" {
		t.Errorf("marker = %q, want the matched file name", marker)
	}
}

func TestResolveFolderIDFromDir_FolderJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "999_my-folder.folder.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	id, marker, err := resolveFolderIDFromDir(dir)
	if err != nil || id != 999 {
		t.Errorf("got (%d, %v), want (999, nil)", id, err)
	}
	if marker != "999_my-folder.folder.json" {
		t.Errorf("marker = %q, want the matched file name", marker)
	}
}

func TestResolveFolderIDFromDir_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveFolderIDFromDir(dir)
	if err == nil {
		t.Error("expected error when no folder/stage json present, got nil")
	}
}

func TestResolveFolderIDFromDir_BadDir(t *testing.T) {
	_, _, err := resolveFolderIDFromDir("/nonexistent_dir_xyz_abc")
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
		filepath.Join(dir, "ok.conv.json"):        "ok.conv.json",
		filepath.Join(dir, "sub", "in.conv.json"): filepath.Join("sub", "in.conv.json"),
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

// Regression: create-process used to write "<id>_<name>.json", which
// auto-discovery silently skipped — every later push/pull/lint call in the same
// directory failed with "no .conv.json file found" unless process_path was
// passed explicitly.
func TestResolveProcessPath_FindsFileNamedByConvFileName(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	name := convFileName(12345, "My Process")
	os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0644) //nolint:errcheck
	p, err := resolveProcessPath(map[string]interface{}{}, "process_path")
	if err != nil {
		t.Fatalf("file created as %q was not auto-discovered: %v", name, err)
	}
	if p != name {
		t.Errorf("got %q, want %q", p, name)
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

// ---- resolveProcessID -------------------------------------------------------

// process_id must resolve the process without touching the filesystem at
// all — this is the path hosts with no local process repository (e.g. the
// Simulator.Company AI console) rely on to call run-task / snapshot tools
// without a preceding pull-process.
func TestResolveProcessID_ProcessIDSkipsFileResolution(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Deliberately no .conv.json anywhere in cwd.

	id, filePath, errMsg := resolveProcessID(map[string]interface{}{"process_id": float64(456)}, "process_path", "process_id")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if id != 456 {
		t.Errorf("got id=%d, want 456", id)
	}
	if filePath != "" {
		t.Errorf("expected empty filePath for process_id resolution, got %q", filePath)
	}
}

// Supplying both must be rejected, not silently resolved by picking one:
// several callers of resolveProcessID are destructive (e.g. delete-snapshot),
// so a caller pointing at two different processes by mistake needs to be
// told, not have one argument quietly ignored.
func TestResolveProcessID_BothArgsGivenIsRejected(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "111_proc.conv.json"), []byte("{}"), 0644) //nolint:errcheck
	_, _, errMsg := resolveProcessID(map[string]interface{}{
		"process_path": "111_proc.conv.json",
		"process_id":   float64(999),
	}, "process_path", "process_id")
	if errMsg == "" {
		t.Fatal("expected an error when both process_path and process_id are given")
	}
	if !strings.Contains(errMsg, "process_path") || !strings.Contains(errMsg, "process_id") {
		t.Errorf("expected error to name both process_path and process_id, got: %s", errMsg)
	}
}

// Some MCP hosts serialize a declared-but-unset optional field as "" or null
// rather than omitting the key. A valid process_id must not be rejected as
// "both given" just because such a client also sent an empty/null
// process_path — optStrArg already treats "" and null as absent everywhere
// else process_path is read, and the conflict check has to agree.
func TestResolveProcessID_EmptyOrNullProcessPathIsNotAConflict(t *testing.T) {
	for name, pathVal := range map[string]interface{}{"empty string": "", "null": nil} {
		t.Run(name, func(t *testing.T) {
			id, filePath, errMsg := resolveProcessID(map[string]interface{}{
				"process_path": pathVal,
				"process_id":   float64(456),
			}, "process_path", "process_id")
			if errMsg != "" {
				t.Fatalf("unexpected error: %s", errMsg)
			}
			if id != 456 {
				t.Errorf("got id=%d, want 456", id)
			}
			if filePath != "" {
				t.Errorf("expected empty filePath, got %q", filePath)
			}
		})
	}
}

// Symmetric case: a null process_id must not be treated as "given" either —
// it falls back to process_path like an absent key would.
func TestResolveProcessID_NullProcessIDFallsBackToProcessPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "333_proc.conv.json"), []byte("{}"), 0644) //nolint:errcheck
	id, filePath, errMsg := resolveProcessID(map[string]interface{}{
		"process_path": "333_proc.conv.json",
		"process_id":   nil,
	}, "process_path", "process_id")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if id != 333 {
		t.Errorf("got id=%d, want 333", id)
	}
	if filePath != "333_proc.conv.json" {
		t.Errorf("got filePath=%q, want 333_proc.conv.json", filePath)
	}
}

func TestResolveProcessID_ZeroIsRejected(t *testing.T) {
	_, _, errMsg := resolveProcessID(map[string]interface{}{"process_id": float64(0)}, "process_path", "process_id")
	if errMsg == "" {
		t.Fatal("expected an error for process_id=0")
	}
	if !strings.Contains(errMsg, "greater than zero") {
		t.Errorf("expected a greater-than-zero message, got: %s", errMsg)
	}
}

func TestResolveProcessID_NegativeIsRejected(t *testing.T) {
	_, _, errMsg := resolveProcessID(map[string]interface{}{"process_id": float64(-5)}, "process_path", "process_id")
	if errMsg == "" {
		t.Fatal("expected an error for a negative process_id")
	}
	if !strings.Contains(errMsg, "greater than zero") {
		t.Errorf("expected a greater-than-zero message, got: %s", errMsg)
	}
}

// intArg truncates a fractional float64 (123.9 -> 123), which is the right
// call for arguments genuinely typed as integers upstream. process_id has no
// legitimate fractional input, though, so silently truncating it would hide
// a caller bug (e.g. a miscomputed ID) behind a wrong-but-plausible process.
func TestResolveProcessID_FractionalIsRejected(t *testing.T) {
	_, _, errMsg := resolveProcessID(map[string]interface{}{"process_id": float64(123.9)}, "process_path", "process_id")
	if errMsg == "" {
		t.Fatal("expected an error for a fractional process_id")
	}
	if !strings.Contains(errMsg, "whole number") {
		t.Errorf("expected a whole-number message, got: %s", errMsg)
	}
}

func TestResolveProcessID_FallsBackToProcessPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "222_proc.conv.json"), []byte("{}"), 0644) //nolint:errcheck
	id, filePath, errMsg := resolveProcessID(map[string]interface{}{
		"process_path": "222_proc.conv.json",
	}, "process_path", "process_id")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if id != 222 {
		t.Errorf("got id=%d, want 222", id)
	}
	if filePath != "222_proc.conv.json" {
		t.Errorf("got filePath=%q, want 222_proc.conv.json", filePath)
	}
}

// Neither argument given must produce one error naming both accepted options,
// not the file-only message resolveProcessPath returns on its own.
func TestResolveProcessID_NeitherArgGivenMentionsBothOptions(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// No .conv.json present, so process_path auto-discovery also fails.

	_, _, errMsg := resolveProcessID(map[string]interface{}{}, "process_path", "process_id")
	if errMsg == "" {
		t.Fatal("expected an error when neither process_path nor process_id is given")
	}
	if !strings.Contains(errMsg, "process_path") || !strings.Contains(errMsg, "process_id") {
		t.Errorf("expected error to name both process_path and process_id, got: %s", errMsg)
	}
}

func TestResolveProcessID_InvalidProcessIDType(t *testing.T) {
	_, _, errMsg := resolveProcessID(map[string]interface{}{"process_id": "not-a-number"}, "process_path", "process_id")
	if errMsg == "" {
		t.Fatal("expected an error for a non-numeric process_id")
	}
}

// ---- findStageRootFromCWD --------------------------------------------------

// chdirTemp switches CWD to dir for the duration of the test.
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %q: %v", dir, err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
}

func TestFindStageRootFromCWD_ReturnsRootPath(t *testing.T) {
	rootDir := tmpHomeAndCWD(t)
	if err := UpdateCurrent(func(f *Folder) { f.StageID = 671255 }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}

	got := findStageRootFromCWD(671255)
	gotReal, _ := filepath.EvalSymlinks(got)
	rootReal, _ := filepath.EvalSymlinks(rootDir)
	if gotReal != rootReal {
		t.Errorf("findStageRootFromCWD(671255) = %q, want %q", gotReal, rootReal)
	}
}

func TestFindStageRootFromCWD_MismatchedStageIDReturnsEmpty(t *testing.T) {
	tmpHomeAndCWD(t)
	if err := UpdateCurrent(func(f *Folder) { f.StageID = 999999 }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}

	if got := findStageRootFromCWD(671255); got != "" {
		t.Errorf("expected empty when Folder.StageID disagrees, got %q", got)
	}
}

func TestFindStageRootFromCWD_ZeroStageIDMatches(t *testing.T) {
	rootDir := tmpHomeAndCWD(t)
	if err := UpdateCurrent(func(f *Folder) { f.StageID = 42 }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}

	// expectedStageID=0 skips the guard — any Folder.StageID is accepted.
	got := findStageRootFromCWD(0)
	gotReal, _ := filepath.EvalSymlinks(got)
	rootReal, _ := filepath.EvalSymlinks(rootDir)
	if gotReal != rootReal {
		t.Errorf("expected match when expectedStageID=0, got %q want %q", gotReal, rootReal)
	}
}

func TestFindStageRootFromCWD_NoFolderReturnsEmpty(t *testing.T) {
	// tmpHome sets HOME to a fresh dir but does NOT register a Folder for
	// COREZOID_WORK_DIR — Current() returns nil, findStageRootFromCWD "".
	tmpHome(t)
	if got := findStageRootFromCWD(0); got != "" {
		t.Errorf("expected empty when no Folder registered, got %q", got)
	}
}

// TestPullProcess_TargetDirComposition documents the invariant that
// handlePullProcess relies on: resolveFolderPathFromAPI returns a path
// RELATIVE to the stage root, and filepath.Join(stageRoot, resolved) yields
// the correct on-disk location for any depth of nesting — including the
// stage-root case where resolved == "".
func TestPullProcess_TargetDirComposition(t *testing.T) {
	rootDir := tmpHomeAndCWD(t)
	if err := UpdateCurrent(func(f *Folder) { f.StageID = 671255 }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}

	stageRoot := findStageRootFromCWD(671255)
	if stageRoot == "" {
		t.Fatal("stage root not resolved from Folder")
	}
	stageRootReal, _ := filepath.EvalSymlinks(stageRoot)
	rootReal, _ := filepath.EvalSymlinks(rootDir)

	cases := []struct {
		name     string
		resolved string // what resolveFolderPathFromAPI would return
		want     string
	}{
		{"process at stage root", "", rootReal},
		{"one-level nesting", "671257_API", filepath.Join(rootReal, "671257_API")},
		{"deep nesting", filepath.Join("671257_API", "671262_GPT"), filepath.Join(rootReal, "671257_API", "671262_GPT")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filepath.Join(stageRootReal, c.resolved)
			if got != c.want {
				t.Errorf("resolved=%q: got %q, want %q", c.resolved, got, c.want)
			}
		})
	}
}

// ---- resolvePullDest -------------------------------------------------------

// TestResolvePullDest_NoFolderReturnsDot: first-ever pull, no Folder is
// registered yet — resolvePullDest falls back to "." so downloadStageRecursively
// writes into the current cwd, which UpdateCurrent will then register as
// RootPath on commit.
func TestResolvePullDest_NoFolderReturnsDot(t *testing.T) {
	tmpHome(t)
	if got := resolvePullDest(); got != "." {
		t.Errorf("expected fallback to \".\" when no Folder registered, got %q", got)
	}
}

// TestResolvePullDest_ReturnsRootPathFromDeeperCWD is the regression test for
// the reported bug: user is already logged in with RootPath = /workspace, then
// descends into /workspace/sub and re-runs pull-folder — the pull must land at
// /workspace, not at /workspace/sub. Before the fix, both handlePullFolder and
// the login auto-pull passed literal "." (== the process cwd) and produced a
// duplicate stage tree under the caller's subfolder.
func TestResolvePullDest_ReturnsRootPathFromDeeperCWD(t *testing.T) {
	rootDir := tmpHomeAndCWD(t)
	if err := UpdateCurrent(func(f *Folder) { f.StageID = 671255 }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}

	sub := filepath.Join(rootDir, "671257_API")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	t.Setenv("COREZOID_WORK_DIR", sub)

	got := resolvePullDest()
	gotReal, _ := filepath.EvalSymlinks(got)
	rootReal, _ := filepath.EvalSymlinks(rootDir)
	if gotReal != rootReal {
		t.Errorf("expected pull to anchor at Folder.RootPath=%q from subfolder cwd=%q, got %q", rootReal, sub, gotReal)
	}
}

// TestIntArg_RejectsFractional covers the defect an inbound review reproduced:
// delete-process{process_id: 123.9} deleted process 123 and reported success.
// JSON has one number type, so every id reaches intArg as a float64, and
// declaring the property "integer" in the tool schema changes nothing — the
// argument validator checks NAMES, not values. Truncation here is not a
// rounding question, it is an operation on a different object than the caller
// named, and several of the tools reading ids this way delete things.
func TestIntArg_RejectsFractional(t *testing.T) {
	for _, v := range []interface{}{123.9, 123.0001, -123.5} {
		if got, err := intArg(map[string]interface{}{"process_id": v}, "process_id"); err == nil {
			t.Errorf("intArg(%v) = %d, want an error — a fractional id must never be truncated onto a neighbouring object", v, got)
		}
	}
	// Whole numbers keep working in every form ids actually arrive in.
	for _, v := range []interface{}{float64(123), 123, "123"} {
		got, err := intArg(map[string]interface{}{"process_id": v}, "process_id")
		if err != nil || got != 123 {
			t.Errorf("intArg(%#v) = %d, %v; want 123, nil", v, got, err)
		}
	}
}

// argInt is the dashboard-side reader. It reports a fractional value as
// unusable rather than truncating it, so callers fall back to their documented
// default instead of to a neighbouring object.
func TestArgInt_RejectsFractional(t *testing.T) {
	if got, ok := argInt(map[string]interface{}{"stage_id": 12345.6}, "stage_id"); ok {
		t.Errorf("argInt(12345.6) = %d, true; want not-ok", got)
	}
	if got, ok := argInt(map[string]interface{}{"stage_id": float64(12345)}, "stage_id"); !ok || got != 12345 {
		t.Errorf("argInt(12345) = %d, %v; want 12345, true", got, ok)
	}
}

// TestConfineToWorkdir_RejectsSymlinkEscape covers the second half of the
// symlink hole an inbound review found. unzipFile was fixed to resolve
// symlinks; confineToWorkdir — the guard every process_path argument goes
// through — was still lexical on the relative branch, which is the spelling
// callers actually use. A clean relative path says nothing about where its
// components point.
func TestConfineToWorkdir_RejectsSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	work := t.TempDir()
	t.Chdir(work)

	if err := os.Symlink(outside, filepath.Join(work, "exports")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A symlinked DIRECTORY component: the path is clean and relative, and
	// every byte written through it lands outside the workspace.
	if got, err := confineToWorkdir("exports/stolen.conv.json"); err == nil {
		t.Errorf("confineToWorkdir through a symlinked directory = %q, want an error", got)
	}

	// A symlinked LEAF: the parent is a genuine workspace directory, so
	// resolving the parent alone accepts it — the write still follows the link.
	target := filepath.Join(outside, "target.conv.json")
	if err := os.WriteFile(target, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(work, "leaf.conv.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := confineToWorkdir("leaf.conv.json"); err == nil {
		t.Errorf("confineToWorkdir through a symlinked leaf = %q, want an error", got)
	}

	// The guard must not cost the ordinary cases: an existing file, and a
	// write target whose directory does not exist yet (create-process into a
	// folder that push will materialise).
	if err := os.WriteFile(filepath.Join(work, "real.conv.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := confineToWorkdir("real.conv.json"); err != nil {
		t.Errorf("a real file inside the workspace must be accepted: %v", err)
	}
	if _, err := confineToWorkdir("not/created/yet.conv.json"); err != nil {
		t.Errorf("a write target under a not-yet-created directory must be accepted: %v", err)
	}
}

// TestNoDirectBooleanArgAssertions pins the one policy for reading boolean
// arguments. Handlers used to be split between two: variables and access read
// a stringified "true" as true, while push-process, deploy-stage, layout,
// clean and modify-task asserted it straight to FALSE and ran with the flag
// off. That is how modify-task{deep_merge:"true"} shallow-replaced a task and
// dropped every key it did not send — no error, just missing data.
//
// The CLI passes every argument as a string, and hosts and models do send
// quoted booleans, so the reading has to be one function: boolishArg, or
// requiredBoolArg where false is itself an instruction.
//
// Scope, stated so nobody over-trusts it: this walks the AST for a bool type
// assertion applied directly to an index of a map named args, in any function
// other than the two readers. It therefore catches args["x"].(bool) however it
// is wrapped or line-broken, and does NOT catch a value copied into a local
// first (v := args["x"]; b, _ := v.(bool)). Assertions on server responses
// (op["immutable"].(bool)) are untouched on purpose — those decode an API
// payload, not a caller's argument.
func TestNoDirectBooleanArgAssertions(t *testing.T) {
	const argsMap = "args"
	readers := map[string]bool{"boolishArg": true, "requiredBoolArg": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || readers[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				assert, ok := n.(*ast.TypeAssertExpr)
				if !ok || assert.Type == nil {
					return true
				}
				if id, ok := assert.Type.(*ast.Ident); !ok || id.Name != "bool" {
					return true
				}
				index, ok := assert.X.(*ast.IndexExpr)
				if !ok {
					return true
				}
				base, ok := index.X.(*ast.Ident)
				if !ok || base.Name != argsMap {
					return true
				}
				t.Errorf("%s: %s asserts a boolean argument directly — use boolishArg "+
					"(or requiredBoolArg), or a quoted \"true\" silently reads as false",
					fset.Position(assert.Pos()), fn.Name.Name)
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no source files were checked — the guard would pass vacuously")
	}
}

// ---- path confinement: symlink escapes -------------------------------------

// The guard validates filepath.Clean(p) but the OS resolves the path the
// caller was handed. Those two readings diverge exactly when a symlink meets
// "..": Clean("link/../victim") is "victim" — lexically inside cwd, which is
// what passes the check — while the kernel walks through "link" to wherever it
// points and only then applies "..", landing outside. The fix is that
// confineToWorkdir returns the cleaned spelling it actually validated, so this
// test asserts on the RETURNED path, not just on the absence of an error.
func TestConfineToWorkdir_SymlinkDotDotCannotEscape(t *testing.T) {
	outside := t.TempDir()
	sub := filepath.Join(outside, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	if err := os.Symlink(sub, filepath.Join(work, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Chdir(work)

	got, err := confineToWorkdir("link/../victim.txt")
	if err != nil {
		return // rejecting outright is also a correct answer
	}
	// Accepted — then the path handed back must not reach outside when a
	// handler opens it, which is the only thing the caller does with it.
	if writeErr := os.WriteFile(got, []byte("OVERWRITTEN"), 0o600); writeErr != nil {
		t.Fatalf("writing the returned path: %v", writeErr)
	}
	b, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(b) != "ORIGINAL" {
		t.Fatalf("confineToWorkdir returned %q, and writing it overwrote a file outside the working directory", got)
	}
}

// Auto-discovery is the zero-argument path: no process_path, so the handler
// scans cwd and takes the single .conv.json it finds. os.ReadDir reports a
// symlink as an ordinary entry, so that scan used to accept a link pointing at
// somebody else's file and hand it straight to a writer. An explicit
// process_path naming the same link is rejected; the implicit route must not
// be the more permissive of the two.
func TestResolveProcessPath_AutoDiscoveredSymlinkCannotEscape(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "999_live.conv.json")
	if err := os.WriteFile(victim, []byte(`{"obj_type":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(work, "999_live.conv.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Chdir(work)

	got, err := resolveProcessPath(map[string]interface{}{}, "process_path")
	if err != nil {
		return // rejected, which is the intended outcome
	}
	if writeErr := os.WriteFile(got, []byte(`{"obj_type":1,"clobbered":true}`), 0o600); writeErr != nil {
		t.Fatalf("writing the returned path: %v", writeErr)
	}
	b, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(b) != `{"obj_type":1}` {
		t.Fatalf("auto-discovery returned %q, and writing it overwrote a file outside the working directory", got)
	}
}

// The three readers had drifted: boolishArg read " true" as false while
// requiredBoolArg read it as true, and a JSON number 1 — what a host that
// serialises booleans as 0/1 sends — was false to one, an error to the other.
// They now share parseBoolish, so this table is the whole policy, once.
func TestBoolReaders_AgreeOnEveryAcceptedSpelling(t *testing.T) {
	affirmative := []interface{}{true, "true", "TRUE", " true ", "1", " 1", float64(1), 1}
	negative := []interface{}{false, "false", "FALSE", " false ", "0", float64(0), 0}
	unreadable := []interface{}{"yes", "on", "maybe", float64(2), 1.5, []interface{}{}}

	for _, v := range affirmative {
		args := map[string]interface{}{"f": v}
		if !boolishArg(args, "f") {
			t.Errorf("boolishArg(%#v) = false, want true", v)
		}
		if got, msg := strictBoolArg(args, "f"); !got || msg != "" {
			t.Errorf("strictBoolArg(%#v) = %v, %q; want true with no error", v, got, msg)
		}
		if got, msg := requiredBoolArg(args, "f"); !got || msg != "" {
			t.Errorf("requiredBoolArg(%#v) = %v, %q; want true with no error", v, got, msg)
		}
	}
	for _, v := range negative {
		args := map[string]interface{}{"f": v}
		if boolishArg(args, "f") {
			t.Errorf("boolishArg(%#v) = true, want false", v)
		}
		if got, msg := strictBoolArg(args, "f"); got || msg != "" {
			t.Errorf("strictBoolArg(%#v) = %v, %q; want false with no error", v, got, msg)
		}
		if got, msg := requiredBoolArg(args, "f"); got || msg != "" {
			t.Errorf("requiredBoolArg(%#v) = %v, %q; want false with no error", v, got, msg)
		}
	}
	// Nothing outside the listed spellings is guessed at. boolishArg answers
	// false — the safe side of a waiver flag — while the strict readers refuse.
	for _, v := range unreadable {
		args := map[string]interface{}{"f": v}
		if boolishArg(args, "f") {
			t.Errorf("boolishArg(%#v) = true; unreadable must not turn a flag on", v)
		}
		if _, msg := strictBoolArg(args, "f"); msg == "" {
			t.Errorf("strictBoolArg(%#v) accepted an unreadable value", v)
		}
		if _, msg := requiredBoolArg(args, "f"); msg == "" {
			t.Errorf("requiredBoolArg(%#v) accepted an unreadable value", v)
		}
	}
}

// Absent is the one place the strict readers differ: strictBoolArg fronts a
// flag with a documented default, requiredBoolArg one where false is itself
// an instruction and silence cannot stand in for it.
func TestBoolReaders_DisagreeOnlyOnAbsence(t *testing.T) {
	for _, args := range []map[string]interface{}{{}, {"f": nil}} {
		if got, msg := strictBoolArg(args, "f"); got || msg != "" {
			t.Errorf("strictBoolArg(absent) = %v, %q; want the default with no error", got, msg)
		}
		if _, msg := requiredBoolArg(args, "f"); msg == "" {
			t.Error("requiredBoolArg(absent) must demand the argument")
		}
	}
}

func TestDocIntField(t *testing.T) {
	absent := []map[string]interface{}{{}, {"obj_id": nil}, {"obj_id": ""}, {"obj_id": "  "}}
	for _, doc := range absent {
		got, err := docIntField(doc, "obj_id")
		if got != 0 || err != nil {
			t.Errorf("docIntField(%#v) = %d, %v; want 0 with no error", doc, got, err)
		}
	}
	readable := map[string]interface{}{"f": float64(834936), "s": "834936", "i": 834936, "p": " 834936 "}
	for key := range readable {
		got, err := docIntField(readable, key)
		if got != 834936 || err != nil {
			t.Errorf("docIntField(%q) = %d, %v; want 834936", key, got, err)
		}
	}
	// Truncating 1234.9 to 1234 would name a different, existing process.
	for _, doc := range []map[string]interface{}{
		{"obj_id": 1234.9}, {"obj_id": "not-an-id"}, {"obj_id": true}, {"obj_id": []interface{}{}},
	} {
		if got, err := docIntField(doc, "obj_id"); err == nil {
			t.Errorf("docIntField(%#v) = %d with no error; unreadable must not read as absent", doc, got)
		}
	}
}
