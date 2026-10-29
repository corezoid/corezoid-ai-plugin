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

// By default run-task must not echo tens of KB of task data (#240): the
// summary stays small, keeps the small fields, and says how to get the rest.
func TestHandleRunTask_CompactsLargeDataByDefault(t *testing.T) {
	result, isErr := runTaskWithData(t, bigTaskData(), nil)
	if isErr || !strings.Contains(result, "Status: completed") {
		t.Fatalf("run failed: %s", result)
	}
	if len(result) > 6000 {
		t.Fatalf("run-task summary is %d bytes; want a compact result", len(result))
	}
	for _, want := range []string{`"status":"ok"`, "[array truncated", "truncated, 40000 bytes total", "full_data", "fields"} {
		if !strings.Contains(result, want) {
			t.Errorf("summary missing %q: %s", want, result)
		}
	}
}

func TestHandleRunTask_FieldsSelectsValues(t *testing.T) {
	result, isErr := runTaskWithData(t, bigTaskData(), map[string]interface{}{
		"fields": []interface{}{"status", "customer.id", "nope"},
	})
	if isErr {
		t.Fatalf("run failed: %s", result)
	}
	for _, want := range []string{`"customer.id":"c-42"`, `"status":"ok"`, "Missing fields: nope", "raw_body"} {
		if !strings.Contains(result, want) {
			t.Errorf("summary missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "xxxxxxxx") || len(result) > 2000 {
		t.Fatalf("fields must return only the picked values: %s", result)
	}
}

func TestHandleRunTask_FullDataReturnsEverything(t *testing.T) {
	result, isErr := runTaskWithData(t, bigTaskData(), map[string]interface{}{"full_data": true})
	if isErr {
		t.Fatalf("run failed: %s", result)
	}
	if !strings.Contains(result, strings.Repeat("ё", 20000)) || strings.Contains(result, "truncated") {
		t.Fatalf("full_data must return the data verbatim (got %d bytes)", len(result))
	}
}

func TestHandleRunTask_SmallDataUnchanged(t *testing.T) {
	result, _ := runTaskWithData(t, map[string]interface{}{"result": "ok"}, nil)
	if !strings.HasSuffix(result, `Data: {"result":"ok"}`) {
		t.Fatalf("small data must be returned as-is: %s", result)
	}
}

func TestHandleRunTask_RejectsBadSelectionArgs(t *testing.T) {
	for name, extra := range map[string]map[string]interface{}{
		"fields not strings": {"fields": []interface{}{float64(1)}},
		"full_data garbage":  {"full_data": "maybe"},
	} {
		t.Run(name, func(t *testing.T) {
			result, isErr := runTaskWithData(t, nil, extra)
			if !isErr || !strings.HasPrefix(result, "Error:") {
				t.Fatalf("want an argument error, got %v: %s", isErr, result)
			}
		})
	}
}

func TestRenderRunTaskData_KeysOnlyWhenStillLarge(t *testing.T) {
	data := map[string]interface{}{}
	for i := 0; i < 2000; i++ {
		data[fmt.Sprintf("field_%04d", i)] = strings.Repeat("v", 400)
	}
	out := renderRunTaskData(data, nil, false)
	if !strings.Contains(out, "too large to show") || !strings.Contains(out, "field_0000") || len(out) > 5000 {
		t.Fatalf("unexpected rendering (%d bytes): %.300s", len(out), out)
	}
}

func TestTruncateUTF8KeepsRunesWhole(t *testing.T) {
	if got := truncateUTF8("ёё", 3); got != "ё" {
		t.Fatalf("truncateUTF8 split a rune: %q", got)
	}
}
