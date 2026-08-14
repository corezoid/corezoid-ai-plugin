package main

import (
	"fmt"
	"strings"
)

// ProcessLifecycleInfo is the live metadata needed by pause/resume and move.
// The lightweight show-conv operation avoids exporting the whole scheme.
type ProcessLifecycleInfo struct {
	ObjID         int
	Title         string
	Description   string
	Status        string
	ConvType      string
	ParentObjID   int
	ParentObjType string
	ProjectID     int
	StageID       int
	Immutable     bool
}

// ShowProcessLifecycle returns current process metadata without downloading
// its graph. It is intentionally separate from GetProcessByID, whose list op
// also returns every node and is much heavier for a status/location check.
func (v *Executor) ShowProcessLifecycle(processID int) (*ProcessLifecycleInfo, error) {
	ops := []map[string]any{{
		"type":       "show",
		"obj":        "conv",
		"obj_id":     processID,
		"company_id": v.WorkspaceID,
	}}
	response, err := v.req("show_process", ops)
	if err != nil {
		return nil, fmt.Errorf("show process failed: %w", err)
	}
	op, err := firstOp(response)
	if err != nil {
		return nil, fmt.Errorf("show process failed: %w", err)
	}

	info := &ProcessLifecycleInfo{}
	info.ObjID = lifecycleMapInt(op, "obj_id")
	info.Title, _ = op["title"].(string)
	info.Description, _ = op["description"].(string)
	info.Status, _ = op["status"].(string)
	info.ConvType, _ = op["conv_type"].(string)
	info.ParentObjID = lifecycleMapInt(op, "parent_obj_id")
	info.ParentObjType, _ = op["parent_obj_type"].(string)
	info.ProjectID = lifecycleMapInt(op, "project_id")
	info.StageID = lifecycleMapInt(op, "stage_id")
	info.Immutable, _ = op["immutable"].(bool)

	if info.ObjID == 0 {
		return nil, fmt.Errorf("show process returned no obj_id")
	}
	if info.ObjID != processID {
		return nil, fmt.Errorf("show process #%d returned metadata for #%d", processID, info.ObjID)
	}
	if strings.TrimSpace(info.Status) == "" {
		return nil, fmt.Errorf("show process #%d returned no status", processID)
	}
	return info, nil
}

// SetProcessLifecycleStatus changes only the conv status. Omitting title and
// description is deliberate: live v6.12.0 accepts the minimal operation, so
// a lifecycle action cannot accidentally overwrite process metadata.
func (v *Executor) SetProcessLifecycleStatus(processID int, status string) (bool, error) {
	if status != "active" && status != "paused" {
		return false, fmt.Errorf("unsupported process status %q", status)
	}
	ops := []map[string]any{{
		"type":       "modify",
		"obj":        "conv",
		"obj_id":     processID,
		"company_id": v.WorkspaceID,
		"status":     status,
	}}
	response, err := v.req("set_process_status", ops)
	if err != nil {
		return false, fmt.Errorf("set process status failed: %w", err)
	}
	op, err := firstOp(response)
	if err != nil {
		return false, fmt.Errorf("set process status failed: %w", err)
	}
	changed, _ := op["is_changed"].(bool)
	return changed, nil
}

// MoveCorezoidObject reparents a process (objType=conv) or normal folder.
// currentParentID must come from a fresh show call; Corezoid accepts a stale
// parent_id and echoes it in the response, so callers must not trust input or
// response alone and must verify the live parent after this operation.
func (v *Executor) MoveCorezoidObject(objType string, objID, currentParentID, destinationFolderID int) error {
	if objType != "conv" && objType != "folder" {
		return fmt.Errorf("unsupported move object type %q", objType)
	}
	ops := []map[string]any{{
		"type":       "link",
		"obj":        "folder",
		"obj_type":   objType,
		"obj_id":     objID,
		"folder_id":  destinationFolderID,
		"parent_id":  currentParentID,
		"company_id": v.WorkspaceID,
	}}
	response, err := v.req("move_object", ops)
	if err != nil {
		return fmt.Errorf("move %s #%d failed: %w", objType, objID, err)
	}
	if _, err := firstOp(response); err != nil {
		return fmt.Errorf("move %s #%d failed: %w", objType, objID, err)
	}
	return nil
}

func lifecycleMapInt(m map[string]interface{}, key string) int {
	switch value := m[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	default:
		return 0
	}
}
