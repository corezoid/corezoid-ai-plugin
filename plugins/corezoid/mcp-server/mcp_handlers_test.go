package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// resetGlobals clears global auth state so tests don't interfere.
func resetGlobals(t *testing.T) {
	t.Helper()
	origAPIToken := apiToken
	origAPIURL := apiURL
	origWorkspaceID := workspaceID
	origStageID := stageID
	origCachedProjectID := cachedProjectID
	origProjectIDEnv, hadProjectIDEnv := os.LookupEnv("COREZOID_PROJECT_ID")
	apiToken = ""
	apiURL = ""
	workspaceID = ""
	stageID = 0
	cachedProjectID = 0
	os.Unsetenv("COREZOID_PROJECT_ID") //nolint:errcheck
	t.Cleanup(func() {
		apiToken = origAPIToken
		apiURL = origAPIURL
		workspaceID = origWorkspaceID
		stageID = origStageID
		cachedProjectID = origCachedProjectID
		if hadProjectIDEnv {
			os.Setenv("COREZOID_PROJECT_ID", origProjectIDEnv) //nolint:errcheck
		} else {
			os.Unsetenv("COREZOID_PROJECT_ID") //nolint:errcheck
		}
	})
}

// ---- Unknown tool ----------------------------------------------------------

func TestHandleToolCall_UnknownTool(t *testing.T) {
	// Unknown tool hits ensureAuth first when no credentials — still an error.
	result, isErr := handleToolCall(context.Background(), "nonexistent-tool-xyz", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true for unknown tool")
	}
	_ = result
}

// ---- lint-process ----------------------------------------------------------

func TestHandleToolCall_LintProcess_MissingArg(t *testing.T) {
	// No process_path arg and no .conv.json in current dir.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "lint-process", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when no .conv.json present")
	}
	_ = result
}

func TestHandleToolCall_LintProcess_ValidFile(t *testing.T) {
	// Path-traversal hardening rejects absolute paths, so feed the lint via
	// a project-relative form. The sample lives at samples/valid_process.json
	// relative to this package's directory, which is also the test cwd.
	samplePath := filepath.Join("samples", "valid_process.json")
	if _, err := os.Stat(samplePath); err != nil {
		t.Skip("valid_process.json not found")
	}
	result, isErr := handleToolCall(context.Background(), "lint-process", map[string]interface{}{
		"process_path": samplePath,
	})
	if isErr {
		t.Errorf("expected success for valid process, got error: %q", result)
	}
}

// ---- push-process ----------------------------------------------------------

func TestHandleToolCall_PushProcess_MissingFile(t *testing.T) {
	resetGlobals(t)
	// Supply a non-existent path with valid filename format.
	result, isErr := handleToolCall(context.Background(), "push-process", map[string]interface{}{
		"process_path": "/nonexistent/99_process.conv.json",
	})
	if !isErr {
		t.Error("expected isError=true for missing file")
	}
	_ = result
}

func TestHandleToolCall_PushProcess_BadFilename(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	// File with no numeric prefix.
	p := filepath.Join(dir, "noid_process.conv.json")
	os.WriteFile(p, []byte(`{"scheme":{"nodes":[]}}`), 0644) //nolint:errcheck

	// Auth check fires before filename validation when credentials are missing.
	result, isErr := handleToolCall(context.Background(), "push-process", map[string]interface{}{
		"process_path": p,
	})
	if !isErr {
		t.Error("expected isError=true for filename without ID prefix")
	}
	_ = result
}

func TestHandlePushProcess_BlocksRpcReplyMismatch(t *testing.T) {
	resetGlobals(t)

	sample, err := os.ReadFile(filepath.Join("samples", "api_rpc_reply_mismatch.json"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "123_rpc_reply_mismatch.conv.json")
	if err := os.WriteFile(p, sample, 0644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(p),
	})
	if !isErr {
		t.Fatalf("expected push-process to block RpcReplyMismatches, got success: %q", result)
	}
	for _, want := range []string{
		"Push blocked: lint found",
		"API_RPC_REPLY MISMATCHES",
		`res_data key "status" has no matching res_data_type entry`,
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

func TestHandlePushProcess_GitCallAdvisoryDoesNotBlock(t *testing.T) {
	resetGlobals(t)
	p := writeGitConv(t, "git_call")

	orig, _ := os.Getwd()
	if err := os.Chdir(filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	calls := 0
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		return wrapOp(map[string]interface{}{
			"proc":        "error",
			"description": "reached API after git_call advisory",
		})
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(p),
	})
	if !isErr {
		t.Fatalf("expected the downstream mock API error, got success: %q", result)
	}
	if calls == 0 {
		t.Fatalf("git_call advisory blocked before the API was called; result:\n%s", result)
	}
	if strings.Contains(result, "Push blocked: lint found") {
		t.Fatalf("git_call must remain advisory-only without force=true; result:\n%s", result)
	}
	if !strings.Contains(result, "reached API after git_call advisory") {
		t.Fatalf("expected downstream API marker, got:\n%s", result)
	}
}

func TestHandlePushProcess_BlocksActiveStubMode(t *testing.T) {
	resetGlobals(t)

	sample, err := os.ReadFile(filepath.Join("samples", "stubbed_api_rpc.json"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "123_stubbed.conv.json")
	if err := os.WriteFile(p, sample, 0644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(p),
		"force":        true,
	})
	if !isErr {
		t.Fatalf("expected push-process to block active Stub Mode, got success: %q", result)
	}
	for _, want := range []string{
		"Push blocked: active Stub Mode found",
		"allow_active_stub_mode=true",
		"target stage is unknown",
		"ACTIVE STUB MODE NODES",
		"bypasses the real called process",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

func TestHandlePushProcess_BlocksActiveStubModeOnImmutableStage(t *testing.T) {
	resetGlobals(t)
	stageID = 999

	sample, err := os.ReadFile(filepath.Join("samples", "stubbed_api_rpc.json"))
	if err != nil {
		t.Fatal(err)
	}
	sample = []byte(strings.Replace(string(sample), `"parent_id": 1`, `"parent_id": 321`, 1))

	dir := t.TempDir()
	p := filepath.Join(dir, "123_stubbed.conv.json")
	if err := os.WriteFile(p, sample, 0644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	calls := 0
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		if len(ops) != 1 {
			t.Fatalf("expected one op per call, got %#v", ops)
		}
		op := ops[0]
		if op["type"] != "show" {
			t.Fatalf("expected only read-only show calls before Stub block, got %#v", op)
		}
		switch op["obj"] {
		case "folder":
			if id, _ := op["obj_id"].(float64); int(id) != 321 {
				t.Fatalf("expected policy to resolve stage from process parent_id 321, got show folder op %#v", op)
			}
			return wrapOp(map[string]interface{}{
				"proc":            "ok",
				"obj_id":          float64(321),
				"obj_type":        float64(0),
				"parent_obj_id":   float64(654),
				"parent_obj_type": "project",
			})
		case "stage":
			if id, _ := op["obj_id"].(float64); int(id) != 321 {
				t.Fatalf("expected stageInfo for parent stage 321, got %#v", op)
			}
			return wrapOp(map[string]interface{}{
				"proc":       "ok",
				"immutable":  true,
				"title":      "production",
				"short_name": "prod",
			})
		default:
			t.Fatalf("expected show folder or show stage, got %#v", op)
		}
		return wrapOp(map[string]interface{}{"proc": "error", "description": "unexpected op"})
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(p),
	})
	if !isErr {
		t.Fatalf("expected push-process to block active Stub Mode on immutable stage, got success: %q", result)
	}
	if calls != 2 {
		t.Fatalf("expected two read-only stage policy calls and no deploy mutations, got %d calls", calls)
	}
	for _, want := range []string{
		"Push blocked: active Stub Mode found",
		"stage 321",
		"immutable/read-only",
		"allow_active_stub_mode=true",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected result to contain %q, got:\n%s", want, result)
		}
	}
}

func TestHandlePushProcess_WarnsOnlyForDevelopStageStubMode(t *testing.T) {
	resetGlobals(t)
	stageID = 999

	sample, err := os.ReadFile(filepath.Join("samples", "stubbed_api_rpc.json"))
	if err != nil {
		t.Fatal(err)
	}
	sample = []byte(strings.Replace(string(sample), `"parent_id": 1`, `"parent_id": 321`, 1))

	dir := t.TempDir()
	p := filepath.Join(dir, "123_stubbed.conv.json")
	if err := os.WriteFile(p, sample, 0644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	calls := 0
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		if len(ops) == 1 && ops[0]["type"] == "show" && ops[0]["obj"] == "folder" {
			if id, _ := ops[0]["obj_id"].(float64); int(id) != 321 {
				return wrapOp(map[string]interface{}{"proc": "error", "description": "stopped after Stub warning"})
			}
			return wrapOp(map[string]interface{}{
				"proc":            "ok",
				"obj_id":          float64(321),
				"obj_type":        float64(0),
				"parent_obj_id":   float64(654),
				"parent_obj_type": "project",
			})
		}
		if len(ops) == 1 && ops[0]["type"] == "show" && ops[0]["obj"] == "stage" {
			return wrapOp(map[string]interface{}{
				"proc":       "ok",
				"immutable":  false,
				"title":      "develop",
				"short_name": "dev",
			})
		}
		return wrapOp(map[string]interface{}{"proc": "error", "description": "stopped after Stub warning"})
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(p),
	})
	if !isErr {
		t.Fatalf("expected downstream deploy error from mock API, got success: %q", result)
	}
	if calls < 3 {
		t.Fatalf("expected push-process to continue past Stub warning, got %d API call(s); result:\n%s", calls, result)
	}
	if strings.Contains(result, "Push blocked: active Stub Mode found") {
		t.Fatalf("develop stage should not return the Stub block message, got:\n%s", result)
	}
	if !strings.Contains(result, "stopped after Stub warning") {
		t.Fatalf("expected downstream mock API error, got:\n%s", result)
	}
}

func TestStageNameLooksProduction(t *testing.T) {
	for _, tc := range []struct {
		title     string
		shortName string
		want      bool
	}{
		{title: "production", shortName: "p", want: true},
		{title: "Release", shortName: "prod", want: true},
		{title: "prod old", shortName: "p-old", want: true},
		{title: "production mirror", shortName: "mirror", want: true},
		{title: "pre production", shortName: "pre", want: true},
		{title: "Product sandbox", shortName: "dev", want: false},
		{title: "develop", shortName: "dev", want: false},
	} {
		if got := stageNameLooksProduction(tc.title, tc.shortName); got != tc.want {
			t.Fatalf("stageNameLooksProduction(%q, %q) = %v, want %v", tc.title, tc.shortName, got, tc.want)
		}
	}
}

func TestResolveStageAndProjectFromFolder_WalksToStageRoot(t *testing.T) {
	resetGlobals(t)

	var seen []int
	srv, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) != 1 || ops[0]["type"] != "show" || ops[0]["obj"] != "folder" {
			t.Fatalf("expected show folder, got %#v", ops)
		}
		id, _ := ops[0]["obj_id"].(float64)
		seen = append(seen, int(id))
		switch int(id) {
		case 111:
			return wrapOp(map[string]interface{}{
				"proc":            "ok",
				"obj_id":          float64(111),
				"obj_type":        float64(0),
				"parent_obj_id":   float64(222),
				"parent_obj_type": "folder",
			})
		case 222:
			return wrapOp(map[string]interface{}{
				"proc":            "ok",
				"obj_id":          float64(222),
				"obj_type":        float64(0),
				"parent_obj_id":   float64(333),
				"parent_obj_type": "project",
			})
		default:
			return wrapOp(map[string]interface{}{"proc": "error", "description": "unexpected folder"})
		}
	})
	e.APIUrl = srv.URL
	e.WorkspaceID = "i260836082"
	e.Token = "test-token"

	stage, project, err := resolveStageAndProjectFromFolder(e, 111)
	if err != nil {
		t.Fatalf("resolveStageAndProjectFromFolder: %v", err)
	}
	if stage != 222 || project != 333 {
		t.Fatalf("resolved stage/project = %d/%d, want 222/333", stage, project)
	}
	if strings.Join([]string{strconv.Itoa(seen[0]), strconv.Itoa(seen[1])}, ",") != "111,222" {
		t.Fatalf("unexpected folder walk: %+v", seen)
	}
}

func TestHandlePushProcess_AllowStubModeContinuesPastStubGate(t *testing.T) {
	resetGlobals(t)

	sample, err := os.ReadFile(filepath.Join("samples", "stubbed_api_rpc.json"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "123_stubbed.conv.json")
	if err := os.WriteFile(p, sample, 0644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	calls := 0
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		return wrapOp(map[string]interface{}{"proc": "error", "description": "stopped after stub gate"})
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":           filepath.Base(p),
		"allow_active_stub_mode": true,
	})
	if !isErr {
		t.Fatalf("expected downstream deploy error from mock API, got success: %q", result)
	}
	if calls == 0 {
		t.Fatalf("expected push-process to continue to API after allow_active_stub_mode=true; result:\n%s", result)
	}
	if strings.Contains(result, "Push blocked: active Stub Mode found") {
		t.Fatalf("allow_active_stub_mode=true should not return the Stub block message, got:\n%s", result)
	}
	if !strings.Contains(result, "stopped after stub gate") {
		t.Fatalf("expected downstream mock API error, got:\n%s", result)
	}
}

func TestPushProcessToolSchema_DocumentsStubModeConfirmation(t *testing.T) {
	var pushTool *mcpTool
	for i := range toolRegistry {
		if toolRegistry[i].Name == "push-process" {
			pushTool = &toolRegistry[i]
			break
		}
	}
	if pushTool == nil {
		t.Fatal("push-process tool not found")
	}
	if !strings.Contains(pushTool.Description, "allow_active_stub_mode=true") {
		t.Fatalf("expected push-process description to mention allow_active_stub_mode=true, got:\n%s", pushTool.Description)
	}

	inputSchema, ok := pushTool.InputSchema.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected push-process input schema: %#v", pushTool.InputSchema)
	}
	schema, ok := inputSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected push-process input schema: %#v", pushTool.InputSchema)
	}
	rawAllow, ok := schema["allow_active_stub_mode"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected allow_active_stub_mode property in push-process schema, got %#v", schema)
	}
	desc, _ := rawAllow["description"].(string)
	for _, want := range []string{"Stub Mode", "obj_type:4", "temporary mock replies"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("expected allow_active_stub_mode description to contain %q, got:\n%s", want, desc)
		}
	}
}

// ---- pull-process ----------------------------------------------------------

func TestHandleToolCall_PullProcess_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "pull-process", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when process_id missing")
	}
	_ = result
}

// ---- pull-folder -----------------------------------------------------------

func TestHandleToolCall_PullFolder_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "pull-folder", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when folder_id missing")
	}
	_ = result
}

// ---- create-folder ---------------------------------------------------------

func TestHandleToolCall_CreateFolder_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "create-folder", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when folder_name missing")
	}
	_ = result
}

func TestHandleToolCall_CreateFolder_NoFolderFile(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	result, isErr := handleToolCall(context.Background(), "create-folder", map[string]interface{}{
		"parent_path": dir,
		"folder_name": "test",
	})
	if !isErr {
		t.Error("expected isError=true when no folder.json in dir")
	}
	_ = result
}

// ---- create-process --------------------------------------------------------

func TestHandleToolCall_CreateProcess_NoFolderFile(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	result, isErr := handleToolCall(context.Background(), "create-process", map[string]interface{}{
		"folder_path":  dir,
		"process_name": "test-process",
	})
	if !isErr {
		t.Error("expected isError=true when no folder.json in dir")
	}
	_ = result
}

// ---- create-variable -------------------------------------------------------

func TestHandleToolCall_CreateVariable_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "create-variable", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when stage_id missing")
	}
	_ = result
}

// ---- create-alias ----------------------------------------------------------

func TestHandleToolCall_CreateAlias_NoStageID(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "123_proc.conv.json")
	os.WriteFile(p, []byte(`{}`), 0644) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "create-alias", map[string]interface{}{
		"process_path": p,
		"short_name":   "my-alias",
	})
	if !isErr {
		t.Error("expected isError=true when stageID is 0 or no credentials")
	}
	_ = result
}

func TestHandleToolCall_CreateAlias_BadFilename(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "noprefix.conv.json")
	os.WriteFile(p, []byte(`{}`), 0644) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "create-alias", map[string]interface{}{
		"process_path": p,
		"short_name":   "alias",
	})
	if !isErr {
		t.Error("expected isError=true for bad filename")
	}
	_ = result
}

// TestHandleToolCall_CreateAlias_StageMismatchHint verifies that when the
// server returns "Object is not in stage" (the exact failure mode from
// issue #26), the tool surfaces a hint pointing the user at pull-process so
// the file's parent_id gets refreshed. stage_id is no longer an accepted
// argument — the LLM never supplies it — so the fallback path (marker's
// stage_id) is what triggers the mismatch here.
func TestHandleToolCall_CreateAlias_StageMismatchHint(t *testing.T) {
	resetGlobals(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Fail every op with the exact server phrase we want to detect.
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":        "error",
				"description": "Object is not in stage",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	origAccount := accountURL
	accountURL = srv.URL
	t.Cleanup(func() { accountURL = origAccount })

	apiURL = srv.URL
	apiToken = "test-token"
	workspaceID = "1"
	stageID = 9026 // frozen (wrong) env stage — the bug from the issue

	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	p := filepath.Join(dir, "123_proc.conv.json")
	os.WriteFile(p, []byte(`{"obj_id":123}`), 0644) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "create-alias", map[string]interface{}{
		"process_path": "123_proc.conv.json",
		"short_name":   "my-alias",
	})
	if !isErr {
		t.Fatalf("expected isError=true when server rejects with 'Object is not in stage', got %q", result)
	}
	if !strings.Contains(result, "Object is not in stage") {
		t.Errorf("expected server error to be surfaced, got %q", result)
	}
	if !strings.Contains(result, "stage 9026") {
		t.Errorf("expected hint mentioning the stage the tool attempted (9026), got %q", result)
	}
	if !strings.Contains(result, "Pull-process this file again") {
		t.Errorf("expected pull-process remediation hint, got %q", result)
	}
}

// TestHandleToolCall_CreateAlias_UsesParentIDFromFile verifies that the tool
// walks the process's parent_id chain (via ShowFolder) to find the correct
// stage instead of blindly using COREZOID_STAGE_ID. This is the core fix for
// issue #26.
func TestHandleToolCall_CreateAlias_UsesParentIDFromFile(t *testing.T) {
	resetGlobals(t)

	// Mock server that answers ShowFolder + create_alias flows.
	// parent_id 555 → stage 10605 (obj_type 3), and create_alias returns ok
	// only when stage_id in the payload is 10605.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")

		opsOut := make([]interface{}, 0, len(body.Ops))
		for _, op := range body.Ops {
			switch op["obj"] {
			case "folder":
				id, _ := op["obj_id"].(float64)
				switch int(id) {
				case 555: // subfolder → parent = stage
					opsOut = append(opsOut, map[string]interface{}{
						"proc":            "ok",
						"obj_id":          float64(555),
						"obj_type":        float64(0),
						"parent_obj_id":   float64(10605),
						"parent_obj_type": "folder",
					})
				case 10605: // the correct stage
					opsOut = append(opsOut, map[string]interface{}{
						"proc":            "ok",
						"obj_id":          float64(10605),
						"obj_type":        float64(3),
						"parent_obj_id":   float64(7000),
						"parent_obj_type": "project",
					})
				default:
					// GetProjectIDByStageID walk (called by CreateAlias) also lands here.
					opsOut = append(opsOut, map[string]interface{}{
						"proc":            "ok",
						"obj_id":          id,
						"obj_type":        float64(3),
						"parent_obj_id":   float64(7000),
						"parent_obj_type": "project",
					})
				}
			case "alias":
				// Reject if the request tries to create in the wrong stage.
				stage, _ := op["stage_id"].(float64)
				if int(stage) != 10605 {
					opsOut = append(opsOut, map[string]interface{}{
						"proc":        "error",
						"description": "Object is not in stage",
					})
					continue
				}
				opsOut = append(opsOut, map[string]interface{}{
					"proc":   "ok",
					"obj_id": float64(9999),
				})
			default:
				opsOut = append(opsOut, map[string]interface{}{"proc": "ok"})
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"request_proc": "ok",
			"ops":          opsOut,
		})
	}))
	t.Cleanup(srv.Close)

	origAccount := accountURL
	accountURL = srv.URL
	t.Cleanup(func() { accountURL = origAccount })

	apiURL = srv.URL
	apiToken = "test-token"
	workspaceID = "1"
	stageID = 9026 // wrong env stage — the frozen value from the issue

	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)                        //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	// parent_id 555 → stage 10605 (per the mock).
	p := filepath.Join(dir, "123_proc.conv.json")
	os.WriteFile(p, []byte(`{"obj_id":123,"parent_id":555}`), 0644) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "create-alias", map[string]interface{}{
		"process_path": "123_proc.conv.json",
		"short_name":   "my-alias",
	})
	if isErr {
		t.Fatalf("expected success; alias should have been created in derived stage 10605, got error: %q", result)
	}
	if !strings.Contains(result, "AliasID: 9999") {
		t.Errorf("expected AliasID in success message, got %q", result)
	}
	if !strings.Contains(result, "stage 10605") {
		t.Errorf("expected derived stage 10605 in success message, got %q", result)
	}
}

// ---- modify-task / delete-task argument validation -------------------------

func TestHandleToolCall_ModifyTask_MissingProcessID(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "modify-task", map[string]interface{}{
		"data": `{}`,
	})
	if !isErr {
		t.Error("expected isError=true when process_id missing")
	}
	_ = result
}

func TestHandleToolCall_ModifyTask_MissingRefAndTaskID(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "modify-task", map[string]interface{}{
		"process_id": float64(123),
		"data":       `{}`,
	})
	if !isErr {
		t.Error("expected isError=true when both ref and task_id missing")
	}
	_ = result
}

func TestHandleToolCall_ModifyTask_BadDataJSON(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "modify-task", map[string]interface{}{
		"process_id": float64(123),
		"task_id":    "abc",
		"data":       `not-json`,
	})
	if !isErr {
		t.Error("expected isError=true for bad data JSON")
	}
	_ = result
}

// ---- deepMerge unit tests ---------------------------------------------------

func TestDeepMerge_ShallowScalar(t *testing.T) {
	dst := map[string]interface{}{"a": 1, "b": 2}
	src := map[string]interface{}{"b": 99, "c": 3}
	got := deepMerge(dst, src)
	if got["a"] != 1 || got["b"] != 99 || got["c"] != 3 {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestDeepMerge_NestedObjectPreservesExistingKeys(t *testing.T) {
	dst := map[string]interface{}{
		"currencies": map[string]interface{}{
			"count": 5, "ms": 100, "seconds": 1, "value": 42, "Requests ": 10,
		},
	}
	src := map[string]interface{}{
		"currencies": map[string]interface{}{
			"usd": 1, "eur": 2, "gbp": 3, "jpy": 4,
		},
	}
	got := deepMerge(dst, src)
	cur, ok := got["currencies"].(map[string]interface{})
	if !ok {
		t.Fatalf("currencies not a map: %T", got["currencies"])
	}
	// original keys must survive
	for _, k := range []string{"count", "ms", "seconds", "value", "Requests "} {
		if _, exists := cur[k]; !exists {
			t.Errorf("key %q was lost after deep merge", k)
		}
	}
	// new keys must be present
	for _, k := range []string{"usd", "eur", "gbp", "jpy"} {
		if _, exists := cur[k]; !exists {
			t.Errorf("new key %q missing after deep merge", k)
		}
	}
}

func TestDeepMerge_NestedObjectScalarOverwrites(t *testing.T) {
	// When src has a scalar at a key that dst has a map, src wins.
	dst := map[string]interface{}{"x": map[string]interface{}{"a": 1}}
	src := map[string]interface{}{"x": "flat"}
	got := deepMerge(dst, src)
	if got["x"] != "flat" {
		t.Errorf("expected scalar overwrite, got %v", got["x"])
	}
}

func TestDeepMerge_DoesNotMutateDst(t *testing.T) {
	dst := map[string]interface{}{"a": map[string]interface{}{"k": 1}}
	src := map[string]interface{}{"a": map[string]interface{}{"k": 2}}
	_ = deepMerge(dst, src)
	inner := dst["a"].(map[string]interface{})
	if inner["k"] != 1 {
		t.Error("deepMerge mutated dst")
	}
}

func TestHandleToolCall_DeleteTask_MissingRefAndTaskID(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "delete-task", map[string]interface{}{
		"process_id": float64(123),
	})
	if !isErr {
		t.Error("expected isError=true when both ref and task_id missing")
	}
	_ = result
}

// ---- list-task-history / list-node-tasks argument validation ---------------

func TestHandleToolCall_ListTaskHistory_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "list-task-history", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when process_id missing")
	}
	_ = result
}

func TestHandleToolCall_ListNodeTasks_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "list-node-tasks", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when process_id missing")
	}
	_ = result
}

// ---- add-chart / modify-chart / get-chart ----------------------------------

func TestHandleToolCall_AddChart_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "add-chart", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when args missing")
	}
	_ = result
}

func TestHandleToolCall_AddChart_BadSeriesJSON(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "add-chart", map[string]interface{}{
		"dashboard_id": float64(1),
		"name":         "chart",
		"chart_type":   "line",
		"series":       "not-json",
	})
	if !isErr {
		t.Error("expected isError=true for bad series JSON")
	}
	_ = result
}

func TestHandleToolCall_ModifyChart_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "modify-chart", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when args missing")
	}
	_ = result
}

func TestHandleToolCall_GetChart_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "get-chart", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when args missing")
	}
	_ = result
}

// ---- set-dashboard-layout --------------------------------------------------

func TestHandleToolCall_SetDashboardLayout_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "set-dashboard-layout", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when args missing")
	}
	_ = result
}

func TestHandleToolCall_SetDashboardLayout_BadGrid(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "set-dashboard-layout", map[string]interface{}{
		"dashboard_id": float64(1),
		"grid":         "not-json",
	})
	if !isErr {
		t.Error("expected isError=true for bad grid JSON")
	}
	_ = result
}

func TestHandleToolCall_SetDashboardLayout_MissingChartID(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "set-dashboard-layout", map[string]interface{}{
		"dashboard_id": float64(1),
		"grid":         `[{"x":0,"y":0,"width":1,"height":1}]`,
	})
	if !isErr {
		t.Error("expected isError=true for grid entry without chart_id")
	}
	_ = result
}

// ---- list-projects / list-stages argument validation -----------------------

func TestHandleToolCall_ListProjects_MissingArg(t *testing.T) {
	resetGlobals(t)
	// Missing company_id.
	result, isErr := handleToolCall(context.Background(), "list-projects", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when company_id missing")
	}
	_ = result
}

func TestHandleToolCall_ListStages_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "list-stages", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when project_id missing")
	}
	_ = result
}

// ---- run-task argument validation ------------------------------------------

func TestHandleToolCall_RunTask_BadFilename(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "noid.conv.json")
	os.WriteFile(p, []byte(`{}`), 0644) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "run-task", map[string]interface{}{
		"process_path": p,
		"data":         `{}`,
	})
	if !isErr {
		t.Error("expected isError=true for bad filename")
	}
	_ = result
}

func TestHandleToolCall_RunTask_MissingData(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "123_proc.conv.json")
	os.WriteFile(p, []byte(`{}`), 0644) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "run-task", map[string]interface{}{
		"process_path": p,
	})
	if !isErr {
		t.Error("expected isError=true when data missing")
	}
	_ = result
}

func TestHandleRunTask_UsesDeployedProcessWithoutRedeploy(t *testing.T) {
	resetGlobals(t)
	var operations []string
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) != 1 {
			return map[string]interface{}{"request_proc": "error", "description": "unexpected operation count"}
		}
		obj, _ := ops[0]["obj"].(string)
		typ, _ := ops[0]["type"].(string)
		operations = append(operations, obj+":"+typ)
		switch obj + ":" + typ {
		case "conv:list":
			return map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok",
					"list": []interface{}{map[string]interface{}{
						"scheme": map[string]interface{}{"nodes": []interface{}{
							map[string]interface{}{
								"id": "server-final", "title": "Final", "obj_type": float64(2),
								"extra": `{"icon":""}`,
							},
						}},
					}},
				}},
			}
		case "task:create":
			return map[string]interface{}{
				"request_proc": "ok",
				"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
			}
		case "task:show":
			return map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok", "obj_id": "task-1", "node_id": "server-final",
					"data": map[string]interface{}{"result": "ok"},
				}},
			}
		default:
			return map[string]interface{}{"request_proc": "error", "description": "unexpected mutating operation"}
		}
	})
	setProjectAuth(t, srv.URL)

	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "123_runtime.conv.json")
	localDraft := []byte(`this intentionally is not a deployable process`)
	if err := os.WriteFile(path, localDraft, 0644); err != nil {
		t.Fatal(err)
	}
	originalFirstPoll := runTaskFirstPollAfter
	originalPollEvery := runTaskPollEvery
	runTaskFirstPollAfter = time.Millisecond
	runTaskPollEvery = time.Millisecond
	t.Cleanup(func() {
		runTaskFirstPollAfter = originalFirstPoll
		runTaskPollEvery = originalPollEvery
	})

	result, isErr := handleRunTask(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "data": `{}`, "ref": "read-only-run", "wait_sec": 1,
	})
	if isErr || !strings.Contains(result, "Task completed") || !strings.Contains(result, "NodeName: Final") {
		t.Fatalf("read-only task run failed: %s", result)
	}
	if got := strings.Join(operations, ","); got != "conv:list,task:create,task:show" {
		t.Fatalf("run-task issued unexpected operations: %s", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(localDraft) {
		t.Fatalf("run-task modified the local process file: %q", after)
	}
}

func TestLoadRuntimeNodeMap_FallsBackToReadOnlyExport(t *testing.T) {
	var srv *httptest.Server
	var requests []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]interface{}{map[string]interface{}{
				"obj_id": float64(123),
				"scheme": map[string]interface{}{"nodes": []interface{}{
					map[string]interface{}{
						"id": "exported-error", "title": "Failed", "obj_type": float64(2),
						"extra": `{"icon":"error"}`,
					},
				}},
			}})
			return
		}
		var body struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/api/2/download" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok", "download_url": srv.URL + "/runtime-process.json",
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"request_proc": "error"})
	}))
	t.Cleanup(srv.Close)

	v := &Executor{
		Ctx: context.Background(), APIUrl: srv.URL, Token: "test-token",
		WorkspaceID: "workspace", ProcessID: 123, NodeIDMap: map[string]NodeInfo{},
	}
	if err := loadRuntimeNodeMap(v); err != nil {
		t.Fatal(err)
	}
	node, ok := v.NodeIDMap["exported-error"]
	if !ok || node.Type != 2 || node.Name != "Failed" || node.Icon != "error" {
		t.Fatalf("unexpected exported runtime node: %+v", v.NodeIDMap)
	}
	if got := strings.Join(requests, ","); got != "POST /api/2/json,POST /api/2/download,GET /runtime-process.json" {
		t.Fatalf("unexpected runtime metadata requests: %s", got)
	}
}

// ---- get-dashboard ---------------------------------------------------------

func TestHandleToolCall_GetDashboard_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "get-dashboard", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when args missing")
	}
	_ = result
}

// ---- create-dashboard ------------------------------------------------------

func TestHandleToolCall_CreateDashboard_MissingArg(t *testing.T) {
	resetGlobals(t)
	result, isErr := handleToolCall(context.Background(), "create-dashboard", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when title missing")
	}
	_ = result
}
