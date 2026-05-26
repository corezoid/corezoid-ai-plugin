package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// argInt accepts either a float64 (JSON number) or a string and returns an
// int. Many dashboard arguments come from MCP clients that serialise numbers
// as strings, so we tolerate both forms.
func argInt(args map[string]interface{}, key string) (int, bool) {
	raw, ok := args[key]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	return 0, false
}

// handleCreateDashboard creates a Corezoid dashboard rooted at folder_id
// (defaulting to the configured stage) and returns the raw API response.
func handleCreateDashboard(ctx context.Context, args map[string]interface{}) (string, bool) {
	title, err := strArg(args, "title")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	description, _ := args["description"].(string)
	tzOffset, _ := argInt(args, "timezone_offset")

	v := NewValidator(ctx, 0)
	// Preserve the original semantics: when folder_id is supplied as a
	// malformed string we explicitly overwrite folderID (Atoi falls back to
	// 0), instead of silently falling back to the configured stage. argInt's
	// "missing or unparseable" semantics would have been a behavior change.
	folderID := v.StageID
	if fid, ok := args["folder_id"]; ok {
		if fidFloat, ok := fid.(float64); ok {
			folderID = int(fidFloat)
		} else if fidStr, ok := fid.(string); ok {
			folderID, _ = strconv.Atoi(fidStr)
		}
	}

	projectID := v.GetProjectIDByStageID(folderID)
	now := int(time.Now().Unix())

	op := map[string]any{
		"obj":         "dashboard",
		"type":        "create",
		"obj_type":    0,
		"title":       title,
		"description": description,
		"folder_id":   folderID,
		"stage_id":    folderID,
		"project_id":  projectID,
		"company_id":  v.WorkspaceID,
		"status":      "active",
		"time_range": map[string]any{
			"select":          "online",
			"start":           now,
			"stop":            now,
			"timezone_offset": tzOffset,
		},
	}

	resp, err := v.req("json", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleGetDashboard returns the raw show-dashboard response for the given ID.
func handleGetDashboard(ctx context.Context, args map[string]interface{}) (string, bool) {
	dashboardID, err := intArg(args, "dashboard_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	op := map[string]any{
		"obj":        "dashboard",
		"type":       "show",
		"obj_id":     dashboardID,
		"company_id": v.WorkspaceID,
	}

	resp, err := v.req("json", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleAddChart adds a chart to an existing dashboard.
func handleAddChart(ctx context.Context, args map[string]interface{}) (string, bool) {
	dashboardID, err := intArg(args, "dashboard_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	name, err := strArg(args, "name")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	chartType, err := strArg(args, "chart_type")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	seriesStr, err := strArg(args, "series")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	var series []interface{}
	if err := json.Unmarshal([]byte(seriesStr), &series); err != nil {
		return fmt.Sprintf("Error parsing series JSON: %v", err), true
	}

	v := NewValidator(ctx, 0)
	op := map[string]any{
		"obj":          "chart",
		"type":         "create",
		"obj_type":     chartType,
		"dashboard_id": dashboardID,
		"name":         name,
		"description":  "",
		"series":       series,
		"company_id":   v.WorkspaceID,
	}

	resp, err := v.req("json", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleModifyChart updates an existing chart's name/type/series.
func handleModifyChart(ctx context.Context, args map[string]interface{}) (string, bool) {
	chartID, err := strArg(args, "chart_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	dashboardID, err := intArg(args, "dashboard_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title, err := strArg(args, "name")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	chartType, err := strArg(args, "chart_type")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	seriesStr, err := strArg(args, "series")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	var series []interface{}
	if err := json.Unmarshal([]byte(seriesStr), &series); err != nil {
		return fmt.Sprintf("Error parsing series JSON: %v", err), true
	}

	v := NewValidator(ctx, 0)
	op := map[string]any{
		"obj":          "chart",
		"type":         "modify",
		"obj_type":     chartType,
		"obj_id":       chartID,
		"dashboard_id": dashboardID,
		"name":         title,
		"description":  "",
		"series":       series,
		"company_id":   v.WorkspaceID,
	}

	resp, err := v.req("json", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleGetChart fetches a single chart's config.
func handleGetChart(ctx context.Context, args map[string]interface{}) (string, bool) {
	chartID, err := strArg(args, "chart_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	dashboardID, err := intArg(args, "dashboard_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	op := map[string]any{
		"obj":          "chart",
		"type":         "get",
		"obj_id":       chartID,
		"dashboard_id": dashboardID,
		"company_id":   v.WorkspaceID,
	}

	resp, err := v.req("json", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleSetDashboardLayout rearranges the dashboard's chart grid. Input is
// [{chart_id, x, y, width, height}, …]; chart_id maps to obj_id on the wire.
func handleSetDashboardLayout(ctx context.Context, args map[string]interface{}) (string, bool) {
	dashboardID, err := intArg(args, "dashboard_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	gridStr, err := strArg(args, "grid")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	tzOffset, _ := argInt(args, "timezone_offset")

	// Parse grid input: [{chart_id, x, y, width, height}]
	var gridInput []map[string]interface{}
	if err := json.Unmarshal([]byte(gridStr), &gridInput); err != nil {
		return fmt.Sprintf("Error parsing grid JSON: %v", err), true
	}

	// Convert chart_id -> obj_id for the API payload
	apiGrid := make([]map[string]interface{}, 0, len(gridInput))
	for _, entry := range gridInput {
		chartHexID, _ := entry["chart_id"].(string)
		if chartHexID == "" {
			return "Error: each grid entry must have a non-empty chart_id", true
		}
		gridEntry := map[string]interface{}{
			"obj_id": chartHexID,
		}
		for _, field := range []string{"x", "y", "width", "height"} {
			if v, ok := entry[field]; ok {
				gridEntry[field] = v
			}
		}
		apiGrid = append(apiGrid, gridEntry)
	}

	v := NewValidator(ctx, 0)
	op := map[string]any{
		"obj":        "dashboard",
		"type":       "modify",
		"obj_id":     dashboardID,
		"company_id": v.WorkspaceID,
		"time_range": map[string]any{
			"select":          "online",
			"timezone_offset": tzOffset,
		},
		"grid": apiGrid,
	}

	resp, err := v.req("json", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}
