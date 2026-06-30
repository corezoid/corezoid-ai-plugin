package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// reProcessIDFromFilename extracts the leading numeric process ID from a
// filename like "12345_my_process.conv.json". Compiled once and shared by the
// handlers that resolve a process ID from a file path.
var reProcessIDFromFilename = regexp.MustCompile(`^(\d+)_`)

// extractProcessIDFromPath returns the numeric process ID encoded in the
// filename, or an error message describing the expected format.
func extractProcessIDFromPath(filePath string) (int, string) {
	baseName := filepath.Base(filePath)
	matches := reProcessIDFromFilename.FindStringSubmatch(baseName)
	if matches == nil {
		return 0, fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.json'", baseName)
	}
	id, _ := strconv.Atoi(matches[1])
	return id, ""
}

// handlePullProcess downloads a process by ID and writes its JSON to disk in
// the folder that mirrors its parent_id chain, so re-pulling places the file
// back where it lived.
func handlePullProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	v := NewValidator(ctx, processID)
	procInfo1, err := v.ExportProcess()
	if err != nil {
		return fmt.Sprintf("Error fetching process: %v", err), true
	}
	var procInfo interface{}
	if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
		procInfo = arr[0]
	} else {
		procInfo = procInfo1
	}
	data, err := json.MarshalIndent(procInfo, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling process: %v", err), true
	}

	// Derive filename from process title if available
	fileName := fmt.Sprintf("%d.conv.json", processID)
	if m, ok := procInfo.(map[string]interface{}); ok {
		if title, _ := m["title"].(string); title != "" {
			safeName := strings.ReplaceAll(title, " ", "_")
			fileName = fmt.Sprintf("%d_%s.conv.json", processID, safeName)
		}
	}

	// Resolve save directory from parent_id so the file lands in the correct folder tree.
	var dir string
	if m, ok := procInfo.(map[string]interface{}); ok {
		parentID := 0
		if pid, ok := m["parent_id"].(float64); ok {
			parentID = int(pid)
		}
		if parentID != 0 && v.StageID != 0 {
			resolved, resolveErr := v.resolveFolderPathFromAPI(parentID)
			if resolveErr != nil {
				logger.Warn("pull-process: could not resolve folder path for parent_id %d: %v", parentID, resolveErr)
			} else {
				dir = resolved
			}
		}
	}

	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Sprintf("Error creating directory: %v", err), true
		}
	}

	filePath := filepath.Join(dir, fileName)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Sprintf("Error writing file: %v", err), true
	}
	return fmt.Sprintf("Process %d saved to %s", processID, filePath), false
}

// handlePullFolder recursively downloads a folder (stage) and all its
// processes/subfolders into the current working directory.
func handlePullFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	if err := downloadStageRecursively(v, folderID, "."); err != nil {
		return fmt.Sprintf("Error fetching folder: %v", err), true
	}
	return fmt.Sprintf("Folder %d saved to current directory", folderID), false
}

// handleCreateVariable creates a Corezoid env variable scoped to the given stage.
func handleCreateVariable(ctx context.Context, args map[string]interface{}) (string, bool) {
	rootFolderID, err := strArg(args, "stage_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	name, err := strArg(args, "name")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	description, err := strArg(args, "description")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	value, err := strArg(args, "value")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	if err := v.CreateVariable(rootFolderID, name, description, value); err != nil {
		return fmt.Sprintf("Error creating variable: %v", err), true
	}
	return fmt.Sprintf("Environment variable '%s' created successfully", name), false
}

// handlePushProcess validates a local .conv.json and deploys it to Corezoid.
func handlePushProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	v := NewValidator(ctx, procID)

	jsonContent, err := LoadBinFromFile(filePath)
	if err != nil {
		return fmt.Sprintf("Error loading JSON file: %v", err), true
	}

	jsonContent1, messages := fixStruct(jsonContent, procID)
	if len(messages) > 0 {
		for _, msg := range messages {
			fmt.Fprintln(os.Stderr, msg)
		}
	}
	if jsonContent1 != jsonContent {
		jsonContent = jsonContent1
		if err := os.WriteFile(filePath, []byte(jsonContent), 0644); err != nil {
			return fmt.Sprintf("Error writing fixed JSON: %v", err), true
		}
	}

	if err := ValidateJSONSchema(filePath, debug); err != nil {
		return fmt.Sprintf("JSON schema validation failed: %v", err), true
	}

	if err := v.BeforeValidation(jsonContent, nil); err != nil {
		return fmt.Sprintf("Validation failed: %v", err), true
	}

	if _, err := v.ProcessJSON(filePath, jsonContent); err != nil {
		return fmt.Sprintf("Error deploying process: %v", err), true
	}

	return fmt.Sprintf("Process deployed successfully, ProcessID: %d", procID), false
}

// handleLintProcess validates a local .conv.json without touching the server.
func handleLintProcess(_ context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	result, err := lintProcess(filePath)
	if err != nil {
		return fmt.Sprintf("Error: lint failed: %v", err), true
	}
	return FormatLintResult(result), false
}

// handleRunTask deploys the local process, fires a task at it, and reports
// which node the task settled on. Used to smoke-test a process end-to-end.
func handleRunTask(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	dataStr, err := strArg(args, "data")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	v := NewValidator(ctx, procID)

	jsonContent, err := LoadBinFromFile(filePath)
	if err != nil {
		return fmt.Sprintf("Error reading process file: %v", err), true
	}

	if _, err := v.ProcessJSON(filePath, jsonContent); err != nil {
		return fmt.Sprintf("Error deploying process: %v", err), true
	}

	taskData := make(map[string]interface{})
	if err := json.Unmarshal([]byte(dataStr), &taskData); err != nil {
		return fmt.Sprintf("Error parsing task data: %v", err), true
	}

	ref := fmt.Sprintf("%d_%d", time.Now().Unix(), rand.Intn(1000000))
	if err := v.createTask(ref, taskData); err != nil {
		return fmt.Sprintf("Error creating task: %v", err), true
	}

	time.Sleep(time.Second * 5)

	rspTask, err := v.showTask(ref)
	if err != nil {
		return fmt.Sprintf("Error getting task result: %v", err), true
	}
	logger.Info("Task response: %+v", rspTask)
	rspTaskData, _ := rspTask["data"].(map[string]interface{})
	rspTaskDataBin, _ := json.Marshal(rspTaskData)
	serverNodeID, _ := rspTask["node_id"].(string)

	if v.Debug {
		for k, ni := range v.NodeIDMap {
			logger.Debug("NodeIDMap entry: key=%s type=%d serverID=%s name=%s", k, ni.Type, ni.ServerID, ni.Name)
		}
	}

	nodeInfo, found := v.NodeIDMap[serverNodeID]
	if !found {
		for _, ni := range v.NodeIDMap {
			if serverNodeID == ni.ServerID {
				nodeInfo = ni
				found = true
				break
			}
		}
	}
	logger.Info("Node info (found=%v): %+v", found, nodeInfo)
	nodeType := "logic (not final)"
	msg := "Task failed: it stopped at the non-final node"
	isErr := true
	if nodeInfo.Type == 1 {
		nodeType = "start"
	} else if nodeInfo.Type == 2 {
		isErr = false
		nodeType = "end (Success)"
		msg = "Task completed"
		if nodeInfo.Icon == "error" {
			isErr = true
			nodeType = "error node"
			msg = "Task failed: stopped at error node"
		}
	}

	summary := fmt.Sprintf("%s\nNodeID: %s\nNodeName: %s\nNodeType: %s\nProcessID: %d\nData: %s",
		msg, serverNodeID, nodeInfo.Name, nodeType, v.ProcessID, string(rspTaskDataBin))
	return summary, isErr
}

// handleCreateProcess creates an empty process in the given local folder and
// writes its skeleton JSON to disk for the user to flesh out.
func handleCreateProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	return createConv(ctx, args, "process")
}

// handleCreateStateDiagram creates an empty state diagram (conv_type "state")
// in the given local folder and writes its skeleton JSON to disk.
func handleCreateStateDiagram(ctx context.Context, args map[string]interface{}) (string, bool) {
	return createConv(ctx, args, "state")
}

// createConv is the shared implementation for create-process and
// create-state-diagram. It accepts a conv_type ("process" or "state") and
// produces a .conv.json skeleton on disk inside the requested folder.
func createConv(ctx context.Context, args map[string]interface{}, convType string) (string, bool) {
	folderPath := resolveDirPath(args, "folder_path")
	processName, err := strArg(args, "process_name")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	folderID, err := resolveFolderIDFromDir(folderPath)
	if err != nil {
		return fmt.Sprintf("Error resolving folder ID: %v", err), true
	}

	v := NewValidator(ctx, 0)
	processID := v.CreateEmptyConv(folderID, processName, "", convType)
	if processID == 0 {
		return fmt.Sprintf("Error: failed to create %s '%s'", convType, processName), true
	}

	procInfo1, err := v.ExportProcess()
	if err != nil {
		return fmt.Sprintf("Error exporting process: %v", err), true
	}
	var procInfo interface{}
	if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
		procInfo = arr[0]
	} else {
		procInfo = procInfo1
	}
	data, err := json.MarshalIndent(procInfo, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling process: %v", err), true
	}

	safeName := strings.ReplaceAll(processName, " ", "_")
	fileName := fmt.Sprintf("%d_%s.json", processID, safeName)
	filePath := filepath.Join(folderPath, fileName)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Sprintf("Error writing file: %v", err), true
	}

	label := "Process"
	if convType == "state" {
		label = "State diagram"
	}
	return fmt.Sprintf("%s '%s' created and saved to %s", label, processName, filePath), false
}

// handleCreateFolder creates a new folder under the given parent, mirrors it
// on disk, and writes a placeholder *.folder.json so the directory is
// recognizable as a Corezoid folder.
func handleCreateFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	parentPath := resolveDirPath(args, "parent_path")
	folderName, err := strArg(args, "folder_name")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	parentFolderID, err := resolveFolderIDFromDir(parentPath)
	if err != nil {
		return fmt.Sprintf("Error resolving parent folder ID: %v", err), true
	}

	v := NewValidator(ctx, 0)
	newFolderID, err := v.CreateFolder(parentFolderID, folderName, "")
	if err != nil {
		return fmt.Sprintf("Error creating folder '%s': %v", folderName, err), true
	}

	safeName := strings.ReplaceAll(folderName, " ", "_")
	dirName := fmt.Sprintf("%d_%s", newFolderID, safeName)
	dirPath := filepath.Join(parentPath, dirName)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Sprintf("Error creating directory '%s': %v", dirPath, err), true
	}

	type folderFileContent struct {
		Description string `json:"description"`
		ObjID       int    `json:"obj_id"`
		ObjType     int    `json:"obj_type"`
		ParentID    int    `json:"parent_id"`
		Title       string `json:"title"`
	}
	fileContent := folderFileContent{
		Description: "",
		ObjID:       newFolderID,
		ObjType:     0,
		ParentID:    parentFolderID,
		Title:       folderName,
	}
	fileData, err := json.MarshalIndent(fileContent, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling folder file: %v", err), true
	}
	fileName := fmt.Sprintf("%d_%s.folder.json", newFolderID, safeName)
	filePath := filepath.Join(dirPath, fileName)
	if err := os.WriteFile(filePath, fileData, 0644); err != nil {
		return fmt.Sprintf("Error writing folder file: %v", err), true
	}

	return fmt.Sprintf("Folder '%s' created and saved to %s", folderName, filePath), false
}

// handleShowFolder returns metadata for a single folder (title, obj_type,
// parent). Used to introspect folders without writing anything to disk.
func handleShowFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	info, err := v.ShowFolder(folderID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	kind := "folder"
	switch info.ObjType {
	case 1:
		kind = "root"
	case 2:
		kind = "project"
	case 3:
		kind = "stage"
	}
	return fmt.Sprintf("Folder #%d %q (kind=%s, parent=%s#%d)",
		info.ObjID, info.Title, kind, info.ParentObjType, info.ParentObjID), false
}

// handleListFolders prints the immediate children of a folder in a tabular
// form. Subfolders come first, then convs (processes + state diagrams).
func handleListFolders(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	children, err := v.ListFolder(folderID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	if len(children) == 0 {
		return fmt.Sprintf("Folder #%d is empty.", folderID), false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Folder #%d children (%d total):\n\n", folderID, len(children)))
	sb.WriteString(fmt.Sprintf("  %-10s  %-12s  %s\n", "ID", "Kind", "Title"))
	sb.WriteString("  " + strings.Repeat("-", 50) + "\n")
	for _, c := range children {
		kind := c.Obj
		if c.Obj == "conv" && c.ConvType != "" {
			kind = c.ConvType
		}
		sb.WriteString(fmt.Sprintf("  %-10d  %-12s  %s\n", c.ObjID, kind, c.Title))
	}
	return sb.String(), false
}

// handleModifyFolder renames a folder and/or updates its description. At
// least one of title / description must be provided — the API silently
// accepts an empty modify so we guard client-side.
func handleModifyFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title := optStrArg(args, "title")
	description := optStrArg(args, "description")
	if title == "" && description == "" {
		return "Error: at least one of title or description must be provided", true
	}

	v := NewValidator(ctx, 0)
	if err := v.ModifyFolder(folderID, title, description); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	parts := []string{}
	if title != "" {
		parts = append(parts, fmt.Sprintf("title=%q", title))
	}
	if description != "" {
		parts = append(parts, fmt.Sprintf("description=%q", description))
	}
	return fmt.Sprintf("Folder #%d updated (%s)", folderID, strings.Join(parts, ", ")), false
}

// handleDeleteFolder moves a folder to the recycle bin. The Corezoid UI's
// Trash view restores it; permanent destruction is intentionally not exposed.
func handleDeleteFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	if err := v.DeleteFolder(folderID); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf("Folder #%d moved to Trash.", folderID), false
}

// handleCreateAlias creates a Corezoid alias (short_name → conv) pointing at
// the process whose ID is encoded in the file path. Requires a configured
// COREZOID_STAGE_ID since aliases are stage-scoped.
func handleCreateAlias(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	shortName, err := strArg(args, "short_name")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	v := NewValidator(ctx, 0)
	if v.StageID == 0 {
		return "Error: COREZOID_STAGE_ID environment variable is not set or invalid", true
	}
	aliasID, err := v.CreateAlias(shortName, procID, v.StageID)
	if err != nil {
		return fmt.Sprintf("Error creating alias: %v", err), true
	}

	return fmt.Sprintf("Alias '%s' created successfully, AliasID: %d", shortName, aliasID), false
}
