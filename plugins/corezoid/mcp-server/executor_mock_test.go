package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockAPIServer starts a test HTTP server that responds like the Corezoid API.
// The handler fn receives the decoded ops list and returns the response body.
func mockAPIServer(t *testing.T, fn func(ops []map[string]interface{}) interface{}) (*httptest.Server, *Executor) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fn(body.Ops)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	e := &Executor{
		APIUrl:    srv.URL,
		Token:     "test-token",
		NodeIDMap: make(map[string]NodeInfo),
	}
	return srv, e
}

func okResponse(ops []map[string]interface{}) interface{} {
	opResults := make([]interface{}, len(ops))
	for i := range ops {
		opResults[i] = map[string]interface{}{"proc": "ok"}
	}
	return map[string]interface{}{
		"request_proc": "ok",
		"ops":          opResults,
	}
}

// ---- req -------------------------------------------------------------------

func TestReq_OK(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	resp, err := e.req("test_method", []map[string]any{{"type": "list"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp["request_proc"] != "ok" {
		t.Errorf("unexpected response: %v", resp)
	}
}

func TestReq_ServerError(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "fail",
		}
	})

	_, err := e.req("test_method", []map[string]any{})
	if err == nil {
		t.Error("expected error for request_proc=fail, got nil")
	}
}

func TestReq_OpError(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "op failed"}},
		}
	})

	_, err := e.req("test_method", []map[string]any{{"type": "test"}})
	if err == nil {
		t.Error("expected error for op proc=error, got nil")
	}
}

func TestReq_InvalidJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not-json")
	}))
	t.Cleanup(srv.Close)

	e := &Executor{APIUrl: srv.URL, Token: "test", NodeIDMap: make(map[string]NodeInfo)}
	_, err := e.req("test_method", []map[string]any{})
	if err == nil {
		t.Error("expected error for invalid JSON response, got nil")
	}
}

func TestReq_BadURL(t *testing.T) {
	e := &Executor{APIUrl: "http://127.0.0.1:1", Token: "test", NodeIDMap: make(map[string]NodeInfo)}
	_, err := e.req("test_method", []map[string]any{})
	if err == nil {
		t.Error("expected error for unreachable server, got nil")
	}
}

func TestReq_DropsEmptyCompanyID(t *testing.T) {
	origWS := workspaceID
	workspaceID = ""
	t.Cleanup(func() { workspaceID = origWS })

	var capturedOps []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		capturedOps = ops
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	e.req("test", []map[string]any{ //nolint:errcheck
		{"type": "list", "company_id": "", "from_company_id": ""},
	})

	if len(capturedOps) > 0 {
		if _, ok := capturedOps[0]["company_id"]; ok {
			t.Error("expected empty company_id to be dropped from request")
		}
	}
}

// ---- NewValidator ----------------------------------------------------------

func TestNewValidator(t *testing.T) {
	origAPIURL := apiURL
	origToken := apiToken
	apiURL = "https://api.example.com"
	apiToken = "my-token"
	t.Cleanup(func() { apiURL = origAPIURL; apiToken = origToken })

	v := NewValidator(context.Background(), 42)
	if v == nil {
		t.Fatal("expected non-nil Executor")
	}
	if v.ProcessID != 42 {
		t.Errorf("ProcessID = %d, want 42", v.ProcessID)
	}
	if v.APIUrl != "https://api.example.com" {
		t.Errorf("APIUrl = %q", v.APIUrl)
	}
	if v.Token != "my-token" {
		t.Errorf("Token = %q", v.Token)
	}
	if v.NodeIDMap == nil {
		t.Error("expected non-nil NodeIDMap")
	}
	if v.Ctx == nil {
		t.Error("expected non-nil Ctx")
	}
}

// ---- Executor method smoke tests (error paths) -----------------------------

func TestExportProcess_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})
	e.ProcessID = 123

	_, err := e.ExportProcess()
	if err == nil {
		t.Error("expected error from ExportProcess, got nil")
	}
}

func TestPullZip_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	e := &Executor{APIUrl: srv.URL, Token: "test", NodeIDMap: make(map[string]NodeInfo)}
	_, err := e.PullZip(123, "stage")
	if err == nil {
		t.Error("expected error from PullZip on bad response, got nil")
	}
}

func TestCreateFolder_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.CreateFolder(1, "name", "")
	if err == nil {
		t.Error("expected error from CreateFolder, got nil")
	}
}

func TestCreateEmptyProcess_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	id := e.CreateEmptyProcess(1, "test", "")
	if id != 0 {
		t.Errorf("expected 0 on error, got %d", id)
	}
}

func TestShowFolder_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.ShowFolder(1)
	if err == nil {
		t.Error("expected error from ShowFolder, got nil")
	}
}

func TestGetProcessByID_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.GetProcessByID(123)
	if err == nil {
		t.Error("expected error from GetProcessByID, got nil")
	}
}

func TestCreateAlias_OK(t *testing.T) {
	call := 0
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		call++
		if call == 1 {
			// First call: GetProjectIDByStageID → ShowFolder
			return map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok",
					"obj_id": float64(2),
					"parent_obj_id": float64(0),
				}},
			}
		}
		// Second call: actual CreateAlias
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok", "obj_id": float64(99)}},
		}
	})

	_, err := e.CreateAlias("alias", 1, 2)
	if err != nil {
		t.Errorf("unexpected error from CreateAlias: %v", err)
	}
}

func TestListAliasesByStage_Error(t *testing.T) {
	// GetProjectIDByStageID calls ShowFolder first; must succeed or it panics (nil-deref bug).
	// We satisfy the ShowFolder call, then fail the alias list call.
	call := 0
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		call++
		if call == 1 {
			return map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok", "obj_id": float64(1), "parent_obj_id": float64(0),
				}},
			}
		}
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.listAliasesByStage(1)
	if err == nil {
		t.Error("expected error from listAliasesByStage, got nil")
	}
}

func TestListEnvVarsByStage_Error(t *testing.T) {
	call := 0
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		call++
		if call == 1 {
			return map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok", "obj_id": float64(1), "parent_obj_id": float64(0),
				}},
			}
		}
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.listEnvVarsByStage(1)
	if err == nil {
		t.Error("expected error from listEnvVarsByStage, got nil")
	}
}

func TestGetAliasByShortName_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.GetAliasByShortName("name")
	if err == nil {
		t.Error("expected error from GetAliasByShortName, got nil")
	}
}

func TestGetEnvVarByShortName_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.GetEnvVarByShortName("name")
	if err == nil {
		t.Error("expected error from GetEnvVarByShortName, got nil")
	}
}

func TestSetParams_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	err := e.SetParams([]interface{}{map[string]interface{}{"name": "x"}})
	if err == nil {
		t.Error("expected error from SetParams, got nil")
	}
}

func TestCommit_ReturnsMap(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	result, err := e.Commit()
	if err != nil {
		t.Errorf("unexpected error from Commit: %v", err)
	}
	_ = result
}

func TestCommit_PropagatesError(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	_, err := e.Commit()
	if err == nil {
		t.Error("expected error from Commit on request_proc=fail, got nil")
	}
}

func TestDeleteVersion_NoError(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	e.DeleteVersion(1) // returns nothing — just ensure no panic
}

func TestShare_ReturnsMap(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	result := e.Share(1, 2)
	_ = result
}

func TestDeleteNotUsedNodes_NoError(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	old := []any{map[string]interface{}{"id": "aabb", "server_id": "srv1"}}
	result := e.DeleteNotUsedNodes(old, []any{})
	_ = result
}

func TestDeleteNode_NoError(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	e.DeleteNode("node1") // returns nothing
}

// ---- CompileAPICode (git_call inline code path) ----------------------------

// TestCompileAPICode_GitCallInlineCode verifies that a git_call logic node
// with inline JS code (the "src" field populated) triggers the load+compile
// sequence before commit, just like api_code nodes do. This exercises the fix
// for the push-process failure on processes containing git_call nodes.
func TestCompileAPICode_GitCallInlineCode(t *testing.T) {
	calls := 0
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		// Both load and compile requests should succeed.
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})
	e.ProcessID = 42

	nodes := []interface{}{
		map[string]interface{}{
			"id":       "aabbccddeeff001122334455",
			"obj_type": float64(0),
			"condition": map[string]interface{}{
				"logics": []interface{}{
					map[string]interface{}{
						"type": "git_call",
						"lang": "js",
						"src":  "corezoid.callback();",
						"err_node_id": "aabbccddeeff001122334456",
					},
					map[string]interface{}{"type": "go", "to_node_id": "aabbccddeeff001122334457"},
				},
			},
		},
	}

	if err := e.CompileAPICode(nodes); err != nil {
		t.Fatalf("CompileAPICode returned unexpected error: %v", err)
	}
	// Expect exactly 2 API calls: load_api_code + compile_api_code.
	if calls != 2 {
		t.Errorf("expected 2 API calls (load+compile), got %d", calls)
	}
}

// TestCompileAPICode_GitCallNoInlineCode verifies that a git_call logic that
// references an external git repo (no "src"/"code" field) does NOT trigger any
// API calls — those nodes compile server-side when the task runs.
func TestCompileAPICode_GitCallNoInlineCode(t *testing.T) {
	calls := 0
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})
	e.ProcessID = 42

	nodes := []interface{}{
		map[string]interface{}{
			"id":       "aabbccddeeff001122334455",
			"obj_type": float64(0),
			"condition": map[string]interface{}{
				"logics": []interface{}{
					map[string]interface{}{
						"type":   "git_call",
						"lang":   "js",
						"repo":   "https://github.com/example/repo.git",
						"commit": "main",
						"err_node_id": "aabbccddeeff001122334456",
						// no "src" or "code" — external repo reference
					},
					map[string]interface{}{"type": "go", "to_node_id": "aabbccddeeff001122334457"},
				},
			},
		},
	}

	if err := e.CompileAPICode(nodes); err != nil {
		t.Fatalf("CompileAPICode returned unexpected error: %v", err)
	}
	if calls != 0 {
		t.Errorf("expected 0 API calls for external-repo git_call, got %d", calls)
	}
}

func TestCreateVariable_Error(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "fail"}
	})

	err := e.CreateVariable("stageID", "name", "desc", "val")
	if err == nil {
		t.Error("expected error from CreateVariable, got nil")
	}
}

// ---- GetProjectIDByStageID -------------------------------------------------

func TestGetProjectIDByStageID_OK(t *testing.T) {
	// ShowFolder returns folder with parent_obj_id — GetProjectIDByStageID should return parent.
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":          "ok",
				"obj_id":        float64(1),
				"parent_obj_id": float64(999),
			}},
		}
	})

	id := e.GetProjectIDByStageID(1)
	if id != 999 {
		t.Errorf("expected 999, got %d", id)
	}
}
