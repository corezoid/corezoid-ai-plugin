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

func TestConflict_NoBaselineIsAdvisory(t *testing.T) {
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal) // no baseline written
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false))
	if blocked || !strings.Contains(msg, "no pull baseline") {
		t.Fatalf("no baseline must be advisory, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestConflict_CorruptBaselineBlocks(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "1_x.conv.json")
	if err := os.WriteFile(filepath.Join(dir, baselineFileName), []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, true, false))
	if !blocked || !strings.Contains(msg, "baseline") || !strings.Contains(msg, "unreadable") {
		t.Fatalf("corrupt baseline must fail closed even with force=true, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestConflict_InSyncProceeds(t *testing.T) {
	conv := map[string]interface{}{"change_time": float64(100), "last_confirmed_version": float64(10)}
	_, e := mockAPIServer(t, convResp(conv))
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false))
	if blocked || msg != "" {
		t.Fatalf("in-sync must proceed silently, got blocked=%v msg=%q", blocked, msg)
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
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false))
	if !blocked {
		t.Fatalf("changed server must block, got blocked=%v", blocked)
	}
	for _, want := range []string{"changed on the server", "Alice", "would DELETE", "C-added-by-other", "force=true"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("conflict report missing %q:\n%s", want, msg)
		}
	}
}

func TestConflict_ForceOverrides(t *testing.T) {
	conv := map[string]interface{}{"change_time": float64(300), "last_confirmed_version": float64(30)}
	_, e := mockAPIServer(t, convResp(conv))
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, true, false))
	if blocked || msg != "" {
		t.Fatalf("force must override the conflict, got blocked=%v msg=%q", blocked, msg)
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
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false))
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
	blocked, msg := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, false, false))
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
	blocked, _ := blockedResult(resolveConflict(e, fp, 1, twoNodeLocal, true, false))
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

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false))
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

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, scheme, false, false))
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

	blocked, msg := blockedResult(resolveConflict(e, fp, 1, ancestor, false, false))
	if !blocked {
		t.Fatalf("legacy baseline + equal change_time + differing server scheme must block, got blocked=%v msg=%q", blocked, msg)
	}
	if !strings.Contains(msg, "D-server-added") {
		t.Fatalf("block report should surface the server-only node D-server-added:\n%s", msg)
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
