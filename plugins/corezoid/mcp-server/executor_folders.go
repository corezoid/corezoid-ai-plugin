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
// obj_type values observed in current admin API: 0 = normal, 5 = project,
// 10 = stage. Older deployments/tests may still return 2/3 for project/stage.
func (v *Executor) ShowFolder(folderID int) (*FolderInfo, error) {
	ops := []map[string]any{
		{
			"type":       "show",
			"obj":        "folder",
			"obj_id":     folderID,
			"company_id": v.WorkspaceID,
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

func isFolderProjectObjType(objType int) bool {
	return objType == 2 || objType == 5
}

func isFolderStageObjType(objType int) bool {
	return objType == 3 || objType == 10
}

// FolderChild describes one entry returned by ListFolder. Folders and convs
// share the same response slice, distinguished by Obj ("folder" | "conv") and
// — for convs — ConvType ("process" | "state"). ChangeTime/Version/IsDeployed
// are populated from the same list response so a pull-folder baseline snapshot
// can be built without a follow-up GetProcessByID per conv.
type FolderChild struct {
	Obj        string // "folder" | "conv"
	ObjID      int
	Title      string
	ObjType    int    // folders: 0 normal; project/stage are 5/10 (legacy 2/3)
	ConvType   string // for convs only
	Status     string
	ChangeTime int64 // server last-modified (advances on every server commit)
	Version    int64 // draft version timestamp for undeployed convs, 0 when deployed or absent
	IsDeployed bool  // true when the process is currently deployed (no pending draft)
}

// ListFolder returns the immediate children of folderID (subfolders + convs).
// The Corezoid list-folder endpoint returns a flat slice with each entry
// tagged by "obj"; this method preserves that shape so callers can filter.
func (v *Executor) ListFolder(folderID int) ([]FolderChild, error) {
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "folder",
			"obj_id":     folderID,
			"company_id": v.WorkspaceID,
		},
	}
	response, err := v.req("list_folder", ops)
	if err != nil {
		return nil, fmt.Errorf("ListFolder request failed: %w", err)
	}
	first, err := firstOp(response)
	if err != nil {
		return nil, err
	}
	list, _ := first["list"].([]any)
	out := make([]FolderChild, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		child := FolderChild{}
		if s, ok := m["obj"].(string); ok {
			child.Obj = s
		}
		if f, ok := m["obj_id"].(float64); ok {
			child.ObjID = int(f)
		}
		if s, ok := m["title"].(string); ok {
			child.Title = s
		}
		if f, ok := m["obj_type"].(float64); ok {
			child.ObjType = int(f)
		}
		if s, ok := m["conv_type"].(string); ok {
			child.ConvType = s
		}
		if s, ok := m["status"].(string); ok {
			child.Status = s
		}
		if f, ok := m["change_time"].(float64); ok {
			child.ChangeTime = int64(f)
		}
		if f, ok := m["version"].(float64); ok {
			child.Version = int64(f)
		}
		if b, ok := m["is_deployed"].(bool); ok {
			child.IsDeployed = b
		}
		out = append(out, child)
	}
	return out, nil
}

// WorkspaceRootItem is one top-level entry in a Corezoid workspace: a folder,
// a conv (process / state diagram) or a dashboard living directly at the
// workspace root (project_id=0, stage_id=0). Used by ListWorkspaceRoot and
// BatchExportSchemes to mirror the workspace UI's root view onto disk when the
// user selects "No Project". ChangeTime/Version/IsDeployed mirror FolderChild
// so a No-Project baseline snapshot builds without extra GetProcessByID calls.
type WorkspaceRootItem struct {
	ObjID      int
	Title      string
	ObjType    string // "folder" | "conv" | "dashboard"
	ConvType   string // for convs only: "process" | "state"
	ChangeTime int64
	Version    int64
	IsDeployed bool
}

// ListWorkspaceRoot lists the top-level items (folders, convs, dashboards) in
// the current workspace. The equivalent of opening a workspace's root view in
// the Corezoid UI — items whose project_id and stage_id are both 0.
func (v *Executor) ListWorkspaceRoot() ([]WorkspaceRootItem, error) {
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "folder",
			"obj_id":     0,
			"company_id": v.WorkspaceID,
			"id":         v.WorkspaceID,
			"sort":       "date",
			"order":      "asc",
		},
	}
	response, err := v.req("list_workspace_root", ops)
	if err != nil {
		return nil, fmt.Errorf("ListWorkspaceRoot request failed: %w", err)
	}
	first, err := firstOp(response)
	if err != nil {
		return nil, err
	}
	list, _ := first["list"].([]any)
	out := make([]WorkspaceRootItem, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		it := WorkspaceRootItem{}
		if f, ok := m["obj_id"].(float64); ok {
			it.ObjID = int(f)
		}
		if s, ok := m["title"].(string); ok {
			it.Title = s
		}
		if s, ok := m["obj_type"].(string); ok {
			it.ObjType = s
		}
		if s, ok := m["conv_type"].(string); ok {
			it.ConvType = s
		}
		if f, ok := m["change_time"].(float64); ok {
			it.ChangeTime = int64(f)
		}
		if f, ok := m["version"].(float64); ok {
			it.Version = int64(f)
		}
		if b, ok := m["is_deployed"].(bool); ok {
			it.IsDeployed = b
		}
		if it.ObjID == 0 || it.ObjType == "" {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// BatchExportSchemes exports many items in a single API call and returns their
// download URLs, one per item in the same order. Cuts N round-trips (one per
// item) down to one when mirroring a whole workspace root — the Corezoid API
// accepts multiple obj_scheme ops per request.
func (v *Executor) BatchExportSchemes(items []WorkspaceRootItem) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	opType := "create"
	if v.Token != "" {
		opType = "download"
	}
	ops := make([]map[string]any, 0, len(items))
	for _, it := range items {
		ops = append(ops, map[string]any{
			"type":       opType,
			"obj":        "obj_scheme",
			"obj_id":     it.ObjID,
			"company_id": v.WorkspaceID,
			"obj_type":   it.ObjType,
			"with_alias": false,
			"async":      false,
			"format":     "zip",
		})
	}
	response, err := v.req("export_process", ops)
	if err != nil {
		return nil, fmt.Errorf("BatchExportSchemes request failed: %w", err)
	}
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("BatchExportSchemes: request_proc not ok: %v", response)
	}
	opsRaw, ok := response["ops"].([]any)
	if !ok || len(opsRaw) != len(items) {
		return nil, fmt.Errorf("BatchExportSchemes: expected %d ops, got %d", len(items), len(opsRaw))
	}
	urls := make([]string, len(items))
	for i, opRaw := range opsRaw {
		opMap, ok := opRaw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("BatchExportSchemes: op %d unexpected format", i)
		}
		if proc, _ := opMap["proc"].(string); proc != "ok" {
			desc, _ := opMap["description"].(string)
			if desc == "" {
				desc = fmt.Sprintf("proc=%v", opMap["proc"])
			}
			return nil, fmt.Errorf("BatchExportSchemes: op %d (%s #%d): %s", i, items[i].ObjType, items[i].ObjID, desc)
		}
		url, _ := opMap["download_url"].(string)
		if url == "" {
			return nil, fmt.Errorf("BatchExportSchemes: op %d (%s #%d): no download_url", i, items[i].ObjType, items[i].ObjID)
		}
		urls[i] = url
	}
	return urls, nil
}

// ModifyFolder renames a folder and/or updates its description. At least one
// of title/description must be non-empty — the caller is expected to enforce
// that before calling.
func (v *Executor) ModifyFolder(folderID int, title, description string) error {
	op := map[string]any{
		"type":       "modify",
		"obj":        "folder",
		"obj_id":     folderID,
		"company_id": v.WorkspaceID,
	}
	if title != "" {
		op["title"] = title
	}
	if description != "" {
		op["description"] = description
	}
	resp, err := v.req("modify_folder", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("ModifyFolder request failed: %w", err)
	}
	if _, err := firstOp(resp); err != nil {
		return err
	}
	return nil
}

// DeleteProcess moves a process (conv) to the recycle bin. The operation is
// reversible from the Corezoid UI's Trash. Permanent destruction is not
// exposed via this tool — the same intentional policy as delete-folder and
// delete-project.
func (v *Executor) DeleteProcess(processID int) error {
	op := map[string]any{
		"type":       "delete",
		"obj":        "conv",
		"obj_id":     processID,
		"company_id": v.WorkspaceID,
	}
	resp, err := v.req("delete_conv", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("DeleteProcess request failed: %w", err)
	}
	if _, err := firstOp(resp); err != nil {
		return err
	}
	return nil
}

// DeleteFolder moves a folder to the recycle bin. Like delete-project this is
// reversible from the Corezoid UI's Trash; permanent destruction is not
// exposed via this tool.
func (v *Executor) DeleteFolder(folderID int) error {
	op := map[string]any{
		"type":       "delete",
		"obj":        "folder",
		"obj_id":     folderID,
		"company_id": v.WorkspaceID,
	}
	resp, err := v.req("delete_folder", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("DeleteFolder request failed: %w", err)
	}
	if _, err := firstOp(resp); err != nil {
		return err
	}
	return nil
}

// CreateFolder creates a new folder under parentFolderID and returns the new folder's obj_id.
func (v *Executor) CreateFolder(parentFolderID int, title, desc string) (int, error) {
	createOp := map[string]any{
		"title":       title,
		"description": desc,
		"folder_id":   parentFolderID,
		"company_id":  v.WorkspaceID,
		"obj":         "folder",
		"type":        "create",
	}
	// v6.12.0 silently ignores folder_id when it points to a subfolder unless
	// project_id is also present — the folder lands at stage root instead.
	if v.StageID != 0 {
		if projectID := v.GetProjectIDByStageID(v.StageID); projectID != 0 {
			createOp["project_id"] = projectID
		}
	}
	ops := []map[string]any{createOp}
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
		if isFolderStageObjType(info.ObjType) || (v.StageID != 0 && info.ObjID == v.StageID) {
			break
		}
		safeName := sanitizeFileSegment(info.Title)
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

// EnvVar is the server-side state of one environment variable, as returned by
// the env_var list op. Value is nil-decoded to "" for secret variables — the
// server returns null there and the plaintext is never retrievable.
type EnvVar struct {
	ObjID      int
	ShortName  string
	Title      string
	DataType   string // "raw" | "json"
	EnvVarType string // "visible" | "secret"
	Value      string
	CreateTime int64
	ChangeTime int64
	UUID       string
}

// ListEnvVars returns full details for every env var in the stage.
func (v *Executor) ListEnvVars(stage int) ([]EnvVar, error) {
	projectID, perr := v.envVarProjectID(stage)
	if perr != nil {
		return nil, perr
	}
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
	op, err := firstOp(response)
	if err != nil {
		return nil, fmt.Errorf("env var list failed: %v", err)
	}
	list, _ := op["list"].([]any)
	out := make([]EnvVar, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ev := EnvVar{}
		ev.ShortName, _ = m["short_name"].(string)
		ev.Title, _ = m["title"].(string)
		ev.DataType, _ = m["data_type"].(string)
		ev.EnvVarType, _ = m["env_var_type"].(string)
		ev.Value, _ = m["value"].(string) // null for secrets -> ""
		ev.UUID, _ = m["uuid"].(string)
		if id, ok := m["obj_id"].(float64); ok {
			ev.ObjID = int(id)
		}
		if t, ok := m["create_time"].(float64); ok {
			ev.CreateTime = int64(t)
		}
		if t, ok := m["change_time"].(float64); ok {
			ev.ChangeTime = int64(t)
		}
		out = append(out, ev)
	}
	return out, nil
}

// listEnvVarsByStage returns a map of short_name -> obj_id for all env vars in the given stage.
func (v *Executor) listEnvVarsByStage(stage int) (map[string]int, error) {
	vars, err := v.ListEnvVars(stage)
	if err != nil {
		return nil, err
	}
	result := make(map[string]int, len(vars))
	for _, ev := range vars {
		if ev.ShortName != "" {
			result[ev.ShortName] = ev.ObjID
		}
	}
	return result, nil
}

// EnvVarChanges lists the fields a modify op should change. Nil pointers mean
// "leave as is" — the server's modify semantics are PARTIAL (verified live):
// omitted keys keep their current value, including a secret's value.
type EnvVarChanges struct {
	ShortName *string // rename
	Title     *string
	DataType  *string
	Value     *string
}

// ModifyEnvVar sends a partial modify op. The server requires short_name in
// every modify (verified live), so currentShortName is sent when no rename is
// requested. env_var_type is NOT settable: the server silently ignores type
// changes in modify (verified live in both directions), so this executor
// never sends it.
func (v *Executor) ModifyEnvVar(stage, objID int, currentShortName string, ch EnvVarChanges) error {
	projectID, perr := v.envVarProjectID(stage)
	if perr != nil {
		return perr
	}
	op := map[string]any{
		"type":       "modify",
		"obj":        "env_var",
		"obj_id":     objID,
		"company_id": v.WorkspaceID,
		"project_id": projectID,
		"stage_id":   stage,
		"short_name": currentShortName,
	}
	if ch.ShortName != nil {
		op["short_name"] = *ch.ShortName
	}
	if ch.Title != nil {
		op["title"] = *ch.Title
	}
	if ch.DataType != nil {
		op["data_type"] = *ch.DataType
	}
	if ch.Value != nil {
		op["value"] = *ch.Value
	}
	resp, err := v.req("modify-variable", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("failed to modify variable: %v", err)
	}
	if _, err := firstOp(resp); err != nil {
		return fmt.Errorf("failed to modify variable: %v", err)
	}
	return nil
}

// DeleteEnvVar permanently deletes an env variable. There is NO recycle bin
// for env vars (verified live) — callers must confirm with the user first.
// The server requires project_id and stage_id (verified live).
func (v *Executor) DeleteEnvVar(stage, objID int) error {
	projectID, perr := v.envVarProjectID(stage)
	if perr != nil {
		return perr
	}
	ops := []map[string]any{
		{
			"type":       "delete",
			"obj":        "env_var",
			"obj_id":     objID,
			"company_id": v.WorkspaceID,
			"project_id": projectID,
			"stage_id":   stage,
		},
	}
	resp, err := v.req("delete-variable", ops)
	if err != nil {
		return fmt.Errorf("failed to delete variable: %v", err)
	}
	if _, err := firstOp(resp); err != nil {
		return fmt.Errorf("failed to delete variable: %v", err)
	}
	return nil
}

// removeVariableFromFile drops a variable from the .processes/variables.json
// cache. A missing file or missing entry is not an error — the cache is a
// convenience mirror, never the source of truth.
func (v *Executor) removeVariableFromFile(name string) error {
	variablesPath := ".processes/variables.json"
	data, err := os.ReadFile(variablesPath)
	if err != nil {
		return nil // no cache — nothing to do
	}
	var variables []map[string]string
	if err := json.Unmarshal(data, &variables); err != nil {
		return fmt.Errorf("failed to parse existing variables.json: %v", err)
	}
	kept := variables[:0]
	for _, vr := range variables {
		if vr["name"] != name {
			kept = append(kept, vr)
		}
	}
	if len(kept) == len(variables) {
		return nil
	}
	out, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(variablesPath, out, 0644)
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

// ResolveStageIDByFolder walks up from folderID until it hits a stage
// (FolderInfo obj_type 10, or legacy 3). folderID itself may already be the
// stage. Returns
// 0 if the walk cannot find one within maxDepth or an API call fails — the
// caller is expected to fall back to the current folder's stage_id or an
// explicit argument. Used by stage-scoped tools (create-alias, env-var ops)
// so they can target the stage the process actually lives in instead of
// blindly trusting the configured stage_id, which caused "Object is not in
// stage" when the two diverged.
func (v *Executor) ResolveStageIDByFolder(folderID int) (int, error) {
	const maxDepth = 20
	if folderID == 0 {
		return 0, fmt.Errorf("folder id is 0")
	}
	currentID := folderID
	for i := 0; i < maxDepth; i++ {
		info, err := v.ShowFolder(currentID)
		if err != nil {
			return 0, fmt.Errorf("show folder %d: %w", currentID, err)
		}
		if isFolderStageObjType(info.ObjType) {
			return info.ObjID, nil
		}
		if info.ParentObjID == 0 || info.ParentObjID == info.ObjID {
			return 0, fmt.Errorf("folder %d has no stage ancestor within %d levels", folderID, i+1)
		}
		currentID = info.ParentObjID
	}
	return 0, fmt.Errorf("folder walk exceeded %d levels starting from %d", maxDepth, folderID)
}

// envVarProjectID resolves the project for a stage PRESERVING the underlying
// failure — the bare "could not resolve project for stage N" hid the real
// cause (typically an invalid session) and sent users hunting stage IDs when
// the fix was a re-login (field incident).
func (v *Executor) envVarProjectID(stage int) (int, error) {
	const maxDepth = 20
	currentID := stage
	for i := 0; i < maxDepth; i++ {
		info, err := v.ShowFolder(currentID)
		if err != nil {
			hint := ""
			msg := strings.ToLower(err.Error())
			for _, marker := range []string{"cookie or headers are not valid", "unauthorized", "access denied", "token is not valid", "invalid token"} {
				if strings.Contains(msg, marker) {
					hint = " — the session token was rejected (stale or revoked); re-run login (force=true if needed)"
					break
				}
			}
			return 0, fmt.Errorf("could not resolve project for stage %d: showing folder %d: %w%s", stage, currentID, err, hint)
		}
		if info.ObjID == stage {
			return info.ParentObjID, nil
		}
		currentID = info.ParentObjID
	}
	return 0, fmt.Errorf("could not resolve project for stage %d: folder walk exceeded %d levels", stage, maxDepth)
}
