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

// TestLintProcess_APIUnderspecified_Caught verifies that an api logic carrying only
// the schema-required fields (type, method, url, extra_headers, max_threads,
// err_node_id) but missing the canonical extras is caught by
// findUnderspecifiedAPINodes. This is the exact "light" node shape that passes
// schema validation yet causes push-process to hang 15–20 s and fail with the
// opaque "no response from server".
func TestLintProcess_APIUnderspecified_Caught(t *testing.T) {
	path := apiContractProcess(t, map[string]interface{}{
		"extra_headers": map[string]interface{}{},
		"max_threads":   5,
		// deliberately omits: extra, extra_type, format, send_sys, debug_info,
		// customize_response, rfc_format, cert_pem, version
	})
	result, err := lintProcess(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.UnderspecifiedAPINodes) == 0 {
		t.Fatal("expected UnderspecifiedAPINodes finding, got none")
	}
	for _, field := range apiCanonicalFields {
		found := false
		for _, n := range result.UnderspecifiedAPINodes {
			for _, mf := range n.MissingFields {
				if mf == field {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("expected canonical field %q to be reported as missing", field)
		}
	}
}

// TestLintProcess_APIFullCanonical_NoUnderspecified verifies that a node carrying
// the full canonical field set is NOT flagged by findUnderspecifiedAPINodes.
func TestLintProcess_APIFullCanonical_NoUnderspecified(t *testing.T) {
	path := apiContractProcess(t, map[string]interface{}{
		"extra_headers":      map[string]interface{}{},
		"max_threads":        5,
		"extra":              map[string]interface{}{},
		"extra_type":         map[string]interface{}{},
		"format":             "",
		"send_sys":           true,
		"debug_info":         false,
		"customize_response": false,
		"rfc_format":         true,
		"cert_pem":           "",
		"version":            2,
	})
	result, err := lintProcess(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.UnderspecifiedAPINodes) != 0 {
		t.Errorf("expected no UnderspecifiedAPINodes, got %d: %+v",
			len(result.UnderspecifiedAPINodes), result.UnderspecifiedAPINodes)
	}
}

// TestLintProcess_APIPartialCanonical_ReportsOnlyMissing verifies that a node
// carrying only some canonical fields is flagged for the ones it is actually
// missing, not the ones it already has.
func TestLintProcess_APIPartialCanonical_ReportsOnlyMissing(t *testing.T) {
	path := apiContractProcess(t, map[string]interface{}{
		"extra_headers": map[string]interface{}{},
		"max_threads":   5,
		"extra":         map[string]interface{}{},
		"extra_type":    map[string]interface{}{},
		// format, send_sys, debug_info, customize_response, rfc_format, cert_pem, version absent
	})
	result, err := lintProcess(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.UnderspecifiedAPINodes) == 0 {
		t.Fatal("expected UnderspecifiedAPINodes finding, got none")
	}
	node := result.UnderspecifiedAPINodes[0]
	// extra and extra_type are present — must NOT appear in MissingFields
	for _, present := range []string{"extra", "extra_type"} {
		for _, mf := range node.MissingFields {
			if mf == present {
				t.Errorf("field %q is present in the logic but appears in MissingFields", present)
			}
		}
	}
	// format, send_sys etc. are absent — must appear in MissingFields
	for _, absent := range []string{"format", "send_sys", "debug_info", "customize_response", "rfc_format", "cert_pem", "version"} {
		found := false
		for _, mf := range node.MissingFields {
			if mf == absent {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected absent field %q in MissingFields, not found; got: %v", absent, node.MissingFields)
		}
	}
}
