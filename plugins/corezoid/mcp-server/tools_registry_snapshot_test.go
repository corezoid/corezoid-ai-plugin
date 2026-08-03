package main

// The tools/list payload is a public contract: clients cache it, and models
// pick tools from it. Splitting the registry across per-domain files (and
// later editing those files) must not silently drop a tool, reorder the wire
// output, or change a description. The golden snapshot below freezes the exact
// serialized payload. Regenerate deliberately after an intentional change:
//
//	go test -run TestToolsList_Snapshot -update ./...

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const toolsListGoldenPath = "testdata/golden/tools_list.json"

// TestToolsList_Snapshot freezes the serialized tools/list result.
func TestToolsList_Snapshot(t *testing.T) {
	got, err := json.MarshalIndent(map[string]interface{}{"tools": toolRegistry}, "", "  ")
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	got = append(got, '\n')

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(toolsListGoldenPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(toolsListGoldenPath, got, 0644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(toolsListGoldenPath)
	if err != nil {
		t.Fatalf("golden file missing (run with -update): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("tools/list payload drifted from %s — if intentional, regenerate with -update", toolsListGoldenPath)
		// Point at the first differing tool so the failure is actionable
		// without diffing a 3000-line blob by hand.
		var wantDoc, gotDoc struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if json.Unmarshal(want, &wantDoc) == nil && json.Unmarshal(got, &gotDoc) == nil {
			if len(wantDoc.Tools) != len(gotDoc.Tools) {
				t.Errorf("tool count: golden %d, got %d", len(wantDoc.Tools), len(gotDoc.Tools))
			}
			for i := 0; i < len(wantDoc.Tools) && i < len(gotDoc.Tools); i++ {
				if wantDoc.Tools[i].Name != gotDoc.Tools[i].Name {
					t.Errorf("order drifted at index %d: golden %q, got %q", i, wantDoc.Tools[i].Name, gotDoc.Tools[i].Name)
					break
				}
			}
		}
	}
}

// TestToolRegistryCoversEveryHandler verifies the per-domain slices composed
// into toolRegistry cover exactly the dispatch table. A domain file that is
// written but never added to concatTools compiles fine and would otherwise
// only show up as a tool that mysteriously stopped being advertised.
func TestToolRegistryCoversEveryHandler(t *testing.T) {
	inRegistry := make(map[string]bool, len(toolRegistry))
	for _, tool := range toolRegistry {
		inRegistry[tool.Name] = true
	}
	for name := range toolHandlers {
		if !inRegistry[name] {
			t.Errorf("tool %q has a handler but is not advertised in toolRegistry — is its domain slice wired into concatTools?", name)
		}
	}
	for name := range inRegistry {
		if _, ok := toolHandlers[name]; !ok {
			t.Errorf("tool %q is advertised in toolRegistry but has no handler in toolHandlers", name)
		}
	}
}
