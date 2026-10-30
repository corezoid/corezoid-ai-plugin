package main

import (
	"context"
	"strings"
	"testing"
)

// pushWithFailingSnapshot pushes a deployed process while CreateSnapshot fails
// with snapDesc and `show conv` reports the process in convStage. It returns
// the push result and whether nodes were written.
func pushWithFailingSnapshot(t *testing.T, snapDesc string, convStage int, allowNoSnapshot bool) (string, bool, bool) {
	t.Helper()
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)

	pushed := false
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		switch {
		case typ == "list" && obj == "snapshots":
			return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
		case typ == "create" && obj == "snapshot":
			return wrapOp(map[string]interface{}{"proc": "error", "description": snapDesc})
		case typ == "list" && obj == "conv":
			return wrapOp(deployedProcessOp())
		case typ == "show" && obj == "conv":
			return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": 123, "status": "active", "stage_id": convStage, "project_id": 11})
		case typ == "show" && obj == "stage":
			return wrapOp(map[string]interface{}{"proc": "ok", "immutable": false, "title": "develop", "short_name": "dev"})
		case typ == "create" && obj == "node":
			pushed = true
			results := make([]interface{}, len(ops))
			for i, nodeOp := range ops {
				localID, _ := nodeOp["id"].(string)
				results[i] = map[string]interface{}{"proc": "ok", "id": localID, "obj_id": localID}
			}
			return map[string]interface{}{"request_proc": "ok", "ops": results}
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":      name,
		"adopt_existing":    true,
		"allow_no_snapshot": allowNoSnapshot,
	})
	return result, isErr, pushed
}

// A process that lives in another stage than the session used to get "retry
// if transient, otherwise allow_no_snapshot=true". The block must name both
// stages and login as the fix, and must not offer the waiver.
func TestHandlePushProcess_SnapshotStageMismatchNamesStageAndLogin(t *testing.T) {
	result, isErr, _ := pushWithFailingSnapshot(t, "Access denied", 30, false)
	if !isErr {
		t.Fatalf("expected the push to be blocked, got:\n%s", result)
	}
	for _, want := range []string{
		"lives in stage 30 (project 11)",
		"session is on stage 20",
		"login with stage_id=30",
		"not a transient API failure",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "re-run with allow_no_snapshot=true") || strings.Contains(result, "retry the push;") {
		t.Errorf("a stage mismatch must not suggest the waiver or a plain retry:\n%s", result)
	}
}

// The waiver is judged against the session's stage, which is the wrong one
// here: allow_no_snapshot must not push the process blind.
func TestHandlePushProcess_SnapshotStageMismatchNotWaivable(t *testing.T) {
	result, isErr, pushed := pushWithFailingSnapshot(t, "Access denied", 30, true)
	if !isErr || pushed {
		t.Fatalf("allow_no_snapshot must not waive a stage mismatch (pushed=%v):\n%s", pushed, result)
	}
	if !strings.Contains(result, "login with stage_id=30") {
		t.Errorf("result must name login as the fix:\n%s", result)
	}
}

func TestHandlePushProcess_SnapshotCompanyMismatchNotBlamedOnAPI(t *testing.T) {
	result, isErr, _ := pushWithFailingSnapshot(t, "Company mismatch: conv 123 belongs to company i999000111", 20, false)
	if !isErr {
		t.Fatalf("expected the push to be blocked, got:\n%s", result)
	}
	for _, want := range []string{"different workspace", `workspace_id="i999000111"`, "not a transient API failure"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "re-run with allow_no_snapshot=true") {
		t.Errorf("a workspace mismatch must not suggest the waiver:\n%s", result)
	}
}

func TestHandlePushProcess_SnapshotNotInStageNamesLogin(t *testing.T) {
	result, isErr, _ := pushWithFailingSnapshot(t, "Object is not in stage", 0, false)
	if !isErr {
		t.Fatalf("expected the push to be blocked, got:\n%s", result)
	}
	for _, want := range []string{"not in the session's stage 20", "list-stages", "call login"} {
		if !strings.Contains(strings.ToLower(result), strings.ToLower(want)) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
}

// When the process is in the session's stage, the failure is not a location
// problem and keeps the existing retry/waiver advice.
func TestHandlePushProcess_SnapshotFailureSameStageKeepsWaiverAdvice(t *testing.T) {
	result, isErr, _ := pushWithFailingSnapshot(t, "Internal server error", 20, false)
	if !isErr || !strings.Contains(result, "allow_no_snapshot=true") || strings.Contains(result, "lives in stage") {
		t.Fatalf("same-stage failure should keep the waiver advice:\n%s", result)
	}
}
