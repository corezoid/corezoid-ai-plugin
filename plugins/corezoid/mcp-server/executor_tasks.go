package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (v *Executor) createTask(ref string, taskData map[string]interface{}) error {
	ops := []map[string]any{
		{
			"obj":     "task",
			"type":    "create",
			"action":  "user",
			"ref":     ref,
			"conv_id": v.ProcessID,
			"data":    taskData,
		},
	}
	if v.Debug {
		logger.Debug("Sending create task request")
	}
	response, err := v.req("create_task", ops)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}
	if opsArray, ok := response["ops"].([]interface{}); ok {
		for _, op := range opsArray {
			if opMap, ok := op.(map[string]interface{}); ok {
				if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
					errorMsg := "unknown error"
					if msg, ok := opMap["description"].(string); ok {
						errorMsg = msg
					}
					return fmt.Errorf("failed to create task code: %s", errorMsg)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("failed to create task: no response from server")
}

func (v *Executor) showTask(ref string) (data map[string]interface{}, err error) {
	ops := []map[string]any{
		{
			"obj":     "task",
			"type":    "show",
			"action":  "user",
			"ref":     ref,
			"conv_id": v.ProcessID,
		},
	}
	if v.Debug {
		logger.Debug("Sending create task request")
	}
	response, err := v.req("show_task", ops)
	if err != nil {
		return nil, fmt.Errorf("failed to show task: %w", err)
	}
	if opsArray, ok := response["ops"].([]interface{}); ok {
		for _, op := range opsArray {
			if opMap, ok := op.(map[string]interface{}); ok {
				if proc, ok := opMap["proc"].(string); !ok || proc != "ok" {
					errorMsg := "unknown error"
					if msg, ok := opMap["description"].(string); ok {
						errorMsg = msg
					}
					return nil, fmt.Errorf("failed to show task code: %s", errorMsg)
				}
				return opMap, nil
			}
		}
	}
	return nil, fmt.Errorf("failed to find task: no response from server")
}

// BeforeValidation validates the process JSON before deployment:
// checks that task data keys appear in params, detects self-references,
// and enforces the single-action-logic-per-node rule.
func (v *Executor) BeforeValidation(jsonContent string, taskData map[string]interface{}) error {
	var processData map[string]interface{}
	err := json.Unmarshal([]byte(jsonContent), &processData)
	if err != nil {
		return fmt.Errorf("error parsing JSON: %v", err)
	}
	params, _ := processData["params"].([]interface{})
	for name := range taskData {
		found := false
		for _, param := range params {
			paramMap, ok := param.(map[string]interface{})
			if !ok {
				continue
			}
			paramName, _ := paramMap["name"].(string)
			if paramName == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("task data key '%s' is not specified in the 'params' field of the process", name)
		}
	}
	nodes, err := getNodes(processData)
	if err != nil {
		return fmt.Errorf("error reading nodes: %v", err)
	}

	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}
		if condition, ok := nodeMap["condition"].(map[string]interface{}); ok {
			if logicsArray, ok := condition["logics"].([]interface{}); ok {
				actionCount := 0
				for _, logic := range logicsArray {
					if logicMap, ok := logic.(map[string]interface{}); ok {
						type1, _ := logicMap["type"].(string)
						if type1 == "api_copy" || type1 == "api_rpc" {
							if convID, ok := logicMap["conv_id"].(float64); ok && int(convID) == v.ProcessID {
								return fmt.Errorf("node with type %s and conv_id %d must not have the same process", type1, int(convID))
							}
						}
						if type1 != "go" && type1 != "go_if_const" {
							actionCount++
						}
					}
				}
				if actionCount > 1 {
					nodeID, _ := nodeMap["id"].(string)
					nodeTitle, _ := nodeMap["title"].(string)
					return fmt.Errorf("node '%s' (%s): condition.logics must not contain more than one action logic (types other than \"go\" and \"go_if_const\"). Found %d action logics.", nodeID, nodeTitle, actionCount)
				}
			}
		}
	}

	// Check that alias references in conv_id actually exist.
	type aliasRef struct {
		name     string
		nodeType string
	}
	var aliasRefs []aliasRef
	seenAlias := make(map[string]bool)
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}
		if condition, ok := nodeMap["condition"].(map[string]interface{}); ok {
			if logicsArray, ok := condition["logics"].([]interface{}); ok {
				for _, logic := range logicsArray {
					if logicMap, ok := logic.(map[string]interface{}); ok {
						type1, _ := logicMap["type"].(string)
						if type1 != "api_copy" && type1 != "api_rpc" {
							continue
						}
						convID, ok := logicMap["conv_id"].(string)
						if !ok || !strings.HasPrefix(convID, "@") {
							continue
						}
						aliasName := convID[1:]
						if strings.Contains(aliasName, "{{") {
							continue
						}
						if !seenAlias[aliasName] {
							seenAlias[aliasName] = true
							aliasRefs = append(aliasRefs, aliasRef{name: aliasName, nodeType: type1})
						}
					}
				}
			}
		}
	}
	if len(aliasRefs) > 0 && v.StageID != 0 {
		existingAliases, err := v.listAliasesByStage(v.StageID)
		if err != nil {
			return fmt.Errorf("alias validation failed: %v", err)
		}
		for _, ref := range aliasRefs {
			if _, exists := existingAliases[ref.name]; !exists {
				return fmt.Errorf("node with type %s references alias '@%s' which does not exist", ref.nodeType, ref.name)
			}
		}
	}

	// Check that all {{env_var[@name]}} references point to existing env variables.
	if v.StageID != 0 {
		matches := reEnvVar.FindAllStringSubmatch(jsonContent, -1)
		seen := make(map[string]bool)
		var uniqueVarNames []string
		for _, m := range matches {
			varName := m[1]
			if !seen[varName] {
				seen[varName] = true
				uniqueVarNames = append(uniqueVarNames, varName)
			}
		}
		if len(uniqueVarNames) > 0 {
			existingVars, err := v.listEnvVarsByStage(v.StageID)
			if err != nil {
				return fmt.Errorf("env var validation failed: %v", err)
			}
			for _, varName := range uniqueVarNames {
				if _, exists := existingVars[varName]; !exists {
					return fmt.Errorf("env variable '@%s' referenced in process does not exist", varName)
				}
			}
		}
	}

	return nil
}
