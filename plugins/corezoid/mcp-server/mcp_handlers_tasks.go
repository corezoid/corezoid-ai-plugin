package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// errTaskNotFound is returned by showTask when the API answers with an empty
// ops array — the task_id/ref resolved to nothing at all. Distinct from an op
// that came back with proc != "ok", which carries the API's own description.
var errTaskNotFound = errors.New("task not found")

// taskSnapshot is the resolved read-only view of one task, as returned by the
// Corezoid {"type":"show","obj":"task"} op. Op holds the raw op result so
// callers that want every field (status, create_time, user_id, …) can forward
// it verbatim; the named fields cover what the internal callers need.
type taskSnapshot struct {
	ObjID  string
	NodeID string
	Data   map[string]interface{}
	Op     map[string]interface{}
}

// showTask resolves a single task by task_id and/or ref via the read-only
// show-task op. It commits nothing, so it works on immutable stages and with
// read-only access.
//
// Exactly one identifier is put on the wire: task_id wins when both are given,
// matching how the Corezoid API keys the lookup. The caller is responsible for
// rejecting the both-empty case before calling.
func showTask(v *Executor, processID int, taskID, ref string) (*taskSnapshot, error) {
	op := map[string]any{
		"type":    "show",
		"obj":     "task",
		"conv_id": processID,
	}
	if taskID != "" {
		op["obj_id"] = taskID
	} else {
		op["ref"] = ref
	}

	resp, err := v.req("show_task", []map[string]any{op})
	if err != nil {
		return nil, err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, errTaskNotFound
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if opMap["proc"] != "ok" {
		desc, _ := opMap["description"].(string)
		if desc == "" {
			desc = "show-task returned a non-ok operation"
		}
		return nil, errors.New(desc)
	}

	snap := &taskSnapshot{Op: opMap}
	snap.ObjID, _ = opMap["obj_id"].(string)
	snap.NodeID, _ = opMap["node_id"].(string)
	snap.Data, _ = opMap["data"].(map[string]interface{})
	return snap, nil
}

// handleShowTask returns the current state of a single task — data, obj_id,
// node_id and status — resolved by task_id and/or ref. This is the read-only
// counterpart to modify-task: it never writes, so it is usable on immutable
// stages and by callers who only hold view rights.
func handleShowTask(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	taskID, _ := args["task_id"].(string)
	ref, _ := args["ref"].(string)
	if taskID == "" && ref == "" {
		return "Error: at least one of task_id or ref must be provided", true
	}

	snap, err := showTask(NewValidator(ctx, processID), processID, taskID, ref)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(snap.Op, "", "  ")
	return string(data), false
}

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
		snap, err := showTask(v, processID, taskID, ref)
		if err != nil {
			return fmt.Sprintf("Error fetching current task for deep merge: %v", err), true
		}
		if snap.Data != nil {
			taskData = deepMerge(snap.Data, taskData)
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
	snap, err := showTask(v, processID, taskID, ref)
	if err != nil {
		return fmt.Sprintf("Error resolving task: %v", err), true
	}

	deleteOp := map[string]any{
		"type":    "delete",
		"obj":     "task",
		"conv_id": processID,
		"obj_id":  snap.ObjID,
		"node_id": snap.NodeID,
	}
	resp, err := v.req("delete_task", []map[string]any{deleteOp})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), false
}
