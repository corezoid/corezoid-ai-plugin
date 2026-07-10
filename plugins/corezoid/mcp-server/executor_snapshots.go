package main

import "fmt"

// Snapshot describes one snapshot entry returned by list-snapshots.
type Snapshot struct {
	ObjID      int    `json:"obj_id"`
	Version    int    `json:"version"`
	UserID     int    `json:"user_id"`
	UserName   string `json:"user_name"`
	CreateTime int64  `json:"create_time"`
	Title      string `json:"title"`
}

// CreateSnapshot creates a snapshot of the process at its current server state.
// Returns (obj_id, version, error).
func (v *Executor) CreateSnapshot(convID, projectID, stageID int, title string) (int, int, error) {
	ops := []map[string]any{
		{
			"type":       "create",
			"obj":        "snapshot",
			"conv_id":    convID,
			"project_id": projectID,
			"stage_id":   stageID,
			"company_id": v.WorkspaceID,
			"title":      title,
		},
	}
	resp, err := v.req("json", ops)
	if err != nil {
		return 0, 0, fmt.Errorf("CreateSnapshot request failed: %w", err)
	}
	op, err := firstOp(resp)
	if err != nil {
		return 0, 0, fmt.Errorf("CreateSnapshot: %w", err)
	}
	objID, _ := op["obj_id"].(float64)
	version, _ := op["version"].(float64)
	return int(objID), int(version), nil
}

// ListSnapshots returns all snapshots for the given process.
func (v *Executor) ListSnapshots(convID, projectID, stageID int) ([]Snapshot, error) {
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "snapshots",
			"conv_id":    convID,
			"project_id": projectID,
			"stage_id":   stageID,
			"company_id": v.WorkspaceID,
		},
	}
	resp, err := v.req("json", ops)
	if err != nil {
		return nil, fmt.Errorf("ListSnapshots request failed: %w", err)
	}
	op, err := firstOp(resp)
	if err != nil {
		return nil, fmt.Errorf("ListSnapshots: %w", err)
	}

	raw, _ := op["list"].([]any)
	snapshots := make([]Snapshot, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		s := Snapshot{}
		if f, ok := m["obj_id"].(float64); ok {
			s.ObjID = int(f)
		}
		if f, ok := m["version"].(float64); ok {
			s.Version = int(f)
		}
		if f, ok := m["user_id"].(float64); ok {
			s.UserID = int(f)
		}
		if str, ok := m["user_name"].(string); ok {
			s.UserName = str
		}
		if f, ok := m["create_time"].(float64); ok {
			s.CreateTime = int64(f)
		}
		if str, ok := m["title"].(string); ok {
			s.Title = str
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, nil
}

// DeleteSnapshot removes a snapshot by its obj_id.
func (v *Executor) DeleteSnapshot(objID, convID, projectID, stageID int) error {
	ops := []map[string]any{
		{
			"type":       "delete",
			"obj":        "snapshot",
			"obj_id":     objID,
			"conv_id":    convID,
			"project_id": projectID,
			"stage_id":   stageID,
			"company_id": v.WorkspaceID,
		},
	}
	resp, err := v.req("json", ops)
	if err != nil {
		return fmt.Errorf("DeleteSnapshot request failed: %w", err)
	}
	if _, err := firstOp(resp); err != nil {
		return fmt.Errorf("DeleteSnapshot: %w", err)
	}
	return nil
}

// GetSnapshot returns the node list of a specific snapshot.
// The result is a raw slice of node maps for diff comparison.
func (v *Executor) GetSnapshot(objID, convID, projectID, stageID int) ([]map[string]any, error) {
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "snapshot",
			"obj_id":     objID,
			"conv_id":    convID,
			"project_id": projectID,
			"stage_id":   stageID,
			"company_id": v.WorkspaceID,
		},
	}
	resp, err := v.req("json", ops)
	if err != nil {
		return nil, fmt.Errorf("GetSnapshot request failed: %w", err)
	}
	op, err := firstOp(resp)
	if err != nil {
		return nil, fmt.Errorf("GetSnapshot: %w", err)
	}

	raw, _ := op["list"].([]any)
	nodes := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			nodes = append(nodes, m)
		}
	}
	return nodes, nil
}
