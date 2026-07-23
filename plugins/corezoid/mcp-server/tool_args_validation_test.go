package main

import (
	"context"
	"strings"
	"testing"
)

// ---- strict argument validation ---------------------------------------------

func TestHandleToolCall_UnknownArgumentRejected(t *testing.T) {
	resetGlobals(t)
	withAuthLock(func() {
		apiToken = "test-token"
		accountURL = "https://account.example"
		stageID = 1
	})
	result, isErr := handleToolCall(context.Background(), "create-process", map[string]interface{}{
		"process_name": "x",
		"bogus_arg":    1,
	})
	if !isErr {
		t.Fatal("expected isError=true for an unknown argument")
	}
	if !strings.Contains(result, "bogus_arg") || !strings.Contains(result, "accepted:") {
		t.Errorf("error must name the unknown argument and list accepted ones, got: %s", result)
	}
}

func TestUnknownArgsError_KnownArgsPass(t *testing.T) {
	if msg := unknownArgsError("create-process", map[string]interface{}{
		"process_name": "x", "folder_path": ".", "folder_id": 42,
	}); msg != "" {
		t.Errorf("declared arguments must pass validation, got: %s", msg)
	}
}

func TestUnknownArgsError_EveryToolHasSchema(t *testing.T) {
	// Guard: every registry entry must expose a parseable properties map. An
	// InputSchema that isn't map[string]interface{} (or lacks "properties")
	// yields an EMPTY allowed set — the validator would then reject every
	// argument of that tool at runtime. So assert against the schema itself:
	// if the schema declares properties, the built set must contain them all.
	toolAllowedArgsOnce.Do(buildToolAllowedArgs)
	for _, tool := range toolRegistry {
		allowed := toolAllowedArgs[tool.Name]
		schema, ok := tool.InputSchema.(map[string]interface{})
		if !ok {
			t.Errorf("tool %s: InputSchema is %T, not map[string]interface{} — all its args would be rejected", tool.Name, tool.InputSchema)
			continue
		}
		props, _ := schema["properties"].(map[string]interface{})
		for k := range props {
			if !allowed[k] {
				t.Errorf("tool %s: declared property %q missing from allowed set", tool.Name, k)
			}
		}
		if len(props) != len(allowed) {
			t.Errorf("tool %s: %d declared properties but %d allowed args", tool.Name, len(props), len(allowed))
		}
	}
}

func TestSchemaRequiredList_BothShapes(t *testing.T) {
	if got := schemaRequiredList([]string{"a", "b"}); len(got) != 2 {
		t.Errorf("[]string shape: got %v", got)
	}
	if got := schemaRequiredList([]interface{}{"a", "b"}); len(got) != 2 {
		t.Errorf("[]interface{} shape (decoded JSON): got %v", got)
	}
	if got := schemaRequiredList(nil); got != nil {
		t.Errorf("nil: got %v", got)
	}
}
