package main

import (
	"context"
	"strings"
	"testing"
)

// ---- argInt (dashboard handler helper) -------------------------------------

func TestArgInt_Float64(t *testing.T) {
	v, ok := argInt(map[string]interface{}{"n": float64(7)}, "n")
	if !ok || v != 7 {
		t.Errorf("got (%d, %v), want (7, true)", v, ok)
	}
}

func TestArgInt_StringNumeric(t *testing.T) {
	v, ok := argInt(map[string]interface{}{"n": "123"}, "n")
	if !ok || v != 123 {
		t.Errorf("got (%d, %v), want (123, true)", v, ok)
	}
}

func TestArgInt_StringInvalid(t *testing.T) {
	_, ok := argInt(map[string]interface{}{"n": "not-a-number"}, "n")
	if ok {
		t.Error("expected ok=false for non-numeric string")
	}
}

func TestArgInt_Missing(t *testing.T) {
	_, ok := argInt(map[string]interface{}{}, "n")
	if ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestArgInt_UnsupportedType(t *testing.T) {
	_, ok := argInt(map[string]interface{}{"n": []int{1}}, "n")
	if ok {
		t.Error("expected ok=false for unsupported type")
	}
}

// ---- extractProcessIDFromPath ----------------------------------------------

func TestExtractProcessIDFromPath_Standard(t *testing.T) {
	id, errMsg := extractProcessIDFromPath("/some/dir/12345_my_process.conv.json")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if id != 12345 {
		t.Errorf("expected 12345, got %d", id)
	}
}

func TestExtractProcessIDFromPath_RelativePath(t *testing.T) {
	id, errMsg := extractProcessIDFromPath("999_test.conv.json")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if id != 999 {
		t.Errorf("expected 999, got %d", id)
	}
}

func TestExtractProcessIDFromPath_Invalid(t *testing.T) {
	_, errMsg := extractProcessIDFromPath("no-id-here.conv.json")
	if errMsg == "" {
		t.Error("expected error for filename without leading ID")
	}
	if !strings.Contains(errMsg, "no-id-here.conv.json") {
		t.Errorf("expected basename in error, got: %s", errMsg)
	}
}

func TestExtractProcessIDFromPath_OnlyDigits(t *testing.T) {
	// Without trailing underscore the regex does not match.
	_, errMsg := extractProcessIDFromPath("12345.conv.json")
	if errMsg == "" {
		t.Error("expected error for filename without underscore separator")
	}
}

// ---- isInSet ----------------------------------------------------------------

func TestIsInSet(t *testing.T) {
	set := map[string]struct{}{"a": {}, "b": {}}
	if !isInSet("a", set) {
		t.Error("expected 'a' to be in set")
	}
	if isInSet("c", set) {
		t.Error("expected 'c' not to be in set")
	}
	if isInSet("a", nil) {
		t.Error("expected nil set lookup to return false")
	}
}

// ---- handleToolCall: nil ctx -----------------------------------------------

// Coverage for the `if ctx == nil` branch — handleToolCall must not panic.
func TestHandleToolCall_NilContextDoesNotPanic(t *testing.T) {
	resetGlobals(t)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("handleToolCall panicked on nil context: %v", r)
		}
	}()
	// Use an unknown tool so we don't accidentally hit the network.
	// ensureAuth() will short-circuit first; we just verify nil ctx is tolerated.
	_, _ = handleToolCall(nil, "nonexistent-tool", map[string]interface{}{}) //nolint:staticcheck
}

// ---- toMapSlice ------------------------------------------------------------

func TestToMapSlice_HappyPath(t *testing.T) {
	in := []interface{}{
		map[string]interface{}{"id": "a"},
		map[string]interface{}{"id": "b"},
	}
	out := toMapSlice(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 items, got %d", len(out))
	}
	if out[0]["id"] != "a" || out[1]["id"] != "b" {
		t.Errorf("unexpected items: %v", out)
	}
}

func TestToMapSlice_NonMapEntriesSkipped(t *testing.T) {
	in := []interface{}{
		map[string]interface{}{"id": "a"},
		"not-a-map",
		42,
		map[string]interface{}{"id": "b"},
	}
	out := toMapSlice(in)
	if len(out) != 2 {
		t.Errorf("expected 2 valid entries, got %d", len(out))
	}
}

func TestToMapSlice_WrongType(t *testing.T) {
	if got := toMapSlice("string"); got != nil {
		t.Errorf("expected nil for non-slice input, got %v", got)
	}
	if got := toMapSlice(nil); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}

// ---- parseProcessNodes -----------------------------------------------------

func TestParseProcessNodes_Basic(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{
			"id":       "node1",
			"title":    "Start",
			"obj_type": float64(1),
			"condition": map[string]interface{}{
				"logics": []interface{}{
					map[string]interface{}{"type": "go", "to_node_id": "node2"},
				},
				"semaphors": []interface{}{},
			},
		},
		map[string]interface{}{
			"id":       "node2",
			"obj_type": float64(2),
		},
	}
	got := parseProcessNodes(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(got))
	}
	if got[0].id != "node1" || got[0].title != "Start" || got[0].objType != 1 {
		t.Errorf("unexpected first node: %+v", got[0])
	}
	if len(got[0].logics) != 1 {
		t.Errorf("expected 1 logic, got %d", len(got[0].logics))
	}
	// Missing condition map -> empty logics/sems, no panic.
	if got[1].id != "node2" || got[1].objType != 2 {
		t.Errorf("unexpected second node: %+v", got[1])
	}
	if got[1].logics != nil {
		t.Errorf("expected nil logics for node without condition, got %v", got[1].logics)
	}
}

func TestParseProcessNodes_SkipsNonMaps(t *testing.T) {
	raw := []interface{}{
		"not-a-node",
		42,
		map[string]interface{}{"id": "ok"},
	}
	got := parseProcessNodes(raw)
	if len(got) != 1 || got[0].id != "ok" {
		t.Errorf("expected only valid map to be kept, got %+v", got)
	}
}

func TestParseProcessNodes_Empty(t *testing.T) {
	got := parseProcessNodes(nil)
	if got == nil || len(got) != 0 {
		t.Errorf("expected empty slice, got %+v", got)
	}
}

// ---- FormatLintResult edge cases -------------------------------------------

func TestFormatLintResult_SchemaInvalid(t *testing.T) {
	res := &LintResult{
		ProcessTitle: "X",
		TotalNodes:   1,
		SchemaValid:  false,
		SchemaError:  "missing required field",
	}
	out := FormatLintResult(res)
	if !strings.Contains(out, "JSON SCHEMA VALIDATION FAILED") {
		t.Errorf("expected schema failure header: %s", out)
	}
	if !strings.Contains(out, "missing required field") {
		t.Errorf("expected schema error message: %s", out)
	}
	if !strings.Contains(out, "Total issues: 1") {
		t.Errorf("expected total count to include schema issue: %s", out)
	}
}

func TestFormatLintResult_OnlyNoopAndUnused(t *testing.T) {
	res := &LintResult{
		ProcessTitle: "P",
		TotalNodes:   3,
		SchemaValid:  true,
		NoopConditions: []NoopCondition{
			{ID: "n1", Title: "Noop", Issue: "all branches go to same dest"},
		},
		UnusedSetParams: []UnusedSetParam{
			{ID: "n2", Title: "Setter", Issue: "vars never referenced"},
		},
	}
	out := FormatLintResult(res)
	if !strings.Contains(out, "NOOP CONDITIONS (1)") || !strings.Contains(out, "UNUSED SET_PARAM (1)") {
		t.Errorf("expected both sections, got: %s", out)
	}
	if strings.Contains(out, "ORPHANED NODES") {
		t.Errorf("did not expect orphan section, got: %s", out)
	}
	if !strings.Contains(out, "Total issues: 2") {
		t.Errorf("expected Total issues: 2, got: %s", out)
	}
}

func TestFormatLintResult_NoIssues(t *testing.T) {
	res := &LintResult{
		ProcessTitle: "Clean",
		TotalNodes:   5,
		SchemaValid:  true,
	}
	out := FormatLintResult(res)
	if !strings.Contains(out, "No issues found.") {
		t.Errorf("expected clean output, got: %s", out)
	}
	if strings.Contains(out, "Total issues:") {
		t.Errorf("did not expect a total-issues line when clean, got: %s", out)
	}
}

// ---- confineToWorkdir extra cases ------------------------------------------

func TestConfineToWorkdir_DotEqualsParent(t *testing.T) {
	if _, err := confineToWorkdir(".."); err == nil {
		t.Error("expected error for path \"..\"")
	}
}

func TestConfineToWorkdir_NestedEscape(t *testing.T) {
	if _, err := confineToWorkdir("../etc/passwd"); err == nil {
		t.Error("expected error for nested ../ traversal")
	}
}

func TestConfineToWorkdir_AllowsCleanRelative(t *testing.T) {
	if _, err := confineToWorkdir("samples/foo.conv.json"); err != nil {
		t.Errorf("expected ok, got: %v", err)
	}
}

func TestConfineToWorkdir_EmptyOK(t *testing.T) {
	got, err := confineToWorkdir("")
	if err != nil {
		t.Errorf("expected nil error for empty path, got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty result, got %q", got)
	}
}

// Compile-time guard: keep context import alive for handleToolCall signature usage.
var _ = context.Background
