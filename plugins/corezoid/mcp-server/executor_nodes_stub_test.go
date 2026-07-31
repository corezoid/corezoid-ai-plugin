package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateNodes_PreservesStubObjType(t *testing.T) {
	var captured []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{
					"proc":   "ok",
					"id":     "bbbbbbbbbbbbbbbbbbbbbbbb",
					"obj_id": "server-node-id",
				},
			},
		}
	})
	e.ProcessID = 1891415
	e.Version = 1

	nodes := []any{
		map[string]interface{}{
			"id":          "bbbbbbbbbbbbbbbbbbbbbbbb",
			"title":       "Call with Stub",
			"description": "",
			"obj_type":    float64(4),
			"extra":       "{\"modeForm\":\"expand\",\"icon\":\"\"}",
		},
	}

	if err := e.CreateNodesFromJSON(nodes); err != nil {
		t.Fatalf("CreateNodesFromJSON returned error: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 create op, got %d", len(captured))
	}
	if captured[0]["obj_type"] != float64(4) {
		t.Fatalf("expected API create obj_type 4 for Stub Mode node, got %#v", captured[0]["obj_type"])
	}
	if e.NodeIDMap["bbbbbbbbbbbbbbbbbbbbbbbb"].Type != 4 {
		t.Fatalf("expected local NodeIDMap to keep exported obj_type 4, got %d", e.NodeIDMap["bbbbbbbbbbbbbbbbbbbbbbbb"].Type)
	}
}

func TestModifyNodes_PreservesConditionStub(t *testing.T) {
	var captured []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return okResponse(ops)
	})
	e.ProcessID = 1891415
	e.Version = 1

	stub := map[string]interface{}{
		"logics": []interface{}{
			[]interface{}{
				map[string]interface{}{
					"type": "go_if_const",
					"conditions": []interface{}{
						map[string]interface{}{
							"param": "{{mode}}",
							"const": "error",
							"fun":   "eq",
							"cast":  "string",
						},
					},
				},
				map[string]interface{}{
					"type":             "api_rpc_reply",
					"mode":             "key_value",
					"res_data":         map[string]interface{}{"status": "error"},
					"res_data_type":    map[string]interface{}{"status": "string"},
					"throw_exception":  true,
					"exception_reason": "stub error",
				},
			},
			[]interface{}{
				map[string]interface{}{
					"type":            "api_rpc_reply",
					"mode":            "key_value",
					"res_data":        map[string]interface{}{"stub_node_response": "ok"},
					"res_data_type":   map[string]interface{}{"stub_node_response": "string"},
					"throw_exception": false,
				},
			},
		},
	}

	nodes := []any{
		map[string]interface{}{
			"id":          "bbbbbbbbbbbbbbbbbbbbbbbb",
			"title":       "Call with Stub",
			"description": "",
			"obj_type":    float64(4),
			"x":           float64(100),
			"y":           float64(300),
			"extra":       "{\"modeForm\":\"expand\",\"icon\":\"\"}",
			"options":     nil,
			"condition": map[string]interface{}{
				"logics": []interface{}{
					map[string]interface{}{
						"type":        "api_rpc",
						"conv_id":     float64(123456),
						"err_node_id": "dddddddddddddddddddddddd",
						"extra":       map[string]interface{}{},
						"extra_type":  map[string]interface{}{},
						"group":       "",
					},
					map[string]interface{}{
						"type":       "go",
						"to_node_id": "cccccccccccccccccccccccc",
					},
				},
				"semaphors": []interface{}{},
				"stub":      stub,
			},
		},
	}

	if err := e.ModifyNodes(nodes); err != nil {
		t.Fatalf("ModifyNodes returned error: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 modify op, got %d", len(captured))
	}
	if captured[0]["obj_type"] != float64(4) {
		t.Fatalf("expected API modify obj_type 4 for Stub Mode node, got %#v", captured[0]["obj_type"])
	}
	got, ok := captured[0]["stub"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected modify op to include stub, got %#v", captured[0])
	}
	if len(got["logics"].([]interface{})) != 2 {
		t.Fatalf("expected 2 stub branches, got %#v", got["logics"])
	}
}

func TestModifyNodes_TogglesStubModeWithExplicitObjType(t *testing.T) {
	for _, tc := range []struct {
		name    string
		objType float64
	}{
		{name: "enabled", objType: 4},
		{name: "disabled_but_config_preserved", objType: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured []map[string]interface{}
			_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
				captured = ops
				return okResponse(ops)
			})
			e.ProcessID = 1891415
			e.Version = 1

			nodes := []any{stubbedCallProcessNode(tc.objType)}
			if err := e.ModifyNodes(nodes); err != nil {
				t.Fatalf("ModifyNodes returned error: %v", err)
			}
			if len(captured) != 1 {
				t.Fatalf("expected 1 modify op, got %d", len(captured))
			}
			if captured[0]["obj_type"] != tc.objType {
				t.Fatalf("expected obj_type %.0f, got %#v", tc.objType, captured[0]["obj_type"])
			}
			if _, ok := captured[0]["stub"].(map[string]interface{}); !ok {
				t.Fatalf("expected stub config to be preserved while switching mode, got %#v", captured[0])
			}
		})
	}
}

func TestProcessJSON_CreatesStubbedProcessRoundTripPayload(t *testing.T) {
	sample, err := os.ReadFile(filepath.Join("samples", "stubbed_api_rpc.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "stubbed_api_rpc.json")
	if err := os.WriteFile(filePath, sample, 0644); err != nil {
		t.Fatal(err)
	}

	serverIDs := map[string]string{
		"aaaaaaaaaaaaaaaaaaaaaaaa": "111111111111111111111111",
		"bbbbbbbbbbbbbbbbbbbbbbbb": "222222222222222222222222",
		"cccccccccccccccccccccccc": "333333333333333333333333",
		"dddddddddddddddddddddddd": "444444444444444444444444",
	}
	var createNodeOps []map[string]interface{}
	var modifyNodeOps []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) == 0 {
			return okResponse(ops)
		}
		op := ops[0]
		switch {
		case op["type"] == "create" && op["obj"] == "conv":
			return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(777)})
		case op["type"] == "create" && op["obj"] == "node":
			createNodeOps = append(createNodeOps, ops...)
			results := make([]interface{}, len(ops))
			for i, createOp := range ops {
				localID, _ := createOp["id"].(string)
				results[i] = map[string]interface{}{
					"proc":   "ok",
					"id":     localID,
					"obj_id": serverIDs[localID],
				}
			}
			return map[string]interface{}{"request_proc": "ok", "ops": results}
		case op["type"] == "modify" && op["obj"] == "node":
			modifyNodeOps = append(modifyNodeOps, ops...)
			return okResponse(ops)
		default:
			return okResponse(ops)
		}
	})
	e.Version = 1
	e.WorkspaceID = "workspace"

	if _, err := e.ProcessJSON(filePath, string(sample)); err != nil {
		t.Fatalf("ProcessJSON returned error: %v", err)
	}

	var createdStub map[string]interface{}
	for _, op := range createNodeOps {
		if op["id"] == "bbbbbbbbbbbbbbbbbbbbbbbb" {
			createdStub = op
			break
		}
	}
	if createdStub == nil {
		t.Fatalf("expected Stub node create op, got %#v", createNodeOps)
	}
	if createdStub["obj_type"] != float64(4) {
		t.Fatalf("expected Stub node create obj_type 4, got %#v", createdStub["obj_type"])
	}

	var modifiedStub map[string]interface{}
	for _, op := range modifyNodeOps {
		if op["obj_id"] == "222222222222222222222222" {
			modifiedStub = op
			break
		}
	}
	if modifiedStub == nil {
		t.Fatalf("expected Stub node modify op, got %#v", modifyNodeOps)
	}
	if modifiedStub["obj_type"] != float64(4) {
		t.Fatalf("expected Stub node modify obj_type 4, got %#v", modifiedStub["obj_type"])
	}
	stub, ok := modifiedStub["stub"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected modify op to include stub config, got %#v", modifiedStub)
	}
	if branches, ok := stub["logics"].([]interface{}); !ok || len(branches) != 2 {
		t.Fatalf("expected 2 stub branches in ProcessJSON payload, got %#v", stub["logics"])
	}
}

func stubbedCallProcessNode(objType float64) map[string]interface{} {
	stub := map[string]interface{}{
		"logics": []interface{}{
			[]interface{}{
				map[string]interface{}{
					"type": "go_if_const",
					"conditions": []interface{}{
						map[string]interface{}{
							"param": "{{mode}}",
							"const": "error",
							"fun":   "eq",
							"cast":  "string",
						},
					},
				},
				map[string]interface{}{
					"type":             "api_rpc_reply",
					"mode":             "key_value",
					"res_data":         map[string]interface{}{"status": "error"},
					"res_data_type":    map[string]interface{}{"status": "string"},
					"throw_exception":  true,
					"exception_reason": "stub error",
				},
			},
			[]interface{}{
				map[string]interface{}{
					"type":            "api_rpc_reply",
					"mode":            "key_value",
					"res_data":        map[string]interface{}{"stub_node_response": "ok"},
					"res_data_type":   map[string]interface{}{"stub_node_response": "string"},
					"throw_exception": false,
				},
			},
		},
	}

	return map[string]interface{}{
		"id":          "bbbbbbbbbbbbbbbbbbbbbbbb",
		"title":       "Call with Stub",
		"description": "",
		"obj_type":    objType,
		"x":           float64(100),
		"y":           float64(300),
		"extra":       "{\"modeForm\":\"expand\",\"icon\":\"\"}",
		"options":     nil,
		"condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{
					"type":        "api_rpc",
					"conv_id":     float64(123456),
					"err_node_id": "dddddddddddddddddddddddd",
					"extra":       map[string]interface{}{},
					"extra_type":  map[string]interface{}{},
					"group":       "",
				},
				map[string]interface{}{
					"type":       "go",
					"to_node_id": "cccccccccccccccccccccccc",
				},
			},
			"semaphors": []interface{}{},
			"stub":      stub,
		},
	}
}
