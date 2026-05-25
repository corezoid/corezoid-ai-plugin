package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// FolderInfo holds the result of a "show folder" API call.
type FolderInfo struct {
	ObjID         int    `json:"obj_id"`
	Title         string `json:"title"`
	ObjType       int    `json:"obj_type"`
	ParentObjID   int    `json:"parent_obj_id"`
	ParentObjType string `json:"parent_obj_type"`
}

// ShowFolder calls the "show folder" API for the given folderID and returns FolderInfo.
// obj_type values: 0 — normal, 1 — root user, 2 — project, 3 — stage.
func (v *Executor) ShowFolder(folderID int) (*FolderInfo, error) {
	ops := []map[string]any{
		{
			"type":   "show",
			"obj":    "folder",
			"obj_id": folderID,
		},
	}
	response, err := v.req("json", ops)
	if err != nil {
		return nil, fmt.Errorf("ShowFolder request failed: %w", err)
	}
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("ShowFolder: unexpected request_proc: %v", response["request_proc"])
	}
	opsRaw, ok := response["ops"].([]any)
	if !ok || len(opsRaw) == 0 {
		return nil, fmt.Errorf("ShowFolder: empty ops in response")
	}
	op, ok := opsRaw[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ShowFolder: unexpected ops format")
	}
	if op["proc"] != "ok" {
		return nil, fmt.Errorf("ShowFolder: proc error: %v", op["proc"])
	}
	info := &FolderInfo{}
	if val, ok := op["obj_id"].(float64); ok {
		info.ObjID = int(val)
	}
	if val, ok := op["title"].(string); ok {
		info.Title = val
	}
	if val, ok := op["obj_type"].(float64); ok {
		info.ObjType = int(val)
	}
	if val, ok := op["parent_obj_id"].(float64); ok {
		info.ParentObjID = int(val)
	}
	if val, ok := op["parent_obj_type"].(string); ok {
		info.ParentObjType = val
	}
	return info, nil
}

// CreateFolder creates a new folder under parentFolderID and returns the new folder's obj_id.
func (v *Executor) CreateFolder(parentFolderID int, title, desc string) (int, error) {
	ops := []map[string]any{
		{
			"title":       title,
			"description": desc,
			"folder_id":   parentFolderID,
			"company_id":  v.WorkspaceID,
			"obj":         "folder",
			"type":        "create",
		},
	}
	response, err := v.req("json", ops)
	if err != nil {
		return 0, fmt.Errorf("CreateFolder request failed: %w", err)
	}
	if response["request_proc"] != "ok" {
		return 0, fmt.Errorf("CreateFolder: unexpected request_proc: %v", response["request_proc"])
	}
	opsRaw, ok := response["ops"].([]any)
	if !ok || len(opsRaw) == 0 {
		return 0, fmt.Errorf("CreateFolder: empty ops in response")
	}
	op, ok := opsRaw[0].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("CreateFolder: unexpected ops format")
	}
	if op["proc"] != "ok" {
		return 0, fmt.Errorf("CreateFolder: proc error: %v", op["proc"])
	}
	objID, ok := op["obj_id"].(float64)
	if !ok {
		return 0, fmt.Errorf("CreateFolder: obj_id not found in response")
	}
	return int(objID), nil
}

// resolveFolderPathFromAPI builds a relative path from the stage root down to folderID
// by walking up the folder hierarchy via ShowFolder API calls.
func (v *Executor) resolveFolderPathFromAPI(folderID int) (string, error) {
	const maxDepth = 20
	type segment struct {
		id    int
		title string
	}
	var segments []segment
	currentID := folderID
	for i := 0; i < maxDepth; i++ {
		if v.StageID != 0 && currentID == v.StageID {
			break
		}
		info, err := v.ShowFolder(currentID)
		if err != nil {
			return "", fmt.Errorf("failed to resolve folder path at id %d: %w", currentID, err)
		}
		if info.ObjType == 3 || (v.StageID != 0 && info.ObjID == v.StageID) {
			break
		}
		safeName := strings.ReplaceAll(info.Title, " ", "_")
		segments = append(segments, segment{id: info.ObjID, title: safeName})
		if info.ParentObjID == 0 || info.ParentObjID == currentID {
			break
		}
		currentID = info.ParentObjID
	}
	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}
	parts := make([]string, len(segments))
	for i, s := range segments {
		parts[i] = fmt.Sprintf("%d_%s", s.id, s.title)
	}
	return strings.Join(parts, string(os.PathSeparator)), nil
}

// CreateVariable creates a new environment variable using the provided parameters.
func (v *Executor) CreateVariable(rootFolderIDBin, name, description, value string) error {
	rootFolderID, _ := strconv.Atoi(rootFolderIDBin)
	ops := []map[string]any{
		{
			"obj":          "env_var",
			"data_type":    "raw",
			"title":        description,
			"short_name":   name,
			"company_id":   v.WorkspaceID,
			"stage_id":     rootFolderID,
			"project_id":   v.GetProjectIDByStageID(rootFolderID),
			"env_var_type": "visible",
			"type":         "create",
			"obj_type":     0,
			"status":       "active",
			"scopes":       []map[string]string{{"type": "*", "fields": "*"}},
			"value":        value,
		},
	}
	if v.Debug {
		logger.Debug("Sending create variable request")
	}
	response, err := v.req("create-variable", ops)
	if err != nil {
		return fmt.Errorf("failed to create variable: %w", err)
	}
	if response["request_proc"] != "ok" {
		return fmt.Errorf("failed to create variable: %v", response)
	}
	if opsArray, ok := response["ops"].([]interface{}); ok {
		for _, op := range opsArray {
			if opMap, ok := op.(map[string]interface{}); ok {
				if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
					errorMsg := "unknown error"
					if msg, ok := opMap["description"].(string); ok {
						errorMsg = msg
					}
					return fmt.Errorf("failed to create variable: %s", errorMsg)
				}
			}
		}
	}
	err = v.updateVariablesFile(name, description, value)
	if err != nil {
		logger.Error("Failed to update local variables file: %v", err)
	}
	return nil
}

// updateVariablesFile updates the local .processes/variables.json file.
func (v *Executor) updateVariablesFile(name, description, value string) error {
	err := os.MkdirAll(".processes", 0755)
	if err != nil {
		return fmt.Errorf("failed to create .processes directory: %v", err)
	}
	variablesPath := ".processes/variables.json"
	var variables []map[string]string
	if data, err := os.ReadFile(variablesPath); err == nil {
		err = json.Unmarshal(data, &variables)
		if err != nil {
			return fmt.Errorf("failed to parse existing variables.json: %v", err)
		}
	}
	found := false
	for i, variable := range variables {
		if variable["name"] == name {
			variables[i] = map[string]string{"name": name, "description": description, "value": value}
			found = true
			break
		}
	}
	if !found {
		variables = append(variables, map[string]string{"name": name, "description": description, "value": value})
	}
	data, err := json.MarshalIndent(variables, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal variables: %v", err)
	}
	err = os.WriteFile(variablesPath, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write variables.json: %v", err)
	}
	if v.Debug {
		logger.Debug("Updated local variables file: %s", variablesPath)
	}
	return nil
}

// CreateAlias creates a new alias for a process and returns the alias ID.
func (v *Executor) CreateAlias(shortName string, procID, stageID int) (int, error) {
	projectID := v.GetProjectIDByStageID(stageID)
	ops := []map[string]any{
		{
			"type":        "create",
			"obj":         "alias",
			"title":       shortName,
			"short_name":  shortName,
			"company_id":  v.WorkspaceID,
			"stage_id":    stageID,
			"project_id":  projectID,
			"obj_to_id":   procID,
			"obj_to_type": "conv",
		},
	}
	if v.Debug {
		logger.Debug("Sending create alias request")
	}
	response, err := v.req("create_alias", ops)
	if err != nil {
		return 0, fmt.Errorf("create alias request failed: %w", err)
	}
	if opsArray, ok := response["ops"].([]interface{}); ok && len(opsArray) > 0 {
		if firstOp, ok := opsArray[0].(map[string]interface{}); ok {
			if proc, _ := firstOp["proc"].(string); proc != "ok" {
				desc, _ := firstOp["description"].(string)
				if desc == "" {
					desc = "unknown error"
				}
				return 0, fmt.Errorf("create alias failed: %s", desc)
			}
			if objID, ok := firstOp["obj_id"].(float64); ok {
				return int(objID), nil
			}
		}
	}
	return 0, fmt.Errorf("create alias: unexpected response format")
}

// listAliasesByStage returns a map of short_name -> obj_id for all aliases in the given stage.
func (v *Executor) listAliasesByStage(stage int) (map[string]int, error) {
	projectID := v.GetProjectIDByStageID(stage)
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "aliases",
			"company_id": v.WorkspaceID,
			"stage_id":   stage,
			"project_id": projectID,
		},
	}
	response, err := v.req("json", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to list aliases: %v", err)
	}
	opsRaw, ok := response["ops"].([]any)
	if !ok || len(opsRaw) == 0 {
		return nil, fmt.Errorf("empty response from alias list")
	}
	op, ok := opsRaw[0].(map[string]any)
	if !ok || op["proc"] != "ok" {
		return nil, fmt.Errorf("alias list failed")
	}
	result := make(map[string]int)
	list, _ := op["list"].([]any)
	for _, item := range list {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sn, _ := itemMap["short_name"].(string)
		if sn == "" {
			continue
		}
		if objID, ok := itemMap["obj_id"].(float64); ok {
			result[sn] = int(objID)
		}
	}
	return result, nil
}

// GetAliasByShortName checks if an alias with the given short_name exists.
func (v *Executor) GetAliasByShortName(shortName string) (int, error) {
	aliases, err := v.listAliasesByStage(v.StageID)
	if err != nil {
		return 0, fmt.Errorf("alias '%s' does not exist: %v", shortName, err)
	}
	if objID, ok := aliases[shortName]; ok {
		return objID, nil
	}
	return 0, fmt.Errorf("alias '%s' does not exist", shortName)
}

// listEnvVarsByStage returns a map of short_name -> obj_id for all env vars in the given stage.
func (v *Executor) listEnvVarsByStage(stage int) (map[string]int, error) {
	projectID := v.GetProjectIDByStageID(stage)
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "env_var",
			"company_id": v.WorkspaceID,
			"stage_id":   stage,
			"project_id": projectID,
		},
	}
	response, err := v.req("json", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to list env variables: %v", err)
	}
	opsRaw, ok := response["ops"].([]any)
	if !ok || len(opsRaw) == 0 {
		return nil, fmt.Errorf("empty response from env var list")
	}
	op, ok := opsRaw[0].(map[string]any)
	if !ok || op["proc"] != "ok" {
		return nil, fmt.Errorf("env var list failed")
	}
	result := make(map[string]int)
	list, _ := op["list"].([]any)
	for _, item := range list {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sn, _ := itemMap["short_name"].(string)
		if sn == "" {
			continue
		}
		if objID, ok := itemMap["obj_id"].(float64); ok {
			result[sn] = int(objID)
		}
	}
	return result, nil
}

// GetEnvVarByShortName checks if an environment variable with the given short_name exists.
func (v *Executor) GetEnvVarByShortName(shortName string) (int, error) {
	vars, err := v.listEnvVarsByStage(v.StageID)
	if err != nil {
		return 0, fmt.Errorf("env variable '@%s' does not exist: %v", shortName, err)
	}
	if objID, ok := vars[shortName]; ok {
		return objID, nil
	}
	return 0, fmt.Errorf("env variable '@%s' does not exist", shortName)
}

// GetProjectIDByStageID resolves the project ID for a given stage/folder ID
// by walking up the folder hierarchy.
func (v *Executor) GetProjectIDByStageID(folderID int) int {
	const maxDepth = 20
	currentID := folderID
	for i := 0; i < maxDepth; i++ {
		info, err := v.ShowFolder(currentID)
		if err != nil {
			logger.Error("GetProjectIDByStageID: error showing folder %d: %v", currentID, err)
			return 0
		}
		if info.ObjID == folderID {
			return info.ParentObjID
		}
		currentID = info.ParentObjID
	}
	logger.Error("GetProjectIDByStageID: cannot find folder %d", folderID)
	return 0
}
