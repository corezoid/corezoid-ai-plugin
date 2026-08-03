package main

// testdata/golden/tools_list.json freezes the MCP "tools/list" surface: the
// ordered sequence of tool names, their descriptions and a hash of each input
// schema. Hosts (Claude Code, Codex, Kiro) may enumerate tools positionally or
// memoise them by index, so regrouping tool definitions across files must not
// reorder or reword them. Regenerate after an intentional change:
//
//	go test -run TestToolsList_Snapshot -update
//
// A golden diff is the review artefact: it shows exactly which tool was added,
// removed, moved or reworded.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// toolSnapshotEntry is one line of the frozen tools/list surface. The input
// schema is hashed rather than inlined to keep the golden reviewable — a
// schema change still flips the hash.
type toolSnapshotEntry struct {
	Tool         string `json:"tool"`
	Description  string `json:"description"`
	SchemaSHA256 string `json:"schema_sha256"`
}

func toolsListSnapshot(t *testing.T) []toolSnapshotEntry {
	t.Helper()
	out := make([]toolSnapshotEntry, 0, len(toolRegistry))
	for _, tool := range toolRegistry {
		// encoding/json sorts map keys, so the hash is stable across runs.
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal input schema for tool %q: %v", tool.Name, err)
		}
		sum := sha256.Sum256(raw)
		out = append(out, toolSnapshotEntry{
			Tool:         tool.Name,
			Description:  tool.Description,
			SchemaSHA256: hex.EncodeToString(sum[:]),
		})
	}
	return out
}

func TestToolsList_Snapshot(t *testing.T) {
	got := toolsListSnapshot(t)
	golden := filepath.Join("testdata", "golden", "tools_list.json")

	if *updateGolden {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(b, '\n'), 0644); err != nil {
			t.Fatalf("write golden %s: %v", golden, err)
		}
		t.Logf("updated golden: %s (%d tools)", golden, len(got))
		return
	}

	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", golden, err)
	}
	var want []toolSnapshotEntry
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse golden %s: %v", golden, err)
	}

	for i := 0; i < len(want) && i < len(got); i++ {
		switch {
		case want[i].Tool != got[i].Tool:
			t.Errorf("tools/list position %d: golden has %q, registry has %q — order or membership changed",
				i, want[i].Tool, got[i].Tool)
		case want[i].Description != got[i].Description:
			t.Errorf("tool %q: description drifted\n--- golden ---\n%s\n--- registry ---\n%s",
				got[i].Tool, want[i].Description, got[i].Description)
		case want[i].SchemaSHA256 != got[i].SchemaSHA256:
			t.Errorf("tool %q: input schema drifted (golden %s, registry %s)",
				got[i].Tool, want[i].SchemaSHA256, got[i].SchemaSHA256)
		}
	}
	for _, extra := range want[min(len(want), len(got)):] {
		t.Errorf("tool %q is in the golden but missing from the registry", extra.Tool)
	}
	for _, extra := range got[min(len(want), len(got)):] {
		t.Errorf("tool %q is in the registry but missing from the golden", extra.Tool)
	}
	if t.Failed() {
		t.Log("if the change is intentional, regenerate with: go test -run TestToolsList_Snapshot -update ./...")
	}
}
