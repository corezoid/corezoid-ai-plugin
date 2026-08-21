package main

import (
	"testing"
)

// ---- snapshotObjectRejected ------------------------------------------------
//
// The classifier is the whole safety property of this feature: read too
// broadly and a transient failure silently disables the pre-push rollback
// point; read too narrowly and pushes stay blocked forever on environments
// that simply have no snapshot API.

func TestSnapshotObjectRejected_UnsupportedMarkers(t *testing.T) {
	cases := []map[string]any{
		{"proc": "unknown_obj"},
		{"proc": "error", "description": "Unknown obj snapshots"},
		{"proc": "error", "description": "Object type is not supported"},
		{"proc": "not_implemented"},
		{"proc": "error", "description": "Snapshots are disabled on this installation"},
	}
	for _, op := range cases {
		if !snapshotObjectRejected(op) {
			t.Errorf("op %v must be read as 'no snapshot API here'", op)
		}
	}
}

func TestSnapshotObjectRejected_OrdinaryFailures(t *testing.T) {
	cases := []map[string]any{
		{},
		{"proc": "error"},
		{"proc": "error", "description": "Access denied"},
		{"proc": "error", "description": "conv_id not found"},
		{"proc": "error", "description": "Value is not valid"},
		{"proc": "error", "description": "Internal server error"},
	}
	for _, op := range cases {
		if snapshotObjectRejected(op) {
			t.Errorf("op %v is an ordinary failure, not a missing snapshot API", op)
		}
	}
}

// ---- snapshotsSupported ----------------------------------------------------

// On an environment without the feature, the probe answers false and no
// `create snapshot` op is ever sent — that is exactly the behaviour asked for:
// where snapshots are unsupported, we do not attempt one.
func TestSnapshotsSupported_UnsupportedEnvironmentNeverCreates(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	var listCalls int
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		for _, op := range ops {
			if op["type"] == "create" {
				t.Errorf("create snapshot must not be issued on an unsupported environment")
			}
			if op["type"] == "list" {
				listCalls++
			}
		}
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":        "error",
				"description": "Unknown obj",
			}},
		}
	})

	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("an API rejecting the snapshot object must be reported as unsupported")
	}
	// Second call must come from cache: the probe costs one request per
	// environment, not one per push.
	if snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("cached answer changed between calls")
	}
	if listCalls != 1 {
		t.Errorf("probe should run once per environment, ran %d times", listCalls)
	}
}

func TestSnapshotsSupported_SupportedEnvironmentProbesOnce(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	var listCalls int
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		for _, op := range ops {
			if op["type"] == "list" {
				listCalls++
			}
		}
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc": "ok",
				"list": []interface{}{},
			}},
		}
	})

	if !snapshotsSupported(e, 555, 10, 20) || !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("an API that answers the list op supports snapshots")
	}
	if listCalls != 1 {
		t.Errorf("probe should run once per environment, ran %d times", listCalls)
	}
}

// A blip (auth, network, server fault) is not evidence that the feature is
// missing. It must keep snapshots enabled — so the existing "snapshot failed →
// block the push" protection still applies — and must not be cached, or one bad
// minute would disable rollback points for the rest of the session.
func TestSnapshotsSupported_TransientFailureStaysEnabledAndUncached(t *testing.T) {
	t.Cleanup(resetSnapshotSupportCache)

	var listCalls int
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		for _, op := range ops {
			if op["type"] == "list" {
				listCalls++
			}
		}
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":        "error",
				"description": "Internal server error",
			}},
		}
	})

	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("a server fault must not be read as 'snapshots unsupported'")
	}
	if !snapshotsSupported(e, 555, 10, 20) {
		t.Fatal("a server fault must not be read as 'snapshots unsupported'")
	}
	if listCalls != 2 {
		t.Errorf("an inconclusive probe must not be cached; got %d probes, want 2", listCalls)
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
	if cachedSnapshotSupport(e) != snapshotSupportUnknown {
		t.Error("an unreachable API must leave the support answer unknown")
	}
}

func TestSnapshotsSupported_NilExecutor(t *testing.T) {
	if snapshotsSupported(nil, 1, 2, 3) {
		t.Error("a nil executor cannot support snapshots")
	}
}

// The cache is keyed per environment, so switching workspaces re-probes instead
// of inheriting the previous installation's answer.
func TestSnapshotSupportKey_SeparatesEnvironments(t *testing.T) {
	a := &Executor{APIUrl: "https://one.corezoid.com/", WorkspaceID: "1"}
	b := &Executor{APIUrl: "https://one.corezoid.com", WorkspaceID: "2"}
	c := &Executor{APIUrl: "https://two.corezoid.com", WorkspaceID: "1"}
	if snapshotSupportKey(a) == snapshotSupportKey(b) {
		t.Error("different workspaces must not share a cached answer")
	}
	if snapshotSupportKey(a) == snapshotSupportKey(c) {
		t.Error("different environments must not share a cached answer")
	}
	// A trailing slash is cosmetic — it must not split the cache entry.
	d := &Executor{APIUrl: "https://one.corezoid.com", WorkspaceID: "1"}
	if snapshotSupportKey(a) != snapshotSupportKey(d) {
		t.Error("trailing slash must not create a second cache entry")
	}
}
