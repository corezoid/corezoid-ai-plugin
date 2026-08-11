package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestShowProcessLifecycle_ParsesLiveMetadata(t *testing.T) {
	_, executor := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return lifecycleResponse(map[string]interface{}{
			"proc": "ok", "obj_id": float64(42), "title": "Billing",
			"description": "Bills customers", "status": "paused",
			"conv_type": "process", "parent_obj_id": float64(300),
			"parent_obj_type": "folder", "project_id": float64(100),
			"stage_id": float64(200), "immutable": true,
		})
	})
	executor.WorkspaceID = "workspace-test"

	info, err := executor.ShowProcessLifecycle(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ObjID != 42 || info.Status != "paused" || info.ParentObjID != 300 || info.ProjectID != 100 || info.StageID != 200 || !info.Immutable {
		t.Errorf("unexpected metadata: %+v", info)
	}
}

func TestShowProcessLifecycle_RejectsWrongObjectResponse(t *testing.T) {
	_, executor := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return lifecycleResponse(map[string]interface{}{
			"proc": "ok", "obj_id": float64(99), "status": "active",
		})
	})
	_, err := executor.ShowProcessLifecycle(42)
	if err == nil || !strings.Contains(err.Error(), "metadata for #99") {
		t.Fatalf("expected identity mismatch, got: %v", err)
	}
}

func TestShowProcessLifecycle_RequiresStatus(t *testing.T) {
	_, executor := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return lifecycleResponse(map[string]interface{}{
			"proc": "ok", "obj_id": float64(42),
		})
	})
	_, err := executor.ShowProcessLifecycle(42)
	if err == nil || !strings.Contains(err.Error(), "no status") {
		t.Fatalf("expected missing-status error, got: %v", err)
	}
}

func TestSetProcessLifecycleStatus_RejectsUnsupportedStatusBeforeAPI(t *testing.T) {
	calls := 0
	_, executor := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		return lifecycleResponse(map[string]interface{}{"proc": "ok"})
	})
	_, err := executor.SetProcessLifecycleStatus(42, "debug")
	if err == nil || calls != 0 {
		t.Fatalf("unsupported status should not reach API, err=%v calls=%d", err, calls)
	}
}

func TestSetProcessLifecycleStatus_SendsMinimalWireOperation(t *testing.T) {
	var got map[string]interface{}
	_, executor := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		got = cloneLifecycleOp(ops[0])
		return lifecycleResponse(map[string]interface{}{"proc": "ok", "is_changed": true})
	})
	executor.WorkspaceID = "workspace-test"

	changed, err := executor.SetProcessLifecycleStatus(42, "paused")
	if err != nil || !changed {
		t.Fatalf("SetProcessLifecycleStatus failed: changed=%v err=%v", changed, err)
	}
	want := map[string]interface{}{
		"type": "modify", "obj": "conv", "obj_id": float64(42),
		"company_id": "workspace-test", "status": "paused",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status wire op = %#v, want %#v", got, want)
	}
}

func TestMoveCorezoidObject_RejectsUnsupportedTypeBeforeAPI(t *testing.T) {
	calls := 0
	_, executor := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		return lifecycleResponse(map[string]interface{}{"proc": "ok"})
	})
	err := executor.MoveCorezoidObject("project", 42, 1, 2)
	if err == nil || calls != 0 {
		t.Fatalf("unsupported type should not reach API, err=%v calls=%d", err, calls)
	}
}

func TestMoveCorezoidObject_SendsExactLinkWireOperation(t *testing.T) {
	for _, objType := range []string{"conv", "folder"} {
		t.Run(objType, func(t *testing.T) {
			var got map[string]interface{}
			_, executor := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
				got = cloneLifecycleOp(ops[0])
				return lifecycleResponse(map[string]interface{}{"proc": "ok"})
			})
			executor.WorkspaceID = "workspace-test"

			if err := executor.MoveCorezoidObject(objType, 42, 100, 200); err != nil {
				t.Fatalf("MoveCorezoidObject failed: %v", err)
			}
			want := map[string]interface{}{
				"type": "link", "obj": "folder", "obj_type": objType,
				"obj_id": float64(42), "folder_id": float64(200),
				"parent_id": float64(100), "company_id": "workspace-test",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("move wire op = %#v, want %#v", got, want)
			}
		})
	}
}
