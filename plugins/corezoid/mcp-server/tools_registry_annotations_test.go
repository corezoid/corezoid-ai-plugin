package main

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// destructivePrefixes are name patterns whose tools MUST declare
// destructiveHint: true. Adding a tool that matches one of these without
// the hint is a safety regression — clients use the hint to decide whether
// to ask the user before invoking.
var destructivePrefixes = []string{"delete-", "deploy-", "revert-"}

// destructiveExact covers destructive tools whose names do not carry a
// telling prefix.
var destructiveExact = []string{"push-process"}

// readOnlyPrefixes are name patterns whose tools MUST declare
// readOnlyHint: true and MUST NOT declare destructiveHint: true.
var readOnlyPrefixes = []string{"list-", "get-", "describe-", "lint-", "preview-", "pull-"}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func isTrue(b *bool) bool { return b != nil && *b }

// reportViolations fails the test with one line per offending tool, so a
// broken registry entry is obvious without re-running under a debugger.
func reportViolations(t *testing.T, rule string, violations []string) {
	t.Helper()
	if len(violations) == 0 {
		return
	}
	sort.Strings(violations)
	t.Errorf("%d tool(s) violate the annotation taxonomy (%s):\n  %s",
		len(violations), rule, strings.Join(violations, "\n  "))
}

func TestToolAnnotations_AllToolsAnnotated(t *testing.T) {
	var violations []string
	for _, tool := range toolRegistry {
		if tool.Annotations == nil {
			violations = append(violations, tool.Name+": Annotations is nil")
			continue
		}
		for _, hint := range []struct {
			name string
			val  *bool
		}{
			{"readOnlyHint", tool.Annotations.ReadOnlyHint},
			{"destructiveHint", tool.Annotations.DestructiveHint},
			{"idempotentHint", tool.Annotations.IdempotentHint},
			{"openWorldHint", tool.Annotations.OpenWorldHint},
		} {
			if hint.val == nil {
				violations = append(violations, tool.Name+": "+hint.name+" is unset")
			}
		}
	}
	reportViolations(t, "every tool must declare all four hints", violations)
}

func TestToolAnnotations_DestructiveTaxonomy(t *testing.T) {
	exact := make(map[string]bool, len(destructiveExact))
	for _, name := range destructiveExact {
		exact[name] = true
	}

	var violations []string
	seenExact := make(map[string]bool, len(exact))
	for _, tool := range toolRegistry {
		if exact[tool.Name] {
			seenExact[tool.Name] = true
		}
		if !hasAnyPrefix(tool.Name, destructivePrefixes) && !exact[tool.Name] {
			continue
		}
		if tool.Annotations == nil || !isTrue(tool.Annotations.DestructiveHint) {
			violations = append(violations, tool.Name+": expected destructiveHint: true")
			continue
		}
		if isTrue(tool.Annotations.ReadOnlyHint) {
			violations = append(violations, tool.Name+": destructive tool must not be readOnlyHint: true")
		}
	}
	reportViolations(t, "delete-/deploy-/revert-/push-process must be destructive", violations)

	// Guard the guard: if push-process is ever renamed, this test would
	// silently stop checking it.
	for name := range exact {
		if !seenExact[name] {
			t.Errorf("tool %q is missing from toolRegistry — update destructiveExact", name)
		}
	}
}

func TestToolAnnotations_ReadOnlyTaxonomy(t *testing.T) {
	var violations []string
	for _, tool := range toolRegistry {
		if !hasAnyPrefix(tool.Name, readOnlyPrefixes) {
			continue
		}
		if tool.Annotations == nil || !isTrue(tool.Annotations.ReadOnlyHint) {
			violations = append(violations, tool.Name+": expected readOnlyHint: true")
			continue
		}
		if isTrue(tool.Annotations.DestructiveHint) {
			violations = append(violations, tool.Name+": read-only tool must not be destructiveHint: true")
		}
	}
	reportViolations(t, "list-/get-/describe-/lint-/preview-/pull- must be read-only and non-destructive", violations)
}

// TestToolAnnotations_DestructiveIsNonIdempotent pins the registry-wide rule
// documented on the hint constants: retrying a destructive call is never
// free, so destructive tools never claim idempotency.
func TestToolAnnotations_DestructiveIsNonIdempotent(t *testing.T) {
	var violations []string
	for _, tool := range toolRegistry {
		if tool.Annotations == nil || !isTrue(tool.Annotations.DestructiveHint) {
			continue
		}
		if isTrue(tool.Annotations.IdempotentHint) {
			violations = append(violations, tool.Name+": destructive tool must not be idempotentHint: true")
		}
	}
	reportViolations(t, "destructive implies non-idempotent", violations)
}

// TestToolAnnotations_ReadOnlyIsIdempotent — a tool that changes nothing
// cannot accumulate an effect by being called twice.
func TestToolAnnotations_ReadOnlyIsIdempotent(t *testing.T) {
	var violations []string
	for _, tool := range toolRegistry {
		if tool.Annotations == nil || !isTrue(tool.Annotations.ReadOnlyHint) {
			continue
		}
		if !isTrue(tool.Annotations.IdempotentHint) {
			violations = append(violations, tool.Name+": read-only tool must be idempotentHint: true")
		}
		if isTrue(tool.Annotations.DestructiveHint) {
			violations = append(violations, tool.Name+": read-only tool must not be destructiveHint: true")
		}
	}
	reportViolations(t, "read-only implies idempotent and non-destructive", violations)
}

// schemaHasProperty reports whether an InputSchema declares a top-level
// property with the given name.
func schemaHasProperty(schema interface{}, name string) bool {
	obj, ok := schema.(map[string]interface{})
	if !ok {
		return false
	}
	props, ok := obj["properties"].(map[string]interface{})
	if !ok {
		return false
	}
	_, ok = props[name]
	return ok
}

// TestToolAnnotations_ConfirmGatedIsDestructive ties the annotation to the
// registry's own safety gate. A tool that demands an explicit confirm token is
// by construction an operation the user must approve first, so it must not
// ship destructiveHint: false — a client reads that as a positive claim of
// safety and waves the call through without prompting. Name prefixes alone do
// not catch these (set-stage-immutable is neither delete-* nor deploy-*).
func TestToolAnnotations_ConfirmGatedIsDestructive(t *testing.T) {
	var violations []string
	gated := 0
	for _, tool := range toolRegistry {
		if !schemaHasProperty(tool.InputSchema, "confirm") {
			continue
		}
		gated++
		if tool.Annotations == nil || !isTrue(tool.Annotations.DestructiveHint) {
			violations = append(violations,
				tool.Name+": confirm-gated tool must declare destructiveHint: true")
		}
	}
	reportViolations(t, "tools with a confirm token must be destructive", violations)

	// Guard against a vacuous pass: if schemaHasProperty ever stops matching
	// the schema shape, the loop above would silently check nothing.
	if gated == 0 {
		t.Error("found no confirm-gated tools — schemaHasProperty no longer matches the schema shape")
	}
}

// TestToolAnnotations_UnsetHintsAreOmitted documents why the hints are
// *bool: an absent hint must serialise as an absent key, not as false.
// "unknown" and "definitely not" are different claims.
func TestToolAnnotations_UnsetHintsAreOmitted(t *testing.T) {
	raw, err := json.Marshal(mcpTool{
		Name:        "probe",
		InputSchema: map[string]interface{}{"type": "object"},
		Annotations: &toolAnnotations{ReadOnlyHint: boolPtr(true)},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"readOnlyHint":true`) {
		t.Errorf("set hint missing from JSON: %s", got)
	}
	for _, key := range []string{"destructiveHint", "idempotentHint", "openWorldHint", "title"} {
		if strings.Contains(got, key) {
			t.Errorf("unset %q must be omitted, got: %s", key, got)
		}
	}

	// A tool with no annotations at all omits the whole object.
	raw, err = json.Marshal(mcpTool{Name: "bare", InputSchema: map[string]interface{}{}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "annotations") {
		t.Errorf("nil Annotations must be omitted, got: %s", raw)
	}
}

// TestToolAnnotations_Representative pins the classification of a few tools
// that clients and reviewers are most likely to check by hand.
func TestToolAnnotations_Representative(t *testing.T) {
	want := map[string]struct{ readOnly, destructive, idempotent, openWorld bool }{
		"delete-process":           {false, true, false, true},
		"push-process":             {false, true, false, true},
		"deploy-stage":             {false, true, false, true},
		"list-variables":           {true, false, true, true},
		"pull-process":             {true, false, true, true},
		"lint-process":             {true, false, true, false},
		"layout-process":           {false, false, true, false},
		"show-project-policy":      {true, false, true, false},
		"configure-project-policy": {false, false, true, false},
	}
	byName := make(map[string]mcpTool, len(toolRegistry))
	for _, tool := range toolRegistry {
		byName[tool.Name] = tool
	}
	for name, exp := range want {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("tool %q is missing from toolRegistry", name)
			continue
		}
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s: no annotations", name)
			continue
		}
		if isTrue(a.ReadOnlyHint) != exp.readOnly ||
			isTrue(a.DestructiveHint) != exp.destructive ||
			isTrue(a.IdempotentHint) != exp.idempotent ||
			isTrue(a.OpenWorldHint) != exp.openWorld {
			t.Errorf("%s: got readOnly=%v destructive=%v idempotent=%v openWorld=%v, want %+v",
				name, isTrue(a.ReadOnlyHint), isTrue(a.DestructiveHint),
				isTrue(a.IdempotentHint), isTrue(a.OpenWorldHint), exp)
		}
	}
}
