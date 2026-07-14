package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// apiContractProcess builds a minimal process with one api-logic node. The
// fields argument is merged into the api logic on top of the schema-required
// base, letting tests drop or add server-mandatory keys.
func apiContractProcess(t *testing.T, extraFields map[string]interface{}) string {
	t.Helper()
	api := map[string]interface{}{
		"type":        "api",
		"method":      "POST",
		"url":         "https://example.com/hook",
		"err_node_id": "bbccddaabbccddaabbcc0003",
	}
	for k, v := range extraFields {
		api[k] = v
	}
	proc := map[string]interface{}{
		"obj_type": 1, "obj_id": 0, "title": "api contract", "params": []interface{}{},
		"status": "active", "ref_mask": true, "conv_type": "process",
		"scheme": map[string]interface{}{
			"web_settings": []interface{}{[]interface{}{}, []interface{}{}},
			"nodes": []interface{}{
				map[string]interface{}{
					"id": "bbccddaabbccddaabbcc0001", "obj_type": 1, "title": "Start",
					"x": 0, "y": 0, "extra": `{"modeForm":"collapse","icon":""}`, "options": nil,
					"condition": map[string]interface{}{
						"logics":    []interface{}{map[string]interface{}{"type": "go", "to_node_id": "bbccddaabbccddaabbcc0002"}},
						"semaphors": []interface{}{},
					},
				},
				map[string]interface{}{
					"id": "bbccddaabbccddaabbcc0002", "obj_type": 0, "title": "Call API",
					"x": 200, "y": 200, "extra": `{"modeForm":"expand","icon":""}`, "options": nil,
					"condition": map[string]interface{}{
						"logics": []interface{}{
							api,
							map[string]interface{}{"type": "go", "to_node_id": "bbccddaabbccddaabbcc0004"},
						},
						"semaphors": []interface{}{},
					},
				},
				map[string]interface{}{
					"id": "bbccddaabbccddaabbcc0003", "obj_type": 2, "title": "Error",
					"x": 500, "y": 200, "extra": `{"modeForm":"collapse","icon":"error"}`, "options": nil,
					"condition": map[string]interface{}{"logics": []interface{}{}, "semaphors": []interface{}{}},
				},
				map[string]interface{}{
					"id": "bbccddaabbccddaabbcc0004", "obj_type": 2, "title": "Final",
					"x": 200, "y": 500, "extra": `{"modeForm":"collapse","icon":"success"}`, "options": nil,
					"condition": map[string]interface{}{"logics": []interface{}{}, "semaphors": []interface{}{}},
				},
			},
		},
	}
	data, err := json.Marshal(proc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "api_contract.conv.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The server rejects a commit whose api logic lacks extra_headers or
// max_threads ("Key 'extra_headers' is required"). Lint must catch that
// BEFORE push — a green lint followed by a failed commit is the exact
// false-confidence this schema addition removes.
func TestLintProcess_APIMissingServerMandatoryFields(t *testing.T) {
	path := apiContractProcess(t, nil) // no extra_headers, no max_threads
	result, err := lintProcess(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SchemaError == "" {
		t.Fatal("expected a schema error for api logic without extra_headers/max_threads")
	}
	for _, key := range []string{"extra_headers", "max_threads"} {
		if !strings.Contains(result.SchemaError, key) {
			t.Errorf("schema error must mention %q, got:\n%s", key, result.SchemaError)
		}
	}
}

func TestLintProcess_APIWithServerMandatoryFieldsPasses(t *testing.T) {
	path := apiContractProcess(t, map[string]interface{}{
		"extra_headers": map[string]interface{}{"content-type": "application/json"},
		"max_threads":   5,
	})
	result, err := lintProcess(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SchemaError != "" {
		t.Errorf("expected no schema error, got:\n%s", result.SchemaError)
	}
}
