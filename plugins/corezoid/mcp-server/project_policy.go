package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	policyModeOff    = "off"
	policyModeWarn   = "warn"
	policyModeStrict = "strict"

	maxProjectPolicyBytes = 4 << 10
	maxProjectPolicyDepth = 32
)

var policyStageMarkerRE = regexp.MustCompile(`^\d+_.*\.stage\.json$`)

// ProjectPolicy is an opt-in, host-neutral policy shared by every MCP client.
// It is stored at .corezoid/policy.json in the pulled Corezoid project.
type ProjectPolicy struct {
	Version          int                    `json:"version"`
	CycleSafety      CycleSafetyPolicy      `json:"cycle_safety"`
	ProcessContracts ProcessContractsPolicy `json:"process_contracts"`
}

type CycleSafetyPolicy struct {
	Mode               string `json:"mode"`
	MaxIterations      int    `json:"max_iterations"`
	MaxDurationSeconds int    `json:"max_duration_seconds"`
	RequireRetryDelay  bool   `json:"require_retry_delay"`
}

type ProcessContractsPolicy struct {
	Mode            string `json:"mode"`
	DependencyScope string `json:"dependency_scope"`
}

type EffectiveProjectPolicy struct {
	ProjectPolicy
	Root       string
	Sources    []string
	Configured bool
}

func defaultProjectPolicy() ProjectPolicy {
	return ProjectPolicy{
		Version: 1,
		CycleSafety: CycleSafetyPolicy{
			Mode:               policyModeOff,
			MaxIterations:      100,
			MaxDurationSeconds: 86400,
			RequireRetryDelay:  true,
		},
		ProcessContracts: ProcessContractsPolicy{
			Mode:            policyModeOff,
			DependencyScope: "project",
		},
	}
}

func validateAndNormalizeProjectPolicy(p *ProjectPolicy) error {
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Version != 1 {
		return fmt.Errorf("unsupported policy version %d (supported: 1)", p.Version)
	}
	if p.CycleSafety.Mode == "" {
		p.CycleSafety.Mode = policyModeOff
	}
	if err := validatePolicyMode("cycle_safety.mode", p.CycleSafety.Mode); err != nil {
		return err
	}
	if p.CycleSafety.MaxIterations == 0 {
		p.CycleSafety.MaxIterations = 100
	}
	if p.CycleSafety.MaxIterations < 1 {
		return fmt.Errorf("cycle_safety.max_iterations must be at least 1")
	}
	if p.CycleSafety.MaxDurationSeconds == 0 {
		p.CycleSafety.MaxDurationSeconds = 86400
	}
	if p.CycleSafety.MaxDurationSeconds < 1 {
		return fmt.Errorf("cycle_safety.max_duration_seconds must be at least 1")
	}
	// Strict cycle safety always requires pacing. A user may acknowledge an
	// intentionally unbounded loop, but an accidental tight loop must still not
	// be made cheap to create by omitting this field from a hand-written policy.
	if p.CycleSafety.Mode == policyModeStrict {
		p.CycleSafety.RequireRetryDelay = true
	}

	if p.ProcessContracts.Mode == "" {
		p.ProcessContracts.Mode = policyModeOff
	}
	if err := validatePolicyMode("process_contracts.mode", p.ProcessContracts.Mode); err != nil {
		return err
	}
	if p.ProcessContracts.DependencyScope == "" {
		p.ProcessContracts.DependencyScope = "project"
	}
	switch p.ProcessContracts.DependencyScope {
	case "self", "project":
	default:
		return fmt.Errorf("process_contracts.dependency_scope must be self or project, got %q", p.ProcessContracts.DependencyScope)
	}
	return nil
}

func validatePolicyMode(field, mode string) error {
	switch mode {
	case policyModeOff, policyModeWarn, policyModeStrict:
		return nil
	default:
		return fmt.Errorf("%s must be off, warn, or strict, got %q", field, mode)
	}
}

func policyModeRank(mode string) int {
	switch mode {
	case policyModeStrict:
		return 2
	case policyModeWarn:
		return 1
	default:
		return 0
	}
}

func stricterPolicyMode(a, b string) string {
	if policyModeRank(b) > policyModeRank(a) {
		return b
	}
	return a
}

// mergePolicy treats the overlay as a minimum policy floor: it can make the
// effective policy stricter, never weaker. This lets a read-only machine policy
// protect a project even when an agent can edit files inside the workspace.
func mergePolicy(base, overlay ProjectPolicy) ProjectPolicy {
	out := base
	out.CycleSafety.Mode = stricterPolicyMode(base.CycleSafety.Mode, overlay.CycleSafety.Mode)
	if overlay.CycleSafety.MaxIterations > 0 &&
		(out.CycleSafety.MaxIterations == 0 || overlay.CycleSafety.MaxIterations < out.CycleSafety.MaxIterations) {
		out.CycleSafety.MaxIterations = overlay.CycleSafety.MaxIterations
	}
	if overlay.CycleSafety.MaxDurationSeconds > 0 &&
		(out.CycleSafety.MaxDurationSeconds == 0 || overlay.CycleSafety.MaxDurationSeconds < out.CycleSafety.MaxDurationSeconds) {
		out.CycleSafety.MaxDurationSeconds = overlay.CycleSafety.MaxDurationSeconds
	}
	out.CycleSafety.RequireRetryDelay = base.CycleSafety.RequireRetryDelay || overlay.CycleSafety.RequireRetryDelay
	out.ProcessContracts.Mode = stricterPolicyMode(base.ProcessContracts.Mode, overlay.ProcessContracts.Mode)
	if overlay.ProcessContracts.DependencyScope == "project" {
		out.ProcessContracts.DependencyScope = "project"
	}
	return out
}

func readProjectPolicy(path string) (ProjectPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectPolicy{}, err
	}
	return parseProjectPolicy(data, path)
}

func parseProjectPolicy(data []byte, source string) (ProjectPolicy, error) {
	if len(data) > maxProjectPolicyBytes {
		return ProjectPolicy{}, fmt.Errorf("parse %s: project policy exceeds %d bytes", source, maxProjectPolicyBytes)
	}
	if err := validateProjectPolicyJSON(data); err != nil {
		return ProjectPolicy{}, fmt.Errorf("parse %s: %w", source, err)
	}
	p := defaultProjectPolicy()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return ProjectPolicy{}, fmt.Errorf("parse %s: %w", source, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return ProjectPolicy{}, fmt.Errorf("parse %s: %w", source, err)
	}
	if err := validateAndNormalizeProjectPolicy(&p); err != nil {
		return ProjectPolicy{}, fmt.Errorf("invalid %s: %w", source, err)
	}
	return p, nil
}

// validateProjectPolicyJSON rejects ambiguous JSON before encoding/json maps
// it onto a struct. In particular, JSON null would otherwise preserve the
// preloaded off defaults, and duplicate keys would silently use the last
// value. Neither behavior is acceptable for a safety policy.
func validateProjectPolicyJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkUniqueJSONObject(decoder, "$", true, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return err
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("project policy must be a JSON object, not null")
	}
	for _, section := range []string{"cycle_safety", "process_contracts"} {
		if raw, ok := root[section]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s must be a JSON object, not null", section)
		}
	}
	return nil
}

func walkUniqueJSONObject(decoder *json.Decoder, path string, requireObject bool, depth int) error {
	if depth > maxProjectPolicyDepth {
		return fmt.Errorf("project policy exceeds maximum JSON depth %d at %s", maxProjectPolicyDepth, path)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		if requireObject {
			return fmt.Errorf("project policy must be a JSON object")
		}
		return nil
	}
	if requireObject && delim != '{' {
		return fmt.Errorf("project policy must be a JSON object")
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return keyErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if seen[key] {
				return fmt.Errorf("duplicate key %q at %s", key, path)
			}
			seen[key] = true
			if err := walkUniqueJSONObject(decoder, path+"."+key, false, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		index := 0
		for decoder.More() {
			if err := walkUniqueJSONObject(decoder, fmt.Sprintf("%s[%d]", path, index), false, depth+1); err != nil {
				return err
			}
			index++
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delim, path)
	}
}

// resolveProjectPolicyRoot walks from a process or directory hint up to the
// nearest pulled stage root. Paths used by MCP handlers are confined to cwd;
// direct library callers outside cwd are treated as single-directory projects.
func resolveProjectPolicyRoot(hint string) (string, error) {
	if hint == "" {
		hint = "."
	}
	abs, err := filepath.Abs(hint)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	} else if statErr != nil && filepath.Ext(abs) != "" {
		abs = filepath.Dir(abs)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	boundary := cwd
	rel, relErr := filepath.Rel(cwd, abs)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		boundary = abs
	}

	dir := abs
	for {
		if _, err := os.Stat(filepath.Join(dir, ".corezoid", "policy.json")); err == nil {
			return dir, nil
		}
		if isPolicyProjectRoot(dir) {
			return dir, nil
		}
		if dir == boundary {
			return boundary, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd, nil
		}
		dir = parent
	}
}

func isPolicyProjectRoot(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && policyStageMarkerRE.MatchString(entry.Name()) {
			return true
		}
	}
	return false
}

func readConfinedProjectPolicy(root, path string) (ProjectPolicy, error) {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ProjectPolicy{}, fmt.Errorf("resolve project root: %w", err)
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ProjectPolicy{}, fmt.Errorf("resolve project policy: %w", err)
	}
	rel, err := filepath.Rel(rootReal, pathReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ProjectPolicy{}, fmt.Errorf("project policy resolves outside project root")
	}
	return readProjectPolicy(pathReal)
}

func loadEffectiveProjectPolicy(hint string) (EffectiveProjectPolicy, error) {
	root, err := resolveProjectPolicyRoot(hint)
	if err != nil {
		return EffectiveProjectPolicy{}, err
	}
	effective := EffectiveProjectPolicy{ProjectPolicy: defaultProjectPolicy(), Root: root}
	projectPath := filepath.Join(root, ".corezoid", "policy.json")
	if _, err := os.Stat(projectPath); err == nil {
		p, readErr := readConfinedProjectPolicy(root, projectPath)
		if readErr != nil {
			return EffectiveProjectPolicy{}, readErr
		}
		effective.ProjectPolicy = p
		effective.Sources = append(effective.Sources, projectPath)
		effective.Configured = true
	} else if !os.IsNotExist(err) {
		return EffectiveProjectPolicy{}, fmt.Errorf("read project policy: %w", err)
	}

	if externalPath := strings.TrimSpace(os.Getenv("COREZOID_POLICY_FILE")); externalPath != "" {
		p, readErr := readProjectPolicy(externalPath)
		if readErr != nil {
			return EffectiveProjectPolicy{}, fmt.Errorf("load COREZOID_POLICY_FILE: %w", readErr)
		}
		if effective.Configured {
			effective.ProjectPolicy = mergePolicy(effective.ProjectPolicy, p)
		} else {
			effective.ProjectPolicy = p
		}
		effective.Sources = append(effective.Sources, externalPath+" (minimum floor)")
		effective.Configured = true
	}

	if mode := strings.TrimSpace(os.Getenv("COREZOID_MIN_CYCLE_SAFETY_MODE")); mode != "" {
		if err := validatePolicyMode("COREZOID_MIN_CYCLE_SAFETY_MODE", mode); err != nil {
			return EffectiveProjectPolicy{}, err
		}
		effective.CycleSafety.Mode = stricterPolicyMode(effective.CycleSafety.Mode, mode)
		effective.Sources = append(effective.Sources, "COREZOID_MIN_CYCLE_SAFETY_MODE (minimum floor)")
		effective.Configured = true
	}
	if mode := strings.TrimSpace(os.Getenv("COREZOID_MIN_PROCESS_CONTRACTS_MODE")); mode != "" {
		if err := validatePolicyMode("COREZOID_MIN_PROCESS_CONTRACTS_MODE", mode); err != nil {
			return EffectiveProjectPolicy{}, err
		}
		effective.ProcessContracts.Mode = stricterPolicyMode(effective.ProcessContracts.Mode, mode)
		effective.Sources = append(effective.Sources, "COREZOID_MIN_PROCESS_CONTRACTS_MODE (minimum floor)")
		effective.Configured = true
	}
	if err := validateAndNormalizeProjectPolicy(&effective.ProjectPolicy); err != nil {
		return EffectiveProjectPolicy{}, err
	}
	return effective, nil
}

func projectPolicyPath(root string) string {
	return filepath.Join(root, ".corezoid", "policy.json")
}

func writeProjectPolicy(root string, p ProjectPolicy) (string, error) {
	if err := validateAndNormalizeProjectPolicy(&p); err != nil {
		return "", err
	}
	path := projectPolicyPath(root)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create .corezoid directory: %w", err)
	}
	// If .corezoid already existed as a symlink, verify its resolved write
	// target still belongs to the workspace before following it.
	if absPath, absErr := filepath.Abs(path); absErr != nil {
		return "", fmt.Errorf("resolve policy path: %w", absErr)
	} else if _, confineErr := relativeToCwd(absPath); confineErr != nil {
		return "", fmt.Errorf("policy path escapes working directory: %w", confineErr)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode policy: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".policy-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary policy: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0644); err != nil {
		cleanup()
		return "", fmt.Errorf("set policy permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return "", fmt.Errorf("write temporary policy: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("sync temporary policy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temporary policy: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("replace policy: %w", err)
	}
	return path, nil
}
