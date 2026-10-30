package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// runTaskDataServer serves a deployed process with one final node and a task
// that settled on it carrying data.
func runTaskDataServer(t *testing.T, data map[string]interface{}) string {
	t.Helper()
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		obj, _ := ops[0]["obj"].(string)
		typ, _ := ops[0]["type"].(string)
		switch obj + ":" + typ {
		case "conv:list":
			return map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok",
					"list": []interface{}{map[string]interface{}{
						"scheme": map[string]interface{}{"nodes": []interface{}{
							map[string]interface{}{"id": "server-final", "title": "Final", "obj_type": float64(2), "extra": `{"icon":""}`},
						}},
					}},
				}},
			}
		case "task:create":
			return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{map[string]interface{}{"proc": "ok"}}}
		case "task:show":
			return map[string]interface{}{
				"request_proc": "ok",
				"ops": []interface{}{map[string]interface{}{
					"proc": "ok", "obj_id": "task-1", "node_id": "server-final", "data": data,
				}},
			}
		}
		return map[string]interface{}{"request_proc": "error", "description": "unexpected operation"}
	})
	return srv.URL
}

func runTaskWithData(t *testing.T, data map[string]interface{}, extra map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	setProjectAuth(t, runTaskDataServer(t, data))
	t.Chdir(t.TempDir())
	originalFirstPoll, originalPollEvery := runTaskFirstPollAfter, runTaskPollEvery
	runTaskFirstPollAfter, runTaskPollEvery = time.Millisecond, time.Millisecond
	t.Cleanup(func() { runTaskFirstPollAfter, runTaskPollEvery = originalFirstPoll, originalPollEvery })

	args := map[string]interface{}{"process_id": float64(123), "data": `{}`, "ref": "big", "wait_sec": 1}
	for k, v := range extra {
		args[k] = v
	}
	return handleRunTask(context.Background(), args)
}

// bigTaskData is the shape that filled sessions' context (#240): a couple of
// small fields the caller wants next to tens of KB accumulated by earlier nodes.
func bigTaskData() map[string]interface{} {
	items := make([]interface{}, 0, 500)
	for i := 0; i < 500; i++ {
		items = append(items, map[string]interface{}{"id": i, "name": strings.Repeat("x", 40)})
	}
	return map[string]interface{}{
		"status":   "ok",
		"customer": map[string]interface{}{"id": "c-42", "profile": strings.Repeat("p", 2000)},
		"items":    items,
		"raw_body": strings.Repeat("ё", 20000),
	}
}

// By default run-task must not echo tens of KB of task data (#240). It leaves
// the data out rather than cutting values — a shortened value read as the
// task's data would be written back through modify-task — and points at the
// read-only show-task, not at a re-run that starts a second task.
func TestHandleRunTask_LeavesOutLargeDataByDefault(t *testing.T) {
	result, isErr := runTaskWithData(t, bigTaskData(), nil)
	if isErr || !strings.Contains(result, "Status: completed") {
		t.Fatalf("run failed: %s", result)
	}
	if len(result) > 2000 {
		t.Fatalf("run-task summary is %d bytes; want the data left out", len(result))
	}
	for _, want := range []string{
		"Data: (not shown:", "4 keys)",
		"DataKeys: customer, items, raw_body, status",
		`cz-tasks {"action":"show-task","args":{"process_id":123,"task_id":"task-1"}}`,
		"starts a second task",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("summary missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "truncated") || strings.Contains(result, "ppp") {
		t.Errorf("default view must not print cut values: %s", result)
	}
}

// fields come back on their own line keyed by the path, never as Data: that
// map is not the task's data shape, and modify-task would write "customer.id"
// as a literal key.
func TestHandleRunTask_FieldsOnOwnLine(t *testing.T) {
	result, isErr := runTaskWithData(t, bigTaskData(), map[string]interface{}{
		"fields": []interface{}{"status", "customer.id", "nope"},
	})
	if isErr {
		t.Fatalf("run failed: %s", result)
	}
	for _, want := range []string{`Fields: {"customer.id":"c-42","status":"ok"}`, "MissingFields: nope (data keys: customer, items, raw_body, status)"} {
		if !strings.Contains(result, want) {
			t.Errorf("summary missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "Data:") || len(result) > 1500 {
		t.Fatalf("fields must return only the picked values: %s", result)
	}
}

// The CLI sends every argument as a string.
func TestHandleRunTask_FieldsFromCLIString(t *testing.T) {
	result, _ := runTaskWithData(t, bigTaskData(), map[string]interface{}{"fields": " status , customer.id"})
	if !strings.Contains(result, `Fields: {"customer.id":"c-42","status":"ok"}`) {
		t.Fatalf("comma-separated fields not applied: %s", result)
	}
}

func TestHandleRunTask_ModeFullReturnsEverything(t *testing.T) {
	result, isErr := runTaskWithData(t, bigTaskData(), map[string]interface{}{"mode": "full"})
	if isErr {
		t.Fatalf("run failed: %s", result)
	}
	if !strings.Contains(result, strings.Repeat("ё", 20000)) || strings.Contains(result, "truncated") {
		t.Fatalf("mode full must return the data verbatim (got %d bytes)", len(result))
	}
}

// max_value_length cuts only when asked, and only in fields / full output.
func TestHandleRunTask_MaxValueLengthOptIn(t *testing.T) {
	result, _ := runTaskWithData(t, bigTaskData(), map[string]interface{}{"mode": "full", "max_value_length": 10})
	if !strings.Contains(result, `"raw_body":"ёёёёёёёёёё…[truncated, 20000 chars total]"`) || len(result) > 50000 {
		t.Fatalf("max_value_length not applied to mode full: %.400s", result)
	}
	result, _ = runTaskWithData(t, map[string]interface{}{"result": strings.Repeat("r", 100)}, map[string]interface{}{"max_value_length": 10})
	if strings.Contains(result, "truncated") {
		t.Fatalf("max_value_length must not cut the default view: %s", result)
	}
}

func TestHandleRunTask_SmallDataUnchanged(t *testing.T) {
	result, _ := runTaskWithData(t, map[string]interface{}{"result": "ok"}, nil)
	if !strings.HasSuffix(result, `Data: {"result":"ok"}`) {
		t.Fatalf("small data must be returned as-is: %s", result)
	}
}

func TestHandleRunTask_RejectsBadViewArgs(t *testing.T) {
	for name, extra := range map[string]map[string]interface{}{
		"fields not strings":        {"fields": []interface{}{float64(1)}},
		"fields empty string":       {"fields": []interface{}{""}},
		"mode unknown":              {"mode": "everything"},
		"max_value_length negative": {"max_value_length": -1},
	} {
		t.Run(name, func(t *testing.T) {
			result, isErr := runTaskWithData(t, nil, extra)
			if !isErr || !strings.HasPrefix(result, "Error:") {
				t.Fatalf("want an argument error, got %v: %s", isErr, result)
			}
		})
	}
}

func TestRenderRunTaskData_CapsKeyList(t *testing.T) {
	data := map[string]interface{}{}
	for i := 0; i < 2000; i++ {
		data[fmt.Sprintf("field_%04d", i)] = "v"
	}
	out := renderRunTaskData(data, nodeTaskView{}, 1, "", "my-ref")
	if !strings.Contains(out, "field_0000") || !strings.Contains(out, "more") || len(out) > runTaskDataKeysMax+600 {
		t.Fatalf("unexpected rendering (%d bytes): %.300s", len(out), out)
	}
	if !strings.Contains(out, `"ref":"my-ref"`) {
		t.Fatalf("without a task id the show-task hint must use the ref: %s", out)
	}
}
