package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBaseline_ReadMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if m := readBaselines(dir); len(m) != 0 {
		t.Fatalf("missing sidecar must read as empty, got %v", m)
	}
	if _, ok := lookupBaseline(dir, 123); ok {
		t.Fatalf("missing sidecar must have no entry")
	}
	// corrupt content → empty, no panic
	if err := os.WriteFile(filepath.Join(dir, baselineFileName), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if m := readBaselines(dir); len(m) != 0 {
		t.Fatalf("corrupt sidecar must read as empty, got %v", m)
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
	e1, ok1 := lookupBaseline(dir, 111)
	e2, ok2 := lookupBaseline(dir, 222)
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

func TestCaptureFolderBaselines(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "10_a.conv.json"), `{"obj_id":10,"scheme":{"nodes":[]}}`)
	mustWrite(t, filepath.Join(sub, "20_b.conv.json"), `{"obj_id":20,"scheme":{"nodes":[]}}`)
	mustWrite(t, filepath.Join(dir, "notes.txt"), `not a process`)

	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		id, _ := ops[0]["obj_id"].(float64)
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "change_time": id * 10, "last_confirmed_version": id},
		}}
	})
	if n := captureFolderBaselines(e, dir); n != 2 {
		t.Fatalf("expected 2 baselines recorded, got %d", n)
	}
	e10, ok10 := lookupBaseline(dir, 10)
	e20, ok20 := lookupBaseline(sub, 20)
	if !ok10 || e10.ChangeTime != 100 || e10.Version != 10 {
		t.Fatalf("process 10 baseline wrong: %+v ok=%v", e10, ok10)
	}
	if !ok20 || e20.ChangeTime != 200 || e20.Version != 20 {
		t.Fatalf("process 20 baseline (subfolder) wrong: %+v ok=%v", e20, ok20)
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
