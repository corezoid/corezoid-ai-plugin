package main

import (
	"context"
	"testing"
)

func wsExecMock(t *testing.T, resp map[string]interface{}) *Executor {
	t.Helper()
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} { return resp })
	e.Ctx = context.Background()
	return e
}

func TestDeployDiffList(t *testing.T) {
	ok := map[string]interface{}{"ops": []interface{}{map[string]interface{}{
		"proc": "ok", "list": []interface{}{map[string]interface{}{"title": "a"}}}}}
	list, e := deployDiffList(ok)
	if e != "" || len(list) != 1 {
		t.Fatalf("ok: list=%d err=%q", len(list), e)
	}
	if _, e := deployDiffList(map[string]interface{}{"ops": []interface{}{
		map[string]interface{}{"proc": "error", "description": "boom"}}}); e == "" {
		t.Fatalf("expected error for proc!=ok")
	}
	if _, e := deployDiffList(map[string]interface{}{"ops": []interface{}{}}); e == "" {
		t.Fatalf("expected error for empty ops")
	}
	if list, e := deployDiffList(map[string]interface{}{"ops": []interface{}{
		map[string]interface{}{"proc": "ok"}}}); e != "" || len(list) != 0 {
		t.Fatalf("missing list: len=%d err=%q", len(list), e)
	}
}

func TestDeployOpProc(t *testing.T) {
	if s := deployOpProc(map[string]interface{}{"ops": []interface{}{
		map[string]interface{}{"proc": "ok"}}}); s != "" {
		t.Fatalf("ok returned %q", s)
	}
	if s := deployOpProc(map[string]interface{}{"ops": []interface{}{
		map[string]interface{}{"proc": "error", "description": "x"}}}); s == "" {
		t.Fatalf("expected error string")
	}
	if s := deployOpProc(map[string]interface{}{"ops": []interface{}{}}); s == "" {
		t.Fatalf("expected error for empty ops")
	}
}

func TestStageInfo(t *testing.T) {
	e := wsExecMock(t, map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
		map[string]interface{}{"proc": "ok", "immutable": true, "undeployed": float64(3), "title": "prod", "short_name": "p"}}})
	imm, undep, title, short, err := e.stageInfo(1, 2)
	if err != nil || !imm || undep != 3 || title != "prod" || short != "p" {
		t.Fatalf("stageInfo: err=%v imm=%v undep=%d title=%q short=%q", err, imm, undep, title, short)
	}
	e2 := wsExecMock(t, map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
		map[string]interface{}{"proc": "error", "description": "nope"}}})
	if _, _, _, _, err := e2.stageInfo(1, 2); err == nil {
		t.Fatalf("expected error for proc!=ok")
	}
}

func TestSetStageImmutableExec(t *testing.T) {
	e := wsExecMock(t, map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
		map[string]interface{}{"proc": "ok"}}})
	if err := e.setStageImmutable(1, 2, "t", "s", true); err != nil {
		t.Fatalf("ok: %v", err)
	}
	e2 := wsExecMock(t, map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
		map[string]interface{}{"proc": "error", "description": "no"}}})
	if err := e2.setStageImmutable(1, 2, "t", "s", true); err == nil {
		t.Fatalf("expected error for proc!=ok")
	}
}

// Real progress frames captured from the admin UI deploy WebSocket.
const (
	frameUploadComplete = `{"request_proc":"ok","ops":[{"type":"monitor_stat","obj":"scheme_progress_info","obj_id":"H","mode":"upload","proc":"ok","list":[{"status":"complete","progress":100}]}]}`
	frameMergeComplete  = `{"request_proc":"ok","ops":[{"type":"monitor_stat","obj":"scheme_progress_info","obj_id":"H","mode":"merge","proc":"ok","list":[{"status":"complete","progress":100}]}]}`
	frameNotifyMerge    = `{"request_proc":"ok","ops":[{"type":"notify","action_type":"merge","obj_type":"scheme","proc":"ok","folder_id":684082,"company_id":"a","hash":"H"}]}`
	frameError          = `{"request_proc":"ok","ops":[{"type":"monitor_stat","mode":"merge","proc":"error","list":[]}]}`
	frameProgressMid    = `{"request_proc":"ok","ops":[{"type":"monitor_stat","mode":"merge","proc":"ok","list":[{"status":"in_progress","progress":40}]}]}`
	// Real completion frame: proc "error" but description "Merging is finished",
	// status "complete", empty errors — this is SUCCESS, not a failure.
	frameMergingFinished = `{"request_proc":"ok","ops":[{"proc":"error","description":"Merging is finished","obj_id":"H","status":"complete","errors":[]}]}`
)

func TestParseDeployProgress(t *testing.T) {
	cases := []struct {
		name             string
		frame            string
		wantDone, wantFail bool
	}{
		{"upload complete is not done", frameUploadComplete, false, false},
		{"merge complete is done", frameMergeComplete, true, false},
		{"notify merge is done", frameNotifyMerge, true, false},
		{"error frame fails", frameError, false, true},
		{"mid progress keeps waiting", frameProgressMid, false, false},
		{"merging-is-finished is success not failure", frameMergingFinished, true, false},
		{"garbage is ignored", `not json`, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			done, failed, _ := parseDeployProgress([]byte(c.frame))
			if done != c.wantDone || failed != c.wantFail {
				t.Fatalf("parseDeployProgress = (done=%v, failed=%v), want (done=%v, failed=%v)", done, failed, c.wantDone, c.wantFail)
			}
		})
	}
}

func TestDeployMergeHash(t *testing.T) {
	resp := map[string]interface{}{
		"request_proc": "ok",
		"ops": []interface{}{
			map[string]interface{}{"obj": "obj_scheme", "proc": "ok", "hash": "ABC123"},
		},
	}
	if got := deployMergeHash(resp); got != "ABC123" {
		t.Fatalf("deployMergeHash = %q, want ABC123", got)
	}
	if got := deployMergeHash(map[string]interface{}{"ops": []interface{}{}}); got != "" {
		t.Fatalf("deployMergeHash empty ops = %q, want empty", got)
	}
}

func TestFormatDeployDiff(t *testing.T) {
	list := []map[string]interface{}{
		{"title": "proc-a", "obj_type": "conv", "__status": "changed"},
		{"title": "proc-b", "obj_type": "conv", "__status": "added"},
		{"title": "same", "obj_type": "conv", "__status": ""},
		{"title": "weird", "obj_type": "conv", "__status": "conflict", "obj_id": float64(42)},
	}
	summary, conflicts, removed := formatDeployDiff(list, 684083, 684082)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, want exactly the 'conflict' status entry", conflicts)
	}
	if want := `status "conflict": conv #42 "weird"`; conflicts[0] != want {
		t.Fatalf("conflict line = %q, want %q", conflicts[0], want)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	for _, want := range []string{"proc-a", "proc-b", "weird", "changed", "added"} {
		if !contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
}

// TestFormatDeployDiff_DeletedIsRemoval pins the real /api/2/compare
// vocabulary: an object deleted in the source comes back as "deleted" and
// must count as a removal (merge deletes it from the target), not a conflict.
func TestFormatDeployDiff_DeletedIsRemoval(t *testing.T) {
	list := []map[string]interface{}{
		{"title": "gone", "obj_type": "conv", "__status": "deleted"},
		{"title": "legacy", "obj_type": "conv", "__status": "removed"},
	}
	_, conflicts, removed := formatDeployDiff(list, 1, 2)
	if len(conflicts) != 0 {
		t.Fatalf("deleted/removed must not be conflicts, got %v", conflicts)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
