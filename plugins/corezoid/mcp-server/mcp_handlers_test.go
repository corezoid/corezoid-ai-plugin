package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
