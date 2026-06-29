package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// handleListWorkspaces prints the workspaces the authenticated user can see.
// Used during login when ACCOUNT_URL is set but WORKSPACE_ID hasn't been
// picked yet.
func handleListWorkspaces(ctx context.Context, _ map[string]interface{}) (string, bool) {
	v := NewValidator(ctx, 0)
	ops := []map[string]any{
		{
			"type": "list",
			"obj":  "company",
		},
	}
	resp, err := v.req("list_workspaces", ops)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	// Extract the workspace list from ops[0].list
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return "No workspaces found", false
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	list, _ := opMap["list"].([]interface{})
	if len(list) == 0 {
		return "No workspaces found", false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Workspaces (%d total):\n\n", len(list)))
	for _, item := range list {
		ws, _ := item.(map[string]interface{})
		companyID, _ := ws["company_id"].(string)
		title, _ := ws["title"].(string)
		isOwner, _ := ws["is_owner"].(bool)
		isAdmin, _ := ws["is_admin"].(bool)

		role := "member"
		if isOwner {
			role = "owner"
		} else if isAdmin {
			role = "admin"
		}

		sb.WriteString(fmt.Sprintf("  %-45s  %s  [%s]\n", companyID, title, role))
	}
	return sb.String(), false
}

// handleListProjects prints the projects in a given workspace.
func handleListProjects(ctx context.Context, args map[string]interface{}) (string, bool) {
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "projects",
			"obj_id":     0,
			"id":         companyID,
			"company_id": companyID,
			"sort":       "title",
		},
	}
	resp, err := v.req("list_projects", ops)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return "No projects found", false
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		return fmt.Sprintf("Error: %s", desc), true
	}
	list, _ := opMap["list"].([]interface{})
	if len(list) == 0 {
		return "No projects found", false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Projects in workspace %s (%d total):\n\n", companyID, len(list)))
	sb.WriteString(fmt.Sprintf("  %-10s  %-35s  %-30s  %s\n", "ID", "Title", "Short name", "Owner"))
	sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("-", 95)))
	for _, item := range list {
		p, _ := item.(map[string]interface{})
		projectID := int64(0)
		if f, ok := p["project_id"].(float64); ok {
			projectID = int64(f)
		}
		title, _ := p["title"].(string)
		shortName, _ := p["short_name"].(string)
		ownerLogin, _ := p["owner_login"].(string)
		undeployed := int(0)
		if f, ok := p["undeployed"].(float64); ok {
			undeployed = int(f)
		}
		undeployedStr := ""
		if undeployed > 0 {
			undeployedStr = fmt.Sprintf(" [%d undeployed]", undeployed)
		}
		sb.WriteString(fmt.Sprintf("  %-10d  %-35s  %-30s  %s%s\n",
			projectID, title, shortName, ownerLogin, undeployedStr))
	}
	return sb.String(), false
}

// handleListStages prints the stages (root folders) in a project.
func handleListStages(ctx context.Context, args map[string]interface{}) (string, bool) {
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "project",
			"obj_id":     projectID,
			"id":         companyID,
			"company_id": companyID,
			"sort":       "date",
			"order":      "asc",
		},
	}
	resp, err := v.req("list_stages", ops)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return "No stages found", false
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		return fmt.Sprintf("Error: %s", desc), true
	}
	list, _ := opMap["list"].([]interface{})
	if len(list) == 0 {
		return "No stages found", false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Stages in project %d (%d total):\n\n", projectID, len(list)))
	sb.WriteString(fmt.Sprintf("  %-10s  %-20s  %-20s  %s\n", "ID", "Title", "Short name", "Immutable"))
	sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("-", 70)))
	for _, item := range list {
		s, _ := item.(map[string]interface{})
		stageID := int64(0)
		if f, ok := s["obj_id"].(float64); ok {
			stageID = int64(f)
		}
		title, _ := s["title"].(string)
		shortName, _ := s["short_name"].(string)
		immutable, _ := s["immutable"].(bool)
		immutableStr := "no"
		if immutable {
			immutableStr = "yes"
		}
		sb.WriteString(fmt.Sprintf("  %-10d  %-20s  %-20s  %s\n", stageID, title, shortName, immutableStr))
	}
	return sb.String(), false
}

// handleCreateProject creates a new project in a workspace. Optional `stages`
// arg is a JSON array of {"title":"...","immutable":bool}.
func handleCreateProject(ctx context.Context, args map[string]interface{}) (string, bool) {
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title, err := strArg(args, "title")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	shortName := optStrArg(args, "short_name")
	description := optStrArg(args, "description")

	op := map[string]any{
		"type":       "create",
		"obj":        "project",
		"company_id": companyID,
		"title":      title,
	}
	if shortName != "" {
		op["short_name"] = shortName
	}
	if description != "" {
		op["description"] = description
	}
	if stagesJSON := strings.TrimSpace(optStrArg(args, "stages")); stagesJSON != "" {
		var stages []map[string]any
		if err := json.Unmarshal([]byte(stagesJSON), &stages); err != nil {
			return fmt.Sprintf("Error: stages must be a JSON array, got: %v", err), true
		}
		op["stages"] = stages
	}

	v := NewValidator(ctx, 0)
	resp, err := v.req("create_project", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	opMap, opErr := firstOp(resp)
	if opErr != nil {
		return fmt.Sprintf("Error: %v", opErr), true
	}

	projectID := int64(0)
	if f, ok := opMap["obj_id"].(float64); ok {
		projectID = int64(f)
	}
	stageIDs := []int64{}
	if arr, ok := opMap["stages"].([]interface{}); ok {
		for _, item := range arr {
			if f, ok := item.(float64); ok {
				stageIDs = append(stageIDs, int64(f))
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project %q created — project_id=%d", title, projectID))
	if len(stageIDs) > 0 {
		parts := make([]string, len(stageIDs))
		for i, id := range stageIDs {
			parts[i] = fmt.Sprintf("%d", id)
		}
		sb.WriteString(fmt.Sprintf("  stages=[%s]", strings.Join(parts, ", ")))
	}
	return sb.String(), false
}

// handleModifyProject renames a project and/or updates its short_name and
// description. At least one of title/short_name/description must be supplied.
func handleModifyProject(ctx context.Context, args map[string]interface{}) (string, bool) {
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title := optStrArg(args, "title")
	shortName := optStrArg(args, "short_name")
	description := optStrArg(args, "description")
	if title == "" && shortName == "" && description == "" {
		return "Error: at least one of title, short_name, description must be provided", true
	}

	op := map[string]any{
		"type":       "modify",
		"obj":        "project",
		"obj_id":     projectID,
		"company_id": companyID,
	}
	if title != "" {
		op["title"] = title
	}
	if shortName != "" {
		op["short_name"] = shortName
	}
	if description != "" {
		op["description"] = description
	}

	v := NewValidator(ctx, 0)
	resp, err := v.req("modify_project", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if _, opErr := firstOp(resp); opErr != nil {
		return fmt.Sprintf("Error: %v", opErr), true
	}

	parts := []string{}
	if title != "" {
		parts = append(parts, fmt.Sprintf("title=%q", title))
	}
	if shortName != "" {
		parts = append(parts, fmt.Sprintf("short_name=%q", shortName))
	}
	if description != "" {
		parts = append(parts, fmt.Sprintf("description=%q", description))
	}
	return fmt.Sprintf("Project #%d updated (%s)", projectID, strings.Join(parts, ", ")), false
}

// handleDeleteProject moves a project to the recycle bin (Trash). The server
// preserves the project — restore-project (not yet exposed) brings it back.
func handleDeleteProject(ctx context.Context, args map[string]interface{}) (string, bool) {
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	op := map[string]any{
		"type":       "delete",
		"obj":        "project",
		"obj_id":     projectID,
		"company_id": companyID,
	}
	v := NewValidator(ctx, 0)
	resp, err := v.req("del_project", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if _, opErr := firstOp(resp); opErr != nil {
		return fmt.Sprintf("Error: %v", opErr), true
	}
	return fmt.Sprintf("Project #%d moved to Trash.", projectID), false
}

// handleCreateStage creates a new empty stage (obj_type=3 folder) inside a project.
// The stage is immediately visible in list-stages. An optional description can be
// provided — if omitted the stage is created with an empty description.
func handleCreateStage(ctx context.Context, args map[string]interface{}) (string, bool) {
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title, err := strArg(args, "title")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	description := optStrArg(args, "description")

	op := map[string]any{
		"type":       "create",
		"obj":        "stage",
		"obj_id":     projectID,
		"company_id": companyID,
		"title":      title,
	}
	if description != "" {
		op["description"] = description
	}

	v := NewValidator(ctx, 0)
	resp, err := v.req("create_stage", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	opMap, opErr := firstOp(resp)
	if opErr != nil {
		return fmt.Sprintf("Error: %v", opErr), true
	}

	stageID := int64(0)
	if f, ok := opMap["obj_id"].(float64); ok {
		stageID = int64(f)
	}
	return fmt.Sprintf("Stage %q created — stage_id=%d, project_id=%d", title, stageID, projectID), false
}

// handleCloneStage duplicates an existing stage (with all its processes) under
// a new title inside the same project. The clone is performed server-side via
// the Corezoid copy operation, so all processes, folders and their content are
// preserved. Returns the new stage_id on success.
func handleCloneStage(ctx context.Context, args map[string]interface{}) (string, bool) {
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	stageID, err := intArg(args, "stage_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	newTitle, err := strArg(args, "new_title")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	op := map[string]any{
		"type":       "copy",
		"obj":        "stage",
		"obj_id":     stageID,
		"company_id": companyID,
		"title":      newTitle,
	}

	v := NewValidator(ctx, 0)
	resp, err := v.req("clone_stage", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	opMap, opErr := firstOp(resp)
	if opErr != nil {
		return fmt.Sprintf("Error: %v", opErr), true
	}

	newStageID := int64(0)
	if f, ok := opMap["obj_id"].(float64); ok {
		newStageID = int64(f)
	}
	return fmt.Sprintf("Stage #%d cloned as %q — new_stage_id=%d", stageID, newTitle, newStageID), false
}

// handleShowProject returns a project's stages and short_name plus the parent
// folder ID. Use list-stages for a richer per-stage view.
func handleShowProject(ctx context.Context, args map[string]interface{}) (string, bool) {
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	op := map[string]any{
		"type":       "show",
		"obj":        "project",
		"obj_id":     projectID,
		"company_id": companyID,
	}
	v := NewValidator(ctx, 0)
	resp, err := v.req("show_project", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	opMap, opErr := firstOp(resp)
	if opErr != nil {
		return fmt.Sprintf("Error: %v", opErr), true
	}

	shortName, _ := opMap["obj_short_name"].(string)
	parentID := int64(0)
	if f, ok := opMap["parent_obj_id"].(float64); ok {
		parentID = int64(f)
	}
	parentType, _ := opMap["parent_obj_type"].(string)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project #%d (short_name=%q, parent=%s#%d):\n\n", projectID, shortName, parentType, parentID))

	stages, _ := opMap["stages"].([]interface{})
	if len(stages) == 0 {
		sb.WriteString("  (no stages visible to the caller)\n")
		return sb.String(), false
	}
	sb.WriteString(fmt.Sprintf("  %-10s  %-25s  %s\n", "Stage ID", "Title", "Short name"))
	sb.WriteString("  " + strings.Repeat("-", 55) + "\n")
	for _, item := range stages {
		s, _ := item.(map[string]interface{})
		stageID := int64(0)
		if f, ok := s["obj_id"].(float64); ok {
			stageID = int64(f)
		}
		title, _ := s["title"].(string)
		sn, _ := s["obj_short_name"].(string)
		sb.WriteString(fmt.Sprintf("  %-10d  %-25s  %s\n", stageID, title, sn))
	}
	return sb.String(), false
}
