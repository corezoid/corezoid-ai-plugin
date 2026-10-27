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
		// Set explicitly: without it ensureAuth derives api_url over the network
		// (see ensureAPIURL) and this test never reaches argument validation.
		apiURL = "https://api.example"
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

func TestApplyArgAliases_ConvID(t *testing.T) {
	in := map[string]interface{}{"conv_id": float64(5), "node_id": "n"}
	out, msg := applyArgAliases("list-node-tasks", in)
	if msg != "" || out["process_id"] != float64(5) || out["conv_id"] != nil {
		t.Errorf("conv_id must become process_id, got %v (%s)", out, msg)
	}
	if _, still := in["conv_id"]; !still {
		t.Error("caller's args must not be mutated")
	}
	if msg := unknownArgsError("list-node-tasks", out); msg != "" {
		t.Errorf("aliased args must pass validation, got: %s", msg)
	}

	if _, msg := applyArgAliases("show-task", map[string]interface{}{"conv_id": 5, "process_id": 6}); !strings.Contains(msg, "different values") {
		t.Errorf("conflicting conv_id/process_id must be rejected, got %q", msg)
	}
	out, msg = applyArgAliases("show-task", map[string]interface{}{"conv_id": float64(5), "process_id": float64(5)})
	if msg != "" || out["process_id"] != float64(5) || out["conv_id"] != nil {
		t.Errorf("matching conv_id/process_id must collapse to process_id, got %v (%s)", out, msg)
	}

	// A tool that declares no process_id keeps rejecting conv_id as unknown.
	out, _ = applyArgAliases("login", map[string]interface{}{"conv_id": 5})
	if out["conv_id"] == nil {
		t.Error("alias must not apply to tools without process_id")
	}
}

// The first create-folder call of a session used to name the parent
// parent_folder_id and the folder title, and was spent on an unknown-argument
// error. Those names now reach the handler as folder_id and folder_name.
func TestApplyArgAliases_CreateFolder(t *testing.T) {
	for _, in := range []map[string]interface{}{
		{"parent_folder_id": float64(7), "title": "Orders", "description": "d"},
		{"parent_id": float64(7), "name": "Orders"},
	} {
		out, msg := applyArgAliases("create-folder", in)
		if msg != "" || out["folder_id"] != float64(7) || out["folder_name"] != "Orders" {
			t.Errorf("%v: want folder_id=7 folder_name=Orders, got %v (%s)", in, out, msg)
		}
		if msg := unknownArgsError("create-folder", out); msg != "" {
			t.Errorf("%v: aliased args must pass validation, got: %s", in, msg)
		}
	}

	if _, msg := applyArgAliases("create-folder", map[string]interface{}{"parent_folder_id": 7, "folder_id": 8, "folder_name": "x"}); !strings.Contains(msg, "different values") {
		t.Errorf("conflicting parent_folder_id/folder_id must be rejected, got %q", msg)
	}

	// title is modify-folder's own argument, not an alias there.
	out, _ := applyArgAliases("modify-folder", map[string]interface{}{"folder_id": 1, "title": "x"})
	if out["title"] != "x" || out["folder_name"] != nil {
		t.Errorf("create-folder aliases must not leak into modify-folder, got %v", out)
	}
}

func TestHandleToolCall_CreateFolderAliasesThroughRouter(t *testing.T) {
	resetGlobals(t)
	withAuthLock(func() {
		apiToken = "test-token"
		accountURL = "https://account.example"
		apiURL = "https://api.example"
		stageID = 1
	})
	// An empty parent_path directory has no folder marker, so the handler
	// fails on target resolution — after argument validation, which is the
	// part under test.
	result, _ := handleToolCall(context.Background(), "cz-structure", map[string]interface{}{
		"action": "create-folder",
		"args": map[string]interface{}{
			"parent_path": t.TempDir(),
			"title":       "Orders",
			"description": "order processes",
		},
	})
	if strings.Contains(result, "unknown argument") {
		t.Errorf("title/description must be accepted by create-folder, got: %s", result)
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
	for _, tool := range append(allToolDefs(), routerToolDefs()...) {
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

// Some clients fill a call to a tool with no required arguments with a dummy
// "random_string" argument; rejecting it cost every init a failed login (#229).
func TestUnknownArgsError_PlaceholderIgnoredWithoutRequiredArgs(t *testing.T) {
	if msg := unknownArgsError("login", map[string]interface{}{"random_string": "x"}); msg != "" {
		t.Errorf("random_string placeholder must be accepted for login, got: %s", msg)
	}
	// A tool with required arguments still rejects it: there the placeholder
	// stands in for an argument the call is actually missing.
	if msg := unknownArgsError("create-process", map[string]interface{}{"process_name": "x", "random_string": "x"}); msg == "" {
		t.Error("random_string must still be rejected for a tool with required arguments")
	}
	// Any other invented name is still an error.
	if msg := unknownArgsError("login", map[string]interface{}{"service": "corezoid"}); msg == "" {
		t.Error("unknown arguments other than the placeholder must still be rejected for login")
	}
}

// git-push-context accepts "message" as an alias for "commit_message" — models
// reach for git's name first, and rejecting it cost every push a retry (#218).
func TestGitPushContext_MessageAlias(t *testing.T) {
	if msg := unknownArgsError("git-push-context", map[string]interface{}{"message": "docs: x"}); msg != "" {
		t.Errorf("message must be accepted for git-push-context, got: %s", msg)
	}
	if got := gitPushCommitMessage(map[string]interface{}{"message": "docs: x"}); got != "docs: x" {
		t.Errorf("message alias: got %q", got)
	}
	if got := gitPushCommitMessage(map[string]interface{}{"commit_message": "a", "message": "b"}); got != "a" {
		t.Errorf("commit_message must win over message: got %q", got)
	}
}
