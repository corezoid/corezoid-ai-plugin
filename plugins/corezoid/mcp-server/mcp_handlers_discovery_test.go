package main

import (
	"context"
	"strings"
	"testing"
)

// setProjectAuth points the global auth state at a mock server. Callers must
// have invoked resetGlobals to register the cleanup that restores them.
func setProjectAuth(t *testing.T, srvURL string) {
	t.Helper()
	apiURL = srvURL
	apiToken = "test-token"
	workspaceID = "i260836082"
}

// ---- create-project --------------------------------------------------------

func TestHandleToolCall_CreateProject_OK(t *testing.T) {
	resetGlobals(t)

	var captured []map[string]interface{}
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{
					"proc":   "ok",
					"obj":    "project",
					"obj_id": float64(316469),
					"stages": []interface{}{float64(316470), float64(316471)},
				},
			},
		}
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handleToolCall(context.Background(), "create-project", map[string]interface{}{
		"company_id":  "i260836082",
		"title":       "Test",
		"short_name":  "test",
		"description": "demo",
		"stages":      `[{"title":"production","immutable":true},{"title":"develop","immutable":false}]`,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "project_id=316469") {
		t.Errorf("missing project_id in output: %s", result)
	}
	if !strings.Contains(result, "316470") || !strings.Contains(result, "316471") {
		t.Errorf("missing stage IDs in output: %s", result)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 captured op, got %d", len(captured))
	}
	op := captured[0]
	for k, want := range map[string]interface{}{
		"type":        "create",
		"obj":         "project",
		"company_id":  "i260836082",
		"title":       "Test",
		"short_name":  "test",
		"description": "demo",
	} {
		if got, ok := op[k]; !ok || got != want {
			t.Errorf("op[%s] = %v, want %v", k, got, want)
		}
	}
	stages, ok := op["stages"].([]interface{})
	if !ok || len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %v", op["stages"])
	}
	first, _ := stages[0].(map[string]interface{})
	if first["title"] != "production" || first["immutable"] != true {
		t.Errorf("first stage = %v, want production/immutable=true", first)
	}
}

func TestHandleToolCall_CreateProject_MissingCompanyID(t *testing.T) {
	resetGlobals(t)
	apiToken = "test-token" // bypass token gate so the arg check fires
	result, isErr := handleToolCall(context.Background(), "create-project", map[string]interface{}{
		"title": "Test",
	})
	if !isErr {
		t.Errorf("expected isError=true, got %q", result)
	}
}

func TestHandleToolCall_CreateProject_BadStagesJSON(t *testing.T) {
	resetGlobals(t)
	apiToken = "test-token"
	result, isErr := handleToolCall(context.Background(), "create-project", map[string]interface{}{
		"company_id": "i260836082",
		"title":      "Test",
		"stages":     "not-json",
	})
	if !isErr {
		t.Errorf("expected isError=true, got %q", result)
	}
}

// ---- modify-project --------------------------------------------------------

func TestHandleToolCall_ModifyProject_OK(t *testing.T) {
	resetGlobals(t)

	var captured []map[string]interface{}
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{"proc": "ok", "obj": "project", "obj_id": float64(316469)},
			},
		}
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handleToolCall(context.Background(), "modify-project", map[string]interface{}{
		"company_id": "i260836082",
		"project_id": float64(316469),
		"title":      "Renamed",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "Project #316469 updated") || !strings.Contains(result, `title="Renamed"`) {
		t.Errorf("unexpected output: %s", result)
	}
	op := captured[0]
	if op["type"] != "modify" || op["obj"] != "project" {
		t.Errorf("op type/obj = %v/%v", op["type"], op["obj"])
	}
	if op["title"] != "Renamed" {
		t.Errorf("op title = %v", op["title"])
	}
	// fields that weren't supplied must not be sent
	if _, present := op["short_name"]; present {
		t.Errorf("short_name should be absent, got %v", op["short_name"])
	}
}

func TestHandleToolCall_ModifyProject_NoFields(t *testing.T) {
	resetGlobals(t)
	apiToken = "test-token"
	result, isErr := handleToolCall(context.Background(), "modify-project", map[string]interface{}{
		"company_id": "i260836082",
		"project_id": float64(316469),
	})
	if !isErr {
		t.Errorf("expected isError=true when no fields supplied, got %q", result)
	}
}

// ---- delete-project --------------------------------------------------------

func TestHandleToolCall_DeleteProject_OK(t *testing.T) {
	resetGlobals(t)

	var captured []map[string]interface{}
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok", "obj": "project", "obj_id": float64(316469)}},
		}
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handleToolCall(context.Background(), "delete-project", map[string]interface{}{
		"company_id": "i260836082",
		"project_id": float64(316469),
	})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "Project #316469 moved to Trash.") {
		t.Errorf("unexpected output: %s", result)
	}
	op := captured[0]
	if op["type"] != "delete" || op["obj"] != "project" {
		t.Errorf("op type/obj = %v/%v", op["type"], op["obj"])
	}
}

func TestHandleToolCall_DeleteProject_MissingProjectID(t *testing.T) {
	resetGlobals(t)
	apiToken = "test-token"
	result, isErr := handleToolCall(context.Background(), "delete-project", map[string]interface{}{
		"company_id": "i260836082",
	})
	if !isErr {
		t.Errorf("expected isError=true, got %q", result)
	}
}

// ---- show-project ----------------------------------------------------------

func TestHandleToolCall_ShowProject_OK(t *testing.T) {
	resetGlobals(t)

	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{
					"proc":            "ok",
					"obj":             "project",
					"obj_id":          float64(316469),
					"obj_short_name":  "test",
					"parent_obj_id":   float64(51948),
					"parent_obj_type": "folder",
					"stages": []interface{}{
						map[string]interface{}{
							"obj_id":         float64(316470),
							"title":          "production",
							"obj_short_name": "production",
						},
						map[string]interface{}{
							"obj_id":         float64(316471),
							"title":          "develop",
							"obj_short_name": "develop",
						},
					},
				},
			},
		}
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handleToolCall(context.Background(), "show-project", map[string]interface{}{
		"company_id": "i260836082",
		"project_id": float64(316469),
	})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	for _, want := range []string{
		"Project #316469",
		`short_name="test"`,
		"folder#51948",
		"316470",
		"production",
		"316471",
		"develop",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("output missing %q: %s", want, result)
		}
	}
}
