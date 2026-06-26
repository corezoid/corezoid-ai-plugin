package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- privsPayload ----------------------------------------------------------

func TestPrivsPayload_Empty(t *testing.T) {
	got := privsPayload(nil)
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected empty payload, got %v", got)
	}

	got = privsPayload([]PrivType{})
	if len(got) != 0 {
		t.Errorf("expected empty payload for empty input, got %v", got)
	}
}

func TestPrivsPayload_AllPrivs(t *testing.T) {
	got := privsPayload(AllPrivs)
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(got))
	}
	wantTypes := map[string]bool{"create": false, "modify": false, "delete": false, "view": false}
	for _, m := range got {
		typ, _ := m["type"].(string)
		if _, ok := wantTypes[typ]; !ok {
			t.Errorf("unexpected priv type %q", typ)
			continue
		}
		wantTypes[typ] = true
		listObj, _ := m["list_obj"].([]string)
		if len(listObj) != 1 || listObj[0] != "all" {
			t.Errorf("expected list_obj=[\"all\"], got %v", listObj)
		}
	}
	for typ, seen := range wantTypes {
		if !seen {
			t.Errorf("priv %q missing from payload", typ)
		}
	}
}

func TestPrivsPayload_PreservesOrder(t *testing.T) {
	in := []PrivType{PrivView, PrivCreate}
	got := privsPayload(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0]["type"] != "view" || got[1]["type"] != "create" {
		t.Errorf("order not preserved: %v", got)
	}
}

// ---- validateObjKind -------------------------------------------------------

func TestValidateObjKind_Valid(t *testing.T) {
	for _, k := range []string{"conv", "folder", "stage", "project"} {
		if err := validateObjKind(k); err != nil {
			t.Errorf("expected %q to be valid: %v", k, err)
		}
	}
}

func TestValidateObjKind_Invalid(t *testing.T) {
	for _, k := range []string{"", "user", "dashboard", "Conv", "FOLDER"} {
		if err := validateObjKind(k); err == nil {
			t.Errorf("expected %q to be rejected", k)
		}
	}
}

// ---- validatePrincipalKind -------------------------------------------------

func TestValidatePrincipalKind_Valid(t *testing.T) {
	for _, k := range []string{"user", "group"} {
		if err := validatePrincipalKind(k); err != nil {
			t.Errorf("expected %q to be valid: %v", k, err)
		}
	}
}

func TestValidatePrincipalKind_Invalid(t *testing.T) {
	for _, k := range []string{"", "api_key", "User", "GROUP", "other"} {
		if err := validatePrincipalKind(k); err == nil {
			t.Errorf("expected %q to be rejected", k)
		}
	}
}

// ---- firstOp ---------------------------------------------------------------

func TestFirstOp_Success(t *testing.T) {
	resp := map[string]any{
		"ops": []any{
			map[string]any{"proc": "ok", "id": float64(1)},
		},
	}
	op, err := firstOp(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op["id"].(float64) != 1 {
		t.Errorf("unexpected op contents: %v", op)
	}
}

func TestFirstOp_NilResponse(t *testing.T) {
	_, err := firstOp(nil)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected empty-response error, got %v", err)
	}
}

func TestFirstOp_NoOps(t *testing.T) {
	_, err := firstOp(map[string]any{})
	if err == nil {
		t.Error("expected error for missing ops")
	}

	_, err = firstOp(map[string]any{"ops": []any{}})
	if err == nil {
		t.Error("expected error for empty ops slice")
	}
}

func TestFirstOp_BadOpShape(t *testing.T) {
	_, err := firstOp(map[string]any{
		"ops": []any{"not-a-map"},
	})
	if err == nil {
		t.Error("expected error for non-map op")
	}
}

func TestFirstOp_ProcNotOK_WithDescription(t *testing.T) {
	resp := map[string]any{
		"ops": []any{
			map[string]any{"proc": "fail", "description": "boom"},
		},
	}
	op, err := firstOp(resp)
	if err == nil {
		t.Fatal("expected error for proc!=ok")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected description in error, got %v", err)
	}
	// Caller should still see the op for inspection.
	if op == nil {
		t.Error("expected op to be returned alongside the error")
	}
}

func TestFirstOp_ProcNotOK_NoDescription(t *testing.T) {
	resp := map[string]any{
		"ops": []any{
			map[string]any{"proc": "fail"},
		},
	}
	_, err := firstOp(resp)
	if err == nil || !strings.Contains(err.Error(), "fail") {
		t.Errorf("expected proc value in error, got %v", err)
	}
}

// ---- stringValue -----------------------------------------------------------

func TestStringValue(t *testing.T) {
	m := map[string]any{"url": "https://x", "count": 5, "obj": map[string]any{}}
	if got := stringValue(m, "url"); got != "https://x" {
		t.Errorf("expected URL, got %q", got)
	}
	// Non-string field is returned as empty.
	if got := stringValue(m, "count"); got != "" {
		t.Errorf("expected empty string for non-string value, got %q", got)
	}
	// Missing key.
	if got := stringValue(m, "missing"); got != "" {
		t.Errorf("expected empty string for missing key, got %q", got)
	}
	// Nil map.
	if got := stringValue(nil, "url"); got != "" {
		t.Errorf("expected empty string for nil map, got %q", got)
	}
}

// ---- secretsDir ------------------------------------------------------------

func TestSecretsDir_CreatesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := secretsDir()
	if err != nil {
		t.Fatalf("secretsDir error: %v", err)
	}
	want := filepath.Join(home, ".corezoid", "api-keys")
	if dir != want {
		t.Errorf("expected %q, got %q", want, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected dir to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}
}

// ---- writeAPIKeySecret -----------------------------------------------------

func TestWriteAPIKeySecret_WritesValidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := &Principal{ID: 42, APILogin: 137128, APIKey: "shhh"}
	path, err := writeAPIKeySecret(p, "My API Key!", "for tests")
	if err != nil {
		t.Fatalf("writeAPIKeySecret error: %v", err)
	}
	// Slug should collapse unsafe characters; ID is appended.
	wantSuffix := "My-API-Key-42.json"
	if filepath.Base(path) != wantSuffix {
		t.Errorf("expected file %q, got %q", wantSuffix, filepath.Base(path))
	}

	// File must be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected mode 0600, got %v", info.Mode().Perm())
	}

	// Content must be valid JSON with the expected fields.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload["secret"] != "shhh" {
		t.Errorf("expected secret to be persisted, got %v", payload["secret"])
	}
	// JSON numbers round-trip as float64.
	if login, _ := payload["login"].(float64); int(login) != 137128 {
		t.Errorf("expected login=137128 persisted, got %v", payload["login"])
	}
	if payload["title"] != "My API Key!" {
		t.Errorf("expected title persisted, got %v", payload["title"])
	}
	if payload["description"] != "for tests" {
		t.Errorf("expected description persisted, got %v", payload["description"])
	}
	// obj_id round-trips through JSON as float64.
	if id, _ := payload["obj_id"].(float64); int(id) != 42 {
		t.Errorf("expected obj_id 42, got %v", payload["obj_id"])
	}
}

func TestWriteAPIKeySecret_EmptyTitleFallsBackToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := &Principal{ID: 7}
	path, err := writeAPIKeySecret(p, "", "")
	if err != nil {
		t.Fatalf("writeAPIKeySecret error: %v", err)
	}
	if filepath.Base(path) != "api-key-7.json" {
		t.Errorf("expected default slug, got %q", filepath.Base(path))
	}
}

func TestWriteAPIKeySecret_SlugCollapsesUnsafeChars(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := &Principal{ID: 1}
	path, err := writeAPIKeySecret(p, "a/b\\c?d", "")
	if err != nil {
		t.Fatalf("writeAPIKeySecret error: %v", err)
	}
	base := filepath.Base(path)
	// All unsafe characters collapse to '-'.
	if !strings.HasPrefix(base, "a-b-c-d-") {
		t.Errorf("unexpected slug: %q", base)
	}
}
