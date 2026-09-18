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

func TestMaterializeProcessFile_ReusesTheDirectoryTheProcessWasPulledTo(t *testing.T) {
	inTempWorkdir(t)

	// A pulled process keeps its baseline sidecar next to the file. Writing a
	// second copy in the root would leave the push with no baseline, which the
	// concurrency gate reads as "never pulled" and blocks on.
	pulled := filepath.Join("projects", "7_Demo", "stages", "9_Dev", "123_Foo.conv.json")
	if err := os.MkdirAll(filepath.Dir(pulled), 0755); err != nil {
		t.Fatalf("seeding tree: %v", err)
	}
	if err := os.WriteFile(pulled, []byte(`{"obj_id": 123, "title": "Foo"}`), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	got, err := materializeProcessFile(map[string]interface{}{
		"content": `{"obj_id": 123, "title": "Foo", "rev": 2}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != pulled {
		t.Errorf("wrote to %q, want the pulled copy at %q", got, pulled)
	}
	if _, err := os.Stat("123_Foo.conv.json"); err == nil {
		t.Error("a duplicate was created in the working directory root")
	}
}

func TestMaterializeProcessFile_RefusesAnIDMismatchBetweenContentAndPath(t *testing.T) {
	inTempWorkdir(t)

	// push takes the deploy target from the file name and the body from
	// content, so a mismatch deploys one process over another.
	got, err := materializeProcessFile(map[string]interface{}{
		"content":      `{"obj_id": 5, "title": "Five"}`,
		"process_path": "9_Other.conv.json",
	})
	if err == nil {
		t.Fatalf("expected refusal, got %q", got)
	}
	if !strings.Contains(err.Error(), "#5") || !strings.Contains(err.Error(), "#9") {
		t.Errorf("error should name both ids, got %v", err)
	}
	if _, err := os.Stat("9_Other.conv.json"); err == nil {
		t.Error("refused call still created the file")
	}
}

func TestMaterializeProcessFile_BacksUpTheFileItReplaces(t *testing.T) {
	inTempWorkdir(t)

	target := "7_Backed.conv.json"
	original := `{"obj_id": 7, "title": "Backed", "rev": 1}`
	if err := os.WriteFile(target, []byte(original), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	if _, err := materializeProcessFile(map[string]interface{}{
		"content":      `{"obj_id": 7, "title": "Backed", "rev": 2}`,
		"process_path": target,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Content is validated only after this point, so a push rejected by lint
	// must leave the pulled version recoverable.
	data, err := os.ReadFile(target + ".pre-write")
	if err != nil {
		t.Fatalf("expected a backup of the replaced file: %v", err)
	}
	if string(data) != original {
		t.Errorf("backup holds %s, want %s", data, original)
	}
}

func TestMaterializeProcessFile_RefusesContentThatIsNotAString(t *testing.T) {
	inTempWorkdir(t)

	if err := os.WriteFile("3_OnDisk.conv.json", []byte(`{"obj_id": 3}`), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}
	// An object in the content field must not degrade into "no content given",
	// which would deploy the unrelated file sitting on disk.
	got, err := materializeProcessFile(map[string]interface{}{
		"content": map[string]interface{}{"obj_id": 3},
	})
	if err == nil {
		t.Fatalf("expected refusal, got %q", got)
	}
	if !strings.Contains(err.Error(), "string") {
		t.Errorf("error should name the expected type, got %v", err)
	}
}

// A model writing JSON quotes ids as often as not. Reading only float64 left
// contentID at 0, which is indistinguishable from "no id given" — so the
// mismatch check below was skipped and the body was written into, and then
// deployed over, an unrelated process.
func TestMaterializeProcessFile_RefusesAnIDMismatchWhenTheIDIsQuoted(t *testing.T) {
	inTempWorkdir(t)

	got, err := materializeProcessFile(map[string]interface{}{
		"content":      `{"obj_id": "5", "title": "Five"}`,
		"process_path": "9_Other.conv.json",
	})
	if err == nil {
		t.Fatalf("expected refusal, got %q", got)
	}
	if !strings.Contains(err.Error(), "#5") || !strings.Contains(err.Error(), "#9") {
		t.Errorf("error should name both ids, got %v", err)
	}
	if _, err := os.Stat("9_Other.conv.json"); err == nil {
		t.Error("refused call still created the file")
	}
}

// A quoted id is the id it spells, so it must still satisfy a matching path
// and still derive the conventional file name.
func TestMaterializeProcessFile_AcceptsAQuotedIDThatAgrees(t *testing.T) {
	inTempWorkdir(t)

	got, err := materializeProcessFile(map[string]interface{}{
		"content": `{"obj_id": "1832359", "title": "Rates", "scheme": {"nodes": []}}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != convFileName(1832359, "Rates") {
		t.Errorf("wrote %q, want the conventional name for #1832359", got)
	}
}

// 1234.9 truncated to 1234 is a correct-looking write onto a DIFFERENT
// process — the same failure intArg grew a whole-number guard for.
func TestMaterializeProcessFile_RefusesANonIntegralID(t *testing.T) {
	inTempWorkdir(t)

	got, err := materializeProcessFile(map[string]interface{}{
		"content":      `{"obj_id": 1234.9, "title": "Drifted"}`,
		"process_path": "1234_Victim.conv.json",
	})
	if err == nil {
		t.Fatalf("expected refusal, got %q", got)
	}
	if !strings.Contains(err.Error(), "whole number") {
		t.Errorf("error should name the whole-number rule, got %v", err)
	}
	if _, err := os.Stat("1234_Victim.conv.json"); err == nil {
		t.Error("refused call still created the file")
	}
}

// An id nobody can read must not degrade into "no id given" — that is the
// state that skips the guard.
func TestMaterializeProcessFile_RefusesAnUnreadableID(t *testing.T) {
	inTempWorkdir(t)

	got, err := materializeProcessFile(map[string]interface{}{
		"content":      `{"obj_id": "not-an-id", "title": "Junk"}`,
		"process_path": "1234_Victim.conv.json",
	})
	if err == nil {
		t.Fatalf("expected refusal, got %q", got)
	}
	if _, err := os.Stat("1234_Victim.conv.json"); err == nil {
		t.Error("refused call still created the file")
	}
}

// null obj_id is how a never-deployed process is spelled; it stays "absent".
func TestMaterializeProcessFile_TreatsNullIDAsAbsent(t *testing.T) {
	inTempWorkdir(t)

	got, err := materializeProcessFile(map[string]interface{}{
		"content":      `{"obj_id": null, "title": "New", "scheme": {"nodes": []}}`,
		"process_path": "9_Other.conv.json",
	})
	if err != nil {
		t.Fatalf("a null id must not block an explicit path: %v", err)
	}
	if got != "9_Other.conv.json" {
		t.Errorf("wrote %q, want the path that was asked for", got)
	}
}
