package main

import (
	"testing"
	"time"
)

// ---- classifySnapshotRejection ---------------------------------------------
//
// The classifier is the whole safety property of this feature: read too
// broadly and a per-process, per-stage or per-project complaint silently
// disables the pre-push rollback point for everything that follows; read too
// narrowly and pushes stay blocked forever on environments that simply have no
// snapshot API.

// These messages talk about one stage, one object id or one project type. None
// of them says anything about whether the API knows the snapshot object, so
// none may be read as "this environment has no snapshots".
func TestClassifySnapshotRejection_RequestSpecificNeverDisablesSnapshots(t *testing.T) {
	cases := []map[string]any{
		{"proc": "error", "description": "Unsupported stage"},
		{"proc": "error", "description": "Invalid obj_id"},
		{"proc": "error", "description": "Invalid obj_type"},
		{"proc": "error", "description": "Unsupported project type"},
		{"proc": "error", "description": "Object type is not supported"},
		{"proc": "unsupported_stage"},
		{"proc": "error", "description": "Object conv with id 999999999 does not exist"},
		{"proc": "error", "description": "Snapshot 5 not found"},
		{"proc": "error", "description": "Snapshot with id 5 does not exist"},
		{"proc": "error", "description": "Snapshot limit exceeded"},
		{"proc": "error", "description": "stage is immutable"},
		{"proc": "error", "description": "Version 3 does not exist"},
		{"proc": "error", "description": "user has no privs"},
		{"proc": "error", "description": "company_id is not valid"},
		{"proc": "error", "description": "Node not found"},
		{"proc": "error", "description": "invalid json"},
		{"proc": "error", "description": "timeout"},
		{"proc": "error", "description": "Access denied"},
		{"proc": "error", "description": "conv_id not found"},
		{"proc": "error", "description": "Value is not valid"},
		{"proc": "error", "description": "Internal server error"},
		{"proc": "error"},
		{},
	}
	for _, op := range cases {
		if got := classifySnapshotRejection(op); got != rejectionOrdinary {
			t.Errorf("op %v: classified %v, want rejectionOrdinary — an ordinary failure must keep snapshots enabled", op, got)
		}
	}
}

// Only a refusal that names the snapshot object itself is conclusive on its own.
func TestClassifySnapshotRejection_SnapshotNamedIsConclusive(t *testing.T) {
	cases := []map[string]any{
		{"proc": "error", "description": "Unknown object: snapshots"},
		{"proc": "error", "description": "Snapshots are not supported"},
		{"proc": "error", "description": "Unknown obj snapshots"},
		{"proc": "error", "description": "Snapshots are disabled on this installation"},
		{"proc": "error", "description": "snapshot object is not available here"},
		// Named-and-scoped: the snapshot rule wins over the request-specific
		// veto, so a build that mentions the conv while refusing the object is
		// still recognised.
		{"proc": "error", "description": "unknown obj snapshots for conv 5"},
	}
	for _, op := range cases {
		if got := classifySnapshotRejection(op); got != rejectionUnknownObjSnapshot {
			t.Errorf("op %v: classified %v, want rejectionUnknownObjSnapshot", op, got)
		}
	}
}

// The dangerous middle ground: the message names snapshots AND says something
// that sounds like absence, but scopes it to one stage, project, user or to
// access rights. Those are policy and permission statements from a build that
// does implement snapshots. Classifying them as feature-absence caches
// snapshotSupportNo for the whole project+stage and silently drops the pre-push
// rollback point for every push that follows, so each one must stay ordinary
// and leave the real CreateSnapshot call to decide.
func TestClassifySnapshotRejection_ScopedSnapshotComplaintNeverDisablesSnapshots(t *testing.T) {
	for _, description := range []string{
		"Snapshots are disabled for this stage",
		"Snapshot access is disabled",
		"Snapshots are not available for this user",
		"Snapshots are unsupported for this project type",
		"Snapshots are not supported for this project",
		"Snapshot creation is disabled for your user",
		"Snapshots not available: permission denied",
		"Snapshots are disabled for company 42",
		"Snapshots unsupported for conv_id 5",
	} {
		t.Run(description, func(t *testing.T) {
			op := map[string]any{"proc": "error", "description": description}
			if got := classifySnapshotRejection(op); got != rejectionOrdinary {
				t.Errorf("classified %v, want rejectionOrdinary — a scoped complaint must keep snapshots enabled", got)
			}
		})
	}
}

// The counterpart: worded installation-wide, with no target named, the same
// markers ARE the answer. Absent this, pushes on a snapshot-less build would be
// blocked forever by a CreateSnapshot that can never succeed.
func TestClassifySnapshotRejection_InstallationWideAbsenceStaysConclusive(t *testing.T) {
	for _, description := range []string{
		"Snapshots are disabled on this installation",
		"snapshot object is not available here",
		"Snapshots are not supported",
		"Snapshots are not supported by this build",
	} {
		t.Run(description, func(t *testing.T) {
			op := map[string]any{"proc": "error", "description": description}
			if got := classifySnapshotRejection(op); got != rejectionUnknownObjSnapshot {
				t.Errorf("classified %v, want rejectionUnknownObjSnapshot", got)
			}
		})
	}
}

func TestClassifySnapshotRejection_TemporarySnapshotFailureStaysOrdinary(t *testing.T) {
	for _, description := range []string{
		"snapshot service temporarily not available",
		"snapshots disabled during maintenance",
	} {
		t.Run(description, func(t *testing.T) {
			op := map[string]any{"proc": "error", "description": description}
			if got := classifySnapshotRejection(op); got != rejectionOrdinary {
				t.Errorf("classified %v, want rejectionOrdinary", got)
			}
		})
	}
}

// An "unknown obj" shaped refusal that never mentions snapshots is suggestive
// only — the control ops decide (see the ProbeSnapshotSupport tests). This is
// what dev.corezoid.com actually answers: a bare "bad object".
func TestClassifySnapshotRejection_GenericObjRefusalNeedsConfirmation(t *testing.T) {
	cases := []map[string]any{
		{"proc": "error", "description": "bad object"},
		{"proc": "unknown_obj"},
		{"proc": "bad_object"},
		{"proc": "not_implemented"},
		{"proc": "error", "description": "no such obj"},
		{"proc": "error", "description": "Method not implemented for this obj"},
	}
	for _, op := range cases {
		if got := classifySnapshotRejection(op); got != rejectionUnknownObjGeneric {
			t.Errorf("op %v: classified %v, want rejectionUnknownObjGeneric", op, got)
		}
	}
}

// Structured proc codes are matched exactly, so a code naming something else
// cannot slip through on a substring.
func TestNormalizeProcCode_MatchesExactlyNotBySubstring(t *testing.T) {
	if !snapshotUnknownObjProcCodes[normalizeProcCode("Unknown-Obj")] {
		t.Error("unknown_obj must be recognised regardless of case and separator")
	}
	for _, code := range []string{"unknown_obj_for_stage", "unsupported_stage", "obj_id_invalid"} {
		if snapshotUnknownObjProcCodes[normalizeProcCode(code)] {
			t.Errorf("proc code %q must not count as an unknown-object code", code)
		}
	}
}

// ---- ProbeSnapshotSupport: the control ops ---------------------------------

// probeMock answers the read-only ops a probe can issue, so each test only has
// to say what the environment does with them.
type probeMock struct {
	t             *testing.T
	snapshotsResp map[string]interface{} // answer to `list snapshots`
	unknownResp   map[string]interface{} // answer to the nonsense-obj control
	controlErr    bool                   // the positive control fails
	calls         map[string]int
}

func newProbeMock(t *testing.T) *probeMock {
	return &probeMock{t: t, calls: map[string]int{}}
}

func (m *probeMock) fn(ops []map[string]interface{}) interface{} {
	if len(ops) == 0 {
		return wrapOp(map[string]interface{}{"proc": "ok"})
	}
	op := ops[0]
	if op["type"] == "create" {
		m.t.Errorf("a support probe must never issue a mutating op, got %#v", op)
	}
	obj, _ := op["obj"].(string)
	m.calls[obj]++
	switch obj {
	case "snapshots":
		return wrapOp(m.snapshotsResp)
	case snapshotProbeUnknownObj:
		return wrapOp(m.unknownResp)
	case "commits":
		// Conv-scoped positive control: this process is reachable and this
		// build does answer its other per-process version object.
		if m.controlErr {
			return wrapOp(map[string]interface{}{"proc": "error", "description": "bad object"})
		}
		return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{
			map[string]interface{}{"conv_id": float64(555), "version": float64(1787315841)},
		}})
	case "folder":
		// Stage-scoped fallback, used when the probe has no conv.
		if m.controlErr {
			return wrapOp(map[string]interface{}{"proc": "error", "description": "bad object"})
		}
		return wrapOp(map[string]interface{}{
			"proc": "ok", "obj_id": float64(20), "obj_type": float64(10), "parent_obj_id": float64(10),
		})
	}
	m.t.Errorf("unexpected probe op %#v", op)
	return wrapOp(map[string]interface{}{"proc": "error", "description": "unexpected"})
}

// The real known-unsupported environment (dev.corezoid.com) answers `list
// snapshots` with a bare "bad object" and answers a nonsense obj name exactly
// the same way, while ordinary ops still work. That combination — and only that
// combination — is what disables snapshots.
func TestSnapshotsSupported_ObjNameRefusalConfirmedByControlOps(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	m.unknownResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	_, e := mockAPIServer(t, m.fn)

	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("an API that refuses the snapshot obj name exactly as it refuses a nonsense name has no snapshot feature")
	}
	if m.calls["commits"] != 1 || m.calls[snapshotProbeUnknownObj] != 1 {
		t.Errorf("both control ops must run before disabling snapshots, got %#v", m.calls)
	}
	// Second call comes from cache: the probe costs one round of ops per
	// target, not one per push.
	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("cached answer changed between calls")
	}
	if m.calls["snapshots"] != 1 {
		t.Errorf("probe should run once per target, ran %d times", m.calls["snapshots"])
	}
}

// The dangerous case: a per-process failure that happens to be obj-shaped. The
// negative control answers differently, which proves the API does know the
// snapshot object — snapshots stay on and nothing is cached.
func TestSnapshotsSupported_ObjRefusalNotMatchingControlStaysEnabled(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	m.unknownResp = map[string]interface{}{"proc": "error", "description": "unknown obj"}
	_, e := mockAPIServer(t, m.fn)

	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("a refusal the controls could not confirm must keep snapshots enabled")
	}
	if cachedSnapshotSupport(e, 10, 20) != snapshotSupportUnknown {
		t.Error("an unconfirmed refusal must not be cached")
	}
	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("second call flipped the answer")
	}
	if m.calls["snapshots"] != 2 {
		t.Errorf("an unconfirmed refusal must be re-probed; got %d probes, want 2", m.calls["snapshots"])
	}
}

// An environment that answers everything with the same generic text says
// nothing about obj names. The positive control catches it: `show folder` must
// succeed before any refusal is believed.
func TestSnapshotsSupported_EnvironmentFailingEverythingStaysEnabled(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	m.unknownResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	m.controlErr = true
	_, e := mockAPIServer(t, m.fn)

	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("an API that also refuses ordinary ops proves nothing about snapshots")
	}
	if cachedSnapshotSupport(e, 10, 20) != snapshotSupportUnknown {
		t.Error("an unconfirmed refusal must not be cached")
	}
}

// With neither a conv nor a stage there is no positive control to run, so no
// refusal can be confirmed.
func TestSnapshotsSupported_NoTargetCannotConfirm(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	_, e := mockAPIServer(t, m.fn)

	if !snapshotsSupported(e, 0, 0, 0) {
		t.Fatal("with nothing to control against, snapshots must stay enabled")
	}
	if m.calls[snapshotProbeUnknownObj] != 0 {
		t.Error("the negative control is pointless without a positive one")
	}
}

// Without a conv the control falls back to the stage, so a stage-only probe
// (the snapshot tools before a process is known) still works.
func TestSnapshotsSupported_StageFallbackControl(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	m.unknownResp = map[string]interface{}{"proc": "error", "description": "bad object"}
	_, e := mockAPIServer(t, m.fn)

	if snapshotsSupported(e, 0, 10, 20) {
		t.Fatal("with a stage to control against, a confirmed refusal must disable snapshots")
	}
	if m.calls["folder"] != 1 || m.calls["commits"] != 0 {
		t.Errorf("without a conv the control must be the stage show, got %#v", m.calls)
	}
}

// A refusal naming the snapshot object needs no controls at all.
func TestSnapshotsSupported_SnapshotNamedRefusalNeedsNoControls(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "Snapshots are not supported"}
	_, e := mockAPIServer(t, m.fn)

	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("an API naming the snapshot object as unsupported has no snapshot feature")
	}
	if m.calls["commits"] != 0 || m.calls[snapshotProbeUnknownObj] != 0 {
		t.Errorf("a conclusive refusal must not cost extra ops, got %#v", m.calls)
	}
}

// A request-specific refusal is handled without any control ops: it is simply
// not evidence about the feature.
func TestSnapshotsSupported_RequestSpecificRefusalCostsOneOp(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "Unsupported stage"}
	_, e := mockAPIServer(t, m.fn)

	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("a stage-specific refusal must not disable snapshots")
	}
	if m.calls["commits"] != 0 || m.calls[snapshotProbeUnknownObj] != 0 {
		t.Errorf("an ordinary refusal needs no control ops, got %#v", m.calls)
	}
	if cachedSnapshotSupport(e, 10, 20) != snapshotSupportUnknown {
		t.Error("an ordinary refusal must not be cached")
	}
}

func TestSnapshotsSupported_SupportedEnvironmentProbesOnce(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "ok", "list": []interface{}{}}
	_, e := mockAPIServer(t, m.fn)

	if !snapshotsSupported(e, 555, 10, 20) || !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("an API that answers the list op supports snapshots")
	}
	if m.calls["snapshots"] != 1 {
		t.Errorf("probe should run once per target, ran %d times", m.calls["snapshots"])
	}
}

// A blip (auth, network, server fault) is not evidence that the feature is
// missing. It must keep snapshots enabled — so the existing "snapshot failed →
// block the push" protection still applies — and must not be cached, or one bad
// minute would disable rollback points for the rest of the session.
func TestSnapshotsSupported_TransientFailureStaysEnabledAndUncached(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "Internal server error"}
	_, e := mockAPIServer(t, m.fn)

	if !snapshotsSupported(e, 555, 10, 20) || !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("a server fault must not be read as 'snapshots unsupported'")
	}
	if m.calls["snapshots"] != 2 {
		t.Errorf("an inconclusive probe must not be cached; got %d probes, want 2", m.calls["snapshots"])
	}
}

func TestSnapshotsSupported_TemporarySnapshotFailureStaysEnabledAndUncached(t *testing.T) {
	for _, description := range []string{
		"snapshot service temporarily not available",
		"snapshots disabled during maintenance",
	} {
		t.Run(description, func(t *testing.T) {
			resetSnapshotSupportCache()
			t.Cleanup(resetSnapshotSupportCache)

			m := newProbeMock(t)
			m.snapshotsResp = map[string]interface{}{"proc": "error", "description": description}
			_, e := mockAPIServer(t, m.fn)

			if !snapshotsSupported(e, 555, 10, 20) || !snapshotsSupported(e, 555, 10, 20) {
				t.Fatal("a temporary snapshot failure must keep snapshots enabled")
			}
			if m.calls["snapshots"] != 2 {
				t.Errorf("a temporary failure must not be cached; got %d probes, want 2", m.calls["snapshots"])
			}
		})
	}
}

// A dead endpoint answers on the shared /api/2/json path, so it says nothing
// about the snapshot object specifically — it must not disable snapshots.
func TestSnapshotsSupported_UnreachableAPIStaysEnabled(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	e := &Executor{APIUrl: "http://127.0.0.1:1", Token: "t", NodeIDMap: map[string]NodeInfo{}}
	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("an unreachable API must not be read as 'snapshots unsupported'")
	}
	if cachedSnapshotSupport(e, 10, 20) != snapshotSupportUnknown {
		t.Error("an unreachable API must leave the support answer unknown")
	}
}

func TestSnapshotsSupported_NilExecutor(t *testing.T) {
	if snapshotsSupported(nil, 1, 2, 3) {
		t.Error("a nil executor cannot support snapshots")
	}
}

// ---- cache scope and lifetime ---------------------------------------------

// A negative answer is the one that turns a safety net off, so it expires and
// gets re-verified instead of lasting until the process restarts.
func TestSnapshotSupport_NegativeAnswerExpires(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)
	now := time.Now()
	origNow, origTTL := snapshotSupportNow, snapshotSupportNegativeTTL
	snapshotSupportNow = func() time.Time { return now }
	snapshotSupportNegativeTTL = time.Minute
	t.Cleanup(func() { snapshotSupportNow, snapshotSupportNegativeTTL = origNow, origTTL })

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "error", "description": "Snapshots are not supported"}
	_, e := mockAPIServer(t, m.fn)

	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("expected unsupported")
	}
	now = now.Add(30 * time.Second)
	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("within the TTL the cached answer must still hold")
	}
	if m.calls["snapshots"] != 1 {
		t.Fatalf("expected the answer to be cached inside the TTL, got %d probes", m.calls["snapshots"])
	}
	now = now.Add(2 * time.Minute)
	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("re-probe returns the same answer on this environment")
	}
	if m.calls["snapshots"] != 2 {
		t.Errorf("an expired negative answer must be re-verified; got %d probes, want 2", m.calls["snapshots"])
	}
}

// A positive answer costs nothing to keep: being wrong there only means the real
// snapshot call reports the error itself.
func TestSnapshotSupport_PositiveAnswerDoesNotExpire(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)
	now := time.Now()
	origNow := snapshotSupportNow
	snapshotSupportNow = func() time.Time { return now }
	t.Cleanup(func() { snapshotSupportNow = origNow })

	m := newProbeMock(t)
	m.snapshotsResp = map[string]interface{}{"proc": "ok", "list": []interface{}{}}
	_, e := mockAPIServer(t, m.fn)

	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("expected supported")
	}
	now = now.Add(24 * time.Hour)
	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("a positive answer must survive")
	}
	if m.calls["snapshots"] != 1 {
		t.Errorf("a positive answer must not be re-probed, got %d probes", m.calls["snapshots"])
	}
}

// The cache key is deliberately narrow: an answer never speaks for another
// environment, workspace, project or stage.
func TestSnapshotSupportKey_SeparatesTargets(t *testing.T) {
	a := &Executor{APIUrl: "https://one.corezoid.com/", WorkspaceID: "1"}
	b := &Executor{APIUrl: "https://one.corezoid.com", WorkspaceID: "2"}
	c := &Executor{APIUrl: "https://two.corezoid.com", WorkspaceID: "1"}
	if snapshotSupportKey(a, 10, 20) == snapshotSupportKey(b, 10, 20) {
		t.Error("different workspaces must not share a cached answer")
	}
	if snapshotSupportKey(a, 10, 20) == snapshotSupportKey(c, 10, 20) {
		t.Error("different environments must not share a cached answer")
	}
	if snapshotSupportKey(a, 10, 20) == snapshotSupportKey(a, 10, 21) {
		t.Error("different stages must not share a cached answer")
	}
	if snapshotSupportKey(a, 10, 20) == snapshotSupportKey(a, 11, 20) {
		t.Error("different projects must not share a cached answer")
	}
	// A trailing slash is cosmetic — it must not split the cache entry.
	d := &Executor{APIUrl: "https://one.corezoid.com", WorkspaceID: "1"}
	if snapshotSupportKey(a, 10, 20) != snapshotSupportKey(d, 10, 20) {
		t.Error("trailing slash must not create a second cache entry")
	}
}

// One process's failure must not switch snapshots off for the next process:
// with an ordinary refusal nothing is cached, so the second process probes for
// itself and its own working snapshot API is used.
func TestSnapshotsSupported_OneProcessFailureDoesNotDisableTheStage(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	first := true
	var createSeen bool
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		if op["type"] == "create" {
			createSeen = true
			return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(9), "version": float64(3)})
		}
		if op["obj"] == "snapshots" && first {
			first = false
			// A complaint about this one process, obj-shaped but scoped.
			return wrapOp(map[string]interface{}{"proc": "error", "description": "Invalid obj_id"})
		}
		return wrapOp(map[string]interface{}{"proc": "ok", "list": []interface{}{}})
	})

	if !snapshotsSupported(e, 111, 10, 20) {
		t.Fatal("a process-specific refusal must not disable snapshots")
	}
	if !snapshotsSupported(e, 222, 10, 20) {
		t.Fatal("the next process must still get its snapshot")
	}
	if _, _, err := e.CreateSnapshot(222, 10, 20, "t"); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if !createSeen {
		t.Error("expected the snapshot to actually be created for the second process")
	}
}

// ---- the capability matrix -------------------------------------------------

// The messages that must never switch snapshots off, and the ones that must,
// asserted at the level that matters: the capability answer the push gate reads.
// The three "Unsupported …"/"Invalid …" strings are real API answers about a
// stage, an object id and a project type — a classifier that reads any of them
// as "this API has no snapshots" would disable the pre-push rollback point for
// every later push against that target.
func TestSnapshotsSupported_CapabilityMatrix(t *testing.T) {
	for _, tc := range []struct {
		description string
		want        bool // snapshots still available?
		wantCached  snapshotSupport
	}{
		{"Unsupported stage", true, snapshotSupportUnknown},
		{"Invalid obj_id", true, snapshotSupportUnknown},
		{"Unsupported project type", true, snapshotSupportUnknown},
		{"Unknown object: snapshots", false, snapshotSupportNo},
		{"Snapshots are not supported", false, snapshotSupportNo},
	} {
		t.Run(tc.description, func(t *testing.T) {
			t.Cleanup(resetSnapshotSupportCache)
			m := newProbeMock(t)
			m.snapshotsResp = map[string]interface{}{"proc": "error", "description": tc.description}
			_, e := mockAPIServer(t, m.fn)

			if got := snapshotsSupported(e, 555, 10, 20); got != tc.want {
				t.Errorf("snapshotsSupported after %q = %v, want %v", tc.description, got, tc.want)
			}
			if got := cachedSnapshotSupport(e, 10, 20); got != tc.wantCached {
				t.Errorf("cached answer after %q = %v, want %v", tc.description, got, tc.wantCached)
			}
		})
	}
}

// ---- recorded from a real snapshot-less installation ------------------------
//
// Verbatim /api/2/json answers from admin.dev.corezoid.com (identical on
// dev.corezoid.com) against a live, deployed process — workspace b3107e1c…,
// project 201862, stage 201864, conv 408902 ("abc") — on an installation that
// is known not to ship the snapshot feature:
//
//	create snapshot              → {"id":"","proc":"error","description":"bad object"}
//	list   snapshots             → {"id":"","proc":"error","description":"bad object"}
//	list   snapshot   (obj_id 1) → {"id":"","proc":"error","description":"bad object"}
//	list   <nonexistent obj>     → {"id":"","proc":"error","description":"bad object"}
//	list   commits               → {"id":"","proc":"ok","obj":"commits","list":[{"conv_id":408902,"version":1787315841,...}]}
//	list   conv, obj_id 999999999 → {"id":"","proc":"error","description":"Object conv with id 999999999 does not exist"}
//
// Three facts that decide the design:
//
//   - the refusal never contains the word "snapshot", so a classifier requiring
//     it would not detect the one environment this feature exists for;
//   - "bad object" comes from obj-name dispatch, *before* any id is looked at —
//     it is identical for a real conv and for a garbage one, and identical for
//     an obj name nothing implements. A known obj with a bad id, by contrast,
//     gets a specific "Object conv with id N does not exist";
//   - the very same conv answers `commits` with a live committed version, so
//     the process is reachable and this build does keep per-process versions —
//     only the snapshot object is absent. That is what the two control ops
//     measure.
const (
	recordedConvID    = 408902
	recordedProjectID = 201862
	recordedStageID   = 201864
)

var (
	devBadObject    = map[string]interface{}{"id": "", "proc": "error", "description": "bad object"}
	devCommitsOK    = map[string]interface{}{"id": "", "proc": "ok", "obj": "commits", "list": []interface{}{map[string]interface{}{"conv_id": float64(recordedConvID), "version": float64(1787315841), "create_time": float64(1787315841), "user_id": float64(48446)}}}
	devConvNoSuchID = map[string]interface{}{"id": "", "proc": "error", "description": "Object conv with id 999999999 does not exist"}
)

func TestSnapshotsSupported_RecordedSnapshotlessInstallation(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	var probedObjs []string
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		obj, _ := op["obj"].(string)
		probedObjs = append(probedObjs, obj)
		switch obj {
		case "commits":
			if id, _ := op["conv_id"].(float64); int(id) != recordedConvID {
				t.Errorf("the positive control must ask about the same conv, got %#v", op)
			}
			return wrapOp(devCommitsOK)
		case "conv":
			return wrapOp(devConvNoSuchID)
		default:
			// This build refuses every obj name it does not implement the same
			// way — for `snapshot`, `snapshots`, a nonsense name, and whether
			// the op is a list or a create.
			return wrapOp(devBadObject)
		}
	})
	e.WorkspaceID = "b3107e1c-d8a5-49ac-af0b-5aa686ec4624"

	if snapshotsSupported(e, recordedConvID, recordedProjectID, recordedStageID) {
		t.Fatal("admin.dev.corezoid.com has no snapshot object — the push must proceed without a snapshot there")
	}
	// The evidence gathered: the snapshot obj, the positive control, the
	// negative control — and nothing else.
	want := []string{"snapshots", "commits", snapshotProbeUnknownObj}
	if len(probedObjs) != len(want) {
		t.Fatalf("probe issued %v, want exactly %v", probedObjs, want)
	}
	for i := range want {
		if probedObjs[i] != want[i] {
			t.Fatalf("probe issued %v, want %v", probedObjs, want)
		}
	}
}

// All four snapshot ops the plugin can issue are refused identically on that
// build, so the single probe answers for every one of them — including the
// `create` the pre-push gate would otherwise fire.
func TestClassifySnapshotRejection_RecordedRefusalCoversEverySnapshotOp(t *testing.T) {
	for _, op := range []string{"create snapshot", "list snapshots", "list snapshot", "delete snapshot"} {
		if got := classifySnapshotRejection(devBadObject); got != rejectionUnknownObjGeneric {
			t.Errorf("%s: recorded refusal classified %v, want rejectionUnknownObjGeneric", op, got)
		}
	}
}

// The same installation's answer for a real object with a bad id must stay an
// ordinary failure: it names one conv, so it says nothing about the feature.
func TestClassifySnapshotRejection_RecordedIDSpecificFailureIsOrdinary(t *testing.T) {
	if got := classifySnapshotRejection(devConvNoSuchID); got != rejectionOrdinary {
		t.Errorf("recorded id-specific failure classified %v, want rejectionOrdinary", got)
	}
}

// A curated proc code is exact-matched, so it outranks the text rules and is not
// discarded by the parameter-reference veto that would otherwise fire on the
// underscore in it.
func TestClassifySnapshotRejection_StructuredCodeOutranksTextHeuristics(t *testing.T) {
	if got := classifySnapshotRejection(map[string]any{"proc": "obj_not_supported"}); got != rejectionUnknownObjGeneric {
		t.Errorf("curated proc code classified %v, want rejectionUnknownObjGeneric", got)
	}
	// …while a code naming a concrete thing stays ordinary, because it is not
	// in the set and the text rules veto it.
	for _, code := range []string{"unsupported_stage", "invalid_obj_id", "conv_id_not_found"} {
		if got := classifySnapshotRejection(map[string]any{"proc": code}); got != rejectionOrdinary {
			t.Errorf("proc %q classified %v, want rejectionOrdinary", code, got)
		}
	}
}

// The blast-radius test: for a corpus of realistic Corezoid answers, which ones
// can EVER switch snapshots off? Each case runs against the worst possible
// environment for that message — one that refuses a nonsense obj name with the
// exact same words and answers every ordinary op fine, i.e. one where both
// control ops confirm whatever they can. Anything marked false here cannot
// disable snapshots no matter what the environment does.
func TestSnapshotsSupported_WorstCaseBlastRadius(t *testing.T) {
	for _, tc := range []struct {
		proc, desc   string
		wantDisabled bool
	}{
		// The three false matches from the original report, plus their kin.
		{"error", "Unsupported stage", false},
		{"error", "Unsupported project type", false},
		{"error", "Invalid obj_id", false},
		{"error", "Invalid obj_type", false},
		{"error", "Object type is not supported", false},
		{"unsupported_stage", "", false},
		// Ordinary failures of every flavour.
		{"error", "Object conv with id 999999999 does not exist", false},
		{"error", "Access denied", false},
		{"error", "Value is not valid", false},
		{"error", "conv_id not found", false},
		{"error", "Internal server error", false},
		{"error", "user has no privs", false},
		{"error", "company_id is not valid", false},
		{"error", "stage is immutable", false},
		{"error", "Version 3 does not exist", false},
		{"error", "Node not found", false},
		{"error", "timeout", false},
		{"error", "invalid json", false},
		{"error", "", false},
		{"", "", false},
		// Snapshot-specific failures that are about one snapshot, not the feature.
		{"error", "Snapshot 5 not found", false},
		{"error", "Snapshot with id 5 does not exist", false},
		{"error", "Snapshot limit exceeded", false},
		// The four shapes that may disable snapshots. The first three only do so
		// with both controls agreeing; the last two name the object outright.
		{"error", "bad object", true},
		{"error", "Method not implemented for this obj", true},
		{"unknown_obj", "", true},
		{"error", "Unknown object: snapshots", true},
		{"error", "Snapshots are not supported", true},
	} {
		label := tc.desc
		if label == "" {
			label = "proc=" + tc.proc
		}
		t.Run(label, func(t *testing.T) {
			t.Cleanup(resetSnapshotSupportCache)
			_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
				obj, _ := ops[0]["obj"].(string)
				if obj == "commits" || obj == "folder" {
					return wrapOp(map[string]interface{}{
						"proc": "ok", "list": []interface{}{}, "obj_id": float64(20), "parent_obj_id": float64(10),
					})
				}
				return wrapOp(map[string]interface{}{"proc": tc.proc, "description": tc.desc})
			})

			disabled := !snapshotsSupported(e, recordedConvID, recordedProjectID, recordedStageID)
			if disabled != tc.wantDisabled {
				t.Errorf("worst-case environment answering %q/%q disables snapshots = %v, want %v",
					tc.proc, tc.desc, disabled, tc.wantDisabled)
			}
		})
	}
}
