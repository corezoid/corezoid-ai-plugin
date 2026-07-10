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
	// Guard: every registry entry must expose a properties map, otherwise the
	// validator would reject all arguments of that tool.
	toolAllowedArgsOnce.Do(buildToolAllowedArgs)
	for _, tool := range toolRegistry {
		if _, ok := toolAllowedArgs[tool.Name]; !ok {
			t.Errorf("tool %s missing from allowed-args map", tool.Name)
		}
	}
}
