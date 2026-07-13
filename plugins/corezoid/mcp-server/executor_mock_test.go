package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// ---- Commit ------------------------------------------------------------------

// TestCommit_SurfacesServerError verifies that a server-side commit rejection
// (e.g. a timer below the platform minimum, or an invalid api_rpc_reply shape)
// reaches the caller as an error carrying the server's exact message — not a
// silent nil that the push handler can only report as "no response from server".
func TestCommit_SurfacesServerError(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc": "error",
				"errors": map[string]interface{}{
					"6a4fa136b677ac77708b234c": []interface{}{
						"Key 'value'. 'Timer value 15 sec is less than minimum limit 30 sec'",
					},
				},
			}},
		}
	})

	resp, err := e.Commit()
	if err == nil {
		t.Fatalf("expected error from rejected commit, got nil (resp=%v)", resp)
	}
	if !strings.Contains(err.Error(), "Timer value 15 sec is less than minimum limit 30 sec") {
		t.Errorf("server message lost, got: %v", err)
	}
}

// TestCommit_OK verifies the happy path returns the response and no error.
func TestCommit_OK(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok"}},
		}
	})

	resp, err := e.Commit()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}
