package main

import (
	"fmt"
	"strings"
	"sync"
)

// Not every Corezoid installation ships the snapshot feature: on-prem and older
// environments answer the `snapshot`/`snapshots` ops of /api/2/json with an
// "unknown object" style rejection. The pre-push auto-snapshot must not turn
// that into a blocked push — on such an environment there is no snapshot to
// take, so the push simply proceeds without one.
//
// Support is probed with a read-only `list snapshots` call the first time a
// snapshot would be taken, and the answer is cached per environment for the
// lifetime of the process, so the create call is never even issued where the
// feature does not exist.

type snapshotSupport int

const (
	snapshotSupportUnknown snapshotSupport = iota
	snapshotSupportYes
	snapshotSupportNo
)

var (
	snapshotSupportMu    sync.Mutex
	snapshotSupportCache = map[string]snapshotSupport{}
)

// snapshotSupportKey identifies the environment a probe result belongs to.
// Support is a property of the installation, not of a process, but the
// workspace is part of the key because a workspace switch can also change which
// backend answers (personal vs company accounts hit different clusters).
func snapshotSupportKey(v *Executor) string {
	if v == nil {
		return ""
	}
	return strings.TrimRight(v.APIUrl, "/") + "|" + v.WorkspaceID
}

// resetSnapshotSupportCache drops every cached probe result. Entries are keyed
// per environment, so normal environment switches need no reset — this exists so
// tests do not inherit each other's answers.
func resetSnapshotSupportCache() {
	snapshotSupportMu.Lock()
	defer snapshotSupportMu.Unlock()
	snapshotSupportCache = map[string]snapshotSupport{}
}

// cachedSnapshotSupport reads the memoised probe result for v's environment.
func cachedSnapshotSupport(v *Executor) snapshotSupport {
	snapshotSupportMu.Lock()
	defer snapshotSupportMu.Unlock()
	return snapshotSupportCache[snapshotSupportKey(v)]
}

func storeSnapshotSupport(v *Executor, s snapshotSupport) {
	snapshotSupportMu.Lock()
	defer snapshotSupportMu.Unlock()
	snapshotSupportCache[snapshotSupportKey(v)] = s
}

// snapshotsSupported reports whether this environment exposes the snapshot API.
//
// The first call probes with a read-only list; later calls answer from cache.
// Anything other than a positive "this API does not know snapshots" rejection —
// network failure, auth error, a process-specific complaint — counts as
// supported and is left for the real snapshot call to report, so a transient
// blip never silently disables the rollback point.
func snapshotsSupported(v *Executor, convID, projectID, stageID int) bool {
	if v == nil {
		return false
	}
	switch cachedSnapshotSupport(v) {
	case snapshotSupportYes:
		return true
	case snapshotSupportNo:
		return false
	}

	supported, err := v.ProbeSnapshotSupport(convID, projectID, stageID)
	if !supported {
		logger.Info("[snapshot] environment does not support snapshots (%v) — snapshot calls will be skipped", err)
		storeSnapshotSupport(v, snapshotSupportNo)
		return false
	}
	if err != nil {
		// Inconclusive: assume the feature exists but do not cache the guess,
		// so the next push probes again instead of freezing a wrong answer.
		logger.Warn("[snapshot] support probe inconclusive: %v", err)
		return true
	}
	storeSnapshotSupport(v, snapshotSupportYes)
	return true
}

// ProbeSnapshotSupport issues a read-only `list snapshots` op and classifies the
// answer. It returns false only when the API positively rejects the snapshot
// object itself; the error is returned alongside for logging in every failing
// case.
func (v *Executor) ProbeSnapshotSupport(convID, projectID, stageID int) (bool, error) {
	ops := []map[string]any{
		{
			"type":       "list",
			"obj":        "snapshots",
			"conv_id":    convID,
			"project_id": projectID,
			"stage_id":   stageID,
			"company_id": v.WorkspaceID,
		},
	}
	resp, err := v.req("json", ops)
	if err == nil {
		return true, nil
	}
	if op := rawFirstOp(resp); op != nil && snapshotObjectRejected(op) {
		return false, fmt.Errorf("snapshot API not available: %w", err)
	}
	return true, err
}

// rawFirstOp returns the first op of a response without the proc=="ok" check
// firstOp applies — the failing op is exactly what we need to classify here.
func rawFirstOp(resp map[string]any) map[string]any {
	if resp == nil {
		return nil
	}
	opsRaw, ok := resp["ops"].([]any)
	if !ok || len(opsRaw) == 0 {
		return nil
	}
	op, _ := opsRaw[0].(map[string]any)
	return op
}

// snapshotObjectRejected reports whether a failed op means "this API has no
// snapshot object" rather than "this snapshot request was bad". Markers are
// matched against both the machine-readable `proc` code and the human-readable
// `description`, because different Corezoid builds surface it in either field.
func snapshotObjectRejected(op map[string]any) bool {
	proc, _ := op["proc"].(string)
	desc, _ := op["description"].(string)
	haystack := strings.ToLower(proc + " " + desc)
	markers := []string{
		"unknown obj",
		"unknown_obj",
		"unknown object",
		"undefined obj",
		"unsupported obj",
		"invalid obj",
		"no such obj",
		"not supported",
		"not_supported",
		"unsupported",
		"not implemented",
		"not_implemented",
		"snapshot is disabled",
		"snapshots are disabled",
		"snapshot is not available",
		"snapshots are not available",
	}
	for _, m := range markers {
		if strings.Contains(haystack, m) {
			return true
		}
	}
	return false
}

// snapshotUnsupportedMessage is the single wording every snapshot tool uses when
// the environment has no snapshot feature, so the agent and the user always see
// the same explanation instead of a raw API error.
const snapshotUnsupportedMessage = "Snapshots are not supported in this Corezoid environment (its API has no snapshot object), so nothing was created or read. Keep the .conv.json under version control if you need a rollback point here."
