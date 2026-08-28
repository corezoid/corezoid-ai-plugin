package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// convResp wraps a conv object as a get_process response. The server returns
// proc:"ok" alongside the conv fields in the same op, so inject it.
func convResp(conv map[string]interface{}) func([]map[string]interface{}) interface{} {
	conv["proc"] = "ok"
	return func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{conv},
		}
	}
}

func setupConflict(t *testing.T, base baselineEntry, localJSON string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if base.ChangeTime != 0 || base.Version != 0 {
		if err := writeBaseline(dir, 1, base); err != nil {
			t.Fatal(err)
		}
	}
	fp := filepath.Join(dir, "1_x.conv.json")
	return dir, fp
}

const twoNodeLocal = `{"obj_id":1,"scheme":{"nodes":[
 {"obj_type":1,"title":"Start"},
 {"obj_type":0,"title":"A"},
 {"obj_type":0,"title":"B"}]}}`

// blockedResult adapts resolveConflict to the (blocked, message) shape the
// original decision tests assert on.
func blockedResult(r conflictResult) (bool, string) {
	return r.action == conflictBlock, r.message
}

// No baseline means lost-update detection cannot run at all. Whether that is
// harmless depends on the server: with nothing deployed there is nothing to
// lose, but over a deployed version it is the blind overwrite the whole
// baseline subsystem exists to prevent.
func TestConflict_NoBaselineOnDeployedProcessBlocks(t *testing.T) {
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal) // no baseline written
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, false))
	if !blocked || !strings.Contains(msg, "no pull baseline") {
		t.Fatalf("a deployed process with no baseline must block, got blocked=%v msg=%q", blocked, msg)
	}
	if !strings.Contains(msg, "pull-process") || !strings.Contains(msg, "adopt_existing") {
		t.Errorf("block message must name both ways out, got %q", msg)
	}
}

// force is for a conflict the user was shown; it must not stand in for "I have
// no idea what is on the server".
func TestConflict_NoBaselineIsNotWaivedByForce(t *testing.T) {
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal)
	blocked, _ := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, true, false, false))
	if !blocked {
		t.Fatal("force=true must not waive a missing baseline on a deployed process")
	}
}

func TestConflict_NoBaselineProceedsWithAdoptExisting(t *testing.T) {
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, true))
	if blocked {
		t.Fatalf("adopt_existing=true must allow the push, got %q", msg)
	}
	if !strings.Contains(msg, "adopt_existing") {
		t.Errorf("the waiver must be stated in the result, got %q", msg)
	}
}

// The create-process → push-process flow: the conv exists but carries no
// committed version and no nodes, so there is nothing a concurrent edit could
// have overwritten and no baseline is needed.
func TestConflict_NoBaselineOnNeverDeployedProcessProceeds(t *testing.T) {
	_, e := mockAPIServer(t, convResp(map[string]interface{}{
		"change_time": float64(200),
		"commits":     map[string]interface{}{"version": float64(0)},
		"list":        []interface{}{},
	}))
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, false))
	if blocked {
		t.Fatalf("a never-deployed process must not need a baseline, got %q", msg)
	}
	if !strings.Contains(msg, "no deployed version") {
		t.Errorf("the reason must be stated, got %q", msg)
	}
}

// An unanswerable server is not evidence that nothing is deployed: fail closed.
func TestConflict_NoBaselineFailsClosedWhenServerCannotAnswer(t *testing.T) {
	_, e := mockAPIServer(t, func([]map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "error"}
	})
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal)
	blocked, _ := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, false))
	if !blocked {
		t.Fatal("an unreadable server state must not be read as 'nothing deployed'")
	}
}

func TestConflict_CorruptBaselineBlocks(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	if err := os.WriteFile(filepath.Join(dir, baselineFileName), []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, true, false, false))
	if !blocked || !strings.Contains(msg, "baseline") || !strings.Contains(msg, "unreadable") {
		t.Fatalf("corrupt baseline must fail closed even with force=true, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestConflict_InSyncProceeds(t *testing.T) {
	conv := map[string]interface{}{"change_time": float64(100), "last_confirmed_version": float64(10)}
	_, e := mockAPIServer(t, convResp(conv))
	// Detail-sourced: the version tiebreak is comparable, so the equal-timestamp
	// fallback never runs and an in-sync push says nothing at all.
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10, Source: baselineSourceDetail}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, false))
	if blocked || msg != "" {
		t.Fatalf("in-sync must proceed silently, got blocked=%v msg=%q", blocked, msg)
	}
}

// A legacy (pre-Source-tag) baseline with no recorded ancestor cannot run the
// equal-timestamp content check. Blocking there would stop every push from a
// pre-v3.1.3 workspace, because equal timestamps are the ORDINARY in-sync case,
// not the rare same-second collision — so the push proceeds, the ancestor is
// recorded from the live server scheme, and the gap is reported rather than
// logged. From the next push on, the file gets the full check.
func TestConflict_LegacyBaselineNoAncestorSelfHeals(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	scheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"}]}}`
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 10}); err != nil {
		t.Fatal(err)
	}
	if _, ok := readAncestorScheme(dir, 1); ok {
		t.Fatal("fixture must start without an ancestor")
	}
	e := dualEndpointServer(t, 100, 10, scheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, scheme, false, false, false))
	if blocked {
		t.Fatalf("a legacy sidecar without an ancestor must proceed, got msg=%q", msg)
	}
	if !strings.Contains(msg, "no recorded ancestor") || !strings.Contains(msg, "later pushes get the full check") {
		t.Fatalf("the weakened check must be reported to the user, got %q", msg)
	}
	if _, ok := readAncestorScheme(dir, 1); !ok {
		t.Fatal("the ancestor must be recorded so the blind spot is one push wide, not permanent")
	}
	// Second push: the ancestor now exists, so the content check runs and the
	// in-sync case is silent again.
	blocked, msg = blockedResult(resolveConflict(e, fp, 1, scheme, false, false, false))
	if blocked || msg != "" {
		t.Fatalf("after the heal an in-sync push must be silent, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestConflict_ChangedBlocksWithImpact(t *testing.T) {
	// server advanced (change_time 300 > baseline 100) and has an extra node C
	conv := map[string]interface{}{
		"change_time":            float64(300),
		"last_confirmed_version": float64(30),
		"commits": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"change_time": float64(300), "nick": "Alice"},
		}},
		"list": []interface{}{
			map[string]interface{}{"obj_type": float64(1), "title": "Start"},
			map[string]interface{}{"obj_type": float64(0), "title": "A"},
			map[string]interface{}{"obj_type": float64(0), "title": "B"},
			map[string]interface{}{"obj_type": float64(0), "title": "C-added-by-other"},
		},
	}
	_, e := mockAPIServer(t, convResp(conv))
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, false))
	if !blocked {
		t.Fatalf("changed server must block, got blocked=%v", blocked)
	}
	for _, want := range []string{"changed on the server", "Alice", "would DELETE", "C-added-by-other", "overwrite_server_change=true"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("conflict report missing %q:\n%s", want, msg)
		}
	}
	// The lint override must never be OFFERED as the way out of a conflict: one
	// flag for both gates is what let a force passed for a lint finding
	// pre-authorise dropping a concurrent change nobody had seen. Naming it in
	// order to say it does NOT apply is the opposite of offering it, and it is
	// what a caller written against the old contract needs to read.
	if !strings.Contains(msg, "force=true does NOT waive this gate") {
		t.Fatalf("the report must state that the lint override does not apply here:\n%s", msg)
	}
	for _, offer := range []string{"pass force=true", "with force=true", "re-run with force", "[2] force=true", "[3] force=true"} {
		if strings.Contains(msg, offer) {
			t.Fatalf("the conflict report must not offer the lint override (%q):\n%s", offer, msg)
		}
	}
}

// overwrite_server_change lets the push through — and hands back the report the
// block would have shown, in the past tense. Returning nothing (the old
// behaviour: one line on stderr) left "deployed successfully" as the entire
// record of a lost update, which an MCP host is free to be the only thing it
// shows.
func TestConflict_OverwriteServerChangeReportsWhatItDropped(t *testing.T) {
	conv := map[string]interface{}{
		"change_time":            float64(300),
		"last_confirmed_version": float64(30),
		"commits": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"change_time": float64(300), "nick": "Alice"},
		}},
		"list": []interface{}{
			map[string]interface{}{"obj_type": float64(1), "title": "Start"},
			map[string]interface{}{"obj_type": float64(0), "title": "C-added-by-other"},
		},
	}
	_, e := mockAPIServer(t, convResp(conv))
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	res := resolveConflict(e, fp, 1, twoNodeLocal, true, false, false)
	if res.action != conflictProceed {
		t.Fatalf("overwrite_server_change must let the push through, got action=%v msg=%q", res.action, res.message)
	}
	if !res.overwroteLiveState || res.waiver != "overwrite_server_change=true" {
		t.Fatalf("the overwrite must be flagged for the snapshot pairing gate, got %+v", res)
	}
	for _, want := range []string{"overwrite_server_change=true was used", "Alice", "DELETED", "C-added-by-other", "recoverable only from the pre-push snapshot"} {
		if !strings.Contains(res.message, want) {
			t.Fatalf("the overwrite record is missing %q:\n%s", want, res.message)
		}
	}
}

// adopt_existing overwrites without any comparison at all, so it is flagged the
// same way — the handler refuses that combined with a missing snapshot.
func TestConflict_AdoptExistingIsFlaggedAsAnOverwrite(t *testing.T) {
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal) // no baseline
	res := resolveConflict(e, fp, 1, twoNodeLocal, false, false, true)
	if res.action != conflictProceed {
		t.Fatalf("adopt_existing must let the push through, got action=%v", res.action)
	}
	if !res.overwroteLiveState || res.waiver != "adopt_existing=true" {
		t.Fatalf("adopt_existing must be flagged as an unreconciled overwrite, got %+v", res)
	}
	if !strings.Contains(res.message, "WITHOUT knowing what it contained") {
		t.Fatalf("the waiver must be reported to the user, got %q", res.message)
	}
}

func TestCommitName_FieldFallbacks(t *testing.T) {
	if got := commitName(map[string]any{"nick": "Bob"}); got != "Bob" {
		t.Fatalf("nick: got %q", got)
	}
	if got := commitName(map[string]any{"user_name": "Ivan K"}); got != "Ivan K" {
		t.Fatalf("user_name: got %q", got)
	}
	if got := commitName(map[string]any{"login": "ik@x"}); got != "ik@x" {
		t.Fatalf("login: got %q", got)
	}
	if got := commitName(map[string]any{"user_id": float64(66423)}); got != "user 66423" {
		t.Fatalf("user_id fallback: got %q", got)
	}
	if got := commitName(map[string]any{}); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestLatestSnapshotAuthor_PicksNewest(t *testing.T) {
	snaps := []Snapshot{
		{UserName: "Old", CreateTime: 100},
		{UserName: "Newest", CreateTime: 300},
		{UserName: "Mid", CreateTime: 200},
	}
	name, when := latestSnapshotAuthor(snaps)
	if name != "Newest" || when != 300 {
		t.Fatalf("expected Newest@300, got %s@%d", name, when)
	}
	if name, _ := latestSnapshotAuthor(nil); name != "" {
		t.Fatalf("empty snapshots must yield no author, got %q", name)
	}
}

func TestFormatConflict_ShowsEditorLine(t *testing.T) {
	report := formatConflict(7, baselineEntry{ChangeTime: 100}, baselineEntry{ChangeTime: 200},
		map[string]any{}, twoNodeLocal, mergePlan{}, false, "Ivan Kondratyuk", 1784210222)
	if !strings.Contains(report, "last changed by: Ivan Kondratyuk (") {
		t.Fatalf("editor line missing:\n%s", report)
	}
}

func TestConflict_DeletedOnServerBlocks(t *testing.T) {
	// GetProcessByID op returns proc != ok with "object not found"
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc": "error", "description": "object not found",
			}},
		}
	})
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, false))
	if !blocked || !strings.Contains(msg, "no longer on the server") {
		t.Fatalf("deleted process must block with a stale hint, got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_ServerUnreachableBlocks pins the P1.1 policy: when a baseline
// exists but the server-state fetch fails (network, 5xx, timeout — anything
// other than a genuine "not found"), the push MUST block. Silently proceeding
// would leave lost-update detection off exactly when the API is degraded, and
// the deploy call that follows would hit the same failing endpoint anyway.
func TestConflict_ServerUnreachableBlocks(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc": "error", "description": "internal server error",
			}},
		}
	})
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false, false))
	if !blocked {
		t.Fatalf("unreachable server + baseline must block, got blocked=%v msg=%q", blocked, msg)
	}
	if !strings.Contains(msg, "could not fetch the current server state") {
		t.Fatalf("block message must name the failure:\n%s", msg)
	}
}

// force=true is about overriding a KNOWN conflict; it must NOT double as
// "server state unknown, proceed anyway". If the fetch failed, we still block.
func TestConflict_ServerUnreachableForceStillBlocks(t *testing.T) {
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc": "error", "description": "internal server error",
			}},
		}
	})
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	blocked, _ := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, true, false, false))
	if !blocked {
		t.Fatalf("force=true must NOT waive the unknown-server-state block")
	}
}

func TestApplyMergeBacksUpExactLocalFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	local := `{"obj_id":1,"description":"mine","scheme":{"nodes":[]}}`
	theirs := `{"obj_id":1,"description":"server","scheme":{"nodes":[]}}`
	if err := os.WriteFile(fp, []byte(local), 0600); err != nil {
		t.Fatal(err)
	}
	plan := buildMergePlan(nil, nil, nil)
	if err := addProcessFields(&plan, local, theirs, local); err != nil {
		t.Fatal(err)
	}

	res := applyMerge(dir, fp, 1, local, baselineEntry{ChangeTime: 200, Version: 20}, theirs, plan, nil, "", 0)
	if res.action != conflictMerged {
		t.Fatalf("merge action = %v, message=%s", res.action, res.message)
	}
	backup, err := os.ReadFile(fp + ".pre-merge")
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != local {
		t.Fatalf("backup changed local content: got %q want %q", backup, local)
	}
	merged, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(merged), `"description": "server"`) {
		t.Fatalf("merged file did not graft server field:\n%s", merged)
	}
	if !strings.Contains(res.message, fp+".pre-merge") {
		t.Fatalf("result must disclose backup path:\n%s", res.message)
	}
	info, err := os.Stat(fp)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("merged file mode = %v, want 0600", info.Mode().Perm())
	}
}

// dualEndpointServer wires up a Corezoid-style mock that answers both the JSON
// RPC ops endpoint (get_process on /json) and the export endpoint (on
// /download → download_url pointing back to /file). Used to exercise the
// equal-timestamp content-diff fallback in resolveConflict.
//
// The mock routes by URL path because that's how the real Executor.req routes:
// export_process goes to /download, everything else to /json.
func dualEndpointServer(t *testing.T, changeTime float64, version float64, schemeJSON string) *Executor {
	t.Helper()
	var srv *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// ExportProcess unmarshals into []any so wrap the scheme in a list.
		_, _ = w.Write([]byte("[" + schemeJSON + "]"))
	})
	handler.HandleFunc("/api/2/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":         "ok",
				"download_url": srv.URL + "/file",
			}},
		})
	})
	handler.HandleFunc("/api/2/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":                   "ok",
				"change_time":            changeTime,
				"last_confirmed_version": version,
			}},
		})
	})
	srv = httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Executor{Ctx: context.Background(), APIUrl: srv.URL, Token: "test-token", NodeIDMap: make(map[string]NodeInfo)}
}

// TestConflict_EqualTimestamp_ListSource_ContentDiffers locks in the safety
// commit that closes the pull-folder equal-timestamp blind spot: a list-sourced
// baseline whose change_time matches the current server's but whose ancestor
// scheme differs from the live server scheme must be treated as a conflict —
// two developers writing in the same second can otherwise silently overwrite
// each other because ListFolder and GetProcessByID versions aren't comparable.
func TestConflict_EqualTimestamp_ListSource_ContentDiffers(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"A"}]}}`
	serverScheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"A"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"C-server-added"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, serverScheme) // same change_time, different (incomparable) version

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if !blocked {
		t.Fatalf("list-sourced base + equal change_time + differing server scheme must block, got blocked=%v msg=%q", blocked, msg)
	}
	if !strings.Contains(msg, "C-server-added") {
		t.Fatalf("block report should surface the server-only node C-server-added:\n%s", msg)
	}
}

// TestConflict_EqualTimestamp_ListSource_ContentSame is the negative case: same
// change_time, list-sourced base, and identical server scheme → nothing has
// actually changed, push must proceed. Guards against the content check turning
// into a false positive on genuinely in-sync pull-folder baselines.
func TestConflict_EqualTimestamp_ListSource_ContentSame(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	scheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"A"}]}}`
	if err := writeAncestorScheme(dir, 1, scheme); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, scheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, scheme, false, false, false))
	if blocked {
		t.Fatalf("list-sourced base + equal change_time + matching server scheme must proceed, got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_EqualTimestamp_LegacyBase_ContentDiffers exercises the same
// safety net for sidecars written before the Source tag existed (Source="").
// The pre-Source-tag baseline can't be distinguished from a list-sourced one at
// read time; both must fall through to the ancestor content check.
func TestConflict_EqualTimestamp_LegacyBase_ContentDiffers(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"}]}}`
	serverScheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"dddddddddddddddddddddddd","obj_type":0,"title":"D-server-added"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	// Note: Source omitted — simulates a legacy sidecar.
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 10}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 11, serverScheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if !blocked {
		t.Fatalf("legacy baseline + equal change_time + differing server scheme must block, got blocked=%v msg=%q", blocked, msg)
	}
	if !strings.Contains(msg, "D-server-added") {
		t.Fatalf("block report should surface the server-only node D-server-added:\n%s", msg)
	}
}

// TestConflict_EqualTimestamp_DuplicateTitle_ServerEditsFirst covers the
// duplicate-title blind spot in the equal-timestamp content check: when two
// nodes share a title, canonicalizeNodes keeps only the first occurrence's body
// and flags the key as Ambiguous. If the server edits that first occurrence,
// the classifier can still return clsConflict (via the ambiguous-and-changed
// path) — the check must count that as a real change, not silently pass.
func TestConflict_EqualTimestamp_DuplicateTitle_ServerEditsFirst(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"first-original"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"second"}]}}`
	// Server changed the FIRST "Payment" node's description.
	serverScheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"first-edited-by-server"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"second"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, serverScheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if !blocked {
		t.Fatalf("duplicate title + server edit on first occurrence must block, got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_EqualTimestamp_DuplicateTitle_ServerEditsSecond covers the
// harder half of the same blind spot: canonicalizeNodes keeps only the first
// duplicate's body, so a change to the SECOND duplicate would classify as
// clsUnchanged if we relied on class alone. Guarded by the Ambiguous flag —
// any duplicate title on either side means we can't prove in-sync and must
// treat the state as changed.
func TestConflict_EqualTimestamp_DuplicateTitle_ServerEditsSecond(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"first"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"second-original"}]}}`
	// Server changed only the SECOND "Payment" — canonicalization would hide it.
	serverScheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"first"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"second-edited-by-server"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, serverScheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if !blocked {
		t.Fatalf("duplicate title + server edit on second occurrence must block (Ambiguous safeguard), got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_EqualTimestamp_DuplicateTitle_InSync_Proceeds is the negative
// half: duplicate titles alone must not turn every equal-timestamp pull-folder
// baseline into a false-positive block. When the ancestor and server scheme
// are semantically identical (same multiset of canonical bodies), push must
// proceed even though canonicalizeNodes flags the duplicate key as Ambiguous.
func TestConflict_EqualTimestamp_DuplicateTitle_InSync_Proceeds(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	scheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"first"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"second"}]}}`
	if err := writeAncestorScheme(dir, 1, scheme); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, scheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, scheme, false, false, false))
	if blocked {
		t.Fatalf("duplicate titles with an in-sync server must not block, got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_EqualTimestamp_DuplicateTitle_ServerAddsThird covers a variant
// of the duplicate-title change: the server added ANOTHER duplicate instead of
// editing one. Canonicalizing by title collapses all three into one key, so the
// per-key merge classifier can't see the addition. The whole-scheme multiset
// compare catches it because the node counts diverge.
func TestConflict_EqualTimestamp_DuplicateTitle_ServerAddsThird(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"one"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"two"}]}}`
	serverScheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"one"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"two"},
	 {"id":"dddddddddddddddddddddddd","obj_type":0,"title":"Payment","description":"three-added"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, serverScheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if !blocked {
		t.Fatalf("added third duplicate must block, got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_EqualTimestamp_DuplicateTitle_ReorderedProceeds is the strictest
// negative case for the multiset compare: the server reshuffled duplicates but
// the semantic content is identical. Order-agnostic multiset compare (sorted
// canonical bodies) must accept this as in-sync.
func TestConflict_EqualTimestamp_DuplicateTitle_ReorderedProceeds(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"one"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"two"}]}}`
	// Server swapped the two Payment nodes' storage order but bodies are identical.
	serverScheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"Payment","description":"two"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"Payment","description":"one"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, serverScheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if blocked {
		t.Fatalf("duplicate-title reorder with identical bodies must proceed, got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_EqualTimestamp_ProcessFieldChangeBlocks makes sure the fallback
// also catches process-level field drift (description, obj_type flags, etc.),
// not just node changes. addProcessFields writes those into plan.FieldGrafts,
// which the check surfaces the same way as a node-side change.
func TestConflict_EqualTimestamp_ProcessFieldChangeBlocks(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"description":"orig","scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"}]}}`
	// Same nodes, but the process description was changed on the server.
	serverScheme := `{"obj_id":1,"description":"server-edited","scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 999, serverScheme)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if !blocked {
		t.Fatalf("process-level field change must block, got blocked=%v msg=%q", blocked, msg)
	}
}

// TestConflict_EqualTimestamp_ExportFailure_Blocks locks in the fail-closed
// behaviour: if the equal-timestamp content diff can't run because the export
// endpoint fails, a same-second concurrent overwrite cannot be ruled out.
// Older behaviour warn-and-proceeded here; that was fail-open. Now we block
// unless the user explicitly forces.
func TestConflict_EqualTimestamp_ExportFailure_Blocks(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 42, Source: baselineSourceList}); err != nil {
		t.Fatal(err)
	}
	// Handler that answers get_process but NOT download → export fails.
	handler := http.NewServeMux()
	handler.HandleFunc("/api/2/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc":                   "ok",
				"change_time":            float64(100),
				"last_confirmed_version": float64(999),
			}},
		})
	})
	handler.HandleFunc("/api/2/download", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "export unavailable", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	e := &Executor{Ctx: context.Background(), APIUrl: srv.URL, Token: "test-token", NodeIDMap: make(map[string]NodeInfo)}

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false, false))
	if !blocked {
		t.Fatalf("export failure at equal timestamp must block, got blocked=%v msg=%q", blocked, msg)
	}
	if !strings.Contains(msg, "export or parse failed") || !strings.Contains(msg, "overwrite_server_change=true") {
		t.Fatalf("block message should name the failed content diff and the flag that waives it:\n%s", msg)
	}
	// overwrite_server_change=true overrides the block — the fallback safety net
	// is optional — but the waiver is reported, not swallowed.
	res := resolveConflict(e, fp, 1, ancestor, true, false, false)
	if res.action == conflictBlock {
		t.Fatalf("overwrite_server_change=true must override the export-failure block")
	}
	if !strings.Contains(res.message, "could not complete") {
		t.Fatalf("the waived check must be reported, got %q", res.message)
	}
	// The comparison did not run, so this counts as an unreconciled overwrite:
	// the handler must still refuse it when no rollback point exists.
	if !res.overwroteLiveState || res.waiver != "overwrite_server_change=true" {
		t.Fatalf("a waived content check is an unreconciled overwrite and must be flagged, got %+v", res)
	}
}

// TestConflict_EqualTimestamp_NoAncestor_LegacyProceeds preserves the
// legacy-compat path: a sidecar written before v3.1.3 has no ancestor scheme,
// so the content diff can't run. Treating that as a hard block would regress
// every pre-upgrade file until the user re-pulls — equal timestamps are the
// ordinary in-sync case. Proceed, report, and record the ancestor
// (TestConflict_LegacyBaselineNoAncestorSelfHeals covers the repair).
func TestConflict_EqualTimestamp_NoAncestor_LegacyProceeds(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	local := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"}]}}`
	// No writeAncestorScheme — legacy pre-v3.1.3 sidecar.
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 10}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 100, 10, local)

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, local, false, false, false))
	if blocked {
		t.Fatalf("legacy sidecar without ancestor must proceed (warn only), got blocked=%v msg=%q", blocked, msg)
	}
}

// TestApplyMerge_BaselineWriteFailureDoesNotClaimClean locks in the issue #151
// fix: when applyMerge lands a conflict-free merge but the baseline sidecar
// can't be updated (here: pre-existing corrupt sidecar the non-recovery writer
// refuses to overwrite), the message must NOT promise "proceed cleanly".
// Otherwise the user would push and get the same conflict re-reported, or —
// worse — assume the concurrency state is fresh when it isn't.
func TestApplyMerge_BaselineWriteFailureDoesNotClaimClean(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	local := `{"obj_id":1,"description":"mine","scheme":{"nodes":[]}}`
	theirs := `{"obj_id":1,"description":"server","scheme":{"nodes":[]}}`
	if err := os.WriteFile(fp, []byte(local), 0600); err != nil {
		t.Fatal(err)
	}
	// Corrupt baseline sidecar → writeBaseline (non-recovery path) will fail.
	if err := os.WriteFile(filepath.Join(dir, baselineFileName), []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := buildMergePlan(nil, nil, nil)
	if err := addProcessFields(&plan, local, theirs, local); err != nil {
		t.Fatal(err)
	}

	res := applyMerge(dir, fp, 1, local, baselineEntry{ChangeTime: 200, Version: 20, Source: baselineSourceDetail}, theirs, plan, nil, "", 0)
	if res.action != conflictMerged {
		t.Fatalf("merge action = %v, message=%s", res.action, res.message)
	}
	if strings.Contains(res.message, "proceed cleanly") {
		t.Fatalf("message must NOT claim 'proceed cleanly' when baseline write failed (#151):\n%s", res.message)
	}
	if !strings.Contains(res.message, "baseline") || !strings.Contains(res.message, "could not be updated") {
		t.Fatalf("message must surface the baseline write failure (#151):\n%s", res.message)
	}
}

// With a 3-way ancestor available, the overwrite record itemises what the push
// threw away. It must NOT reuse the block report's renderer: that one is written
// for a decision still to be taken ("what this push would commit", "no overlap,
// mergeable"), and those words in the record of a completed overwrite read as if
// the server's changes were still on offer.
func TestConflict_OverwriteRecordItemisesDroppedServerChanges(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	ancestor := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"A"}]}}`
	serverScheme := `{"obj_id":1,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaaa","obj_type":1,"title":"Start"},
	 {"id":"bbbbbbbbbbbbbbbbbbbbbbbb","obj_type":0,"title":"A"},
	 {"id":"cccccccccccccccccccccccc","obj_type":0,"title":"C-server-added"}]}}`
	if err := writeAncestorScheme(dir, 1, ancestor); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 1, baselineEntry{ChangeTime: 100, Version: 10, Source: baselineSourceDetail}); err != nil {
		t.Fatal(err)
	}
	e := dualEndpointServer(t, 300, 30, serverScheme) // server moved past the baseline

	res := resolveConflict(e, fp, 1, ancestor, true, false, false)
	if res.action != conflictProceed || !res.overwroteLiveState {
		t.Fatalf("overwrite_server_change must proceed and flag the overwrite, got %+v", res)
	}
	for _, want := range []string{"Server changes DROPPED by this push", "C-server-added"} {
		if !strings.Contains(res.message, want) {
			t.Fatalf("the overwrite record must itemise %q:\n%s", want, res.message)
		}
	}
	for _, unwanted := range []string{"mergeable", "would commit", "Choose one"} {
		if strings.Contains(res.message, unwanted) {
			t.Fatalf("the overwrite record must not offer %q — the push already happened:\n%s", unwanted, res.message)
		}
	}
}
