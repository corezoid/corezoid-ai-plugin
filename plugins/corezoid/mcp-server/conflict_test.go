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

func TestConflict_NoBaselineIsAdvisory(t *testing.T) {
	_, e := mockAPIServer(t, convResp(map[string]interface{}{"change_time": float64(200)}))
	_, fp := setupConflict(t, baselineEntry{}, twoNodeLocal) // no baseline written
	blocked, msg := conflictCheck(e, fp, 1, twoNodeLocal, false)
	if blocked || !strings.Contains(msg, "no pull baseline") {
		t.Fatalf("no baseline must be advisory, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestConflict_InSyncProceeds(t *testing.T) {
	conv := map[string]interface{}{"change_time": float64(100), "last_confirmed_version": float64(10)}
	_, e := mockAPIServer(t, convResp(conv))
	_, fp := setupConflict(t, baselineEntry{ChangeTime: 100, Version: 10}, twoNodeLocal)
	blocked, msg := conflictCheck(e, fp, 1, twoNodeLocal, false)
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
	blocked, msg := conflictCheck(e, fp, 1, twoNodeLocal, false)
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
	blocked, msg := conflictCheck(e, fp, 1, twoNodeLocal, true)
	if blocked || msg != "" {
		t.Fatalf("force must override the conflict, got blocked=%v msg=%q", blocked, msg)
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
	blocked, msg := conflictCheck(e, fp, 1, twoNodeLocal, false)
	if !blocked || !strings.Contains(msg, "no longer on the server") {
		t.Fatalf("deleted process must block with a stale hint, got blocked=%v msg=%q", blocked, msg)
	}
}
