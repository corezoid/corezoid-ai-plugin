package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// writeFixtureFile marshals a fixture into a canon-format (2-space indent)
// process file inside a temp dir.
func writeFixtureFile(t *testing.T, nodes []map[string]interface{}) string {
	t.Helper()
	doc := map[string]interface{}{
		"obj_id": 12345678,
		"title":  "layout io fixture",
		"scheme": map[string]interface{}{"nodes": nodesToIface(nodes)},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir) // resolveProcessPath confines paths to the working directory
	path := "12345678_fixture.conv.json"
	if err := os.WriteFile(path, append(b, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func nodesToIface(nodes []map[string]interface{}) []interface{} {
	out := make([]interface{}, len(nodes))
	for i, n := range nodes {
		out[i] = n
	}
	return out
}

func TestLayoutProcess_DryDoesNotTouchTheFile(t *testing.T) {
	path := writeFixtureFile(t, deepCopyNodes(t, topoChain()))
	before, _ := os.ReadFile(path)

	res, isErr := handleLayoutProcess(context.Background(), map[string]interface{}{
		"process_path": path, "dry": true,
	})
	if isErr {
		t.Fatalf("dry run failed: %s", res)
	}
	if !strings.Contains(res, "DRY RUN") || !strings.Contains(res, "strategy: waterfall") {
		t.Errorf("dry output must carry the report and the listing:\n%s", res)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("dry=true modified the file")
	}
}

func TestLayoutProcess_WritesOnlyCoordinateAndExtraLines(t *testing.T) {
	nodes := deepCopyNodes(t, topoChain())
	// HTML-escapable characters must round-trip in the repo's canonical
	// escaped form, or every such line would be rewritten on re-layout.
	nodes[1]["title"] = "check A && B <ok> & co"
	path := writeFixtureFile(t, nodes)
	before, _ := os.ReadFile(path)

	res, isErr := handleLayoutProcess(context.Background(), map[string]interface{}{
		"process_path": path,
	})
	if isErr {
		t.Fatalf("layout failed: %s", res)
	}
	after, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(after), "\n") {
		t.Errorf("trailing newline lost")
	}

	// On a canon-format file the diff is confined to x/y/extra lines.
	beforeLines := strings.Split(string(before), "\n")
	afterLines := strings.Split(string(after), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("line count changed: %d -> %d", len(beforeLines), len(afterLines))
	}
	for i := range beforeLines {
		if beforeLines[i] == afterLines[i] {
			continue
		}
		trimmed := strings.TrimSpace(afterLines[i])
		if !strings.HasPrefix(trimmed, `"x":`) && !strings.HasPrefix(trimmed, `"y":`) && !strings.HasPrefix(trimmed, `"extra":`) {
			t.Errorf("line %d changed outside x/y/extra:\n  before: %s\n  after:  %s", i, beforeLines[i], afterLines[i])
		}
	}

	// A second run over the already-canon result is a no-op.
	res2, isErr2 := handleLayoutProcess(context.Background(), map[string]interface{}{
		"process_path": path,
	})
	if isErr2 {
		t.Fatalf("second layout failed: %s", res2)
	}
	if !strings.Contains(res2, "0 moved") {
		t.Errorf("re-layout of an already laid-out file must move 0 nodes:\n%s", res2)
	}
}

func TestLayoutProcess_IndentAndNumberFidelity(t *testing.T) {
	nodes := deepCopyNodes(t, topoChain())
	doc := map[string]interface{}{
		"obj_id": 9007199254740993, // does not fit float64 exactly
		"scheme": map[string]interface{}{"nodes": nodesToIface(nodes)},
	}
	b, err := json.MarshalIndent(doc, "", "    ") // 4-space indent
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	path := "999_indent.conv.json"
	if err := os.WriteFile(path, b, 0644); err != nil { // no trailing newline
		t.Fatal(err)
	}

	if res, isErr := handleLayoutProcess(context.Background(), map[string]interface{}{"process_path": path}); isErr {
		t.Fatalf("layout failed: %s", res)
	}
	after, _ := os.ReadFile(path)
	if strings.HasSuffix(string(after), "\n") {
		t.Errorf("a file without a trailing newline must stay without one")
	}
	if !strings.Contains(string(after), "\n    \"scheme\"") {
		t.Errorf("4-space indentation not preserved")
	}
	if !strings.Contains(string(after), "9007199254740993") {
		t.Errorf("large integer drifted (UseNumber not honoured)")
	}
}

func TestLayoutProcess_BadDensityAndMissingFile(t *testing.T) {
	path := writeFixtureFile(t, deepCopyNodes(t, topoChain()))
	res, isErr := handleLayoutProcess(context.Background(), map[string]interface{}{
		"process_path": path, "density": "cosy",
	})
	if !isErr || !strings.Contains(res, "compact, medium or roomy") {
		t.Errorf("unknown density must error with the allowed values: %s", res)
	}
	res, isErr = handleLayoutProcess(context.Background(), map[string]interface{}{
		"process_path": "absent.conv.json",
	})
	if !isErr {
		t.Errorf("missing file must error: %s", res)
	}
}
