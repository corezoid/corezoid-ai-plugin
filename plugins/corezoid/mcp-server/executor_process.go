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
			"company_id": workspaceID,
			"obj_type":   objType,
			"with_alias": true,
			"async":      false,
			"format":     "zip",
		},
	}
	response, _ := v.req("export_process", ops)
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("failed to export process: %v, %v, %v", response, workspaceID, id)
	}
	if response["ops"] == nil {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	ops1 := response["ops"].([]any)
	if len(ops1) == 0 {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	downloadURL := ops1[0].(map[string]any)["download_url"]
	if downloadURL == nil {
		fmt.Println("rsp", ops1)
		fmt.Println("req", ops)
		return nil, fmt.Errorf("failed to export process: no download_url in response")
	}
	downloadURL1 := downloadURL.(string)

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
			"company_id": workspaceID,
			"obj_type":   objType,
			"with_alias": true,
			"async":      false,
			"format":     "json",
		},
	}
	response, _ := v.req("export_process", ops)
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("failed to export process: %v", response)
	}
	if response["ops"] == nil {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	ops1 := response["ops"].([]any)
	if len(ops1) == 0 {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	downloadURL := ops1[0].(map[string]any)["download_url"]
	if downloadURL == nil {
		return nil, fmt.Errorf("failed to export process: no download_url in response")
	}
	downloadURL1 := downloadURL.(string)

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
			"company_id": workspaceID,
			"obj_type":   "conv",
			"with_alias": true,
			"async":      false,
			"format":     "json",
		},
	}
	response, _ := v.req("export_process", ops)
	if response["request_proc"] != "ok" {
		return nil, fmt.Errorf("failed to export process: %v", response)
	}
	if response["ops"] == nil {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	ops1 := response["ops"].([]any)
	if len(ops1) == 0 {
		return nil, fmt.Errorf("failed to export process: no ops in response")
	}
	downloadURL := ops1[0].(map[string]any)["download_url"]
	if downloadURL == nil {
		return nil, fmt.Errorf("failed to export process: no download_url in response")
	}
	downloadURL1 := downloadURL.(string)

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
		fmt.Println(string(body))
		return nil, fmt.Errorf("failed to unmarshal process: %v", err)
	}
	return process, nil
}

// ProcessJSON is the main orchestrator: parses JSON, creates/updates nodes, compiles code, commits.
func (validator *Executor) ProcessJSON(filePath, jsonContent string) (newProcessData map[string]interface{}, err error) {
	defer func() {
		if err != nil {
			if validator != nil {
				validator.DeleteVersion(validator.Version)
			}
		}
	}()

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
		validator.ProcessID = validator.CreateEmptyProcess(0, processDataOfAI["title"].(string), "")
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
			if oldVer, ok := commits["version"]; ok && oldVer != nil {
				oldVer1 := int(oldVer.(float64))
				if oldVer1 > 0 {
					validator.DeleteVersion(oldVer1)
				}
			}
		}

		oldNodes, ok := oldProcessData["list"].([]interface{})
		if !ok {
			oldProcessDataBin, _ := json.Marshal(oldProcessData)
			fmt.Printf("COREZOID_PROC_ID: %d, server error rsp: %s\n", validator.ProcessID, oldProcessDataBin)
			err = fmt.Errorf("error getting nodes")
			return nil, err
		}
		nodes = validator.DeleteNotUsedNodes(oldNodes, nodes)
		for _, oldNode := range oldNodes {
			if int(oldNode.(map[string]interface{})["obj_type"].(float64)) == 1 {
				oldNodeID := oldNode.(map[string]interface{})["obj_id"].(string)
				newNodeID := ""
				for i, newNode := range nodes {
					if int(newNode.(map[string]interface{})["obj_type"].(float64)) == 1 {
						newNodeID = newNode.(map[string]interface{})["id"].(string)
						newNode.(map[string]interface{})["existed"] = true
						newNode.(map[string]interface{})["id"] = oldNodeID
						validator.NodeIDMap[newNodeID] = NodeInfo{
							Type:     1,
							Name:     "Start",
							Icon:     "",
							ServerID: oldNodeID,
						}
						nodes[i] = newNode
						break
					}
				}
				if newNodeID == "" {
					return nil, fmt.Errorf("no start node found in process %d", validator.ProcessID)
				}
				break
			}
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
	changed := false
	for inID, extInfo := range validator.NodeIDMap {
		if inID == extInfo.ServerID {
			continue
		}
		changed = true
		jsonContent = strings.Replace(jsonContent, "\""+inID+"\"", "\""+extInfo.ServerID+"\"", -1)
	}
	if changed {
		err = os.WriteFile(filePath, []byte(jsonContent), 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to write file: %v", err)
		}
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

	err = validator.CompileAPICode(nodes)
	if err != nil {
		err = fmt.Errorf("failed to compile API code: %v", err)
		return nil, err
	}

	commitResponse := validator.Commit()
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

	return newProcessData, nil
}

func (v *Executor) GetProcessByID(id int) (rsp map[string]any, err error) {
	ops := []map[string]any{
		{
			"obj_id":     id,
			"type":       "list",
			"obj":        "conv",
			"company_id": workspaceID,
		},
	}
	if v.Debug {
		logger.Debug("Sending get process request")
	}
	response, _ := v.req("get_process", ops)
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
	if title == "" {
		title = time.Now().String()
	}
	ops := []map[string]any{
		{
			"title":       title,
			"description": desc,
			"folder_id":   folderID,
			"company_id":  workspaceID,
			"obj":         "conv",
			"create_mode": "without_nodes",
			"conv_type":   "process",
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
		fmt.Println(response)
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
			"company_id": workspaceID,
		},
	}
	if v.Debug {
		logger.Debug("Sending set params request")
	}
	response, _ := v.req("set_params", ops)
	if response == nil {
		return fmt.Errorf("failed to set params: no response from server")
	}
	if requestProc, ok := response["request_proc"].(string); !ok || requestProc != "ok" {
		return fmt.Errorf("failed to set params: request_proc not ok")
	}
	return nil
}

// Commit confirms and finalizes changes to a process.
func (v *Executor) Commit() map[string]interface{} {
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
	response, _ := v.req("commit_process", ops)
	if response == nil {
		logger.Error("Failed to commit changes: no response from server")
	} else if v.Debug {
		if requestProc, ok := response["request_proc"].(string); ok {
			logger.Debug("Commit response received, request_proc=%s", requestProc)
		}
	}
	logger.Debug("Changes committed, processID=%d", v.ProcessID)
	return response
}

func (v *Executor) DeleteVersion(ver int) {
	ops := []map[string]any{
		{
			"type":       "delete",
			"obj":        "commits",
			"company_id": workspaceID,
			"conv_id":    v.ProcessID,
			"version":    ver,
		},
	}
	response, _ := v.req("delete_version", ops)
	if response == nil {
		logger.Error("Failed to commit changes: no response from server")
	} else if v.Debug {
		if requestProc, ok := response["request_proc"].(string); ok {
			logger.Debug("Commit response received, request_proc=%s", requestProc)
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
	response, _ := v.req("share_process", ops)
	if response == nil {
		logger.Error("Failed to share process: no response from server")
	} else if v.Debug {
		if requestProc, ok := response["request_proc"].(string); ok {
			logger.Debug("Share response received, request_proc=%s", requestProc)
		}
	}
	return response
}
