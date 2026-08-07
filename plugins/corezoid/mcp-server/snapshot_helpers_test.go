package main

import (
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
