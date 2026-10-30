package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// taskParam is one declared process input parameter as run-task sees it.
type taskParam struct {
	Name     string
	Type     string
	Required bool
}

// declaredTaskParams returns the input parameters a process document
// declares, keyed by name. Output-only parameters are skipped: they describe
// what the process returns, and the platform does not check task data against
// them. doc is a parsed .conv.json or an obj_scheme export (a one-element list).
func declaredTaskParams(doc interface{}) map[string]taskParam {
	if list, ok := doc.([]interface{}); ok && len(list) > 0 {
		doc = list[0]
	}
	process, _ := doc.(map[string]interface{})
	raw, _ := process["params"].([]interface{})
	params := make(map[string]taskParam)
	for _, r := range raw {
		p, _ := r.(map[string]interface{})
		name, _ := p["name"].(string)
		if name == "" {
			continue
		}
		var input, output, required bool
		flags, _ := p["flags"].([]interface{})
		for _, f := range flags {
			switch f {
			case "input":
				input = true
			case "output":
				output = true
			case "required":
				required = true
			}
		}
		if output && !input {
			continue
		}
		typ, _ := p["type"].(string)
		params[name] = taskParam{Name: name, Type: typ, Required: required}
	}
	return params
}

// localTaskParams reads the declared input parameters from a local process
// file. Any read or parse failure yields nil: the local copy is a hint, never
// a reason to refuse a run against the deployed process.
func localTaskParams(path string) map[string]taskParam {
	if path == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc interface{}
	if json.Unmarshal(body, &doc) != nil {
		return nil
	}
	return declaredTaskParams(doc)
}

// coerceTaskData converts values whose JSON type differs from the declared
// parameter type when the conversion is lossless and unambiguous: "5" → 5,
// "true" → true, 5 → "5", "[1,2]" → [1,2]. Anything else is left alone for the
// platform to judge. It returns one line per converted key, sorted, so the
// caller can say what was changed.
func coerceTaskData(data map[string]interface{}, params map[string]taskParam) []string {
	var changed []string
	for name, val := range data {
		p, ok := params[name]
		if !ok || jsonTypeOf(val) == p.Type {
			continue
		}
		if conv, ok := coerceValue(val, p.Type); ok {
			data[name] = conv
			changed = append(changed, fmt.Sprintf("%s: %s → %s", name, jsonTypeOf(val), p.Type))
		}
	}
	sort.Strings(changed)
	return changed
}

func coerceValue(val interface{}, typ string) (interface{}, bool) {
	switch v := val.(type) {
	case string:
		s := strings.TrimSpace(v)
		switch typ {
		case "number":
			f, err := strconv.ParseFloat(s, 64)
			// Round-trip check: "007", "1e3", "+1" or a 20-digit ID would not
			// come back unchanged, so they stay strings.
			if err != nil || strconv.FormatFloat(f, 'f', -1, 64) != s {
				return nil, false
			}
			return f, true
		case "boolean":
			switch s {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		case "array", "object":
			var parsed interface{}
			if json.Unmarshal([]byte(s), &parsed) == nil && jsonTypeOf(parsed) == typ {
				return parsed, true
			}
		}
	case float64:
		if typ == "string" {
			return strconv.FormatFloat(v, 'f', -1, 64), true
		}
	case bool:
		if typ == "string" {
			return strconv.FormatBool(v), true
		}
	}
	return nil, false
}

// jsonTypeOf names a decoded JSON value with Corezoid's parameter type names.
func jsonTypeOf(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

// runTaskCreateError reports a refused task together with the declared
// parameter types — the usual cause is a value of the wrong JSON type, and
// without the types the caller can only guess. When there was no local file
// to read them from, the deployed scheme is exported read-only; this costs a
// call only on the failure path.
func runTaskCreateError(v *Executor, params map[string]taskParam, data map[string]interface{}, err error) string {
	msg := fmt.Sprintf("Error creating task: %v", err)
	if params == nil {
		if doc, exportErr := v.ExportProcess(); exportErr == nil {
			params = declaredTaskParams(doc)
		}
	}
	if len(params) == 0 {
		return msg
	}
	return msg + "\n" + formatTaskParamTypes(params, data) +
		"\nTask data values are checked against these types: send numbers and booleans unquoted, arrays and objects as nested JSON (not strings)."
}

// formatTaskParamTypes renders the declared parameters next to the types that
// were actually sent, so a rejected task says exactly which value to fix.
func formatTaskParamTypes(params map[string]taskParam, data map[string]interface{}) string {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("Declared input parameters (name: expected type):")
	for _, name := range names {
		p := params[name]
		typ := p.Type
		if typ == "" {
			typ = "(untyped)"
		}
		fmt.Fprintf(&b, "\n  %s: %s", name, typ)
		if p.Required {
			b.WriteString(", required")
		}
		if val, ok := data[name]; !ok {
			if p.Required {
				b.WriteString(" — MISSING")
			}
		} else if got := jsonTypeOf(val); p.Type != "" && got != p.Type {
			fmt.Fprintf(&b, " — sent %s", got)
		}
	}
	return b.String()
}
