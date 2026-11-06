package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Models call lint-process with a process ID, the same way they call
// pull-process and run-task. lint-process used to accept only process_path,
// so the call was rejected as an unknown argument and a turn was wasted. It
// must now resolve the ID to the pulled local copy, and say to pull first
// when there is none.
func TestHandleLintProcess_AcceptsProcessID(t *testing.T) {
	resetGlobals(t)
	sample, err := os.ReadFile(filepath.Join("samples", "valid_process.json"))
	if err != nil {
		t.Skip("valid_process.json not found")
	}
	chdirTemp(t, t.TempDir())

	sub := filepath.Join("Project", "Stage")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "4242_Order flow.conv.json"), sample, 0o644); err != nil {
		t.Fatal(err)
	}

	for name, args := range map[string]map[string]interface{}{
		"process_id number":       {"process_id": float64(4242)},
		"process_id string":       {"process_id": "4242"},
		"bare id in process_path": {"process_path": "4242"},
	} {
		t.Run(name, func(t *testing.T) {
			out, isErr := handleToolCall(context.Background(), "lint-process", args)
			if isErr {
				t.Fatalf("expected lint to succeed, got: %s", out)
			}
		})
	}

	out, isErr := handleToolCall(context.Background(), "lint-process", map[string]interface{}{"process_id": float64(999)})
	if !isErr || !strings.Contains(out, "pull-process") {
		t.Fatalf("missing local copy must error and point at pull-process, got (isErr=%v): %s", isErr, out)
	}

	if err := os.WriteFile("4242_copy.conv.json", sample, 0o644); err != nil {
		t.Fatal(err)
	}
	out, isErr = handleToolCall(context.Background(), "lint-process", map[string]interface{}{"process_id": float64(4242)})
	if !isErr || !strings.Contains(out, "4242_copy.conv.json") || !strings.Contains(out, "process_path") {
		t.Fatalf("ambiguous ID must list candidates, got (isErr=%v): %s", isErr, out)
	}

	out, isErr = handleToolCall(context.Background(), "lint-process", map[string]interface{}{"process_id": float64(4242), "process_path": "4242_copy.conv.json"})
	if !isErr || !strings.Contains(out, "not both") {
		t.Fatalf("both args must be rejected, got (isErr=%v): %s", isErr, out)
	}
}
