package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
// The history op only keys on obj_id, so a bare ref is first resolved through
// the read-only show-task lookup — the same addressing show-task accepts.
func handleListTaskHistory(ctx context.Context, args map[string]interface{}) (string, bool) {
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
	if taskID == "" {
		snap, err := showTask(v, processID, "", ref)
		if err != nil {
			return fmt.Sprintf("Error resolving task by ref: %v", err), true
		}
		if snap.ObjID == "" {
			return "Error resolving task by ref: show-task returned no obj_id", true
		}
		taskID = snap.ObjID
	}
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

// nodeTaskView is the parsed shaping arguments of list-node-tasks.
//
// The raw list returns every task's full data payload, so a page of ten tasks
// could be 60 KB when the caller only wanted a status or one field. The
// default view therefore drops data entirely and keeps the task metadata;
// fields brings back only the named data paths, and full restores the payload.
//
// fields come back under their own key, keyed by the path as given
// ({"customer.id": 3}), never under data: that map is not the shape of the
// task's data, and modify-task writes keys verbatim, so sent back as a payload
// it would add a literal "customer.id" key and leave customer.id unchanged.
//
// Truncation is opt-in (maxValue). Whatever fields or full return is what a
// caller edits and sends back through modify-task, so a value cut by default
// would be written into the task as its new, shortened content.
type nodeTaskView struct {
	full     bool
	fields   []string
	filter   map[string]interface{}
	maxValue int // 0 = no truncation
}

func parseNodeTaskView(args map[string]interface{}) (nodeTaskView, error) {
	var view nodeTaskView
	switch mode := optStrArg(args, "mode"); mode {
	case "", "compact":
	case "full":
		view.full = true
	default:
		return view, fmt.Errorf(`argument mode must be "compact" or "full", got %q`, mode)
	}
	if raw, ok := args["fields"]; ok && raw != nil {
		// The CLI sends every argument as a string: fields=status,customer.id.
		if s, isStr := raw.(string); isStr {
			var list []interface{}
			for _, f := range strings.Split(s, ",") {
				if f = strings.TrimSpace(f); f != "" {
					list = append(list, f)
				}
			}
			raw = list
		}
		list, ok := raw.([]interface{})
		if !ok {
			return view, fmt.Errorf("argument fields must be an array of strings, got %T", raw)
		}
		for _, f := range list {
			s, ok := f.(string)
			if !ok || s == "" {
				return view, fmt.Errorf("argument fields must contain only non-empty strings, got %#v", f)
			}
			view.fields = append(view.fields, s)
		}
	}
	if raw, ok := args["filter"]; ok && raw != nil {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return view, fmt.Errorf("argument filter must be an object mapping data paths to values, got %T", raw)
		}
		view.filter = m
	}
	if _, ok := args["max_value_length"]; ok {
		n, err := intArg(args, "max_value_length")
		if err != nil {
			return view, err
		}
		if n < 0 {
			return view, fmt.Errorf("argument max_value_length must be >= 0, got %d", n)
		}
		view.maxValue = n
	}
	return view, nil
}

// dataPath looks up a dot-separated path ("customer.id") inside task data.
func dataPath(data map[string]interface{}, path string) (interface{}, bool) {
	var cur interface{} = data
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		if cur, ok = m[key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// scalarText is the text form of a JSON string, number or boolean. A set_param
// value is a string unless its type is set, while callers pass ids as numbers,
// so a filter that compared types would report {"chat_id": 42} as absent from
// a task holding "42".
func scalarText(v interface{}) (string, bool) {
	switch val := v.(type) {
	case string:
		return val, true
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(val), true
	}
	return "", false
}

// filterMatches reports whether every filter path is present in data and
// equal to the expected value. Scalars are compared by their text form, so 42
// matches both 42.0 and "42"; objects, arrays and null must be equal by their
// JSON encoding.
func filterMatches(data map[string]interface{}, filter map[string]interface{}) bool {
	for path, want := range filter {
		got, ok := dataPath(data, path)
		if !ok {
			return false
		}
		if g, ok := scalarText(got); ok {
			if w, ok := scalarText(want); ok {
				if g != w {
					return false
				}
				continue
			}
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			return false
		}
	}
	return true
}

// truncateValues returns a copy of v with every string longer than max runes
// cut down and marked, recursing into objects and arrays.
func truncateValues(v interface{}, max int) interface{} {
	if max <= 0 {
		return v
	}
	switch val := v.(type) {
	case string:
		r := []rune(val)
		if len(r) <= max {
			return val
		}
		return fmt.Sprintf("%s…[truncated, %d chars total]", string(r[:max]), len(r))
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, x := range val {
			out[k] = truncateValues(x, max)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, x := range val {
			out[i] = truncateValues(x, max)
		}
		return out
	}
	return v
}

// compact reports whether the view returns no data at all, only metadata.
func (view nodeTaskView) compact() bool {
	return !view.full && len(view.fields) == 0
}

// shapeTask applies the view to one task entry of the list. Every key other
// than data is metadata (obj_id, ref, status, create_time, …) and is kept.
func (view nodeTaskView) shapeTask(task map[string]interface{}) map[string]interface{} {
	data, _ := task["data"].(map[string]interface{})
	out := make(map[string]interface{}, len(task)+1)
	for k, v := range task {
		if k != "data" {
			out[k] = v
		}
	}
	switch {
	case len(view.fields) > 0:
		picked := make(map[string]interface{}, len(view.fields))
		for _, f := range view.fields {
			if v, ok := dataPath(data, f); ok {
				picked[f] = v
			}
		}
		out["fields"] = truncateValues(picked, view.maxValue)
	case view.full:
		out["data"] = truncateValues(task["data"], view.maxValue)
	default:
		raw, _ := json.Marshal(task["data"])
		out["data_bytes"] = len(raw)
	}
	return out
}

// shapeNodeTasks rewrites the list of every op in a list-node-tasks response
// in place, applying the filter and the view. Each op gains "returned" and,
// when a filter is set, "scanned" so the caller can tell how much of the page
// the filter dropped.
//
// A compact op also gains "data_keys": the top-level data keys of the returned
// tasks, sorted. Without the names a caller cannot write fields or filter and
// would fall back to mode=full just to learn them. It is one list for the page
// rather than one per task because tasks parked in one node mostly share their
// keys, and a bot task can carry dozens of them.
func (view nodeTaskView) shapeNodeTasks(resp map[string]interface{}) {
	ops, _ := resp["ops"].([]interface{})
	for _, o := range ops {
		op, ok := o.(map[string]interface{})
		if !ok {
			continue
		}
		list, ok := op["list"].([]interface{})
		if !ok {
			continue
		}
		shaped := make([]interface{}, 0, len(list))
		keySet := map[string]struct{}{}
		for _, t := range list {
			task, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			data, _ := task["data"].(map[string]interface{})
			if view.filter != nil && !filterMatches(data, view.filter) {
				continue
			}
			for k := range data {
				keySet[k] = struct{}{}
			}
			shaped = append(shaped, view.shapeTask(task))
		}
		op["list"] = shaped
		op["returned"] = len(shaped)
		if view.filter != nil {
			op["scanned"] = len(list)
		}
		if view.compact() {
			keys := make([]string, 0, len(keySet))
			for k := range keySet {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			op["data_keys"] = keys
		}
	}
}

// handleListNodeTasks lists tasks currently sitting at a particular node.
// limit/offset are optional and default to 50/0. The output is compact by
// default (metadata only) — see nodeTaskView.
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
	view, err := parseNodeTaskView(args)
	if err != nil {
		return "Error: " + err.Error(), true
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
	view.shapeNodeTasks(resp)
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
	// Task data is structured, so callers naturally send it as an object;
	// accept that as well as the original JSON-string form.
	var taskData map[string]interface{}
	switch d := args["data"].(type) {
	case map[string]interface{}:
		taskData = d
	case string:
		if err := json.Unmarshal([]byte(d), &taskData); err != nil {
			return fmt.Sprintf("Error parsing data JSON: %v", err), true
		}
	case nil:
		return "Error: missing required argument: data", true
	default:
		return fmt.Sprintf("Error: argument data must be a JSON object or a JSON object string, got %T", d), true
	}
	taskID, _ := args["task_id"].(string)
	ref, _ := args["ref"].(string)
	if taskID == "" && ref == "" {
		return "Error: at least one of task_id or ref must be provided", true
	}

	// deep_merge decides whether this is a merge or a wholesale replace, so it
	// is read strictly and with the other argument checks: absent keeps the
	// documented shallow default, but an unreadable value is refused rather
	// than resolved to false, because false is the destructive answer here —
	// it replaces nested objects and drops the sub-keys the caller did not send.
	deepMergeMode, bErr := strictBoolArg(args, "deep_merge")
	if bErr != "" {
		return bErr + " Refusing rather than defaulting to a shallow write, which would drop nested keys you did not send.", true
	}

	v := NewValidator(ctx, processID)
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
