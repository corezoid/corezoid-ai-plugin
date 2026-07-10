package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// toolAllowedArgs maps tool name → the set of argument names its InputSchema
// declares. Built once from toolRegistry — the single source of truth for
// tool definitions.
var (
	toolAllowedArgsOnce sync.Once
	toolAllowedArgs     map[string]map[string]bool
	toolRequiredArgs    map[string]map[string]bool
)

func buildToolAllowedArgs() {
	toolAllowedArgs = make(map[string]map[string]bool, len(toolRegistry))
	toolRequiredArgs = make(map[string]map[string]bool, len(toolRegistry))
	for _, t := range toolRegistry {
		allowed := make(map[string]bool)
		required := make(map[string]bool)
		if schema, ok := t.InputSchema.(map[string]interface{}); ok {
			if props, ok := schema["properties"].(map[string]interface{}); ok {
				for k := range props {
					allowed[k] = true
				}
			}
			if req, ok := schema["required"].([]string); ok {
				for _, k := range req {
					required[k] = true
				}
			}
		}
		toolAllowedArgs[t.Name] = allowed
		toolRequiredArgs[t.Name] = required
	}
}

// toolRequiresArg reports whether the tool's InputSchema marks the argument
// as required. Used by the CLI to decide when an env-based default is safe
// to inject.
func toolRequiresArg(tool, arg string) bool {
	toolAllowedArgsOnce.Do(buildToolAllowedArgs)
	return toolRequiredArgs[tool][arg]
}

// unknownArgsError returns a non-empty error message when args contains keys
// the tool's InputSchema does not declare. Unknown arguments used to be
// silently ignored; that let calls fall back to defaults and act on the wrong
// object with no warning (e.g. create-process{folder_id: N} creating the
// process in a directory-resolved folder instead of folder N).
func unknownArgsError(tool string, args map[string]interface{}) string {
	toolAllowedArgsOnce.Do(buildToolAllowedArgs)
	allowed, ok := toolAllowedArgs[tool]
	if !ok {
		return "" // unknown tool is reported by the dispatcher itself
	}
	var unknown []string
	for k := range args {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	accepted := make([]string, 0, len(allowed))
	for k := range allowed {
		accepted = append(accepted, k)
	}
	sort.Strings(accepted)
	acceptedDesc := "no arguments"
	if len(accepted) > 0 {
		acceptedDesc = strings.Join(accepted, ", ")
	}
	return fmt.Sprintf("Error: unknown argument(s) %s for tool %s (accepted: %s)",
		strings.Join(unknown, ", "), tool, acceptedDesc)
}
