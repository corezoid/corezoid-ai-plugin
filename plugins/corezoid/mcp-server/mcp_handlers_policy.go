package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var projectPolicyConfigureMu sync.Mutex

func policyIntArg(args map[string]interface{}, key string) (int, error) {
	raw, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing required argument: %s", key)
	}
	switch value := raw.(type) {
	case int:
		return value, nil
	case float64:
		maxInt := int(^uint(0) >> 1)
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value > float64(maxInt) || value < float64(-maxInt) {
			return 0, fmt.Errorf("argument %s must be a finite integer, got: %v", key, value)
		}
		return int(value), nil
	default:
		return 0, fmt.Errorf("argument %s must be an integer, got %T", key, raw)
	}
}

func policyHintFromArgs(args map[string]interface{}) (string, error) {
	hint := optStrArg(args, "project_path")
	if hint == "" {
		return ".", nil
	}
	return confineToWorkdir(hint)
}

func existingPolicyHint(args map[string]interface{}) (string, error) {
	hint, err := policyHintFromArgs(args)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(hint); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("project_path %q does not exist", hint)
		}
		return "", fmt.Errorf("inspect project_path %q: %w", hint, err)
	}
	absHint, err := filepath.Abs(hint)
	if err != nil {
		return "", fmt.Errorf("resolve project_path %q: %w", hint, err)
	}
	realHint, err := filepath.EvalSymlinks(absHint)
	if err != nil {
		return "", fmt.Errorf("resolve project_path %q: %w", hint, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	realCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	rel, err := filepath.Rel(realCWD, realHint)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project_path %q resolves outside the working directory", hint)
	}
	return realHint, nil
}

func formatEffectiveProjectPolicy(p EffectiveProjectPolicy) string {
	var sb strings.Builder
	sb.WriteString("Corezoid project policy\n")
	sb.WriteString(fmt.Sprintf("Root: %s\n", p.Root))
	if len(p.Sources) == 0 {
		sb.WriteString("Source: none (opt-in protections are disabled)\n")
	} else {
		sb.WriteString("Sources: " + strings.Join(p.Sources, ", ") + "\n")
	}
	sb.WriteString(fmt.Sprintf("Cycle safety: %s (maximum %d iterations or %d seconds; retry delay required: %t)\n",
		p.CycleSafety.Mode, p.CycleSafety.MaxIterations, p.CycleSafety.MaxDurationSeconds, p.CycleSafety.RequireRetryDelay))
	sb.WriteString(fmt.Sprintf("Process contracts: %s (dependency scope: %s)\n",
		p.ProcessContracts.Mode, p.ProcessContracts.DependencyScope))
	if p.CycleSafety.Mode == policyModeOff && p.ProcessContracts.Mode == policyModeOff {
		sb.WriteString("\nProtection is not enabled. Use configure-project-policy after the user chooses which opt-in modes to enable.")
	}
	return sb.String()
}

func handleShowProjectPolicy(_ context.Context, args map[string]interface{}) (string, bool) {
	hint, err := existingPolicyHint(args)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	p, err := loadEffectiveProjectPolicy(hint)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	return formatEffectiveProjectPolicy(p), false
}

func handleConfigureProjectPolicy(_ context.Context, args map[string]interface{}) (string, bool) {
	hint, err := existingPolicyHint(args)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectPolicyConfigureMu.Lock()
	defer projectPolicyConfigureMu.Unlock()
	root, err := resolveProjectPolicyRoot(hint)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	path := projectPolicyPath(root)
	p := defaultProjectPolicy()
	if _, statErr := os.Stat(path); statErr == nil {
		p, err = readProjectPolicy(path)
		if err != nil {
			return "Error: " + err.Error(), true
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Sprintf("Error: cannot inspect %s: %v", path, statErr), true
	}
	previous := p

	changed := false
	if mode := optStrArg(args, "cycle_safety"); mode != "" {
		if err := validatePolicyMode("cycle_safety", mode); err != nil {
			return "Error: " + err.Error(), true
		}
		p.CycleSafety.Mode = mode
		changed = true
	}
	if mode := optStrArg(args, "process_contracts"); mode != "" {
		if err := validatePolicyMode("process_contracts", mode); err != nil {
			return "Error: " + err.Error(), true
		}
		p.ProcessContracts.Mode = mode
		changed = true
	}
	if scope := optStrArg(args, "contract_dependency_scope"); scope != "" {
		switch scope {
		case "self", "project":
			p.ProcessContracts.DependencyScope = scope
			changed = true
		default:
			return fmt.Sprintf("Error: contract_dependency_scope must be self or project, got %q", scope), true
		}
	}
	if _, ok := args["max_cycle_iterations"]; ok {
		value, intErr := policyIntArg(args, "max_cycle_iterations")
		if intErr != nil {
			return "Error: " + intErr.Error(), true
		}
		if value < 1 {
			return "Error: max_cycle_iterations must be at least 1", true
		}
		p.CycleSafety.MaxIterations = value
		changed = true
	}
	if _, ok := args["max_cycle_duration_seconds"]; ok {
		value, intErr := policyIntArg(args, "max_cycle_duration_seconds")
		if intErr != nil {
			return "Error: " + intErr.Error(), true
		}
		if value < 1 {
			return "Error: max_cycle_duration_seconds must be at least 1", true
		}
		p.CycleSafety.MaxDurationSeconds = value
		changed = true
	}
	if !changed {
		return "Error: specify cycle_safety, process_contracts, contract_dependency_scope, max_cycle_iterations, or max_cycle_duration_seconds", true
	}
	if reasons := policyDowngradeReasons(previous, p); len(reasons) > 0 {
		confirmed, _ := args["confirm_policy_downgrade"].(bool)
		if !confirmed {
			return fmt.Sprintf("Policy update paused: this change weakens existing project protection (%s). Re-run with confirm_policy_downgrade=true only after the user explicitly approves the downgrade.", strings.Join(reasons, "; ")), true
		}
	}

	written, err := writeProjectPolicy(root, p)
	if err != nil {
		return "Error: " + err.Error(), true
	}
	rel, _ := filepath.Rel(root, written)
	effective, err := loadEffectiveProjectPolicy(root)
	if err != nil {
		return "Error: policy was written but cannot be loaded: " + err.Error(), true
	}
	return fmt.Sprintf("Project policy updated: %s\n\n%s", filepath.ToSlash(rel), formatEffectiveProjectPolicy(effective)), false
}

func policyDowngradeReasons(previous, next ProjectPolicy) []string {
	var reasons []string
	if policyModeRank(next.CycleSafety.Mode) < policyModeRank(previous.CycleSafety.Mode) {
		reasons = append(reasons, fmt.Sprintf("cycle_safety %s -> %s", previous.CycleSafety.Mode, next.CycleSafety.Mode))
	}
	if previous.CycleSafety.Mode != policyModeOff {
		if next.CycleSafety.MaxIterations > previous.CycleSafety.MaxIterations {
			reasons = append(reasons, fmt.Sprintf("max cycle iterations %d -> %d", previous.CycleSafety.MaxIterations, next.CycleSafety.MaxIterations))
		}
		if next.CycleSafety.MaxDurationSeconds > previous.CycleSafety.MaxDurationSeconds {
			reasons = append(reasons, fmt.Sprintf("max cycle duration %d -> %d seconds", previous.CycleSafety.MaxDurationSeconds, next.CycleSafety.MaxDurationSeconds))
		}
	}
	if policyModeRank(next.ProcessContracts.Mode) < policyModeRank(previous.ProcessContracts.Mode) {
		reasons = append(reasons, fmt.Sprintf("process_contracts %s -> %s", previous.ProcessContracts.Mode, next.ProcessContracts.Mode))
	}
	if previous.ProcessContracts.Mode != policyModeOff &&
		previous.ProcessContracts.DependencyScope == "project" && next.ProcessContracts.DependencyScope == "self" {
		reasons = append(reasons, "contract dependency scope project -> self")
	}
	return reasons
}
