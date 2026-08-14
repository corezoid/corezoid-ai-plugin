package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestProjectPolicy_DefaultsOff(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("COREZOID_POLICY_FILE", "")
	t.Setenv("COREZOID_MIN_CYCLE_SAFETY_MODE", "")
	t.Setenv("COREZOID_MIN_PROCESS_CONTRACTS_MODE", "")

	p, err := loadEffectiveProjectPolicy(".")
	if err != nil {
		t.Fatal(err)
	}
	if p.Configured {
		t.Fatal("policy must remain opt-in when no file or environment floor exists")
	}
	if p.CycleSafety.Mode != policyModeOff || p.ProcessContracts.Mode != policyModeOff {
		t.Fatalf("unexpected default modes: %+v", p.ProjectPolicy)
	}
}

func TestConfigureProjectPolicy_EnablesSelectedModes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("COREZOID_POLICY_FILE", "")

	result, isErr := handleConfigureProjectPolicy(context.Background(), map[string]interface{}{
		"cycle_safety":               "strict",
		"process_contracts":          "warn",
		"max_cycle_iterations":       float64(12),
		"max_cycle_duration_seconds": float64(3600),
	})
	if isErr {
		t.Fatalf("configure failed: %s", result)
	}
	if !strings.Contains(result, "Cycle safety: strict") || !strings.Contains(result, "Process contracts: warn") {
		t.Fatalf("unexpected configure result:\n%s", result)
	}
	p, err := readProjectPolicy(filepath.Join(dir, ".corezoid", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.CycleSafety.Mode != policyModeStrict || p.CycleSafety.MaxIterations != 12 || p.CycleSafety.MaxDurationSeconds != 3600 {
		t.Fatalf("unexpected cycle policy: %+v", p.CycleSafety)
	}
	if p.ProcessContracts.Mode != policyModeWarn {
		t.Fatalf("unexpected contract policy: %+v", p.ProcessContracts)
	}
}

func TestProjectPolicy_ExternalFloorCannotBeWeakened(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	project := defaultProjectPolicy()
	project.CycleSafety.Mode = policyModeOff
	project.ProcessContracts.Mode = policyModeWarn
	if _, err := writeProjectPolicy(dir, project); err != nil {
		t.Fatal(err)
	}

	externalDir := t.TempDir()
	external := defaultProjectPolicy()
	external.CycleSafety.Mode = policyModeStrict
	external.CycleSafety.MaxIterations = 7
	external.ProcessContracts.Mode = policyModeStrict
	externalPath := filepath.Join(externalDir, "policy.json")
	externalData, err := json.Marshal(external)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalPath, externalData, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COREZOID_POLICY_FILE", externalPath)

	p, err := loadEffectiveProjectPolicy(".")
	if err != nil {
		t.Fatal(err)
	}
	if p.CycleSafety.Mode != policyModeStrict || p.ProcessContracts.Mode != policyModeStrict {
		t.Fatalf("external floor was weakened: %+v", p.ProjectPolicy)
	}
	if p.CycleSafety.MaxIterations != 7 {
		t.Fatalf("expected stricter max_iterations=7, got %d", p.CycleSafety.MaxIterations)
	}
}

func TestProjectPolicy_InvalidFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, ".corezoid", "policy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"version\":1,\"cycle_safety\":{\"mode\":\"sometimes\"}}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadEffectiveProjectPolicy("."); err == nil || !strings.Contains(err.Error(), "off, warn, or strict") {
		t.Fatalf("expected invalid mode error, got %v", err)
	}
}

func TestProjectPolicy_UnknownFieldFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, ".corezoid", "policy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"cycle_saftey":{"mode":"strict"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadEffectiveProjectPolicy("."); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown policy field to fail closed, got %v", err)
	}
}

func TestProjectPolicy_NullAndDuplicateKeysFailClosed(t *testing.T) {
	tests := map[string]string{
		"top-level null":          `null`,
		"null cycle section":      `{"version":1,"cycle_safety":null}`,
		"null contracts section":  `{"version":1,"process_contracts":null}`,
		"duplicate top-level key": `{"version":1,"version":1}`,
		"duplicate nested key":    `{"version":1,"cycle_safety":{"mode":"strict","mode":"off"}}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseProjectPolicy([]byte(raw), "test policy"); err == nil {
				t.Fatalf("ambiguous safety policy was accepted: %s", raw)
			}
		})
	}
}

func TestProjectPolicy_ResourceLimitsFailClosed(t *testing.T) {
	oversized := strings.Repeat(" ", maxProjectPolicyBytes+1)
	if _, err := parseProjectPolicy([]byte(oversized), "oversized"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized policy must fail closed, got %v", err)
	}
	deep := strings.Repeat(`{"x":`, maxProjectPolicyDepth+2) + `null` + strings.Repeat(`}`, maxProjectPolicyDepth+2)
	if _, err := parseProjectPolicy([]byte(deep), "deep"); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("deeply nested policy must fail closed, got %v", err)
	}
}

func TestConfigureProjectPolicy_ConcurrentUpdatesDoNotLoseFields(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("COREZOID_POLICY_FILE", "")

	const rounds = 20
	for round := 0; round < rounds; round++ {
		_ = os.RemoveAll(filepath.Join(dir, ".corezoid"))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		results := make(chan string, 2)
		go func() {
			defer wg.Done()
			<-start
			result, isErr := handleConfigureProjectPolicy(context.Background(), map[string]interface{}{"cycle_safety": "strict"})
			if isErr {
				results <- result
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			result, isErr := handleConfigureProjectPolicy(context.Background(), map[string]interface{}{"process_contracts": "strict"})
			if isErr {
				results <- result
			}
		}()
		close(start)
		wg.Wait()
		close(results)
		for result := range results {
			t.Fatalf("concurrent configure failed: %s", result)
		}
		policy, err := readProjectPolicy(filepath.Join(dir, ".corezoid", "policy.json"))
		if err != nil {
			t.Fatal(err)
		}
		if policy.CycleSafety.Mode != policyModeStrict || policy.ProcessContracts.Mode != policyModeStrict {
			t.Fatalf("round %d lost a concurrent policy update: %+v", round, policy)
		}
	}
}

func FuzzParseProjectPolicy(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"version":1,"cycle_safety":{"mode":"strict"}}`,
		`null`,
		`{"cycle_safety":{"mode":"strict","mode":"off"}}`,
		`{"unknown":true}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		policy, err := parseProjectPolicy([]byte(raw), "fuzz policy")
		if err == nil {
			if validationErr := validateAndNormalizeProjectPolicy(&policy); validationErr != nil {
				t.Fatalf("accepted policy failed normalization: %v", validationErr)
			}
		}
	})
}

func TestProjectPolicy_DirectLintPathOutsideCwdStillDefaultsOff(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("COREZOID_POLICY_FILE", "")
	externalProject := t.TempDir()
	p, err := loadEffectiveProjectPolicy(filepath.Join(externalProject, "123_process.conv.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Configured || p.Root != externalProject {
		t.Fatalf("direct read-only callers outside cwd should remain opt-in: %+v", p)
	}
}

func TestProjectPolicy_StageMarkerDefinesRootAfterMainRefactor(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "123_Test.stage.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "456_Sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeStrict
	if _, err := writeProjectPolicy(root, policy); err != nil {
		t.Fatal(err)
	}

	effective, err := loadEffectiveProjectPolicy(filepath.Join(sub, "789_Process.conv.json"))
	if err != nil {
		t.Fatal(err)
	}
	if effective.Root != root || effective.CycleSafety.Mode != policyModeStrict {
		t.Fatalf("stage-root policy not resolved from nested process: %+v", effective)
	}
}

func TestConfigureProjectPolicy_SetsContractDependencyScope(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	result, isErr := handleConfigureProjectPolicy(context.Background(), map[string]interface{}{
		"process_contracts":         "strict",
		"contract_dependency_scope": "self",
	})
	if isErr {
		t.Fatalf("configure failed: %s", result)
	}
	p, err := readProjectPolicy(filepath.Join(dir, ".corezoid", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.ProcessContracts.DependencyScope != "self" {
		t.Fatalf("expected self dependency scope, got %+v", p.ProcessContracts)
	}
}

func TestConfigureProjectPolicy_DowngradeRequiresExplicitConfirmation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	policy := defaultProjectPolicy()
	policy.CycleSafety.Mode = policyModeStrict
	policy.CycleSafety.MaxIterations = 10
	policy.ProcessContracts.Mode = policyModeStrict
	if _, err := writeProjectPolicy(dir, policy); err != nil {
		t.Fatal(err)
	}

	args := map[string]interface{}{
		"cycle_safety":              "warn",
		"process_contracts":         "off",
		"contract_dependency_scope": "self",
		"max_cycle_iterations":      float64(20),
	}
	result, isErr := handleConfigureProjectPolicy(context.Background(), args)
	if !isErr || !strings.Contains(result, "confirm_policy_downgrade=true") {
		t.Fatalf("policy downgrade must pause for explicit confirmation: %s", result)
	}
	for _, want := range []string{"cycle_safety strict -> warn", "max cycle iterations 10 -> 20", "process_contracts strict -> off", "contract dependency scope project -> self"} {
		if !strings.Contains(result, want) {
			t.Fatalf("downgrade preview must include %q, got: %s", want, result)
		}
	}
	unchanged, err := readProjectPolicy(filepath.Join(dir, ".corezoid", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.CycleSafety.Mode != policyModeStrict || unchanged.ProcessContracts.Mode != policyModeStrict || unchanged.CycleSafety.MaxIterations != 10 {
		t.Fatalf("paused downgrade modified the policy: %+v", unchanged)
	}

	args["confirm_policy_downgrade"] = true
	result, isErr = handleConfigureProjectPolicy(context.Background(), args)
	if isErr {
		t.Fatalf("confirmed policy downgrade failed: %s", result)
	}
	updated, err := readProjectPolicy(filepath.Join(dir, ".corezoid", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.CycleSafety.Mode != policyModeWarn || updated.ProcessContracts.Mode != policyModeOff || updated.ProcessContracts.DependencyScope != "self" || updated.CycleSafety.MaxIterations != 20 {
		t.Fatalf("confirmed downgrade was not applied: %+v", updated)
	}
}

func TestConfigureProjectPolicy_RejectsFractionalLimits(t *testing.T) {
	t.Chdir(t.TempDir())
	result, isErr := handleConfigureProjectPolicy(context.Background(), map[string]interface{}{
		"max_cycle_iterations": float64(10.5),
	})
	if !isErr || !strings.Contains(result, "finite integer") {
		t.Fatalf("fractional safety limit must fail closed: %s", result)
	}
}

func TestShowProjectPolicy_ExplainsOptInWhenDisabled(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("COREZOID_POLICY_FILE", "")
	result, isErr := handleShowProjectPolicy(context.Background(), map[string]interface{}{})
	if isErr {
		t.Fatalf("show failed: %s", result)
	}
	for _, want := range []string{"Source: none", "Cycle safety: off", "Process contracts: off", "Protection is not enabled"} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in:\n%s", want, result)
		}
	}
}

func TestProjectPolicyTools_DoNotRequireCorezoidAuthentication(t *testing.T) {
	resetGlobals(t)
	t.Chdir(t.TempDir())
	t.Setenv("COREZOID_POLICY_FILE", "")
	result, isErr := handleToolCall(context.Background(), "show-project-policy", map[string]interface{}{})
	if isErr || !strings.Contains(result, "Cycle safety: off") {
		t.Fatalf("local policy inspection must work before login: %s", result)
	}
	result, isErr = handleToolCall(context.Background(), "configure-project-policy", map[string]interface{}{
		"cycle_safety": "warn",
	})
	if isErr || !strings.Contains(result, "Cycle safety: warn") {
		t.Fatalf("local policy configuration must work before login: %s", result)
	}
}

func TestConfigureProjectPolicySchemaExposesDowngradeGuard(t *testing.T) {
	for _, tool := range toolRegistry {
		if tool.Name != "configure-project-policy" {
			continue
		}
		schema, ok := tool.InputSchema.(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected configure-project-policy schema: %#v", tool.InputSchema)
		}
		properties, ok := schema["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected configure-project-policy schema: %#v", tool.InputSchema)
		}
		guard, ok := properties["confirm_policy_downgrade"].(map[string]interface{})
		if !ok || guard["type"] != "boolean" {
			t.Fatalf("configure-project-policy must expose a boolean downgrade guard: %#v", properties)
		}
		return
	}
	t.Fatal("configure-project-policy tool not found")
}

func TestProjectPolicySchema_IsEmbeddedAndValidJSON(t *testing.T) {
	data, err := schemaFS.ReadFile("json-schema/project-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("unexpected project policy schema: %+v", schema)
	}
}

func TestWriteProjectPolicy_ReplacesFileSymlinkWithoutFollowingIt(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, ".corezoid"), 0755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte("do not replace"), 0644); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(root, ".corezoid", "policy.json")
	if err := os.Symlink(victim, policyPath); err != nil {
		t.Fatal(err)
	}
	p := defaultProjectPolicy()
	p.CycleSafety.Mode = policyModeWarn
	if _, err := writeProjectPolicy(root, p); err != nil {
		t.Fatal(err)
	}
	victimData, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(victimData) != "do not replace" {
		t.Fatalf("policy write followed a file symlink outside the workspace: %q", victimData)
	}
	info, err := os.Lstat(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("atomic policy replacement must replace the symlink itself")
	}
}

func TestWriteProjectPolicy_RejectsSymlinkedPolicyDirectoryOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".corezoid")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeProjectPolicy(root, defaultProjectPolicy()); err == nil || !strings.Contains(err.Error(), "escapes working directory") {
		t.Fatalf("expected symlinked policy directory to be rejected, got %v", err)
	}
}

func TestProjectPolicyTools_RejectSymlinkedProjectPathOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	outside := t.TempDir()
	link := filepath.Join(root, "outside-project")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []func(context.Context, map[string]interface{}) (string, bool){
		handleShowProjectPolicy,
		handleConfigureProjectPolicy,
	} {
		result, isErr := handler(context.Background(), map[string]interface{}{
			"project_path": "outside-project", "cycle_safety": "strict",
		})
		if !isErr || !strings.Contains(result, "resolves outside") {
			t.Fatalf("symlinked project path must be rejected, got: %s", result)
		}
	}
}
