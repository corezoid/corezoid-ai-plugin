package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A lint-process call with a bare file name used to fail with only
// "failed to read file: open X: no such file or directory" — no base
// directory, no resolved path, no hint that the file lives in a subfolder.
// Models then looped through path variants. The error must name the working
// directory relative paths resolve against and point at the real file.
func TestHandleLintProcess_NotFoundNamesBaseDirAndSuggestsFile(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)
	cwd, _ := os.Getwd()

	sub := filepath.Join("Project", "Stage", "Процессы")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	realPath := filepath.Join(sub, "123_Обработка заявки.conv.json")
	if err := os.WriteFile(realPath,[]byte(`{"obj_id":123,"scheme":{"nodes":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"bare name":         "123_Обработка заявки.conv.json",
		"mistyped title":    "123_Obrabotka.conv.json",
		"shortened subpath": filepath.Join("Stage", "123_Обработка заявки.conv.json"),
	}
	for name, arg := range cases {
		t.Run(name, func(t *testing.T) {
			out, isErr := handleLintProcess(context.Background(), map[string]interface{}{"process_path": arg})
			if !isErr {
				t.Fatalf("expected error, got: %s", out)
			}
			for _, want := range []string{
				"working directory " + cwd,
				filepath.Join(cwd, arg),
				"absolute path",
				realPath,
			} {
				if !strings.Contains(out, want) {
					t.Errorf("error missing %q:\n%s", want, out)
				}
			}
		})
	}

	// The suggested path works as-is.
	if out, isErr := handleLintProcess(context.Background(), map[string]interface{}{"process_path": realPath}); isErr {
		t.Fatalf("suggested path failed: %s", out)
	}
}

func TestHandleLayoutProcess_NotFoundNamesBaseDir(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)
	cwd, _ := os.Getwd()

	out, isErr := handleLayoutProcess(context.Background(), map[string]interface{}{"process_path": "1_missing.conv.json"})
	if !isErr {
		t.Fatalf("expected error, got: %s", out)
	}
	if !strings.Contains(out, "working directory "+cwd) {
		t.Errorf("error does not name the base directory:\n%s", out)
	}
}

func TestConfineToWorkdir_AbsoluteOutsideNamesRoot(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)
	cwd, _ := os.Getwd()

	_, err := confineToWorkdir(filepath.Join(t.TempDir(), "x.conv.json"))
	if err == nil || !strings.Contains(err.Error(), cwd) {
		t.Fatalf("error should name the working directory %s, got: %v", cwd, err)
	}
}
