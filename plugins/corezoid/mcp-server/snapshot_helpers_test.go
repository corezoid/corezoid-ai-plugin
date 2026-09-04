package main

import (
	"context"
	"strings"
	"testing"
)

// --- extractObjIDFromJSON ---

func TestExtractObjIDFromJSON_ExistingProcess(t *testing.T) {
	json := `{"obj_type":1,"obj_id":379055,"title":"Escalation"}`
	if got := extractObjIDFromJSON(json); got != 379055 {
		t.Errorf("expected 379055, got %d", got)
	}
}

func TestExtractObjIDFromJSON_NewProcess(t *testing.T) {
	json := `{"obj_type":1,"obj_id":null,"title":"New"}`
	if got := extractObjIDFromJSON(json); got != 0 {
		t.Errorf("expected 0 for null obj_id, got %d", got)
	}
}

func TestExtractObjIDFromJSON_MissingField(t *testing.T) {
	json := `{"obj_type":1,"title":"No ID field"}`
	if got := extractObjIDFromJSON(json); got != 0 {
		t.Errorf("expected 0 for missing obj_id, got %d", got)
	}
}

func TestExtractObjIDFromJSON_InvalidJSON(t *testing.T) {
	if got := extractObjIDFromJSON("not json"); got != 0 {
		t.Errorf("expected 0 for invalid JSON, got %d", got)
	}
}

// --- extractProcessNameFromPath ---

func TestExtractProcessNameFromPath_Normal(t *testing.T) {
	got := extractProcessNameFromPath("./folder/379055_Escalation.conv.json")
	if got != "Escalation" {
		t.Errorf("expected Escalation, got %q", got)
	}
}

func TestExtractProcessNameFromPath_UnderscoreInName(t *testing.T) {
	got := extractProcessNameFromPath("188291_Business_Process.conv.json")
	if got != "Business_Process" {
		t.Errorf("expected Business_Process, got %q", got)
	}
}

func TestExtractProcessNameFromPath_NoUnderscore(t *testing.T) {
	got := extractProcessNameFromPath("12345.conv.json")
	if got != "12345" {
		t.Errorf("expected 12345, got %q", got)
	}
}

// --- resolveAndCacheProjectID ---

func TestResolveAndCacheProjectID_FromCache(t *testing.T) {
	tmpHome(t)
	orig := cachedProjectID
	cachedProjectID = 42000
	defer func() { cachedProjectID = orig }()

	v := &Executor{} // StageID=0, would fail API call
	got, notice := resolveAndCacheProjectID(v)
	if got != 42000 {
		t.Errorf("expected cached value 42000, got %d", got)
	}
	if notice != "" {
		t.Errorf("expected no notice from cache path, got %q", notice)
	}
}

// list-snapshots must resolve the process from process_id alone, with no
// local .conv.json anywhere — the same host-without-a-local-repository path
// that run-task supports via process_id.
func TestHandleListSnapshots_ProcessIDWithoutLocalFile(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)

	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		op := ops[0]
		typ, _ := op["type"].(string)
		obj, _ := op["obj"].(string)
		if typ == "list" && obj == "snapshots" {
			return wrapOp(map[string]interface{}{
				"proc": "ok",
				"list": []interface{}{
					map[string]interface{}{"obj_id": float64(1), "version": float64(2), "title": "manual snapshot"},
				},
			})
		}
		return okResponse(ops)
	})
	setProjectAuth(t, srv.URL)
	stageID = 20
	cachedProjectID = 10

	dir := t.TempDir()
	t.Chdir(dir) // no .conv.json anywhere in cwd

	result, isErr := handleListSnapshots(context.Background(), map[string]interface{}{
		"process_id": float64(123),
	})
	if isErr {
		t.Fatalf("process_id-based list-snapshots failed: %s", result)
	}
	if !strings.Contains(result, `"title": "manual snapshot"`) {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestResolveAndCacheProjectID_NoStageReturnsZero(t *testing.T) {
	tmpHome(t)
	orig := cachedProjectID
	cachedProjectID = 0
	defer func() { cachedProjectID = orig }()

	// No stage → no API call, no persistence.
	v := &Executor{StageID: 0}
	got, notice := resolveAndCacheProjectID(v)
	if got != 0 {
		t.Errorf("expected 0 with no stage, got %d", got)
	}
	if notice != "" {
		t.Errorf("expected no notice, got %q", notice)
	}
}
