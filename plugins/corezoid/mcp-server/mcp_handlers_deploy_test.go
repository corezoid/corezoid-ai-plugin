package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
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
	compareErrs   interface{} // optional `errors` tree attached to a failed compare op
	mergeErr      bool
	seen          *[]string
	// inSyncFromCompareN: when > 0, the Nth and later compare calls return an
	// all-in-sync list (every __status empty) — models the diff draining once
	// an async merge lands. compareCalls counts calls across the value receiver.
	inSyncFromCompareN int
	compareCalls       *int
	// compareNoList: proc:"ok" compare responses WITHOUT a list key — a
	// malformed success that must never read as "in sync".
	compareNoList bool
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
			errOp := map[string]interface{}{"proc": "error", "description": "cmp boom"}
			if m.compareErrs != nil {
				errOp["errors"] = m.compareErrs
			}
			return wrapOp(errOp)
		}
		if m.compareNoList {
			return wrapOp(map[string]interface{}{"proc": "ok"})
		}
		call := 1
		if m.compareCalls != nil {
			*m.compareCalls++
			call = *m.compareCalls
		}
		if m.inSyncFromCompareN > 0 && call >= m.inSyncFromCompareN {
			inSync := make([]interface{}, len(m.diff))
			for i, d := range m.diff {
				cp := map[string]interface{}{}
				for k, v := range d {
					cp[k] = v
				}
				cp["__status"] = ""
				inSync[i] = cp
			}
			return wrapOp(map[string]interface{}{"proc": "ok", "list": inSync})
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
	return map[string]interface{}{"title": title, "obj_type": "conv", "__status": status, "obj_id": float64(777)}
}

// errFake mirrors the monitor's routine early-close error text (see
// monitorDeployProgress: "deploy progress websocket closed before done").
var errFake = fmt.Errorf("deploy progress websocket closed before done: EOF (test)")

// shortVerifyRetries dials the compare-verification retry loop down to
// milliseconds so the unconfirmed path doesn't slow the suite.
func shortVerifyRetries(t *testing.T) {
	t.Helper()
	origA, origD := deployVerifyAttempts, deployVerifyDelayVar
	deployVerifyAttempts, deployVerifyDelayVar = 2, time.Millisecond
	t.Cleanup(func() { deployVerifyAttempts, deployVerifyDelayVar = origA, origD })
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
	return callDeployWithFn(t, m.fn, args)
}

// callDeployWithFn is callDeploy with a custom mock-API handler, for tests
// that need per-call response shaping beyond what deployMock models.
func callDeployWithFn(t *testing.T, fn func([]map[string]interface{}) interface{}, args map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	srv, _ := mockAPIServer(t, fn)
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

// TestDeployStage_WSFailVerifiedByCompare: small merges routinely finish and
// close the progress WebSocket before the monitor subscribes. The socket is a
// progress channel, not the source of truth — when it fails, the handler must
// re-run compare and report a VERIFIED success once the diff is drained,
// instead of the old scary "completion could not be confirmed" warning.
func TestDeployStage_WSFailVerifiedByCompare(t *testing.T) {
	stubDeployMonitor(t, "", errFake)
	calls := 0
	m := deployMock{immutable: true, diff: []map[string]interface{}{diffItem("a", "changed")},
		inSyncFromCompareN: 2, compareCalls: &calls}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if isErr {
		t.Fatalf("verified-by-compare deploy must not be an error: %s", res)
	}
	if !strings.Contains(res, "✅ Deployed") || !strings.Contains(res, "verified by compare") {
		t.Errorf("expected verified success, got: %s", res)
	}
}

// TestDeployStage_WSFailStillDiverged: WS failed AND the diff never drains —
// the deploy outcome is genuinely unknown/bad, and the tool must say so with
// an error, not a cheerful ✅ followed by a hedge.
func TestDeployStage_WSFailStillDiverged(t *testing.T) {
	stubDeployMonitor(t, "", errFake)
	shortVerifyRetries(t)
	m := deployMock{immutable: true, diff: []map[string]interface{}{diffItem("a", "changed")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if !isErr {
		t.Fatalf("unconfirmed deploy must be an error: %s", res)
	}
	if !strings.Contains(res, "UNCONFIRMED") || !strings.Contains(res, "1 object(s) still differ") {
		t.Errorf("expected unconfirmed report with leftover count, got: %s", res)
	}
	if strings.Contains(res, "✅") {
		t.Errorf("unconfirmed deploy must not claim success: %s", res)
	}
}

// TestDeployStage_WSServerErrorVerifiedStillWarns: when the socket reported a
// genuine server-side failure (not a routine early close) and compare then
// shows the target in sync, the success note must carry the socket's reason —
// not the false "typical for small, fast merges" story.
func TestDeployStage_WSServerErrorVerifiedStillWarns(t *testing.T) {
	stubDeployMonitor(t, "", fmt.Errorf("deploy reported error: node quota exceeded"))
	calls := 0
	m := deployMock{immutable: true, diff: []map[string]interface{}{diffItem("a", "changed")},
		inSyncFromCompareN: 2, compareCalls: &calls}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if isErr {
		t.Fatalf("in-sync outcome must not be an error: %s", res)
	}
	if !strings.Contains(res, "node quota exceeded") {
		t.Errorf("the socket's failure reason must not be swallowed: %s", res)
	}
	if strings.Contains(res, "typical for small, fast merges") {
		t.Errorf("server-reported failure must not be described as a routine early close: %s", res)
	}
}

// TestDeployStage_MalformedCompareNotVerified: a proc:"ok" compare WITHOUT a
// list key during verification must yield UNCONFIRMED, not a false ✅.
func TestDeployStage_MalformedCompareNotVerified(t *testing.T) {
	stubDeployMonitor(t, "", errFake)
	shortVerifyRetries(t)
	calls := 0
	m := deployMock{immutable: true, diff: []map[string]interface{}{diffItem("a", "changed")}}
	// First compare (the diff) is normal; verification compares are malformed
	// proc:"ok" responses without a list key.
	res, isErr := callDeployWithFn(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) > 0 {
			if typ, _ := ops[0]["type"].(string); typ == "compare" {
				calls++
				if calls > 1 {
					return wrapOp(map[string]interface{}{"proc": "ok"})
				}
			}
		}
		return m.fn(ops)
	}, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if !isErr || !strings.Contains(res, "UNCONFIRMED") || !strings.Contains(res, "no object list") {
		t.Fatalf("malformed compare must be UNCONFIRMED, got isErr=%v: %s", isErr, res)
	}
}

// TestDeployStage_DeletedIsNotAConflict: "deleted" is what /api/2/compare
// actually returns for an object deleted in the source (e.g. a process removed
// on develop after an earlier deploy). The UI merge propagates the deletion
// without complaint, so the tool must treat it as a removal — not refuse it as
// an "unexpected/conflicting status" (the bug this test pins down).
func TestDeployStage_DeletedIsNotAConflict(t *testing.T) {
	m := deployMock{immutable: true, diff: []map[string]interface{}{diffItem("gone-from-dev", "deleted")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": false})
	if isErr {
		t.Fatalf("deleted status must not refuse the deploy: %s", res)
	}
	if !strings.Contains(res, "DELETED") {
		t.Errorf("expected DELETED warning for deleted object: %s", res)
	}
	if strings.Contains(res, "won't auto-merge") || strings.Contains(res, "doesn't recognize") {
		t.Errorf("deleted status wrongly reported as conflict: %s", res)
	}
}

// ---- handleDeployStage: defensive conflict refuse -------------------------

func TestDeployStage_ConflictRefused(t *testing.T) {
	var seen []string
	m := deployMock{immutable: true, seen: &seen, diff: []map[string]interface{}{diffItem("x", "conflict")}}
	res, isErr := callDeploy(t, m, map[string]interface{}{"apply": true, "confirm": "200->300"})
	if !isErr || !strings.Contains(res, "doesn't recognize") {
		t.Fatalf("expected conflict refusal, got isErr=%v: %s", isErr, res)
	}
	// The refusal must name each object and its status, otherwise the user
	// cannot diagnose it through the tool.
	if !strings.Contains(res, `status "conflict": conv #777 "x"`) {
		t.Errorf("refusal must identify the object and status: %s", res)
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

// TestDeployStage_CompareErrorTree: a failed compare (e.g. "One or more
// processes has errors") carries a nested errors tree naming the exact
// stage → process → node and the reason. The tool must surface that tree —
// the bare description alone is undiagnosable. The fixture mirrors a real
// /api/2/compare response for a process referencing another project.
func TestDeployStage_CompareErrorTree(t *testing.T) {
	tree := []interface{}{
		map[string]interface{}{
			"obj_id": float64(685226), "obj": "stage", "title": "dev",
			"destinations": []interface{}{
				map[string]interface{}{
					"obj_id": float64(1882660), "obj": "conv", "title": "repro-external-ref",
					"destinations": []interface{}{
						map[string]interface{}{
							"obj_id": "6a5114a6b677ac7770531702", "obj": "node", "title": "Copy to external",
							"errors": []interface{}{"Project of object do not matches projectId of request"},
						},
					},
				},
			},
		},
	}
	m := deployMock{immutable: true, compareErr: true, compareErrs: tree}
	res, isErr := callDeploy(t, m, nil)
	if !isErr {
		t.Fatalf("expected error, got: %s", res)
	}
	for _, want := range []string{
		`stage #685226 "dev"`,
		`conv #1882660 "repro-external-ref"`,
		`node #6a5114a6b677ac7770531702 "Copy to external"`,
		"• Project of object do not matches projectId of request",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("compare error must contain %q, got:\n%s", want, res)
		}
	}
}

func TestFormatCompareErrors_Empty(t *testing.T) {
	if got := formatCompareErrors(nil, 1); got != "" {
		t.Errorf("nil errors must render empty, got %q", got)
	}
	if got := formatCompareErrors([]interface{}{}, 1); got != "" {
		t.Errorf("empty errors must render empty, got %q", got)
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
