package main

import (
	"os"
	"path/filepath"
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

// --- appendToDotEnv ---

func TestAppendToDotEnv_WritesNewKey(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("WORKSPACE_ID=abc\n"), 0644)

	// Make findDotEnvPath find our temp file by changing cwd.
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	appendToDotEnv("COREZOID_PROJECT_ID", "12345")

	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), "COREZOID_PROJECT_ID=12345") {
		t.Errorf("expected COREZOID_PROJECT_ID=12345 in .env, got:\n%s", data)
	}
}

func TestAppendToDotEnv_DoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("COREZOID_PROJECT_ID=99999\n"), 0644)

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	appendToDotEnv("COREZOID_PROJECT_ID", "11111")

	data, _ := os.ReadFile(envPath)
	count := strings.Count(string(data), "COREZOID_PROJECT_ID")
	if count != 1 {
		t.Errorf("expected key to appear once, got %d times:\n%s", count, data)
	}
	if strings.Contains(string(data), "11111") {
		t.Error("existing value should not be overwritten")
	}
}

// --- resolveAndCacheProjectID: env var path ---

func TestResolveAndCacheProjectID_FromEnv(t *testing.T) {
	// Reset global state.
	orig := cachedProjectID
	cachedProjectID = 0
	defer func() { cachedProjectID = orig }()

	os.Setenv("COREZOID_PROJECT_ID", "188280")
	defer os.Unsetenv("COREZOID_PROJECT_ID")

	// v with StageID=0 — API path must not be reached.
	v := &Executor{}
	got, notice := resolveAndCacheProjectID(v)
	if got != 188280 {
		t.Errorf("expected 188280 from env, got %d", got)
	}
	if notice != "" {
		t.Errorf("expected no notice from env path, got %q", notice)
	}
	if cachedProjectID != 188280 {
		t.Errorf("expected cachedProjectID=188280, got %d", cachedProjectID)
	}
}

func TestResolveAndCacheProjectID_FromCache(t *testing.T) {
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
