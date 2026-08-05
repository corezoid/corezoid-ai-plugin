package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// handleListTaskHistory dumps the raw transition history for a given task.
// The response is forwarded verbatim so callers can inspect every step.
func handleListTaskHistory(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	taskID, err := strArg(args, "task_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, processID)
	ops := []map[string]any{
		{
			"type":    "list",
			"obj":     "task_history",
			"conv_id": processID,
			"obj_id":  taskID,
		},
	}
	resp, err := v.req("list_task_history", ops)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleListNodeTasks lists tasks currently sitting at a particular node.
// limit/offset are optional and default to 50/0.
func handleListNodeTasks(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	nodeID, err := strArg(args, "node_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	limit := 50
	if _, ok := args["limit"]; ok {
		if n, err := intArg(args, "limit"); err == nil {
			limit = n
		}
	}
	offset := 0
	if _, ok := args["offset"]; ok {
		if n, err := intArg(args, "offset"); err == nil {
			offset = n
		}
	}

	validator := NewValidator(ctx, processID)
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "node",
			"company_id": validator.WorkspaceID,
			"conv_id":    processID,
			"obj_id":     nodeID,
			"limit":      limit,
			"offset":     offset,
		},
	}
	resp, err := validator.req("list_node_tasks", ops)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleGetNodeStat returns time-series in/out statistics for a process node
// over a caller-specified time window. The interval parameter controls bucket
// granularity ("day" or "hour", default "day").
func handleGetNodeStat(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	nodeID, err := strArg(args, "node_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	start, err := intArg(args, "start")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	end, err := intArg(args, "end")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	interval := "day"
	if v, ok := args["interval"].(string); ok && v != "" {
		interval = v
	}
	timezoneOffset := 0
	if n, err := intArg(args, "timezone_offset"); err == nil {
		timezoneOffset = n
	}

	v := NewValidator(ctx, processID)
	ops := []map[string]any{
		{
			"obj":             "stat",
			"type":            "show",
			"group":           "time",
			"conv_id":         processID,
			"node_id":         nodeID,
			"company_id":      v.WorkspaceID,
			"start":           start,
			"end":             end,
			"interval":        interval,
			"timezone_offset": timezoneOffset,
		},
	}
	resp, err := v.req("get_node_stat", ops)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// deepMerge recursively merges src into dst and returns the result. For keys
// present in both maps where both values are themselves maps, the values are
// merged recursively. For all other types the src value wins (overwrites dst).
// Neither dst nor src is mutated.
func deepMerge(dst, src map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(dst))
	for k, v := range dst {
		result[k] = v
	}
	for k, v := range src {
		if srcMap, ok := v.(map[string]interface{}); ok {
			if dstMap, ok := result[k].(map[string]interface{}); ok {
				result[k] = deepMerge(dstMap, srcMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

// handleModifyTask updates the data payload of an existing task. Either
// task_id or ref must be supplied to identify the task.
//
// By default the Corezoid API performs a SHALLOW (top-level) merge: every
// top-level key in data overwrites the entire existing value, so nested
// objects are fully replaced. Pass deep_merge: true to fetch the current
// task data first and perform a recursive merge that preserves sub-keys not
// present in the caller's payload.
func handleModifyTask(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	dataStr, err := strArg(args, "data")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	taskID, _ := args["task_id"].(string)
	ref, _ := args["ref"].(string)
	if taskID == "" && ref == "" {
		return "Error: at least one of task_id or ref must be provided", true
	}

	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(dataStr), &taskData); err != nil {
		return fmt.Sprintf("Error parsing data JSON: %v", err), true
	}

	v := NewValidator(ctx, processID)

	// deep_merge: fetch current task data and recursively merge into it.
	deepMergeMode, _ := args["deep_merge"].(bool)
	if deepMergeMode {
		showOp := map[string]any{
			"type":    "show",
			"obj":     "task",
			"conv_id": processID,
		}
		if taskID != "" {
			showOp["obj_id"] = taskID
		} else {
			showOp["ref"] = ref
		}
		showResp, err := v.req("show_task", []map[string]any{showOp})
		if err != nil {
			return fmt.Sprintf("Error fetching current task for deep merge: %v", err), true
		}
		opsArr, _ := showResp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "Error: task not found", true
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		if opMap["proc"] != "ok" {
			desc, _ := opMap["description"].(string)
			return fmt.Sprintf("Error resolving task: %s", desc), true
		}
		if currentData, ok := opMap["data"].(map[string]interface{}); ok {
			taskData = deepMerge(currentData, taskData)
		}
	}

	op := map[string]any{
		"type":    "modify",
		"obj":     "task",
		"conv_id": processID,
		"data":    taskData,
	}
	if taskID != "" {
		op["obj_id"] = taskID
	}
	if ref != "" {
		op["ref"] = ref
	}

	resp, err := v.req("modify_task", []map[string]any{op})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}

// handleDeleteTask removes a task. The Corezoid delete-task RPC requires both
// the resolved obj_id and the current node_id, so we issue a show-task first
// to look those up regardless of whether the caller passed task_id or ref.
func handleDeleteTask(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	taskID, _ := args["task_id"].(string)
	ref, _ := args["ref"].(string)
	if taskID == "" && ref == "" {
		return "Error: at least one of task_id or ref must be provided", true
	}

	v := NewValidator(ctx, processID)

	// Resolve task_id and node_id via show first
	showOp := map[string]any{
		"type":    "show",
		"obj":     "task",
		"conv_id": processID,
	}
	if taskID != "" {
		showOp["obj_id"] = taskID
	} else {
		showOp["ref"] = ref
	}
	showResp, err := v.req("show_task", []map[string]any{showOp})
	if err != nil {
		return fmt.Sprintf("Error resolving task: %v", err), true
	}
	opsArr, _ := showResp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return "Error: task not found", true
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if opMap["proc"] != "ok" {
		desc, _ := opMap["description"].(string)
		return fmt.Sprintf("Error: %s", desc), true
	}
	resolvedTaskID, _ := opMap["obj_id"].(string)
	nodeID, _ := opMap["node_id"].(string)

	deleteOp := map[string]any{
		"type":    "delete",
		"obj":     "task",
		"conv_id": processID,
		"obj_id":  resolvedTaskID,
		"node_id": nodeID,
	}
	resp, err := v.req("delete_task", []map[string]any{deleteOp})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}
