package main

import (
	"strings"
	"testing"
)

// conditionNode builds a minimal modify-node payload carrying the given logics.
func conditionNode(objType float64, logics []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":          "bbbbbbbbbbbbbbbbbbbbbbbb",
		"title":       "Route",
		"description": "",
		"obj_type":    objType,
		"condition": map[string]interface{}{
			"logics":    logics,
			"semaphors": []interface{}{},
		},
	}
}

func TestModifyNodes_RejectsGoNotLast(t *testing.T) {
	var captured []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return okResponse(ops)
	})
	e.ProcessID = 1891415
	e.Version = 1

	nodes := []any{conditionNode(0, []interface{}{
		map[string]interface{}{
			"type":       "go",
			"to_node_id": "cccccccccccccccccccccccc",
		},
		map[string]interface{}{
			"type":        "api_rpc",
			"conv_id":     float64(123456),
			"err_node_id": "dddddddddddddddddddddddd",
			"extra":       map[string]interface{}{},
			"extra_type":  map[string]interface{}{},
		},
	})}

	err := e.ModifyNodes(nodes)
	if err == nil {
		t.Fatalf("expected an error when the last logic is not go, got nil (ops: %#v)", captured)
	}
	if !strings.Contains(err.Error(), "last logic in condition.logics") {
		t.Fatalf("expected a last-position error, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"api_rpc"`) {
		t.Fatalf("expected the error to name the offending logic type, got: %v", err)
	}
	if len(captured) != 0 {
		t.Fatalf("expected no modify op to be sent, got %#v", captured)
	}
}

func TestModifyNodes_AcceptsGoLast(t *testing.T) {
	var captured []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = ops
		return okResponse(ops)
	})
	e.ProcessID = 1891415
	e.Version = 1

	nodes := []any{conditionNode(0, []interface{}{
		map[string]interface{}{
			"type": "go_if_const",
			"conditions": []interface{}{
				map[string]interface{}{
					"param": "{{status}}",
					"const": "ok",
					"fun":   "eq",
					"cast":  "string",
				},
			},
			"to_node_id": "cccccccccccccccccccccccc",
		},
		map[string]interface{}{
			"type":       "go",
			"to_node_id": "dddddddddddddddddddddddd",
		},
	})}

	if err := e.ModifyNodes(nodes); err != nil {
		t.Fatalf("ModifyNodes returned error: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 modify op, got %d", len(captured))
	}
}

// Final nodes carry no default route; the exemption matches findMissingDefaultGo.
func TestModifyNodes_AllowsFinalNodeWithoutTrailingGo(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return okResponse(ops)
	})
	e.ProcessID = 1891415
	e.Version = 1

	nodes := []any{conditionNode(2, []interface{}{
		map[string]interface{}{
			"type":          "api_rpc_reply",
			"mode":          "key_value",
			"res_data":      map[string]interface{}{"status": "ok"},
			"res_data_type": map[string]interface{}{"status": "string"},
		},
		map[string]interface{}{
			"type":       "go",
			"to_node_id": "cccccccccccccccccccccccc",
		},
	})}
	if err := e.ModifyNodes(nodes); err != nil {
		t.Fatalf("ModifyNodes returned error for final node: %v", err)
	}

	nodes = []any{conditionNode(2, []interface{}{
		map[string]interface{}{
			"type":       "go",
			"to_node_id": "cccccccccccccccccccccccc",
		},
		map[string]interface{}{
			"type":          "api_rpc_reply",
			"mode":          "key_value",
			"res_data":      map[string]interface{}{"status": "ok"},
			"res_data_type": map[string]interface{}{"status": "string"},
		},
	})}
	if err := e.ModifyNodes(nodes); err != nil {
		t.Fatalf("ModifyNodes returned error for final node with trailing reply: %v", err)
	}
}
