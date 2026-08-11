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

func TestCaptureFolderBaselineSnapshotRecursesBeforeExport(t *testing.T) {
	var calls []string
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		obj, _ := op["obj"].(string)
		id := int(op["obj_id"].(float64))
		calls = append(calls, fmt.Sprintf("%s:%d", obj, id))
		if obj == "folder" {
			var list []interface{}
			switch id {
			case 1:
				list = []interface{}{
					map[string]interface{}{"obj": "conv", "obj_id": float64(10)},
					map[string]interface{}{"obj": "folder", "obj_id": float64(2)},
				}
			case 2:
				list = []interface{}{map[string]interface{}{"obj": "conv", "obj_id": float64(20)}}
			}
			return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
				map[string]interface{}{"proc": "ok", "list": list},
			}}
		}
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "change_time": float64(id * 10), "last_confirmed_version": float64(id)},
		}}
	})

	got := captureFolderBaselineSnapshot(e, 1)
	if len(got) != 2 || got[10].ChangeTime != 100 || got[20].Version != 20 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	want := []string{"folder:1", "conv:10", "folder:2", "conv:20"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("wire order = %v, want %v", calls, want)
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
	base := baselineEntry{ChangeTime: 100, Version: 10}
	if serverMovedSince(base, baselineEntry{ChangeTime: 100, Version: 10}) {
		t.Fatal("identical baseline must not be flagged as moved")
	}
	if !serverMovedSince(base, baselineEntry{ChangeTime: 101, Version: 10}) {
		t.Fatal("advanced change_time must be flagged")
	}
	// same second, different version (tiebreak)
	if !serverMovedSince(base, baselineEntry{ChangeTime: 100, Version: 11}) {
		t.Fatal("same change_time but different version must be flagged")
	}
}
