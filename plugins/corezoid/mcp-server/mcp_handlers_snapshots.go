package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// handleCreateSnapshot manually creates a snapshot for the given process.
func handleCreateSnapshot(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	title, _ := args["title"].(string)
	if title == "" {
		name := extractProcessNameFromPath(filePath)
		title = fmt.Sprintf("manual snapshot %s %s", name, time.Now().UTC().Format("2006-01-02 15:04"))
	}

	v := NewValidator(ctx, procID)
	projectID, envNotice := resolveAndCacheProjectID(v)
	if projectID == 0 {
		return "Error: could not resolve project_id. Set COREZOID_PROJECT_ID in .env or ensure COREZOID_STAGE_ID is configured.", true
	}

	objID, version, err := v.CreateSnapshot(procID, projectID, v.StageID, title)
	if err != nil {
		return fmt.Sprintf("Error creating snapshot: %v", err), true
	}

	result := fmt.Sprintf("Snapshot created: version %d (obj_id=%d) — %q", version, objID, title)
	if envNotice != "" {
		result += " " + envNotice
	}
	return result, false
}

// handleListSnapshots returns all snapshots for the given process.
func handleListSnapshots(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	v := NewValidator(ctx, procID)
	projectID, _ := resolveAndCacheProjectID(v)
	if projectID == 0 {
		return "Error: could not resolve project_id.", true
	}

	snapshots, err := v.ListSnapshots(procID, projectID, v.StageID)
	if err != nil {
		return fmt.Sprintf("Error listing snapshots: %v", err), true
	}

	if len(snapshots) == 0 {
		return "No snapshots found for this process.", false
	}

	b, _ := json.MarshalIndent(snapshots, "", "  ")
	return string(b), false
}

// handleDeleteSnapshot removes a snapshot by obj_id.
func handleDeleteSnapshot(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	objID, err := intArg(args, "snapshot_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, procID)
	projectID, _ := resolveAndCacheProjectID(v)
	if projectID == 0 {
		return "Error: could not resolve project_id.", true
	}

	if err := v.DeleteSnapshot(objID, procID, projectID, v.StageID); err != nil {
		return fmt.Sprintf("Error deleting snapshot: %v", err), true
	}

	return fmt.Sprintf("Snapshot %d deleted.", objID), false
}

// handleGetSnapshot returns the nodes of a specific snapshot for diff comparison.
func handleGetSnapshot(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	objID, err := intArg(args, "snapshot_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, procID)
	projectID, _ := resolveAndCacheProjectID(v)
	if projectID == 0 {
		return "Error: could not resolve project_id.", true
	}

	nodes, err := v.GetSnapshot(objID, procID, projectID, v.StageID)
	if err != nil {
		return fmt.Sprintf("Error getting snapshot: %v", err), true
	}

	b, _ := json.MarshalIndent(nodes, "", "  ")
	return string(b), false
}

// extractProcessNameFromPath returns the human-readable name from a conv.json filename.
// "379055_Escalation.conv.json" → "Escalation"
func extractProcessNameFromPath(filePath string) string {
	base := filepath.Base(filePath)
	name := strings.TrimSuffix(base, ".conv.json")
	if idx := strings.Index(name, "_"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}
