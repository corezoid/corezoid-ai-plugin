package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CreateNodesFromJSON creates node stubs on the server for each node in the process JSON.
// After creation it populates NodeIDMap with the server-assigned obj_ids.
func (v *Executor) CreateNodesFromJSON(nodes []any) error {
	var ops []map[string]any
	for i, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			if v.Debug {
				logger.Debug("Skipping invalid node, index=%d", i)
			}
			continue
		}
		nodeID, _ := nodeMap["id"].(string)
		nodeTitle, _ := nodeMap["title"].(string)
		objTypeF, _ := nodeMap["obj_type"].(float64)
		nodeType := int(objTypeF)
		nodeIcon := ""
		extraStr, _ := nodeMap["extra"].(string)
		var extra map[string]interface{}
		if extraStr != "" {
			json.Unmarshal([]byte(extraStr), &extra)
		}
		if icon1, ok := extra["icon"].(string); ok {
			nodeIcon = icon1
		}

		if existed, _ := nodeMap["existed"].(bool); existed {
			v.NodeIDMap[nodeID] = NodeInfo{
				Type: nodeType, Name: nodeTitle, Icon: nodeIcon, ServerID: nodeID,
			}
			continue
		}

		if v.Debug {
			logger.Debug("Processing node, index=%d, id=%s, title=%s", i, nodeID, nodeTitle)
		}

		op := map[string]any{
			"id":          nodeMap["id"],
			"type":        "create",
			"obj":         "node",
			"conv_id":     v.ProcessID,
			"title":       nodeMap["title"],
			"description": nodeMap["description"],
			"obj_type":    nodeMap["obj_type"],
			"version":     v.Version,
		}
		v.NodeIDMap[nodeID] = NodeInfo{Type: nodeType, Name: nodeTitle, Icon: nodeIcon}
		ops = append(ops, op)
	}

	if len(ops) == 0 {
		return nil
	}

	if v.Debug {
		logger.Debug("Created operations for nodes, operationCount=%d", len(ops))
	}

	response, err := v.req("create_nodes", ops)
	if err != nil {
		return fmt.Errorf("failed to create nodes: %w", err)
	}
	if response == nil {
		logger.Error("Failed to create nodes: no response from server")
		return fmt.Errorf("failed to create nodes: no response from server")
	}

	nodeUUIDToOp := make(map[string]map[string]any)
	for _, op := range ops {
		if id, ok := op["id"].(string); ok {
			nodeUUIDToOp[id] = op
		}
	}

	if response != nil && response["ops"] != nil {
		if opsArray, ok := response["ops"].([]interface{}); ok {
			if v.Debug {
				logger.Debug("Processing node creation responses, responseCount=%d", len(opsArray))
			}
			for i, op := range opsArray {
				if opMap, ok := op.(map[string]interface{}); ok {
					v.NodeResponses = append(v.NodeResponses, opMap)
					if id, ok := opMap["id"].(string); ok {
						if objID, ok := opMap["obj_id"].(string); ok {
							nodeInfo := v.NodeIDMap[id]
							nodeInfo.ServerID = objID
							v.NodeIDMap[id] = nodeInfo
							if v.Debug {
								logger.Debug("Mapped node ID, index=%d, nodeID=%s, objID=%s", i, id, objID)
							}
						}
					}
				}
			}
		}
	} else {
		logger.Error("Invalid response format from server")
	}

	logger.Debug("Node creation completed, nodeCount=%d", len(v.NodeIDMap))
	return nil
}

// ModifyNodes updates the nodes with their content after they've been created.
func (v *Executor) ModifyNodes(nodes []any) error {
	var ops []map[string]any
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		objID, ok := nodeMap["id"].(string)
		if !ok {
			continue
		}

		title, _ := nodeMap["title"].(string)
		description, _ := nodeMap["description"].(string)
		objType, _ := nodeMap["obj_type"].(float64)

		x, xOk := nodeMap["x"].(float64)
		y, yOk := nodeMap["y"].(float64)
		position := []float64{}
		if xOk && yOk {
			position = []float64{x, y}
		}

		extraStr, _ := nodeMap["extra"].(string)
		var extra map[string]interface{}
		if extraStr != "" {
			json.Unmarshal([]byte(extraStr), &extra)
		}

		optionsStr, _ := nodeMap["options"].(string)
		var options map[string]interface{}
		if optionsStr != "" {
			json.Unmarshal([]byte(optionsStr), &options)
		}

		logics := []map[string]interface{}{}
		if condition, ok := nodeMap["condition"].(map[string]interface{}); ok {
			if logicsArray, ok := condition["logics"].([]interface{}); ok {
				isGo := false
				actionCount := 0
				for _, logic := range logicsArray {
					if logicMap, ok := logic.(map[string]interface{}); ok {
						logicType, _ := logicMap["type"].(string)
						if logicType == "go" {
							isGo = true
						}
						if logicType != "go" && logicType != "go_if_const" {
							actionCount++
						}
						logics = append(logics, logicMap)
					}
				}
				if len(logicsArray) > 0 && !isGo {
					nodeIDStr, _ := nodeMap["id"].(string)
					return fmt.Errorf("error in the %s node, Each node in the condition.logics array must always have a logic with \"type\": \"go\" at the end. This is necessary in case none of the conditions are met, so the request is redirected by default to the specified node.", nodeIDStr)
				}
				if actionCount > 1 {
					return fmt.Errorf("error in the %s node (%s): condition.logics must not contain more than one action logic (types other than \"go\" and \"go_if_const\"). Found %d action logics.", objID, title, actionCount)
				}
			}
		}

		semaphors := []map[string]interface{}{}
		if condition, ok := nodeMap["condition"].(map[string]interface{}); ok {
			if semaphorsArray, ok := condition["semaphors"].([]interface{}); ok {
				for _, semaphor := range semaphorsArray {
					if semaphorMap, ok := semaphor.(map[string]interface{}); ok {
						semaphors = append(semaphors, semaphorMap)
					}
				}
			}
		}

		op := map[string]any{
			"type":        "modify",
			"obj":         "node",
			"obj_id":      objID,
			"conv_id":     v.ProcessID,
			"title":       title,
			"description": description,
			"obj_type":    objType,
			"version":     v.Version,
		}

		if len(logics) > 0 {
			op["logics"] = logics
		} else {
			op["logics"] = []map[string]interface{}{}
		}

		if len(semaphors) > 0 {
			op["semaphors"] = semaphors
		} else {
			op["semaphors"] = []map[string]interface{}{}
		}

		if len(position) == 2 {
			op["position"] = position
		}
		if extra != nil {
			op["extra"] = extra
		}
		if options != nil {
			op["options"] = options
		}

		ops = append(ops, op)
	}

	if len(ops) == 0 {
		return fmt.Errorf("no valid nodes found to modify")
	}

	response, err := v.req("modify_nodes", ops)
	if err != nil {
		return fmt.Errorf("failed to modify nodes: %w", err)
	}
	if response == nil {
		return fmt.Errorf("failed to modify nodes: no response from server")
	}
	if requestProc, ok := response["request_proc"].(string); !ok || requestProc != "ok" {
		return fmt.Errorf("failed to modify nodes: request_proc not ok")
	}

	if opsArray, ok := response["ops"].([]interface{}); ok {
		var errorMsgs []string
		for i, op := range opsArray {
			if opMap, ok := op.(map[string]interface{}); ok {
				if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
					nodeInfo := fmt.Sprintf("Operation %d", i+1)
					if id, ok := ops[i]["obj_id"].(string); ok {
						nodeInfo = id
					}
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
										errorMsgs = append(errorMsgs, fmt.Sprintf("Node %s: %s", nodeID, msg))
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
			return fmt.Errorf("%s", strings.Join(errorMsgs, "\n"))
		}
	}
	logger.Debug("Nodes were modified with their content")
	return nil
}

// CompileAPICode loads and compiles all api_code logic nodes on the server.
func (v *Executor) CompileAPICode(nodes []interface{}) error {
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}
		if condition, ok := nodeMap["condition"].(map[string]interface{}); ok {
			if logicsArray, ok := condition["logics"].([]interface{}); ok {
				for _, logic := range logicsArray {
					if logicMap, ok := logic.(map[string]interface{}); ok {
						lt, _ := logicMap["type"].(string)
						if lt == "api_code" {
							nodeID, ok := nodeMap["id"].(string)
							if !ok {
								continue
							}
							lang, _ := logicMap["lang"].(string)
							if lang == "" {
								lang = "js"
							}
							src, _ := logicMap["src"].(string)
							if src == "" {
								src, _ = logicMap["code"].(string)
								if src == "" {
									continue
								}
							}

							loadOps := []map[string]any{
								{
									"type":    "load",
									"obj":     "api_code",
									"conv_id": v.ProcessID,
									"node_id": nodeID,
									"lang":    lang,
									"src":     src,
									"env":     "sandbox",
								},
							}
							loadResponse, err := v.req("load_api_code", loadOps)
							if err != nil {
								return fmt.Errorf("failed to load API code: %w", err)
							}
							if loadResponse == nil {
								return fmt.Errorf("failed to load API code: no response from server")
							}
							if requestProc, ok := loadResponse["request_proc"].(string); !ok || requestProc != "ok" {
								return fmt.Errorf("failed to load API code: request_proc not ok")
							}
							if opsArray, ok := loadResponse["ops"].([]interface{}); ok {
								for _, op := range opsArray {
									if opMap, ok := op.(map[string]interface{}); ok {
										if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
											if errors, ok := opMap["errors"].(map[string]interface{}); ok {
												var errorMsgs []string
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
												if len(errorMsgs) > 0 {
													return fmt.Errorf("failed to load API code:\n%s", strings.Join(errorMsgs, "\n"))
												}
											}
											errorMsg := "unknown error"
											if msg, ok := opMap["description"].(string); ok {
												errorMsg = msg
											}
											return fmt.Errorf("failed to load API code: %s", errorMsg)
										}
									}
								}
							}

							compileOps := []map[string]any{
								{
									"obj":     "api_code",
									"type":    "compile",
									"conv_id": v.ProcessID,
									"node_id": nodeID,
									"lang":    lang,
									"src":     src,
								},
							}
							compileResponse, err := v.req("compile_api_code", compileOps)
							if err != nil {
								return fmt.Errorf("failed to compile API code: %w", err)
							}
							if compileResponse == nil {
								return fmt.Errorf("failed to compile API code: no response from server")
							}
							if requestProc, ok := compileResponse["request_proc"].(string); !ok || requestProc != "ok" {
								return fmt.Errorf("failed to compile API code: request_proc not ok")
							}
							if opsArray, ok := compileResponse["ops"].([]interface{}); ok {
								for _, op := range opsArray {
									if opMap, ok := op.(map[string]interface{}); ok {
										if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
											if errors, ok := opMap["errors"].(map[string]interface{}); ok {
												var errorMsgs []string
												for objID, errMsgs := range errors {
													nodeID := objID
													if errArray, ok := errMsgs.([]interface{}); ok {
														for _, errMsg := range errArray {
															if msg, ok := errMsg.(string); ok {
																errorMsgs = append(errorMsgs, fmt.Sprintf("Node %s: %s", nodeID, msg))
															}
														}
													}
												}
												if len(errorMsgs) > 0 {
													return fmt.Errorf("failed to compile API code:\n%s", strings.Join(errorMsgs, "\n"))
												}
											}
											errorMsg := "unknown error"
											if msg, ok := opMap["description"].(string); ok {
												errorMsg = msg
											}
											return fmt.Errorf("failed to compile API code: %s", errorMsg)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return nil
}

// DeleteNotUsedNodes compares old server nodes with the new JSON nodes and deletes any orphans.
func (v *Executor) DeleteNotUsedNodes(oldNodes []any, newNodes []any) []any {
	for _, oldNode := range oldNodes {
		oldNodeMap, ok := oldNode.(map[string]interface{})
		if !ok {
			continue
		}
		objTypeF, _ := oldNodeMap["obj_type"].(float64)
		if int(objTypeF) == 1 {
			continue
		}
		id, ok := oldNodeMap["obj_id"].(string)
		if !ok || id == "" {
			continue
		}
		foundID := -1
		for newNodeID, newNode := range newNodes {
			newNodeMap, ok := newNode.(map[string]interface{})
			if !ok {
				continue
			}
			nodeID, _ := newNodeMap["id"].(string)
			if nodeID == id {
				foundID = newNodeID
				break
			}
		}
		if foundID == -1 {
			logger.Debug("Deleting node %s", id)
			v.DeleteNode(id)
		} else {
			nn, ok := newNodes[foundID].(map[string]interface{})
			if ok {
				nn["existed"] = true
				newNodes[foundID] = nn
			}
		}
	}
	return newNodes
}

// DeleteNode removes a single node by its server obj_id.
func (v *Executor) DeleteNode(id string) {
	ops := []map[string]any{
		{
			"type":       "delete",
			"obj":        "node",
			"company_id": v.WorkspaceID,
			"obj_id":     id,
			"conv_id":    v.ProcessID,
			"version":    v.Version,
		},
	}
	if v.Debug {
		logger.Debug("Sending delete node request")
	}
	response, err := v.req("delete_node", ops)
	if err != nil {
		logger.Error("Failed to delete node: %v", err)
		return
	}
	if response == nil {
		logger.Error("Failed to delete node: no response from server")
	} else if v.Debug {
		if requestProc, ok := response["request_proc"].(string); ok {
			logger.Debug("Delete node response received, request_proc=%s", requestProc)
		}
	}
}
