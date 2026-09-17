package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inTempWorkdir moves into a fresh temp directory for the duration of a test,
// so relative paths resolve inside it and the containment guard has something
// real to compare against.
func inTempWorkdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	chdirTemp(t, dir)
	return dir
}

func TestMaterializeProcessFile_DerivesNameFromContent(t *testing.T) {
	inTempWorkdir(t)

	content := `{"obj_id": 1832359, "obj_type": 1, "title": "Get Currency Rates", "scheme": {"nodes": []}}`
	got, err := materializeProcessFile(map[string]interface{}{"content": content})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The name must match what pull-process would have produced, otherwise
	// push-process cannot recover the process id from the file name.
	want := "1832359_Get_Currency_Rates.conv.json"
	if got != want {
		t.Errorf("derived name is %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		entries, _ := os.ReadDir(".")
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected %s to exist, directory holds %v", want, names)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content was rewritten:\n got %s\nwant %s", data, content)
	}
}

func TestMaterializeProcessFile_HonoursExplicitPath(t *testing.T) {
	inTempWorkdir(t)

	content := `{"obj_id": 42, "title": "Nested"}`
	target := filepath.Join("projects", "7_Demo", "42_Nested.conv.json")
	got, err := materializeProcessFile(map[string]interface{}{
		"content":      content,
		"process_path": target,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Intermediate directories are created: a pulled folder tree is exactly
	// where a new process belongs, and requiring the caller to mkdir first
	// would need the file tools this tool exists to replace.
	if got != target {
		t.Errorf("returned path is %q, want %q", got, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected %s to exist: %v", target, err)
	}
}

func TestMaterializeProcessFile_RejectsPathsOutsideTheWorkingDirectory(t *testing.T) {
	inTempWorkdir(t)

	for _, target := range []string{
		"../escape.conv.json",
		"../../elsewhere/evil.conv.json",
	} {
		got, err := materializeProcessFile(map[string]interface{}{
			"content":      `{"obj_id": 1}`,
			"process_path": target,
		})
		if err == nil {
			t.Errorf("%s: expected refusal, got %q", target, got)
		}
	}
}

func TestMaterializeProcessFile_RejectsNonProcessFiles(t *testing.T) {
	inTempWorkdir(t)

	// The whole point of the restriction: a host that withheld file tools must
	// not get them back through this one. .mcp.json and .claude/settings.json
	// are executable configuration for several agent runtimes.
	for _, target := range []string{
		".mcp.json",
		".claude/settings.json",
		"CLAUDE.md",
		"notes.txt",
		"process.json",
	} {
		got, err := materializeProcessFile(map[string]interface{}{
			"content":      `{"obj_id": 1}`,
			"process_path": target,
		})
		if err == nil {
			t.Errorf("%s: expected refusal, got %q", target, got)
		}
		if _, err := os.Stat(target); err == nil {
			t.Errorf("%s: refused call still created the file", target)
		}
	}
}

func TestMaterializeProcessFile_RejectsContentThatIsNotJSON(t *testing.T) {
	inTempWorkdir(t)

	got, err := materializeProcessFile(map[string]interface{}{
		"content":      "not json at all",
		"process_path": "1_Broken.conv.json",
	})
	if err == nil {
		t.Fatalf("expected refusal, got %q", got)
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error should name the problem, got %v", err)
	}
	if _, err := os.Stat("1_Broken.conv.json"); err == nil {
		t.Error("refused call still created the file")
	}
}

func TestMaterializeProcessFile_RequiresAnIDWhenNoPathIsGiven(t *testing.T) {
	inTempWorkdir(t)

	got, err := materializeProcessFile(map[string]interface{}{
		"content": `{"title": "No id here"}`,
	})
	if err == nil {
		t.Fatalf("expected refusal, got %q", got)
	}
	if !strings.Contains(err.Error(), "obj_id") {
		t.Errorf("error should say what is missing, got %v", err)
	}
}

func TestMaterializeProcessFile_OverwritesAnExistingFile(t *testing.T) {
	inTempWorkdir(t)

	target := "9_Iterate.conv.json"
	first := `{"obj_id": 9, "title": "Iterate", "rev": 1}`
	second := `{"obj_id": 9, "title": "Iterate", "rev": 2}`
	for _, content := range []string{first, second} {
		if _, err := materializeProcessFile(map[string]interface{}{
			"content":      content,
			"process_path": target,
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// Editing is a loop, not a one-shot: the second write must fully replace
	// the first rather than append to it.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(data) != second {
		t.Errorf("got %s, want %s", data, second)
	}
}

func TestMaterializeProcessFile_FallsBackToTheFileOnDisk(t *testing.T) {
	inTempWorkdir(t)

	// No content: the argument is optional, and a push that omits it keeps the
	// old behaviour of deploying whatever is already on disk.
	if err := os.WriteFile("5_Existing.conv.json", []byte(`{"obj_id": 5}`), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}
	got, err := materializeProcessFile(map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "5_Existing.conv.json" {
		t.Errorf("got %q, want the single .conv.json in the directory", got)
	}
}
