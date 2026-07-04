package main

import (
	"context"
	"strings"
	"testing"
)

// deployMock builds a mockAPIServer handler that answers the ops handleDeployStage
// and handleSetStageImmutable issue (show-stage, compare, merge, modify-stage)
// from a small config, and records the op types seen.
type deployMock struct {
	immutable     bool                     // target/queried stage immutable?
	title         string                   // stage title
	diff          []map[string]interface{} // compare "list"
	srcUndeployed int                      // undeployed count reported for the source stage (id 200)
	showErr       bool
	compareErr    bool
	mergeErr      bool
	seen          *[]string
}

// wrapOp wraps a single op result in the {request_proc, ops:[...]} envelope,
// PRESERVING custom fields (the shared okResponse() strips everything but proc).
func wrapOp(op map[string]interface{}) interface{} {
	return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{op}}
}

func (m deployMock) fn(ops []map[string]interface{}) interface{} {
	if len(ops) == 0 {
		return wrapOp(map[string]interface{}{"proc": "ok"})
	}
	op := ops[0]
	typ, _ := op["type"].(string)
	obj, _ := op["obj"].(string)
	if m.seen != nil {
		*m.seen = append(*m.seen, typ)
	}
	switch {
	case typ == "show" && obj == "stage":
		if m.showErr {
			return wrapOp(map[string]interface{}{"proc": "error", "description": "show boom"})
		}
		// Source stage is id 200 (see callDeploy); report its undeployed count
		// so the "source not deployed" gate can be exercised. Target is 300.
		if id, _ := op["obj_id"].(float64); int(id) == 200 {
			return wrapOp(map[string]interface{}{"proc": "ok", "immutable": false, "undeployed": float64(m.srcUndeployed), "title": "dev", "short_name": "dev"})
		}
		return wrapOp(map[string]interface{}{"proc": "ok", "immutable": m.immutable, "undeployed": float64(0), "title": m.title, "short_name": "prod"})
	case typ == "compare":
		if m.compareErr {
			return wrapOp(map[string]interface{}{"proc": "error", "description": "cmp boom"})
		}
		list := make([]interface{}, len(m.diff))
		for i, d := range m.diff {
			list[i] = d
		}
		return wrapOp(map[string]interface{}{"proc": "ok", "list": list})
	case typ == "merge":
		if m.mergeErr {
			return wrapOp(map[string]interface{}{"proc": "error", "description": "merge boom"})
		}
		return wrapOp(map[string]interface{}{"proc": "ok", "hash": "HASH123"})
	case typ == "modify" && obj == "stage":
		return wrapOp(map[string]interface{}{"proc": "ok"})
	}
	return wrapOp(map[string]interface{}{"proc": "ok"})
}

func diffItem(title, status string) map[string]interface{} {
	return map[string]interface{}{"title": title, "obj_type": "conv", "__status": status}
}

// stubDeployMonitor replaces the WS wait with a canned log for the duration of a test.
func stubDeployMonitor(t *testing.T, log string, err error) {
	t.Helper()
	orig := deployMonitor
	deployMonitor = func(v *Executor, hash string) (string, error) { return log, err }
	t.Cleanup(func() { deployMonitor = orig })
}

func callDeploy(t *testing.T, m deployMock, args map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	base := map[string]interface{}{
		"project_id":      float64(100),
		"source_stage_id": float64(200),
		"target_stage_id": float64(300),
		"company_id":      "i1",
	}
	for k, v := range args {
		base[k] = v
	}
	return handleToolCall(context.Background(), "deploy-stage", base)
}

// ---- handleDeployStage: arg validation ------------------------------------

func TestDeployStage_MissingArgs(t *testing.T) {
	for _, missing := range []string{"project_id", "source_stage_id", "target_stage_id", "company_id"} {
		t.Run("missing "+missing, func(t *testing.T) {
			resetGlobals(t)
			srv, _ := mockAPIServer(t, deployMock{immutable: true}.fn)
			setProjectAuth(t, srv.URL)
			args := map[string]interface{}{
				"project_id": float64(100), "source_stage_id": float64(200),
				"target_stage_id": float64(300), "company_id": "i1",
			}
			delete(args, missing)
			res, isErr := handleToolCall(context.Background(), "deploy-stage", args)
			if !isErr {
				t.Fatalf("expected error for missing %s, got: %s", missing, res)
			}
		})
	}
}

func TestDeployStage_SameStage(t *testing.T) {
	res, isErr := callDeploy(t, deployMock{immutable: true}, map[string]interface{}{
		"source_stage_id": float64(300), "target_stage_id": float64(300),
	})
	if !isErr || !strings.Contains(res, "must differ") {
		t.Fatalf("expected same-stage error, got isErr=%v: %s", isErr, res)
	}
}

// ---- handleDeployStage: immutable target gate -----------------------------

func TestDeployStage_TargetNotImmutable(t *testing.T) {
	res, isErr := callDeploy(t, deployMock{immutable: false, title: "prod"}, nil)
	if !isErr || !strings.Contains(res, "NOT immutable") {
		t.Fatalf("expected not-immutable refusal, got isErr=%v: %s", isErr, res)
	}
}

func TestDeployStage_ShowStageError(t *testing.T) {
	res, isErr := callDeploy(t, deployMock{showErr: true}, nil)
	if !isErr || !strings.Contains(res, "checking target stage") {
		t.Fatalf("expected show-stage error, got isErr=%v: %s", isErr, res)
	}
}

func TestDeployStage_SourceNotDeployed(t *testing.T) {
	var seen []string
	m := deployMock{immutable: true, srcUndeployed: 2, seen: &seen}
	res, isErr := callDeploy(t, m, nil)
	if !isErr || !strings.Contains(res, "undeployed") {
		t.Fatalf("expected source-not-deployed refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if s == "compare" || s == "merge" {
			t.Fatalf("compare/merge must NOT run when source has undeployed changes; seen=%v", seen)
		}
	}
}

// ---- handleDeployStage: diff / dry-run ------------------------------------

func TestDeployStage_NothingToDeploy(t *testing.T) {
	res, isErr := callDeploy(t, deployMock{immutable: true, diff: nil}, nil)
	if isErr || !strings.Contains(res, "already in sync") {
		t.Fatalf("expected in-sync, got isErr=%v: %s", isErr, res)
	}
}

func TestDeployStage_DryRun(t *testing.T) {
	var seen []string
	m := deployMock{immutable: true, seen: &seen, diff: []map[string]interface{}{diffItem("proc-a", "changed")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": false})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if !strings.Contains(res, "DRY-RUN") || !strings.Contains(res, "OVERWRITES") {
		t.Errorf("dry-run output wrong: %s", res)
	}
	for _, s := range seen {
		if s == "merge" {
			t.Fatalf("merge must NOT run in dry-run; seen=%v", seen)
		}
	}
}

func TestDeployStage_DryRunFlagsRemoved(t *testing.T) {
	m := deployMock{immutable: true, diff: []map[string]interface{}{diffItem("gone", "removed")}}
	res, _ := callDeploy(t, m, map[string]interface{}{"apply": false})
	if !strings.Contains(res, "DELETED") {
		t.Errorf("expected DELETED warning for removed object: %s", res)
	}
}

// ---- handleDeployStage: defensive conflict refuse -------------------------

func TestDeployStage_ConflictRefused(t *testing.T) {
	var seen []string
	m := deployMock{immutable: true, seen: &seen, diff: []map[string]interface{}{diffItem("x", "conflict")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if !isErr || !strings.Contains(res, "conflicting status") {
		t.Fatalf("expected conflict refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if s == "merge" {
			t.Fatalf("merge must NOT run on conflict; seen=%v", seen)
		}
	}
}

// ---- handleDeployStage: apply / confirm -----------------------------------

func TestDeployStage_ApplyWrongConfirm(t *testing.T) {
	var seen []string
	m := deployMock{immutable: true, seen: &seen, diff: []map[string]interface{}{diffItem("a", "changed")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "wrong"})
	if !isErr || !strings.Contains(res, "confirmation required") {
		t.Fatalf("expected confirm refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if s == "merge" {
			t.Fatalf("merge must NOT run with wrong confirm; seen=%v", seen)
		}
	}
}

func TestDeployStage_ApplyOK(t *testing.T) {
	stubDeployMonitor(t, "upload: complete (100%)\nmerge: complete (100%)", nil)
	var seen []string
	m := deployMock{immutable: true, seen: &seen, diff: []map[string]interface{}{diffItem("a", "changed")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if !strings.Contains(res, "Deployed") || !strings.Contains(res, "Progress:") || !strings.Contains(res, "merge: complete") {
		t.Errorf("apply output missing deploy/progress: %s", res)
	}
	merged := false
	for _, s := range seen {
		if s == "merge" {
			merged = true
		}
	}
	if !merged {
		t.Fatalf("merge should have run; seen=%v", seen)
	}
}

func TestDeployStage_CompareError(t *testing.T) {
	res, isErr := callDeploy(t, deployMock{immutable: true, compareErr: true}, nil)
	if !isErr || !strings.Contains(res, "compare") {
		t.Fatalf("expected compare error, got isErr=%v: %s", isErr, res)
	}
}

func TestDeployStage_MergeError(t *testing.T) {
	m := deployMock{immutable: true, mergeErr: true, diff: []map[string]interface{}{diffItem("a", "changed")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if !isErr || !strings.Contains(res, "merge") {
		t.Fatalf("expected merge error, got isErr=%v: %s", isErr, res)
	}
}

// ---- handleSetStageImmutable ----------------------------------------------

func callImmutable(t *testing.T, m deployMock, args map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	base := map[string]interface{}{
		"stage_id": float64(300), "project_id": float64(100), "company_id": "i1",
	}
	for k, v := range args {
		base[k] = v
	}
	return handleToolCall(context.Background(), "set-stage-immutable", base)
}

func TestSetImmutable_MissingImmutable(t *testing.T) {
	res, isErr := callImmutable(t, deployMock{}, nil) // no "immutable" key
	if !isErr || !strings.Contains(res, "immutable") {
		t.Fatalf("expected missing-immutable error, got isErr=%v: %s", isErr, res)
	}
}

func TestSetImmutable_AlreadyInState(t *testing.T) {
	// current immutable=true, requesting true → nothing to change
	res, isErr := callImmutable(t, deployMock{immutable: true, title: "prod"}, map[string]interface{}{"immutable": true})
	if isErr || !strings.Contains(res, "nothing to change") {
		t.Fatalf("expected no-op, got isErr=%v: %s", isErr, res)
	}
}

func TestSetImmutable_WrongConfirm_ToImmutable(t *testing.T) {
	// current mutable, requesting immutable=true, no confirm → refuse (read-only note)
	res, isErr := callImmutable(t, deployMock{immutable: false, title: "prod"}, map[string]interface{}{"immutable": true})
	if !isErr || !strings.Contains(res, "confirmation required") {
		t.Fatalf("expected confirm refusal, got isErr=%v: %s", isErr, res)
	}
}

func TestSetImmutable_WrongConfirm_ToMutable_ShowsRisks(t *testing.T) {
	// current immutable, requesting immutable=false, no confirm → refuse WITH risks
	res, isErr := callImmutable(t, deployMock{immutable: true, title: "prod"}, map[string]interface{}{"immutable": false})
	if !isErr {
		t.Fatalf("expected refusal, got: %s", res)
	}
	for _, want := range []string{"RISKS", "UNAVAILABLE", "ANYONE"} {
		if !strings.Contains(res, want) {
			t.Errorf("risk text missing %q: %s", want, res)
		}
	}
}

func TestSetImmutable_OK(t *testing.T) {
	// current mutable, requesting immutable=true with correct confirm → success
	res, isErr := callImmutable(t, deployMock{immutable: false, title: "prod"}, map[string]interface{}{
		"immutable": true, "confirm": "300:true",
	})
	if isErr || !strings.Contains(res, "now immutable") {
		t.Fatalf("expected success, got isErr=%v: %s", isErr, res)
	}
}
