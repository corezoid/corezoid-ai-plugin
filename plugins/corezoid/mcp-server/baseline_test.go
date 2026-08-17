package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBaseline_ReadMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if m, err := readBaselines(dir); err != nil || len(m) != 0 {
		t.Fatalf("missing sidecar must read as empty, got %v", m)
	}
	if _, ok, err := lookupBaseline(dir, 123); err != nil || ok {
		t.Fatalf("missing sidecar must have no entry")
	}
	// Corrupt content must fail closed, not silently disable the gate.
	if err := os.WriteFile(filepath.Join(dir, baselineFileName), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readBaselines(dir); err == nil {
		t.Fatal("corrupt sidecar must return an error")
	}
	if err := writeBaseline(dir, 123, baselineEntry{Version: 1}); err == nil {
		t.Fatal("write must not overwrite a corrupt sidecar")
	}
	if err := writePulledBaseline(dir, 123, baselineEntry{ChangeTime: 10, Version: 1}); err != nil {
		t.Fatalf("pull recovery failed: %v", err)
	}
	if got, ok, err := lookupBaseline(dir, 123); err != nil || !ok || got.Version != 1 {
		t.Fatalf("recovered baseline = %+v ok=%v err=%v", got, ok, err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, baselineFileName+".corrupt-*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("corrupt baseline backup = %v err=%v, want one", backups, err)
	}
	old, err := os.ReadFile(backups[0])
	if err != nil || string(old) != "{not json" {
		t.Fatalf("corrupt backup content = %q err=%v", old, err)
	}
}

func TestBaseline_WriteUpsertPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	if err := writeBaseline(dir, 111, baselineEntry{ChangeTime: 100, Version: 10}); err != nil {
		t.Fatal(err)
	}
	if err := writeBaseline(dir, 222, baselineEntry{ChangeTime: 200, Version: 20}); err != nil {
		t.Fatal(err)
	}
	// re-upsert 111; 222 must survive
	if err := writeBaseline(dir, 111, baselineEntry{ChangeTime: 150, Version: 15}); err != nil {
		t.Fatal(err)
	}
	e1, ok1, err1 := lookupBaseline(dir, 111)
	e2, ok2, err2 := lookupBaseline(dir, 222)
	if err1 != nil || err2 != nil {
		t.Fatalf("lookup errors: %v / %v", err1, err2)
	}
	if !ok1 || e1.ChangeTime != 150 || e1.Version != 15 {
		t.Fatalf("111 upsert wrong: %+v ok=%v", e1, ok1)
	}
	if !ok2 || e2.ChangeTime != 200 || e2.Version != 20 {
		t.Fatalf("222 must be preserved: %+v ok=%v", e2, ok2)
	}
}

func TestBaseline_FromServer(t *testing.T) {
	// prefers last_confirmed_version
	e := baselineFromServer(map[string]any{
		"change_time":            float64(1783964930),
		"last_confirmed_version": float64(1783964913),
		"commits":                map[string]any{"version": float64(1783965047)},
	})
	if e.ChangeTime != 1783964930 || e.Version != 1783964913 {
		t.Fatalf("expected change_time + last_confirmed_version, got %+v", e)
	}
	// falls back to commits.version when last_confirmed_version absent
	e2 := baselineFromServer(map[string]any{
		"change_time": float64(500),
		"commits":     map[string]any{"version": float64(600)},
	})
	if e2.ChangeTime != 500 || e2.Version != 600 {
		t.Fatalf("expected commits.version fallback, got %+v", e2)
	}
}

func TestRecordPulledBaselinesUsesPreExportSnapshot(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "10_a.conv.json"), `{"obj_id":10,"scheme":{"nodes":[]}}`)
	mustWrite(t, filepath.Join(sub, "20_b.conv.json"), `{"obj_id":20,"scheme":{"nodes":[]}}`)
	mustWrite(t, filepath.Join(dir, "notes.txt"), `not a process`)

	snapshot := map[int]baselineEntry{
		10: {ChangeTime: 100, Version: 10},
		20: {ChangeTime: 200, Version: 20},
	}
	if n := recordPulledBaselines(dir, snapshot); n != 2 {
		t.Fatalf("expected 2 baselines recorded, got %d", n)
	}
	e10, ok10, err10 := lookupBaseline(dir, 10)
	e20, ok20, err20 := lookupBaseline(sub, 20)
	if err10 != nil || err20 != nil {
		t.Fatalf("lookup errors: %v / %v", err10, err20)
	}
	if !ok10 || e10.ChangeTime != 100 || e10.Version != 10 {
		t.Fatalf("process 10 baseline wrong: %+v ok=%v", e10, ok10)
	}
	if !ok20 || e20.ChangeTime != 200 || e20.Version != 20 {
		t.Fatalf("process 20 baseline (subfolder) wrong: %+v ok=%v", e20, ok20)
	}
}

// A pull-folder walking the whole stage root must not touch a locally-edited
// file that belongs to a *different* folder (i.e. not in this export's
// pre-snapshot). Rewriting its ancestor with the local WIP content would make
// a later 3-way merge see base == mine and silently drop the local edits.
func TestRecordPulledBaselinesSkipsFilesOutsideSnapshot(t *testing.T) {
	dir := t.TempDir()
	otherFolder := filepath.Join(dir, "other-folder")
	if err := os.MkdirAll(otherFolder, 0755); err != nil {
		t.Fatal(err)
	}

	// Process 10 was actually pulled by this export.
	mustWrite(t, filepath.Join(dir, "10_a.conv.json"), `{"obj_id":10,"scheme":{"nodes":[]}}`)

	// Process 30 lives in an unrelated folder, was edited locally, and has a
	// pre-existing ancestor from an earlier real pull.
	localWIP := `{"obj_id":30,"scheme":{"nodes":[{"local":"wip"}]}}`
	serverTruth := `{"obj_id":30,"scheme":{"nodes":[]}}`
	mustWrite(t, filepath.Join(otherFolder, "30_c.conv.json"), localWIP)
	if err := writeAncestorScheme(otherFolder, 30, serverTruth); err != nil {
		t.Fatal(err)
	}

	// This export's snapshot contains only process 10.
	snapshot := map[int]baselineEntry{
		10: {ChangeTime: 100, Version: 10},
	}
	if n := recordPulledBaselines(dir, snapshot); n != 1 {
		t.Fatalf("expected 1 baseline recorded (only process 10), got %d", n)
	}

	// Process 30's ancestor must NOT have been overwritten with the local WIP.
	got, ok := readAncestorScheme(otherFolder, 30)
	if !ok {
		t.Fatal("process 30 ancestor was deleted, expected preserved")
	}
	if got != serverTruth {
		t.Fatalf("process 30 ancestor was rewritten with local content:\ngot:  %s\nwant: %s", got, serverTruth)
	}

	// And no baseline should have been written for it.
	if _, ok, err := lookupBaseline(otherFolder, 30); err != nil || ok {
		t.Fatalf("process 30 must have no baseline, got ok=%v err=%v", ok, err)
	}
}

func TestCaptureFolderBaselineSnapshotRecursesBeforeExport(t *testing.T) {
	// After the pull-folder baseline refactor, list-folder responses already
	// carry change_time per conv, so no follow-up GetProcessByID is made per
	// process. The wire log must show ONLY folder-list calls — one per folder.
	var calls []string
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		obj, _ := op["obj"].(string)
		id := int(op["obj_id"].(float64))
		calls = append(calls, fmt.Sprintf("%s:%d", obj, id))
		if obj != "folder" {
			t.Fatalf("unexpected non-folder call: %s:%d — pull-folder baseline should reuse list-folder responses, not fetch per-process", obj, id)
		}
		var list []interface{}
		switch id {
		case 1:
			list = []interface{}{
				map[string]interface{}{"obj": "conv", "obj_id": float64(10), "change_time": float64(100), "version": float64(11)},
				map[string]interface{}{"obj": "folder", "obj_id": float64(2)},
			}
		case 2:
			list = []interface{}{map[string]interface{}{"obj": "conv", "obj_id": float64(20), "change_time": float64(200), "is_deployed": true}}
		}
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "list": list},
		}}
	})

	got := captureFolderBaselineSnapshot(e, 1)
	if len(got) != 2 || got[10].ChangeTime != 100 || got[10].Version != 11 || got[20].ChangeTime != 200 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	want := []string{"folder:1", "folder:2"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("wire order = %v, want %v (no per-process GetProcessByID)", calls, want)
	}
}

// TestCaptureWorkspaceBaselineSnapshotUsesRootList pins the No-Project pull
// path: convs living at the workspace root take their baseline from the
// list-workspace-root response directly (like folder children do from
// list-folder), and folders under the root recurse through list-folder. No
// per-process GetProcessByID.
func TestCaptureWorkspaceBaselineSnapshotUsesRootList(t *testing.T) {
	var calls []string
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		id, _ := op["obj_id"].(float64)
		calls = append(calls, fmt.Sprintf("%s:%s:%d", typ, obj, int(id)))
		if typ != "list" || obj != "folder" {
			t.Fatalf("unexpected call %s:%s:%d — root/folder listing only", typ, obj, int(id))
		}
		if int(id) == 0 { // workspace root
			return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
				map[string]interface{}{"proc": "ok", "list": []interface{}{
					map[string]interface{}{"obj_type": "conv", "obj_id": float64(11), "change_time": float64(111), "version": float64(1), "conv_type": "process"},
					map[string]interface{}{"obj_type": "folder", "obj_id": float64(5), "conv_type": ""},
				}},
			}}
		}
		// folder 5
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "list": []interface{}{
				map[string]interface{}{"obj": "conv", "obj_id": float64(22), "change_time": float64(222)},
			}},
		}}
	})

	got := captureWorkspaceBaselineSnapshot(e)
	if len(got) != 2 || got[11].ChangeTime != 111 || got[11].Version != 1 || got[22].ChangeTime != 222 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
}

func TestBaseline_ConcurrentWritersPreserveEveryEntry(t *testing.T) {
	dir := t.TempDir()
	const writers = 24
	var wg sync.WaitGroup
	for i := 1; i <= writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := writeBaseline(dir, i, baselineEntry{ChangeTime: int64(i), Version: int64(i)}); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	m, err := readBaselines(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != writers {
		t.Fatalf("concurrent writes kept %d entries, want %d", len(m), writers)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBaseline_ServerMovedSince(t *testing.T) {
	// change_time is authoritative regardless of source.
	base := baselineEntry{ChangeTime: 100, Version: 10, Source: baselineSourceDetail}
	current := baselineEntry{ChangeTime: 100, Version: 10, Source: baselineSourceDetail}
	if serverMovedSince(base, current) {
		t.Fatal("identical baseline must not be flagged as moved")
	}
	if !serverMovedSince(base, baselineEntry{ChangeTime: 101, Version: 10, Source: baselineSourceDetail}) {
		t.Fatal("advanced change_time must be flagged")
	}
	if serverMovedSince(base, baselineEntry{ChangeTime: 99, Version: 10, Source: baselineSourceDetail}) {
		t.Fatal("older change_time must not be flagged as moved")
	}
}

// TestBaseline_ServerMovedSince_SameSecondTiebreak locks in the pull-process
// regression fix for issue #152: when both baseline and current came from
// GetProcessByID (Source=detail), a same-second concurrent commit still
// advances commits.version and must be caught as a conflict.
func TestBaseline_ServerMovedSince_SameSecondTiebreak(t *testing.T) {
	base := baselineEntry{ChangeTime: 100, Version: 10, Source: baselineSourceDetail}
	current := baselineEntry{ChangeTime: 100, Version: 11, Source: baselineSourceDetail}
	if !serverMovedSince(base, current) {
		t.Fatal("same change_time + advanced version from detail-sourced baseline must be flagged (issue #152)")
	}
	// Detail-sourced version going backwards is a server anomaly, not a conflict signal.
	older := baselineEntry{ChangeTime: 100, Version: 9, Source: baselineSourceDetail}
	if serverMovedSince(base, older) {
		t.Fatal("version going backwards on equal change_time must not be flagged as moved")
	}
}

// TestBaseline_ServerMovedSince_ListSourceNoTiebreak asserts the pull-folder
// path stays free of the mixed-semantics false positive: ListFolder's
// "version" is a draft timestamp, not comparable to commits.version, so a
// list-sourced baseline skips the tiebreak and accepts the sub-second blind
// spot rather than block on non-changes.
func TestBaseline_ServerMovedSince_ListSourceNoTiebreak(t *testing.T) {
	base := baselineEntry{ChangeTime: 100, Version: 10, Source: baselineSourceList}
	current := baselineEntry{ChangeTime: 100, Version: 999, Source: baselineSourceDetail}
	if serverMovedSince(base, current) {
		t.Fatal("list-sourced baseline must not tiebreak on version (mixed semantics)")
	}
}

// TestBaseline_ServerMovedSince_LegacySourceMissing asserts that a sidecar
// written before the Source field existed (Source="") does NOT tiebreak on
// version. We can't tell whether the entry was list- or detail-sourced, so
// the safe default is no tiebreak.
func TestBaseline_ServerMovedSince_LegacySourceMissing(t *testing.T) {
	base := baselineEntry{ChangeTime: 100, Version: 10} // no Source
	current := baselineEntry{ChangeTime: 100, Version: 11, Source: baselineSourceDetail}
	if serverMovedSince(base, current) {
		t.Fatal("legacy baseline (no Source) must not tiebreak on version")
	}
}
