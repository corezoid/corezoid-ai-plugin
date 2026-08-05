package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListWorkspaceRoot_ParsesFolderConvDashboard verifies that ListWorkspaceRoot
// returns folders, convs, and dashboards at workspace root with the correct
// obj_type and obj_id populated from the response list.
func TestListWorkspaceRoot_ParsesFolderConvDashboard(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{
					"proc":   "ok",
					"obj":    "folder",
					"obj_id": float64(0),
					"list": []interface{}{
						map[string]interface{}{
							"obj_id":   float64(687287),
							"obj_type": "folder",
							"title":    "et",
						},
						map[string]interface{}{
							"obj_id":    float64(1571296),
							"obj_type":  "conv",
							"conv_type": "process",
							"title":     "Graph Maker",
						},
						map[string]interface{}{
							"obj_id":   float64(136538),
							"obj_type": "dashboard",
							"title":    "Test Dashboard",
						},
					},
				},
			},
		}
	})

	items, err := e.ListWorkspaceRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].ObjType != "folder" || items[0].ObjID != 687287 {
		t.Errorf("item[0] = %+v, want folder #687287", items[0])
	}
	if items[1].ObjType != "conv" || items[1].ObjID != 1571296 || items[1].ConvType != "process" {
		t.Errorf("item[1] = %+v, want conv/process #1571296", items[1])
	}
	if items[2].ObjType != "dashboard" || items[2].ObjID != 136538 {
		t.Errorf("item[2] = %+v, want dashboard #136538", items[2])
	}
}

// TestListWorkspaceRoot_SendsCorrectRequest verifies that ListWorkspaceRoot
// issues an op with obj="folder", obj_id=0, and the workspace/company IDs mirrored.
func TestListWorkspaceRoot_SendsCorrectRequest(t *testing.T) {
	var gotOps []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		gotOps = ops
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "ok", "list": []interface{}{}}},
		}
	})
	e.WorkspaceID = "workspace-uuid"

	_, err := e.ListWorkspaceRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotOps) != 1 {
		t.Fatalf("expected 1 op, got %d", len(gotOps))
	}
	op := gotOps[0]
	if op["type"] != "list" || op["obj"] != "folder" {
		t.Errorf("wrong op envelope: %+v", op)
	}
	if id, _ := op["obj_id"].(float64); id != 0 {
		t.Errorf("obj_id = %v, want 0", op["obj_id"])
	}
	if op["company_id"] != "workspace-uuid" || op["id"] != "workspace-uuid" {
		t.Errorf("workspace id not mirrored: %+v", op)
	}
}

// TestListWorkspaceRoot_SkipsMissingIDOrType ignores entries that lack obj_id
// or obj_type — the server occasionally returns partial rows and callers should
// see only well-formed items.
func TestListWorkspaceRoot_SkipsMissingIDOrType(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{
					"proc": "ok",
					"list": []interface{}{
						map[string]interface{}{"obj_id": float64(0), "obj_type": "conv"},         // no id
						map[string]interface{}{"obj_id": float64(1), "obj_type": ""},              // no type
						map[string]interface{}{"obj_id": float64(42), "obj_type": "folder"},      // good
					},
				},
			},
		}
	})

	items, err := e.ListWorkspaceRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].ObjID != 42 {
		t.Errorf("expected only well-formed folder #42, got %+v", items)
	}
}

// TestBatchExportSchemes_ReturnsURLs verifies that BatchExportSchemes fans out
// one obj_scheme op per item and unpacks the returned download URLs in order.
func TestBatchExportSchemes_ReturnsURLs(t *testing.T) {
	var gotOps []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		gotOps = ops
		out := make([]interface{}, len(ops))
		for i, op := range ops {
			out[i] = map[string]interface{}{
				"proc":         "ok",
				"download_url": "https://example.test/dl-" + toString(op["obj_id"]),
			}
		}
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          out,
		}
	})

	items := []WorkspaceRootItem{
		{ObjID: 687287, ObjType: "folder"},
		{ObjID: 1571296, ObjType: "conv"},
		{ObjID: 136538, ObjType: "dashboard"},
	}
	urls, err := e.BatchExportSchemes(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(urls) != 3 {
		t.Fatalf("expected 3 URLs, got %d", len(urls))
	}
	for i, want := range []string{
		"https://example.test/dl-687287",
		"https://example.test/dl-1571296",
		"https://example.test/dl-136538",
	} {
		if urls[i] != want {
			t.Errorf("url[%d] = %q, want %q", i, urls[i], want)
		}
	}

	// Verify each op used obj_scheme, correct obj_type/id, and zip format.
	if len(gotOps) != 3 {
		t.Fatalf("expected 3 ops on the wire, got %d", len(gotOps))
	}
	for i, want := range items {
		if gotOps[i]["obj"] != "obj_scheme" {
			t.Errorf("op[%d].obj = %v, want obj_scheme", i, gotOps[i]["obj"])
		}
		if id, _ := gotOps[i]["obj_id"].(float64); int(id) != want.ObjID {
			t.Errorf("op[%d].obj_id = %v, want %d", i, gotOps[i]["obj_id"], want.ObjID)
		}
		if gotOps[i]["obj_type"] != want.ObjType {
			t.Errorf("op[%d].obj_type = %v, want %s", i, gotOps[i]["obj_type"], want.ObjType)
		}
		if gotOps[i]["format"] != "zip" {
			t.Errorf("op[%d].format = %v, want zip", i, gotOps[i]["format"])
		}
	}
}

// TestBatchExportSchemes_EmptyIsNoOp: with no items to export, the executor
// must not issue any HTTP call — an empty payload would elicit "no ops" from
// the server.
func TestBatchExportSchemes_EmptyIsNoOp(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	e := &Executor{APIUrl: srv.URL, Token: "t", NodeIDMap: make(map[string]NodeInfo)}
	urls, err := e.BatchExportSchemes(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(urls) != 0 {
		t.Errorf("expected 0 URLs, got %d", len(urls))
	}
	if called {
		t.Error("expected no HTTP call for empty input")
	}
}

// TestBatchExportSchemes_OpErrorPropagates surfaces the description from the
// first failed op so callers see WHY export failed (immutable stage, missing
// obj, etc.), not just "failed".
func TestBatchExportSchemes_OpErrorPropagates(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{"proc": "ok", "download_url": "https://example.test/ok"},
				map[string]interface{}{"proc": "error", "description": "object not found"},
			},
		}
	})

	_, err := e.BatchExportSchemes([]WorkspaceRootItem{
		{ObjID: 1, ObjType: "conv"},
		{ObjID: 2, ObjType: "conv"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "object not found") {
		t.Errorf("expected error to include server description, got: %v", err)
	}
}

// toString renders a JSON-decoded number as its integer string. Helps compose
// deterministic download URLs from the obj_id in the mock server above.
func toString(v interface{}) string {
	b, _ := json.Marshal(v)
	s := strings.TrimSuffix(string(b), ".0")
	return s
}
