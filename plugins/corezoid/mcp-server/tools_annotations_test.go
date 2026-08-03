package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// annotationPresets maps each preset in tools_annotations.go to its name. A
// new preset must be registered here, which is the point: the presets are the
// reviewed vocabulary, and a tool annotated with anything else is a mistake.
var annotationPresets = map[mcpToolAnnotations]string{
	annReadOnlyRemote:    "annReadOnlyRemote",
	annReadOnlyLocal:     "annReadOnlyLocal",
	annCreateRemote:      "annCreateRemote",
	annDestructiveRemote: "annDestructiveRemote",
	annDestructiveLocal:  "annDestructiveLocal",
}

// TestEveryToolHasAnnotations rejects a tool that shipped without picking one
// of the presets. The zero mcpToolAnnotations is not merely "unset" on the
// wire — it advertises a non-idempotent, closed-world write, which is wrong
// for most tools and silently wrong for every read-only one.
func TestEveryToolHasAnnotations(t *testing.T) {
	for _, tool := range toolRegistry {
		if _, ok := annotationPresets[tool.Annotations]; !ok {
			t.Errorf("tool %q: annotations %+v are not one of the presets in tools_annotations.go", tool.Name, tool.Annotations)
		}
	}
}

// TestAnnotationsAreInternallyConsistent catches preset/tool pairings that
// contradict themselves or the tool's own name.
func TestAnnotationsAreInternallyConsistent(t *testing.T) {
	for _, tool := range toolRegistry {
		a := tool.Annotations
		if a.ReadOnlyHint && a.DestructiveHint {
			t.Errorf("tool %q: readOnlyHint and destructiveHint are both true", tool.Name)
		}
		if a.ReadOnlyHint && !a.IdempotentHint {
			t.Errorf("tool %q: a read-only tool is idempotent by definition", tool.Name)
		}
		// A delete-* tool that does not admit to being destructive is the
		// exact mislabelling these annotations exist to prevent.
		if strings.HasPrefix(tool.Name, "delete-") && !a.DestructiveHint {
			t.Errorf("tool %q: delete tools must set destructiveHint", tool.Name)
		}
		if strings.HasPrefix(tool.Name, "list-") && !a.ReadOnlyHint {
			t.Errorf("tool %q: list tools must set readOnlyHint", tool.Name)
		}
	}
}

// TestAnnotationsWireFormat verifies all four hints are serialized on every
// tool. Omitting a false hint is not equivalent: per the MCP spec an absent
// destructiveHint/openWorldHint defaults to true.
func TestAnnotationsWireFormat(t *testing.T) {
	raw, err := json.Marshal(map[string]interface{}{"tools": toolRegistry})
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	var doc struct {
		Tools []struct {
			Name        string                     `json:"name"`
			Annotations map[string]json.RawMessage `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if len(doc.Tools) != len(toolRegistry) {
		t.Fatalf("serialized %d tools, registry has %d", len(doc.Tools), len(toolRegistry))
	}
	want := []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"}
	for _, tool := range doc.Tools {
		for _, hint := range want {
			if _, ok := tool.Annotations[hint]; !ok {
				t.Errorf("tool %q: annotations missing %q on the wire", tool.Name, hint)
			}
		}
	}
}

// TestKnownToolAnnotations pins the classification of a few representative
// tools, so a bulk re-annotation cannot quietly downgrade delete-process to
// something a client would auto-approve.
func TestKnownToolAnnotations(t *testing.T) {
	want := map[string]mcpToolAnnotations{
		"delete-process":  annDestructiveRemote,
		"delete-variable": annDestructiveRemote,
		"push-process":    annDestructiveRemote,
		"list-folders":    annReadOnlyRemote,
		"lint-process":    annReadOnlyLocal,
		"layout-process":  annDestructiveLocal,
		"create-process":  annCreateRemote,
	}

	byName := make(map[string]mcpTool, len(toolRegistry))
	for _, tool := range toolRegistry {
		byName[tool.Name] = tool
	}
	for name, expected := range want {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("tool %q not found in registry", name)
			continue
		}
		if tool.Annotations != expected {
			t.Errorf("tool %q: annotations = %+v, want %+v", name, tool.Annotations, expected)
		}
	}
}
