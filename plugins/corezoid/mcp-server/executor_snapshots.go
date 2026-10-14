package main

import "fmt"

// Snapshot describes one snapshot entry returned by list-snapshots.
//
// user_id/user_name name the account that owns the credential; for a JWT
// request that is the owner, not the token that signed it (issue #178). The
// actor_* fields identify the signing credential itself and are filled only
// when the server returns them.
type Snapshot struct {
	ObjID      int    `json:"obj_id"`
	Version    int    `json:"version"`
	UserID     int    `json:"user_id"`
	UserName   string `json:"user_name"`
	ActorID    string `json:"actor_id,omitempty"`
	ActorType  string `json:"actor_type,omitempty"`
	ActorTitle string `json:"actor_title,omitempty"`
	CreateTime int64  `json:"create_time"`
	Title      string `json:"title"`
}

// Author is the best label for whoever made the snapshot: the signing
// credential when the server reports it, else the owner account.
func (s Snapshot) Author() string {
	if a := actorLabel(s.ActorTitle, s.ActorType, s.ActorID); a != "" {
		return a
	}
	return s.UserName
}

// actorLabel names the signing credential: its title, else "<type> <id>".
// Empty when the server reported no actor at all.
func actorLabel(title, kind, id string) string {
	if title != "" {
		return title
	}
	if id == "" {
		return ""
	}
	if kind == "" {
		kind = "actor"
	}
	return kind + " " + id
}

// idString renders an id the server may send as a number or a string.
func idString(v any) string {
	switch x := v.(type) {
	case float64:
		return fmt.Sprintf("%d", int64(x))
	case string:
		return x
	}
	return ""
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
		s.ActorID = idString(m["actor_id"])
		s.ActorType, _ = m["actor_type"].(string)
		s.ActorTitle, _ = m["actor_title"].(string)
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
