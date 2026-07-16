package main

import (
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
