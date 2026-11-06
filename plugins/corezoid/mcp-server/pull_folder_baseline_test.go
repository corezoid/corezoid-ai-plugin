package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #259: a process fresh from pull-folder was blocked on push as a file
// with no baseline. Exported process files do not always carry a numeric
// obj_id; push-process then keys the baseline by the id in the file name
// (fixStruct fills obj_id from it), while recordPulledBaselines read obj_id
// only and recorded nothing — "0 baseline(s) recorded".
func TestPullFolder_BaselineForFileWithoutObjID(t *testing.T) {
	dest := t.TempDir()
	e := stageExportServer(t, stageArchiveWith(t, map[string]string{
		"100_orders.folder/558_noid.conv.json": `{"title":"no id","scheme":{"nodes":[{"id":"n3"}]}}`,
	}))
	pulled, err := downloadStageRecursively(e, 42, dest)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	snapshot := map[int]baselineEntry{
		555: {ChangeTime: 100, Source: baselineSourceList},
		556: {ChangeTime: 100, Source: baselineSourceList},
		558: {ChangeTime: 100, Source: baselineSourceList},
	}
	rec := recordPulledBaselines(dest, snapshot, pulled)
	if rec.recorded != 3 || len(rec.failed) != 0 {
		t.Fatalf("recorded=%d failed=%v, want 3 baselines and no failures", rec.recorded, rec.failed)
	}
	orders := filepath.Join(dest, "100_orders")
	if got, ok, err := lookupBaseline(orders, 558); err != nil || !ok || got.ChangeTime != 100 {
		t.Fatalf("baseline for the obj_id-less file = %+v, %v, %v; want the snapshot entry", got, ok, err)
	}

	// The push of that file must pass the gate, not report a missing baseline.
	fp := filepath.Join(orders, "558_noid.conv.json")
	raw, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := fixStruct(string(raw), 558)
	id := extractObjIDFromJSON(local)
	if id != 558 {
		t.Fatalf("push would key the gate by %d, want 558", id)
	}
	srv := dualEndpointServer(t, 100, 7, local)
	res := resolveConflict(srv, fp, id, local, false, false, false)
	if res.action != conflictProceed {
		t.Fatalf("push of a freshly pulled process must not block, got %q", res.message)
	}
}

// A process the pre-export list did not report (the list call failed, or
// missed it) used to get no baseline, so every push of it was blocked. It now
// gets a content-only baseline: its pulled file is the ancestor, and the push
// compares that with the live scheme.
func TestPullFolder_ContentOnlyBaselineWhenSnapshotMissesProcess(t *testing.T) {
	dest := t.TempDir()
	e := stageExportServer(t, stageArchive(t))
	pulled, err := downloadStageRecursively(e, 42, dest)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if !pulled.written[555] || !pulled.written[556] {
		t.Fatalf("written = %v, want 555 and 556", pulled.written)
	}
	// Empty snapshot: the pre-export list returned nothing.
	rec := recordPulledBaselines(dest, map[int]baselineEntry{}, pulled)
	if rec.recorded != 2 || len(rec.contentOnly) != 2 || len(rec.failed) != 0 {
		t.Fatalf("got %+v, want 2 content-only baselines", rec)
	}
	if s := baselineSummary(rec); !strings.Contains(s, "2 baseline(s) recorded") || !strings.Contains(s, "without a pre-export server version") {
		t.Errorf("summary must say which baselines lack a version: %q", s)
	}

	orders := filepath.Join(dest, "100_orders")
	fp := filepath.Join(orders, "555_proc.conv.json")
	raw, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	pulledJSON := string(raw)

	// Server still holds what was pulled: the push proceeds.
	res := resolveConflict(dualEndpointServer(t, 300, 30, pulledJSON), fp, 555, pulledJSON, false, false, false)
	if res.action != conflictProceed || res.overwroteLiveState {
		t.Fatalf("unchanged server must let the push through, got action=%v msg=%q", res.action, res.message)
	}
	if !strings.Contains(res.message, "pulled without a server version") {
		t.Errorf("the push must say how it verified, got %q", res.message)
	}

	// Someone changed the process since: the push is a conflict, not a blind overwrite.
	changed := `{"obj_id":555,"scheme":{"nodes":[{"id":"n1"},{"id":"dddddddddddddddddddddddd","obj_type":0,"title":"D-server-added"}]}}`
	blocked, msg := blockedResult(resolveConflict(dualEndpointServer(t, 300, 31, changed), fp, 555, pulledJSON, false, false, false))
	if !blocked || !strings.Contains(msg, "changed on the server") {
		t.Fatalf("a server change must block as a conflict, got blocked=%v msg=%q", blocked, msg)
	}
}

// A file the pull did not write and the snapshot does not list still gets no
// baseline: it may be a locally edited process from another folder.
func TestPullFolder_NoContentOnlyBaselineForUnwrittenFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "30_local.conv.json"), `{"obj_id":30,"scheme":{"nodes":[{"local":"wip"}]}}`)
	rec := recordPulledBaselines(dir, map[int]baselineEntry{}, pullResult{written: map[int]bool{31: true}})
	if rec.recorded != 0 {
		t.Fatalf("recorded %d, want 0", rec.recorded)
	}
	if _, ok, _ := lookupBaseline(dir, 30); ok {
		t.Fatal("an unrelated local file must not get a baseline")
	}
	if len(rec.failed) != 1 || rec.failed[0] != 31 {
		t.Fatalf("failed = %v, want [31] (written but never found)", rec.failed)
	}
	if note := baselineFailureNote(rec); !strings.Contains(note, "31") || !strings.Contains(note, "BEFORE editing") {
		t.Errorf("missing baselines must be reported with a way out, got %q", note)
	}
}
