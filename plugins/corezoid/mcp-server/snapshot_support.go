package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Not every Corezoid installation ships the snapshot feature: on-prem and older
// environments answer the `snapshot`/`snapshots` ops of /api/2/json with an
// "unknown object" style rejection. The pre-push auto-snapshot must not turn
// that into a blocked push — on such an environment there is no snapshot to
// take, so the push simply proceeds without one.
//
// What a real snapshot-less installation answers, recorded from
// admin.dev.corezoid.com against a live, deployed process — project 201862,
// stage 201864, conv 408902 (fixtures in snapshot_support_test.go):
//
//	create snapshot             → {"proc":"error","description":"bad object"}
//	list   snapshots            → {"proc":"error","description":"bad object"}
//	list   snapshot   (obj_id 1) → {"proc":"error","description":"bad object"}
//	list   <nonexistent obj>    → {"proc":"error","description":"bad object"}
//	list   commits              → {"proc":"ok","list":[{"version":1787315841,...}]}
//	list   conv, obj_id 9999999 → {"proc":"error","description":"Object conv with id 9999999 does not exist"}
//
// Note what that build does NOT do: it never says "snapshot", and it answers
// "bad object" from obj-name dispatch — invariant to conv_id/project_id/stage_id
// — while a known obj with a bad id gets a specific, id-naming error, and the
// same conv answers `commits` perfectly well. So the signal to key on is not a
// keyword but the *shape* of the refusal, measured against control requests.
//
// The whole safety property of this file is the classification of a failed
// probe. Reading it too broadly is the dangerous direction: a per-process or
// per-stage complaint misread as "no snapshot feature here" would silently
// disable the pre-push rollback point for everything that follows. So a
// negative answer needs *evidence about the obj name itself*, not a keyword:
//
//  1. the API names the snapshot object in its refusal → conclusive on its own;
//  2. an "unknown obj"-style refusal that does NOT name snapshots is only
//     believed after two control ops confirm it (see confirmUnknownSnapshotObject):
//     a request the installation must answer (`show folder`) succeeds, and a
//     deliberately nonsensical obj name is refused in exactly the same words;
//  3. anything that names a conv, project, stage, obj_id, access or a version
//     is about this request, never about the feature.
//
// Negative answers are additionally cached per project+stage and expire, so
// even a misclassification cannot outlive one TTL or spread past its stage.

type snapshotSupport int

const (
	snapshotSupportUnknown snapshotSupport = iota
	snapshotSupportYes
	snapshotSupportNo
)

// snapshotSupportNegativeTTL bounds how long a "this environment has no
// snapshots" answer is trusted. Positive answers never expire (a feature does
// not disappear mid-session, and being wrong there only means the real snapshot
// call reports the error itself), but a negative one disables a safety net —
// so it is re-verified periodically. A var, so tests can shorten it.
var snapshotSupportNegativeTTL = 30 * time.Minute

// snapshotSupportNow is time.Now behind a seam, so TTL expiry is testable
// without sleeping.
var snapshotSupportNow = time.Now

type snapshotSupportEntry struct {
	support snapshotSupport
	expires time.Time // zero value: never expires
}

var (
	snapshotSupportMu    sync.Mutex
	snapshotSupportCache = map[string]snapshotSupportEntry{}
)

// snapshotProbeUnknownObj is the negative control: an obj name no Corezoid
// build implements, used to learn what "I do not know this object" looks like
// on this particular installation. The op carrying it is a read-only `list`, so
// an installation that somehow did know the name would still change nothing.
const snapshotProbeUnknownObj = "corezoid_mcp_capability_probe_obj"

// snapshotSupportKey identifies what a probe result is allowed to speak for.
// Support is really a property of the installation, but the key is deliberately
// narrower than that: workspace (personal vs company accounts hit different
// clusters) and the project/stage the probe ran against, so a wrong negative
// can never reach beyond the stage that produced it.
func snapshotSupportKey(v *Executor, projectID, stageID int) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%d|%d", strings.TrimRight(v.APIUrl, "/"), v.WorkspaceID, projectID, stageID)
}

// resetSnapshotSupportCache drops every cached probe result. Entries are keyed
// per environment, so normal environment switches need no reset — this exists so
// tests do not inherit each other's answers.
func resetSnapshotSupportCache() {
	snapshotSupportMu.Lock()
	defer snapshotSupportMu.Unlock()
	snapshotSupportCache = map[string]snapshotSupportEntry{}
}

// cachedSnapshotSupport reads the memoised probe result for this target,
// dropping it once it has expired.
func cachedSnapshotSupport(v *Executor, projectID, stageID int) snapshotSupport {
	key := snapshotSupportKey(v, projectID, stageID)
	snapshotSupportMu.Lock()
	defer snapshotSupportMu.Unlock()
	entry, ok := snapshotSupportCache[key]
	if !ok {
		return snapshotSupportUnknown
	}
	if !entry.expires.IsZero() && !snapshotSupportNow().Before(entry.expires) {
		delete(snapshotSupportCache, key)
		return snapshotSupportUnknown
	}
	return entry.support
}

// storeSnapshotSupport memoises a probe result. Negative answers carry a TTL;
// positive ones do not.
func storeSnapshotSupport(v *Executor, projectID, stageID int, s snapshotSupport) {
	entry := snapshotSupportEntry{support: s}
	if s == snapshotSupportNo {
		entry.expires = snapshotSupportNow().Add(snapshotSupportNegativeTTL)
	}
	snapshotSupportMu.Lock()
	defer snapshotSupportMu.Unlock()
	snapshotSupportCache[snapshotSupportKey(v, projectID, stageID)] = entry
}

// snapshotsSupported reports whether this environment exposes the snapshot API.
//
// The first call probes with a read-only list; later calls answer from cache.
// Anything short of confirmed evidence that the API has no snapshot object —
// network failure, auth error, a process-specific complaint, an ambiguous
// refusal the control ops could not confirm — counts as supported and is left
// for the real snapshot call to report, so a transient blip never silently
// disables the rollback point.
func snapshotsSupported(v *Executor, convID, projectID, stageID int) bool {
	if v == nil {
		return false
	}
	switch cachedSnapshotSupport(v, projectID, stageID) {
	case snapshotSupportYes:
		return true
	case snapshotSupportNo:
		return false
	}

	supported, err := v.ProbeSnapshotSupport(convID, projectID, stageID)
	if !supported {
		logger.Info("[snapshot] environment does not support snapshots (%v) — snapshot calls will be skipped", err)
		storeSnapshotSupport(v, projectID, stageID, snapshotSupportNo)
		return false
	}
	if err != nil {
		// Inconclusive: assume the feature exists but do not cache the guess,
		// so the next push probes again instead of freezing a wrong answer.
		logger.Warn("[snapshot] support probe inconclusive: %v", err)
		return true
	}
	storeSnapshotSupport(v, projectID, stageID, snapshotSupportYes)
	return true
}

// ProbeSnapshotSupport issues a read-only `list snapshots` op and classifies the
// answer. It returns false only when the API is shown to have no snapshot object
// at all; the error is returned alongside for logging in every failing case.
func (v *Executor) ProbeSnapshotSupport(convID, projectID, stageID int) (bool, error) {
	resp, err := v.req("json", []map[string]any{v.snapshotListProbeOp("snapshots", convID, projectID, stageID)})
	if err == nil {
		return true, nil
	}
	op := rawFirstOp(resp)
	if op == nil {
		return true, err
	}
	switch classifySnapshotRejection(op) {
	case rejectionUnknownObjSnapshot:
		return false, fmt.Errorf("snapshot API not available: %w", err)
	case rejectionUnknownObjGeneric:
		if v.confirmUnknownSnapshotObject(op, convID, projectID, stageID) {
			return false, fmt.Errorf("snapshot API not available: %w", err)
		}
		logger.Warn("[snapshot] ambiguous rejection %q not confirmed as a missing snapshot object — keeping snapshots enabled", opRejectionText(op))
		return true, err
	}
	return true, err
}

// snapshotListProbeOp builds the read-only list op used for probing. The obj
// name is a parameter so the same request shape can carry the real object and
// the negative control — comparing two answers is only meaningful when nothing
// else about the request differs.
func (v *Executor) snapshotListProbeOp(obj string, convID, projectID, stageID int) map[string]any {
	return map[string]any{
		"type":       "list",
		"obj":        obj,
		"conv_id":    convID,
		"project_id": projectID,
		"stage_id":   stageID,
		"company_id": v.WorkspaceID,
	}
}

// confirmUnknownSnapshotObject turns an "unknown obj"-style refusal that never
// mentions snapshots (`bad object`, `unknown_obj`, …) into actual evidence.
//
// Such a message is only trusted when both control ops agree:
//
//   - positive control — a request this installation must be able to answer
//     about the very same target must succeed (see snapshotPositiveControl). It
//     proves the API is reachable, the credentials work, the process itself is
//     accessible and this build does answer ordinary ops — ruling out an
//     environment that fails everything with the same generic text;
//   - negative control — the identical list op carrying an obj name nothing can
//     implement must be refused in exactly the same words. If the API answers
//     `snapshots` the way it answers a nonsense name, the refusal was about the
//     obj name; if it answers differently, the object exists and the failure was
//     about this particular request.
//
// Both are read-only. Either one failing to line up keeps snapshots enabled.
func (v *Executor) confirmUnknownSnapshotObject(rejection map[string]any, convID, projectID, stageID int) bool {
	if err := v.snapshotPositiveControl(convID, stageID); err != nil {
		logger.Warn("[snapshot] control op failed (%v) — cannot conclude the snapshot object is missing", err)
		return false
	}
	resp, err := v.req("json", []map[string]any{v.snapshotListProbeOp(snapshotProbeUnknownObj, convID, projectID, stageID)})
	if err == nil {
		// The API accepted an obj name that cannot exist: its answers carry no
		// information about obj names at all.
		return false
	}
	control := rawFirstOp(resp)
	if control == nil {
		return false
	}
	return sameRejection(rejection, control)
}

// snapshotPositiveControl issues the read-only op that must succeed before any
// refusal is believed.
//
// Preferred: `list commits` for the same conv the snapshot was refused for.
// Snapshots and commits are both per-process version objects, so a build that
// answers one and refuses the other is saying something specifically about the
// snapshot object — and being conv-scoped, it also rules out "this process is
// gone or inaccessible" as the real reason for the refusal. On the recorded
// snapshot-less installation, conv 408902 answers `commits` with a live version
// while `create snapshot`, `list snapshots` and `list snapshot` all come back
// "bad object".
//
// Without a conv the check falls back to `show folder <stage>`, which at least
// proves the endpoint and the credentials work. With neither, nothing can be
// concluded and the caller keeps snapshots enabled.
func (v *Executor) snapshotPositiveControl(convID, stageID int) error {
	if convID != 0 {
		_, err := v.req("json", []map[string]any{{
			"type":       "list",
			"obj":        "commits",
			"conv_id":    convID,
			"company_id": v.WorkspaceID,
		}})
		return err
	}
	if stageID != 0 {
		_, err := v.ShowFolder(stageID)
		return err
	}
	return fmt.Errorf("no positive control available (no conv and no stage)")
}

// sameRejection reports whether two failed ops carry the same refusal, compared
// on the fields the API uses to say why: the machine-readable code and the
// human-readable text.
func sameRejection(a, b map[string]any) bool {
	return opRejectionText(a) == opRejectionText(b)
}

// opRejectionText normalises the refusal of a failed op into one comparable
// string.
func opRejectionText(op map[string]any) string {
	proc, _ := op["proc"].(string)
	desc, _ := op["description"].(string)
	return strings.ToLower(strings.TrimSpace(proc)) + "|" + strings.ToLower(strings.TrimSpace(desc))
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

// snapshotRejection is how much a failed probe op says about the snapshot
// feature's existence.
type snapshotRejection int

const (
	// rejectionOrdinary: the failure is about this request (a conv, a stage, a
	// project, an obj_id, access, a version) or about nothing in particular.
	// It says nothing about whether the API has a snapshot object.
	rejectionOrdinary snapshotRejection = iota
	// rejectionUnknownObjGeneric: an "unknown obj" shaped refusal that never
	// names snapshots. Suggestive, not conclusive — control ops decide.
	rejectionUnknownObjGeneric
	// rejectionUnknownObjSnapshot: the API named the snapshot object itself as
	// unknown, unsupported or disabled. Conclusive.
	rejectionUnknownObjSnapshot
)

// snapshotUnknownObjProcCodes are the structured `proc` codes that mean "I do
// not know this object". Matched exactly (after case/separator normalisation),
// never as a substring, so a code like `unsupported_stage` cannot match.
var snapshotUnknownObjProcCodes = map[string]bool{
	"unknown_obj":        true,
	"unknown_object":     true,
	"bad_obj":            true,
	"bad_object":         true,
	"undefined_obj":      true,
	"undefined_object":   true,
	"unsupported_obj":    true,
	"unsupported_object": true,
	"no_such_obj":        true,
	"not_implemented":    true,
	"obj_not_supported":  true,
}

// snapshotUnknownObjMarkers are description phrases that talk about the *object*
// being unknown. Every one of them is anchored on the word obj/object, so a
// broad "unsupported"/"not supported" about something else (a stage, a project
// type, a node) cannot match on its own.
var snapshotUnknownObjMarkers = []string{
	"unknown obj",
	"unknown object",
	"bad obj",
	"bad object",
	"undefined obj",
	"undefined object",
	"unsupported obj",
	"unsupported object",
	"no such obj",
	"invalid obj",
	"obj is not supported",
	"object is not supported",
	"obj not supported",
	"not implemented",
}

// snapshotFeatureAbsentMarkers are only consulted once the message already
// names snapshots, where a plain "not supported" is unambiguous.
var snapshotFeatureAbsentMarkers = []string{
	"unknown",
	"unsupported",
	"not supported",
	"not_supported",
	"no such",
	"undefined",
	"bad obj",
	"bad object",
	"not implemented",
	"not_implemented",
	"disabled",
	"not available",
	"invalid obj",
}

// snapshotRequestSpecificMarkers name a concrete thing the request asked about.
// A message mentioning any of them is answering "your request was wrong", not
// "this object does not exist here" — the exact confusion that could otherwise
// switch snapshots off for a whole workspace ("Unsupported stage", "Invalid
// obj_id", "Unsupported project type").
var snapshotRequestSpecificMarkers = []string{
	"conv_id",
	"conv id",
	"obj_id",
	"obj_type",
	"obj type",
	"object type",
	"project",
	"stage",
	"company",
	"user",
	"access",
	"denied",
	"permission",
	"not found",
	"expired",
	"token",
	"version",
	"value is not valid",
}

// classifySnapshotRejection decides how much a failed probe op proves. The
// order of the rules is the safety property; see the file comment.
func classifySnapshotRejection(op map[string]any) snapshotRejection {
	if op == nil {
		return rejectionOrdinary
	}
	proc, _ := op["proc"].(string)
	desc, _ := op["description"].(string)
	hay := strings.ToLower(strings.TrimSpace(proc + " " + desc))
	if hay == "" {
		return rejectionOrdinary
	}

	// (1) The API named the snapshot object in its refusal — conclusive, and
	// checked first so a message like "unknown obj snapshots for conv 5" is not
	// discarded by the request-specific rule below.
	if strings.Contains(hay, "snapshot") && containsAnyMarker(hay, snapshotFeatureAbsentMarkers) {
		return rejectionUnknownObjSnapshot
	}

	// (2) A structured code from the curated set. Exact-matched, so it is not a
	// heuristic at all — it outranks the text rules below, and is not subject to
	// the request-specific veto (a code like `obj_not_supported` legitimately
	// looks like a parameter reference).
	if snapshotUnknownObjProcCodes[normalizeProcCode(proc)] {
		return rejectionUnknownObjGeneric
	}

	// (3) Text fallback, and only from here on. First: anything naming a
	// concrete thing the request carried is about the request.
	if requestSpecificRejection(hay) {
		return rejectionOrdinary
	}

	// (4) An obj-level refusal that did not name snapshots: suggestive only.
	if containsAnyMarker(hay, snapshotUnknownObjMarkers) {
		return rejectionUnknownObjGeneric
	}
	return rejectionOrdinary
}

// snapshotParamReference matches a request parameter spelled the way the API
// spells them — obj_id, obj_type, conv_id, stage_id, … A refusal quoting one is
// answering "this field was wrong", which says nothing about whether the
// snapshot object exists. Catching the shape rather than a fixed list is what
// keeps a new parameter name from becoming the next false positive.
var snapshotParamReference = regexp.MustCompile(`\b(obj|conv|node|company|project|stage|user|group|folder|alias|task)_[a-z]+\b`)

// requestSpecificRejection reports whether a refusal is about something this
// particular request named.
func requestSpecificRejection(hay string) bool {
	return snapshotParamReference.MatchString(hay) || containsAnyMarker(hay, snapshotRequestSpecificMarkers)
}

// normalizeProcCode lowercases a proc code and folds spaces/dashes to
// underscores so `Unknown Obj`, `unknown-obj` and `unknown_obj` compare equal.
func normalizeProcCode(proc string) string {
	proc = strings.ToLower(strings.TrimSpace(proc))
	proc = strings.ReplaceAll(proc, " ", "_")
	proc = strings.ReplaceAll(proc, "-", "_")
	return proc
}

func containsAnyMarker(hay string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(hay, m) {
			return true
		}
	}
	return false
}

// snapshotUnsupportedMessage is the single wording every snapshot tool uses when
// the environment has no snapshot feature, so the agent and the user always see
// the same explanation instead of a raw API error.
const snapshotUnsupportedMessage = "Snapshots are not supported in this Corezoid environment (its API has no snapshot object), so nothing was created or read. Keep the .conv.json under version control if you need a rollback point here."
