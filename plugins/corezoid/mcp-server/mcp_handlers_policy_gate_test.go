package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func completePolicyTestProcess(proc map[string]interface{}) {
	proc["status"] = "active"
	proc["ref_mask"] = true
	proc["conv_type"] = "process"
	if _, ok := proc["description"]; !ok {
		proc["description"] = "Policy gate test process"
	}
	scheme := proc["scheme"].(map[string]interface{})
	if _, ok := scheme["web_settings"]; !ok {
		scheme["web_settings"] = []interface{}{[]interface{}{}, []interface{}{}}
	}
}

func writeUnboundedPolicyTestProcess(t *testing.T, sourceDir, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sourceDir, "samples", "valid_process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		t.Fatal(err)
	}
	completePolicyTestProcess(proc)
	scheme := proc["scheme"].(map[string]interface{})
	nodes := scheme["nodes"].([]interface{})
	loop := nodes[1].(map[string]interface{})
	loop["obj_type"] = float64(0)
	loop["title"] = "Unbounded polling loop"
	loop["condition"] = map[string]interface{}{
		"logics":    []interface{}{map[string]interface{}{"type": "go", "to_node_id": loop["id"]}},
		"semaphors": []interface{}{},
	}
	out, err := json.MarshalIndent(proc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "123_unbounded.conv.json")
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPolicyGate_IsOptIn(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	t.Setenv("COREZOID_POLICY_FILE", "")

	path := writeUnboundedPolicyTestProcess(t, orig, dir)
	result, err := lintProcess(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	if result.EffectivePolicy != nil || result.CycleSafety != nil || result.ProcessContracts != nil {
		t.Fatalf("policy checks must be absent until explicitly enabled: %+v", result)
	}
}

func TestPushPolicyGate_StrictCycleNotBypassedByForce(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	t.Setenv("COREZOID_POLICY_FILE", "")

	path := writeUnboundedPolicyTestProcess(t, orig, dir)
	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeStrict
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path),
		"force":        true,
	})
	if !isErr {
		t.Fatalf("strict cycle gate should pause push, got success: %s", result)
	}
	for _, want := range []string{"Push paused: strict cycle safety", "confirm_cycle_risk=\"sha256:", "force=true does not bypass", "Unbounded polling loop"} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in:\n%s", want, result)
		}
	}
}

func TestPushPolicyGate_StrictContractsNotBypassedByForce(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	t.Setenv("COREZOID_POLICY_FILE", "")

	data, err := os.ReadFile(filepath.Join(orig, "samples", "valid_process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		t.Fatal(err)
	}
	completePolicyTestProcess(proc)
	data, err = json.MarshalIndent(proc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "123_contract.conv.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	policy := defaultProjectPolicy()
	policy.ProcessContracts.Mode = policyModeStrict
	policy.ProcessContracts.DependencyScope = "self"
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path),
		"force":        true,
	})
	if !isErr {
		t.Fatalf("strict contract gate should block push, got success: %s", result)
	}
	for _, want := range []string{"Push blocked: strict process contracts", "PARAM_SHAPE", "force=true does not bypass"} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in:\n%s", want, result)
		}
	}
}

func cycleFingerprintFromPush(t *testing.T, result string) string {
	t.Helper()
	match := regexp.MustCompile(`confirm_cycle_risk="(sha256:[0-9a-f]+)"`).FindStringSubmatch(result)
	if len(match) != 2 {
		t.Fatalf("cycle confirmation fingerprint not found in:\n%s", result)
	}
	return match[1]
}

func unresolvedFingerprintFromPush(t *testing.T, result string) string {
	t.Helper()
	match := regexp.MustCompile(`confirm_unresolved_call_risk="(sha256:[0-9a-f]+)"`).FindStringSubmatch(result)
	if len(match) != 2 {
		t.Fatalf("unresolved-target confirmation fingerprint not found in:\n%s", result)
	}
	return match[1]
}

func writeUnresolvedTargetPolicyTestProcess(t *testing.T, sourceDir, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sourceDir, "samples", "valid_process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		t.Fatal(err)
	}
	completePolicyTestProcess(proc)
	proc["obj_id"] = float64(123)
	scheme := proc["scheme"].(map[string]interface{})
	nodes := scheme["nodes"].([]interface{})
	start := nodes[0].(map[string]interface{})
	startLogic := start["condition"].(map[string]interface{})["logics"].([]interface{})[0].(map[string]interface{})
	startLogic["to_node_id"] = "bbccddaabbccddaabbcc0003"
	call := map[string]interface{}{
		"id": "bbccddaabbccddaabbcc0003", "obj_type": float64(0), "title": "Dispatch dynamic target",
		"x": float64(200), "y": float64(0), "extra": `{"modeForm":"collapse","icon":""}`, "options": nil,
		"condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{
					"type": "api_rpc", "conv_id": "{{target_process_id}}", "err_node_id": "bbccddaabbccddaabbcc0004",
					"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{}, "group": "",
				},
				safetyGo("bbccddaabbccddaabbcc0002"),
			},
			"semaphors": []interface{}{},
		},
	}
	errorNode := map[string]interface{}{
		"id": "bbccddaabbccddaabbcc0004", "obj_type": float64(2), "title": "Call failed",
		"x": float64(400), "y": float64(200), "extra": `{"modeForm":"collapse","icon":"error"}`, "options": nil,
		"condition": map[string]interface{}{"logics": []interface{}{}, "semaphors": []interface{}{}},
	}
	scheme["nodes"] = append(nodes, call, errorNode)
	out, err := json.MarshalIndent(proc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "123_unresolved.conv.json")
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func downstreamPolicyTestServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = *calls + 1
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc": "error", "description": "reached downstream deploy",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPushPolicyGate_ExactCycleFingerprintAllowsProceeding(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	t.Setenv("COREZOID_POLICY_FILE", "")

	path := writeUnboundedPolicyTestProcess(t, orig, dir)
	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeStrict
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}

	paused, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
	})
	if !isErr {
		t.Fatalf("first push must pause for confirmation: %s", paused)
	}
	fingerprint := cycleFingerprintFromPush(t, paused)

	calls := 0
	srv := downstreamPolicyTestServer(t, &calls)
	setProjectAuth(t, srv.URL)
	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
		"confirm_cycle_risk": fingerprint,
	})
	if !isErr || calls == 0 {
		t.Fatalf("matching fingerprint must proceed to deploy; calls=%d result=%s", calls, result)
	}
	if strings.Contains(result, "Push paused: strict cycle safety") || !strings.Contains(result, "reached downstream deploy") {
		t.Fatalf("expected downstream mock error after policy gate, got:\n%s", result)
	}
}

func TestPushPolicyGate_UnresolvedTargetRequiresExactConfirmation(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	t.Setenv("COREZOID_POLICY_FILE", "")

	path := writeUnresolvedTargetPolicyTestProcess(t, orig, dir)
	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeStrict
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}
	paused, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
	})
	if !isErr || !strings.Contains(paused, "Push paused: strict cycle safety found 1 unresolved process target") {
		t.Fatalf("strict unresolved-target gate must pause before deploy:\n%s", paused)
	}
	fingerprint := unresolvedFingerprintFromPush(t, paused)

	calls := 0
	srv := downstreamPolicyTestServer(t, &calls)
	setProjectAuth(t, srv.URL)
	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
		"confirm_unresolved_call_risk": fingerprint,
	})
	if !isErr || calls == 0 {
		t.Fatalf("matching unresolved-target fingerprint must proceed to deploy; calls=%d result=%s", calls, result)
	}
	if strings.Contains(result, "Push paused: strict cycle safety") || !strings.Contains(result, "reached downstream deploy") {
		t.Fatalf("expected downstream mock error after unresolved-target gate, got:\n%s", result)
	}
}

func TestPushPolicyGate_GraphChangeInvalidatesCycleFingerprint(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	t.Setenv("COREZOID_POLICY_FILE", "")

	path := writeUnboundedPolicyTestProcess(t, orig, dir)
	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeStrict
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}
	paused, _ := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
	})
	oldFingerprint := cycleFingerprintFromPush(t, paused)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "Unbounded polling loop", "Changed polling loop", 1))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
		"confirm_cycle_risk": oldFingerprint,
	})
	if !isErr || !strings.Contains(result, "Push paused: strict cycle safety") {
		t.Fatalf("changed graph must invalidate prior confirmation, got:\n%s", result)
	}
	if next := cycleFingerprintFromPush(t, result); next == oldFingerprint {
		t.Fatalf("graph change did not rotate fingerprint %s", oldFingerprint)
	}
}

func TestPushPolicyGate_InvalidPolicyNotBypassedByForce(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	path := writeUnboundedPolicyTestProcess(t, orig, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".corezoid"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".corezoid", "policy.json"), []byte(`{"cycle_safety":{"mode":"invalid"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
	})
	if !isErr || !strings.Contains(result, "project policy is invalid") || !strings.Contains(result, "force") {
		t.Fatalf("invalid policy must fail closed regardless of force:\n%s", result)
	}
}

func TestPushPolicyGate_WarnModeDoesNotRequireConfirmation(t *testing.T) {
	resetGlobals(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	path := writeUnboundedPolicyTestProcess(t, orig, dir)
	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeWarn
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := downstreamPolicyTestServer(t, &calls)
	setProjectAuth(t, srv.URL)
	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path": filepath.Base(path), "force": true,
	})
	if !isErr || calls == 0 || strings.Contains(result, "Push paused: strict cycle safety") {
		t.Fatalf("warn mode must proceed without confirmation; calls=%d result=%s", calls, result)
	}
}

func TestPushPolicyGate_LintFailureFailsClosedOnlyForStrictPolicy(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "123_process.conv.json")

	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeStrict
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}
	message, blocked := policyLintFailureResult(path, fmt.Errorf("synthetic lint failure"))
	if !blocked || !strings.Contains(message, "strict project policy could not be evaluated") {
		t.Fatalf("strict policy must fail closed when lint cannot run: %s", message)
	}

	policy.CycleSafety.Mode = policyModeWarn
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}
	message, blocked = policyLintFailureResult(path, fmt.Errorf("synthetic lint failure"))
	if blocked || !strings.Contains(message, "warn-only") {
		t.Fatalf("warn policy must preserve non-blocking semantics while surfacing the failure: %s", message)
	}
}
