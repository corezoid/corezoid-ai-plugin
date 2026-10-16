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

// ---- company_id defaulting --------------------------------------------------

// The structure actions must not demand company_id once login has selected a
// workspace: omitted or empty, it falls back to that workspace, and the
// workspace_id synonym (the name login and list-workspaces use) is accepted.
func TestHandleToolCall_Structure_CompanyIDDefaultsToLoginWorkspace(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]interface{}
		want string
	}{
		{"list-projects omitted", "list-projects", map[string]interface{}{}, "i260836082"},
		{"list-stages omitted", "list-stages", map[string]interface{}{"project_id": float64(1)}, "i260836082"},
		{"show-project empty", "show-project", map[string]interface{}{"project_id": float64(1), "company_id": ""}, "i260836082"},
		{"list-aliases omitted", "list-aliases", map[string]interface{}{"project_id": float64(1), "stage_id": float64(2)}, "i260836082"},
		{"show-project workspace_id", "show-project", map[string]interface{}{"project_id": float64(1), "workspace_id": "i999"}, "i999"},
		{"company_id wins", "list-projects", map[string]interface{}{"company_id": "i111", "workspace_id": "i999"}, "i111"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetGlobals(t)
			var captured []map[string]interface{}
			srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
				captured = ops
				return map[string]interface{}{
					"request_proc": "ok",
					"ops":          []interface{}{map[string]interface{}{"proc": "ok", "obj": "project", "list": []interface{}{}}},
				}
			})
			setProjectAuth(t, srv.URL)

			result, isErr := handleToolCall(context.Background(), tc.tool, tc.args)
			if isErr {
				t.Fatalf("unexpected error: %s", result)
			}
			if len(captured) != 1 {
				t.Fatalf("expected 1 captured op, got %d", len(captured))
			}
			if got := captured[0]["company_id"]; got != tc.want {
				t.Errorf("op company_id = %v, want %s", got, tc.want)
			}
		})
	}
}

func TestHandleToolCall_Structure_CompanyIDMissingWithoutLoginWorkspace(t *testing.T) {
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		t.Errorf("no request expected, got %v", ops)
		return nil
	})
	setProjectAuth(t, srv.URL)
	workspaceID = ""
	result, isErr := handleToolCall(context.Background(), "list-projects", map[string]interface{}{})
	if !isErr || !strings.Contains(result, "company_id") || !strings.Contains(result, "list-workspaces") {
		t.Errorf("expected a company_id error pointing at list-workspaces, got isErr=%v %q", isErr, result)
	}
}

func TestStructureSchemas_CompanyIDNotRequired(t *testing.T) {
	for _, tool := range collapsedToolRegistry {
		switch tool.Name {
		case "list-projects", "show-project", "create-project", "modify-project",
			"delete-project", "list-stages", "list-aliases", "set-stage-immutable":
		default:
			continue
		}
		schema, _ := tool.InputSchema.(map[string]interface{})
		req, _ := schema["required"].([]string)
		for _, r := range req {
			if r == "company_id" {
				t.Errorf("%s: company_id must not be required — it defaults to the login workspace", tool.Name)
			}
		}
	}
}

// ---- list-aliases -----------------------------------------------------------

func TestHandleToolCall_ListAliases_OK(t *testing.T) {
	resetGlobals(t)

	var captured []map[string]interface{}
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{
					"proc": "ok",
					"list": []interface{}{
						map[string]interface{}{
							"obj_id":     float64(153572),
							"title":      "api-gw-create-smart-api",
							"short_name": "api-gw-create-smart-api",
							"obj_to_id":  float64(1922026),
						},
						map[string]interface{}{
							"obj_id":     float64(153574),
							"title":      "Escalation",
							"short_name": "esc",
							"obj_to_id":  float64(1922045),
						},
					},
				},
			},
		}
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handleToolCall(context.Background(), "list-aliases", map[string]interface{}{
		"company_id": "i260836082",
		"project_id": float64(623461),
		"stage_id":   float64(623462),
		"short_name": "api-gw-create-smart-api",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "1922026") {
		t.Errorf("missing target conv_id in output: %s", result)
	}
	if strings.Contains(result, "Escalation") {
		t.Errorf("short_name filter did not exclude the non-matching alias: %s", result)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 captured op, got %d", len(captured))
	}
	op := captured[0]
	for k, want := range map[string]interface{}{
		"type":       "list",
		"obj":        "aliases",
		"company_id": "i260836082",
	} {
		if got, ok := op[k]; !ok || got != want {
			t.Errorf("op[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestHandleToolCall_ListAliases_ShortNameNotFound(t *testing.T) {
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{
					"proc": "ok",
					"list": []interface{}{
						map[string]interface{}{
							"obj_id":     float64(1),
							"title":      "Other",
							"short_name": "other",
							"obj_to_id":  float64(2),
						},
					},
				},
			},
		}
	})
	setProjectAuth(t, srv.URL)

	result, isErr := handleToolCall(context.Background(), "list-aliases", map[string]interface{}{
		"company_id": "i260836082",
		"project_id": float64(1),
		"stage_id":   float64(1),
		"short_name": "does-not-exist",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "No alias with short_name") {
		t.Errorf("expected not-found message, got %q", result)
	}
}
