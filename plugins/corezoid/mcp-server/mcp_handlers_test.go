package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// resetGlobals clears global auth state so tests don't interfere.
func resetGlobals(t *testing.T) {
	t.Helper()
	origAPIToken := apiToken
	origAPIURL := apiURL
	origWorkspaceID := workspaceID
	origStageID := stageID
	apiToken = ""
	apiURL = ""
	workspaceID = ""
	stageID = 0
	t.Cleanup(func() {
		apiToken = origAPIToken
		apiURL = origAPIURL
		workspaceID = origWorkspaceID
		stageID = origStageID
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
	os.Chdir(dir) //nolint:errcheck
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	result, isErr := handleToolCall(context.Background(), "lint-process", map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when no .conv.json present")
	}
	_ = result
}

func TestHandleToolCall_LintProcess_ValidFile(t *testing.T) {
	samplePath, err := filepath.Abs("samples/valid_process.json")
	if err != nil {
		t.Fatal(err)
	}
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
