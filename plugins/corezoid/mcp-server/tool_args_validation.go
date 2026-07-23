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
	toolArgTypes        map[string]map[string]string
)

func buildToolAllowedArgs() {
	toolAllowedArgs = make(map[string]map[string]bool, len(toolRegistry))
	toolRequiredArgs = make(map[string]map[string]bool, len(toolRegistry))
	toolArgTypes = make(map[string]map[string]string, len(toolRegistry))
	for _, t := range toolRegistry {
		allowed := make(map[string]bool)
		required := make(map[string]bool)
		argTypes := make(map[string]string)
		if schema, ok := t.InputSchema.(map[string]interface{}); ok {
			if props, ok := schema["properties"].(map[string]interface{}); ok {
				for k, p := range props {
					allowed[k] = true
					if pm, ok := p.(map[string]interface{}); ok {
						if typ, ok := pm["type"].(string); ok {
							argTypes[k] = typ
						}
					}
				}
			}
			for _, k := range schemaRequiredList(schema["required"]) {
				required[k] = true
			}
		}
		toolAllowedArgs[t.Name] = allowed
		toolRequiredArgs[t.Name] = required
		toolArgTypes[t.Name] = argTypes
	}
}

// coerceCLIArgs converts CLI-supplied string values to the type the tool's
// InputSchema declares. CLI args always arrive as strings ("apply=true"), but
// handlers type-assert booleans (`args["apply"].(bool)`) — so before this,
// boolean flags passed on the CLI were silently ignored: deploy-stage
// apply=true ran as a dry-run. Integers are left alone (intArg already parses
// strings); only booleans need the conversion.
func coerceCLIArgs(tool string, args map[string]interface{}) error {
	toolAllowedArgsOnce.Do(buildToolAllowedArgs)
	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if toolArgTypes[tool][k] == "boolean" {
			switch strings.ToLower(s) {
			case "true", "1", "yes":
				args[k] = true
			case "false", "0", "no":
				args[k] = false
			default:
				// A boolean flag the handler cannot read is exactly how
				// `apply=True` used to degrade to a silent dry-run — refuse
				// loudly instead of guessing.
				return fmt.Errorf("argument %s of %s is boolean; got %q (use true/false)", k, tool, s)
			}
		}
	}
	return nil
}

// schemaRequiredList extracts a schema's "required" list whether it is a
// Go-literal []string (the registry today) or a decoded-JSON []interface{}
// (a future registry entry loaded from a file) — a bare []string assertion
// would silently treat the latter as "nothing required" and disable the CLI
// env-default for that tool.
func schemaRequiredList(v interface{}) []string {
	switch req := v.(type) {
	case []string:
		return req
	case []interface{}:
		out := make([]string, 0, len(req))
		for _, item := range req {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
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
