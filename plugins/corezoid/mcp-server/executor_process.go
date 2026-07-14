package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// PullZip exports a process or folder as a ZIP archive and returns the raw bytes.
func (v *Executor) PullZip(id int, objType string) ([]byte, error) {
	ops := []map[string]any{
		{
			"type":       "create",
			"obj":        "obj_scheme",
			"obj_id":     id,
			"company_id": v.WorkspaceID,
			"obj_type":   objType,
			"with_alias": true,
			"async":      false,
			"format":     "zip",
		},
	}
	response, err := v.req("export_process", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to export process: %w", err)
	}
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("failed to export process: %v, %v, %v", response, v.WorkspaceID, id)
	}
	ops1, ok := response["ops"].([]any)
	if !ok || len(ops1) == 0 {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	firstOp, ok := ops1[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed to export process: unexpected ops format")
	}
	downloadURL1, ok := firstOp["download_url"].(string)
	if !ok || downloadURL1 == "" {
		return nil, fmt.Errorf("failed to export process: no download_url in response")
	}

	req, err := http.NewRequest("GET", downloadURL1, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if v.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Simulator %s", v.Token))
	}
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download process: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read process: %v", err)
	}
	return body, nil
}

// PullFolder exports all processes in a folder as a JSON array.
func (v *Executor) PullFolder(id int, objType string) ([]any, error) {
	ops := []map[string]any{
		{
			"type":       "create",
			"obj":        "obj_scheme",
			"obj_id":     id,
			"company_id": v.WorkspaceID,
			"obj_type":   objType,
			"with_alias": true,
			"async":      false,
			"format":     "json",
		},
	}
	response, err := v.req("export_process", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to export process: %w", err)
	}
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("failed to export process: %v", response)
	}
	ops1, ok := response["ops"].([]any)
	if !ok || len(ops1) == 0 {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	firstOp, ok := ops1[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed to export process: unexpected ops format")
	}
	downloadURL1, ok := firstOp["download_url"].(string)
	if !ok || downloadURL1 == "" {
		return nil, fmt.Errorf("failed to export process: no download_url in response")
	}

	req, err := http.NewRequest("GET", downloadURL1, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if v.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Simulator %s", v.Token))
	}
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download process: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read process: %v", err)
	}
	var processes []any
	err = json.Unmarshal(body, &processes)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal process: %v", err)
	}
	return processes, nil
}

// ExportProcess downloads the current process definition as parsed JSON.
func (v *Executor) ExportProcess() (any, error) {
	ops := []map[string]any{
		{
			"type":       "create",
			"obj":        "obj_scheme",
			"obj_id":     v.ProcessID,
			"company_id": v.WorkspaceID,
			"obj_type":   "conv",
			"with_alias": true,
			"async":      false,
			"format":     "json",
		},
	}
	response, err := v.req("export_process", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to export process: %w", err)
	}
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("failed to export process: %v", response)
	}
	ops1, ok := response["ops"].([]any)
	if !ok || len(ops1) == 0 {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	firstOp, ok := ops1[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed to export process: unexpected ops format")
	}
	downloadURL1, ok := firstOp["download_url"].(string)
	if !ok || downloadURL1 == "" {
		return nil, fmt.Errorf("failed to export process: no download_url in response")
	}

	req, err := http.NewRequest("GET", downloadURL1, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if v.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Simulator %s", v.Token))
	}
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download process: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read process: %v", err)
	}
	var process []any
	err = json.Unmarshal(body, &process)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal process: %v", err)
	}

	// Fallback for undeployed processes: export_process returns empty scheme.nodes
	// for processes that were imported but never deployed (broken reference).
	// Retry via the list API which returns nodes for draft/undeployed processes.
	if len(process) > 0 {
		if proc, ok := process[0].(map[string]any); ok {
			scheme, hasScheme := proc["scheme"].(map[string]any)
			nodes, _ := scheme["nodes"].([]any)
			if hasScheme && len(nodes) == 0 {
				fallbackNodes, err := v.GetProcessNodes()
				if err == nil && len(fallbackNodes) > 0 {
					scheme["nodes"] = fallbackNodes
					proc["scheme"] = scheme
					process[0] = proc
					logger.Info("pull-process: used fallback nodes for undeployed process %d (%d nodes)", v.ProcessID, len(fallbackNodes))
				}
			}
		}
	}
	return process, nil
}

// GetProcessNodes fetches process nodes via the list API, used as a fallback
// for undeployed processes where export_process returns empty scheme.nodes.
func (v *Executor) GetProcessNodes() ([]interface{}, error) {
	ops := []map[string]any{
		{
			"type":          "list",
			"obj":           "conv",
			"obj_id":        v.ProcessID,
			"company_id":    v.WorkspaceID,
			"include_nodes": true,
		},
	}
	response, err := v.req("get_process", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to get process nodes: %w", err)
	}
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("failed to get process nodes: %v", response)
	}
	ops1, ok := response["ops"].([]any)
	if !ok || len(ops1) == 0 {
		return nil, fmt.Errorf("no ops in get_process response")
	}
	firstOp, ok := ops1[0].(map[string]any)
	if !ok {
		return nil, nil
	}
	list, ok := firstOp["list"].([]any)
	if !ok || len(list) == 0 {
		return nil, nil
	}
	proc, ok := list[0].(map[string]any)
	if !ok {
		return nil, nil
	}
	scheme, ok := proc["scheme"].(map[string]any)
	if !ok {
		return nil, nil
	}
	nodes, ok := scheme["nodes"].([]any)
	if !ok {
		return nil, nil
	}
	return nodes, nil
}

// ProcessJSON is the main orchestrator: parses JSON, creates/updates nodes, compiles code, commits.
// Cancellation is checked at the top and between major API-heavy steps so a
// client-side cancel doesn't have to wait for the entire deploy to finish.
func (validator *Executor) ProcessJSON(filePath, jsonContent string) (newProcessData map[string]interface{}, err error) {
	// committed flips to true once Commit succeeds: from that point a failure
	// (e.g. updating the local file) must NOT drop the just-committed version.
	committed := false
	defer func() {
		if err != nil && !committed {
			if validator != nil {
				validator.DeleteVersion(validator.Version)
			}
		}
	}()

	if err = validator.checkCancel(); err != nil {
		return nil, err
	}

	var processDataOfAI map[string]interface{}
	err = json.Unmarshal([]byte(jsonContent), &processDataOfAI)
	if err != nil {
		err = fmt.Errorf("error parsing JSON: %v", err)
		return nil, err
	}

	if validator.Debug {
		logger.Debug("JSON parsed, processDataLength=%d", len(processDataOfAI))
	}
	newProcessData = processDataOfAI
	nodes, err := getNodes(processDataOfAI)
	if err != nil {
		err = fmt.Errorf("error getting nodes: %v", err)
		return nil, err
	}
	if validator.ProcessID == 0 {
		validator.NewProc = true
		title, _ := processDataOfAI["title"].(string)
		validator.ProcessID = validator.CreateEmptyProcess(0, title, "")
		if validator.ProcessID == 0 {
			err = fmt.Errorf("failed to create process")
			return nil, err
		}
	} else {
		oldProcessData, err := validator.GetProcessByID(validator.ProcessID)
		if err != nil {
			logger.Error("Failed to get process: %v", err)
			return nil, fmt.Errorf("error getting process: %v", err)
		}
		if commits, ok := oldProcessData["commits"].(map[string]interface{}); ok && commits != nil {
			if oldVer, ok := commits["version"].(float64); ok && oldVer > 0 {
				validator.DeleteVersion(int(oldVer))
			}
		}

		oldNodes, ok := oldProcessData["list"].([]interface{})
		if !ok {
			oldProcessDataBin, _ := json.Marshal(oldProcessData)
			logger.Error("COREZOID_PROC_ID: %d, server error rsp: %s", validator.ProcessID, oldProcessDataBin)
			err = fmt.Errorf("error getting nodes")
			return nil, err
		}
		nodes = validator.DeleteNotUsedNodes(oldNodes, nodes)
		for _, oldNode := range oldNodes {
			oldNodeMap, ok := oldNode.(map[string]interface{})
			if !ok {
				continue
			}
			objTypeF, _ := oldNodeMap["obj_type"].(float64)
			if int(objTypeF) != 1 {
				continue
			}
			oldNodeID, ok := oldNodeMap["obj_id"].(string)
			if !ok || oldNodeID == "" {
				continue
			}
			newNodeID := ""
			for i, newNode := range nodes {
				newNodeMap, ok := newNode.(map[string]interface{})
				if !ok {
					continue
				}
				newObjTypeF, _ := newNodeMap["obj_type"].(float64)
				if int(newObjTypeF) != 1 {
					continue
				}
				id, ok := newNodeMap["id"].(string)
				if !ok {
					continue
				}
				newNodeID = id
				newNodeMap["existed"] = true
				newNodeMap["id"] = oldNodeID
				validator.NodeIDMap[newNodeID] = NodeInfo{
					Type:     1,
					Name:     "Start",
					Icon:     "",
					ServerID: oldNodeID,
				}
				nodes[i] = newNodeMap
				break
			}
			if newNodeID == "" {
				return nil, fmt.Errorf("no start node found in process %d", validator.ProcessID)
			}
			break
		}
	}

	if len(newProcessData) == 0 {
		err = fmt.Errorf("no process data found in JSON")
		return nil, err
	}

	err = validator.CreateNodesFromJSON(nodes)
	if err != nil {
		err = fmt.Errorf("error creating nodes: %v", err)
		return nil, err
	}
	if err = validator.checkCancel(); err != nil {
		return nil, err
	}
	changed := false
	for inID, extInfo := range validator.NodeIDMap {
		if inID == extInfo.ServerID {
			continue
		}
		changed = true
		jsonContent = strings.Replace(jsonContent, "\""+inID+"\"", "\""+extInfo.ServerID+"\"", -1)
	}
	if changed {
		// Re-parse the id-remapped scheme in memory now, but hold off writing
		// it to disk until the commit succeeds. Writing here left the local
		// file pointing at server node IDs of a draft that the deferred
		// DeleteVersion removes whenever any later step (modify, compile,
		// commit) fails — a silent desync between the file and the server.
		err = json.Unmarshal([]byte(jsonContent), &newProcessData)
		if err != nil {
			logger.Error("Failed to parse1 JSON: %v", err)
			return nil, fmt.Errorf("error parsing1 JSON: %v", err)
		}
	}

	nodes, err = getNodes(newProcessData)
	if err != nil {
		err = fmt.Errorf("error getting nodes: %v", err)
		return nil, err
	}
	params, _ := newProcessData["params"].([]interface{})
	err = validator.SetParams(params)
	if err != nil {
		err = fmt.Errorf("error setting params: %v", err)
		return nil, err
	}

	err = validator.ModifyNodes(nodes)
	if err != nil {
		return nil, err
	}

	// CompileAPICode already prefixes its errors with "failed to compile API
	// code:" — wrapping again produced a doubled prefix in the tool output.
	err = validator.CompileAPICode(nodes)
	if err != nil {
		return nil, err
	}

	// git_call nodes run on a container build service that must finish before
	// Commit, or Commit rejects them with "source has to be built". Interpreted
	// runtimes (js) need no build and are skipped inside BuildGitCallNodes.
	if err = validator.BuildGitCallNodes(nodes); err != nil {
		return nil, fmt.Errorf("failed to build git_call node(s): %v", err)
	}

	commitResponse, err := validator.Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to commit changes: %v", err)
	}
	if commitResponse == nil {
		return nil, fmt.Errorf("failed to commit changes: no response from server")
	}
	if requestProc, ok := commitResponse["request_proc"].(string); !ok || requestProc != "ok" {
		err = fmt.Errorf("failed to commit changes: request_proc not ok")
		return nil, err
	}

	if opsArray, ok := commitResponse["ops"].([]interface{}); ok {
		var errorMsgs []string
		for i, op := range opsArray {
			if opMap, ok := op.(map[string]interface{}); ok {
				if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
					nodeInfo := fmt.Sprintf("Operation %d", i+1)
					if objID, ok := opMap["obj_id"].(string); ok {
						nodeInfo = fmt.Sprintf("Node with obj_id %s", objID)
					}
					if description, ok := opMap["description"].(string); ok {
						errorMsgs = append(errorMsgs, fmt.Sprintf("%s: %s", nodeInfo, description))
						continue
					}
					if errors, ok := opMap["errors"].(map[string]interface{}); ok {
						for objID, errMsgs := range errors {
							nodeID := objID
							if errArray, ok := errMsgs.([]interface{}); ok {
								for _, errMsg := range errArray {
									if msg, ok := errMsg.(string); ok {
										errorMsgs = append(errorMsgs, fmt.Sprintf("Error in the node %s: %s", nodeID, msg))
									}
								}
							}
						}
						continue
					}
					errorMsg := "unknown error"
					if msg, ok := opMap["message"].(string); ok {
						errorMsg = msg
					}
					errorMsgs = append(errorMsgs, fmt.Sprintf("%s: %s", nodeInfo, errorMsg))
				}
			}
		}
		if len(errorMsgs) > 0 {
			err = fmt.Errorf("failed to commit changes: %s", strings.Join(errorMsgs, "\n"))
			return nil, err
		}
	}

	committed = true

	// The deploy is committed — now it is safe to sync the local file to the
	// server's canonical node IDs.
	if changed {
		if werr := os.WriteFile(filePath, []byte(jsonContent), 0644); werr != nil {
			err = fmt.Errorf("process deployed, but failed to update the local file with server node IDs: %v", werr)
			return nil, err
		}
	}

	return newProcessData, nil
}

func (v *Executor) GetProcessByID(id int) (rsp map[string]any, err error) {
	ops := []map[string]any{
		{
			"obj_id":     id,
			"type":       "list",
			"obj":        "conv",
			"company_id": v.WorkspaceID,
		},
	}
	if v.Debug {
		logger.Debug("Sending get process request")
	}
	response, err := v.req("get_process", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to get process: %w", err)
	}
	if response != nil && response["ops"] != nil {
		if opsArray, ok := response["ops"].([]interface{}); ok && len(opsArray) > 0 {
			if firstOp, ok := opsArray[0].(map[string]interface{}); ok {
				return firstOp, nil
			}
		}
	}
	return nil, fmt.Errorf("failed to get process: no response from server")
}

func (v *Executor) CreateEmptyProcess(folderID int, title, desc string) int {
	return v.CreateEmptyConv(folderID, title, desc, "process")
}

// CreateEmptyConv creates an empty Corezoid object of the given conv_type
// ("process" for a regular process, "state" for a state diagram).
// CreateEmptyProcess is preserved as a backward-compatible wrapper that
// defaults to conv_type "process".
func (v *Executor) CreateEmptyConv(folderID int, title, desc, convType string) int {
	if title == "" {
		title = time.Now().String()
	}
	if convType == "" {
		convType = "process"
	}
	ops := []map[string]any{
		{
			"title":       title,
			"description": desc,
			"folder_id":   folderID,
			"company_id":  v.WorkspaceID,
			"obj":         "conv",
			"create_mode": "without_nodes",
			"conv_type":   convType,
			"type":        "create",
			"obj_type":    0,
			"status":      "active",
		},
	}
	if v.Debug {
		logger.Debug("Sending create empty process request")
	}
	response, err := v.req("create_process", ops)
	if err != nil {
		logger.Error("Failed to create empty process: %v", err)
		return 0
	}
	if response != nil && response["ops"] != nil {
		if opsArray, ok := response["ops"].([]interface{}); ok && len(opsArray) > 0 {
			if firstOp, ok := opsArray[0].(map[string]interface{}); ok {
				if objID, ok := firstOp["obj_id"].(float64); ok {
					v.ProcessID = int(objID)
					logger.Debug("Empty process created: %d", v.ProcessID)
					return v.ProcessID
				}
			}
		}
	}
	logger.Error("Failed to create empty process")
	return 0
}

func (v *Executor) SetParams(params []interface{}) error {
	ops := []map[string]any{
		{
			"obj_id":     v.ProcessID,
			"ref_mask":   true,
			"type":       "modify",
			"obj":        "conv_params",
			"params":     params,
			"company_id": v.WorkspaceID,
		},
	}
	if v.Debug {
		logger.Debug("Sending set params request")
	}
	response, err := v.req("set_params", ops)
	if err != nil {
		return fmt.Errorf("failed to set params: %w", err)
	}
	if response == nil {
		return fmt.Errorf("failed to set params: no response from server")
	}
	if requestProc, ok := response["request_proc"].(string); !ok || requestProc != "ok" {
		return fmt.Errorf("failed to set params: request_proc not ok")
	}
	return nil
}

// Commit confirms and finalizes changes to a process. The server rejects a
// commit with a precise, actionable reason (schema-shape violations, timer
// minimums, invalid set_param pairs, …) delivered through the op error that
// v.req converts into a Go error — so that error is returned to the caller
// verbatim instead of being demoted to a log line. Earlier versions logged it
// and returned nil, which the caller could only report as the opaque
// "no response from server", turning every server-side rejection into a
// dead-end mystery.
func (v *Executor) Commit() (map[string]interface{}, error) {
	ops := []map[string]any{
		{
			"type":    "confirm",
			"obj":     "commit",
			"conv_id": v.ProcessID,
			"version": v.Version,
		},
	}
	if v.Debug {
		logger.Debug("Sending commit request")
	}
	response, err := v.req("commit_process", ops)
	if err != nil {
		logger.Error("Failed to commit changes: %v", err)
		return nil, err
	}
	if response == nil {
		// A (nil, nil) from req would otherwise fall through to the success
		// debug line below — a false "Changes committed" in the log.
		logger.Error("Failed to commit changes: no response from server")
		return nil, fmt.Errorf("no response from server")
	}
	if v.Debug {
		if requestProc, ok := response["request_proc"].(string); ok {
			logger.Debug("Commit response received, request_proc=%s", requestProc)
		}
	}
	logger.Debug("Changes committed, processID=%d", v.ProcessID)
	return response, nil
}

func (v *Executor) DeleteVersion(ver int) {
	ops := []map[string]any{
		{
			"type":       "delete",
			"obj":        "commits",
			"company_id": v.WorkspaceID,
			"conv_id":    v.ProcessID,
			"version":    ver,
		},
	}
	response, err := v.req("delete_version", ops)
	if err != nil {
		logger.Error("Failed to delete version: %v", err)
		return
	}
	if response == nil {
		logger.Error("Failed to delete version: no response from server")
	} else if v.Debug {
		if requestProc, ok := response["request_proc"].(string); ok {
			logger.Debug("Delete version response received, request_proc=%s", requestProc)
		}
	}
	logger.Debug("Delete Changes, processID=%d", v.ProcessID)
}

// Share grants all privileges on a process to a specific user.
func (v *Executor) Share(userID, convID int) map[string]interface{} {
	ops := []map[string]any{
		{
			"type":              "link",
			"obj":               "conv",
			"obj_id":            convID,
			"obj_to":            "user",
			"obj_to_id":         userID,
			"is_need_to_notify": true,
			"privs": []map[string]any{
				{"type": "create", "list_obj": []string{"all"}},
				{"type": "modify", "list_obj": []string{"all"}},
				{"type": "delete", "list_obj": []string{"all"}},
				{"type": "view", "list_obj": []string{"all"}},
			},
		},
	}
	if v.Debug {
		logger.Debug("Sending share request, privileges=all")
	}
	response, err := v.req("share_process", ops)
	if err != nil {
		logger.Error("Failed to share process: %v", err)
		return nil
	}
	if response == nil {
		logger.Error("Failed to share process: no response from server")
	} else if v.Debug {
		if requestProc, ok := response["request_proc"].(string); ok {
			logger.Debug("Share response received, request_proc=%s", requestProc)
		}
	}
	return response
}
