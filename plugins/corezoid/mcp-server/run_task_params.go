package main

import (
	"encoding/json"
	"fmt"
	"io"
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

// decodeTaskData parses run-task's data argument. Numbers stay json.Number,
// so an integer beyond 2^53 reaches the platform with every digit instead of
// being rounded through float64, and a conversion to string keeps the literal.
func decodeTaskData(s string) (map[string]interface{}, error) {
	data := make(map[string]interface{})
	if err := decodeJSONUseNumber(s, &data); err != nil {
		return nil, err
	}
	return data, nil
}

// decodeJSONUseNumber is json.Unmarshal with UseNumber: one value, nothing
// after it.
func decodeJSONUseNumber(s string, v interface{}) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("invalid character after top-level value")
	}
	return nil
}

// needsCoercion reports whether coerceTaskData would change anything in data
// under params, without changing it.
func needsCoercion(data map[string]interface{}, params map[string]taskParam) bool {
	for name, val := range data {
		if p, ok := params[name]; ok && jsonTypeOf(val) != p.Type {
			if _, ok := coerceValue(val, p.Type); ok {
				return true
			}
		}
	}
	return false
}

// coerceTaskData converts values whose JSON type differs from the declared
// parameter type when the conversion is lossless and unambiguous: "5" → 5,
// "true" → true, 5 → "5", "[1,2]" → [1,2]. params must be the deployed
// process's: the platform checks the data against those. Anything else is left alone for the
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
			// come back unchanged, so they stay strings. "NaN" and "Inf" do
			// come back, but are not JSON numbers.
			if err != nil || strconv.FormatFloat(f, 'f', -1, 64) != s || !json.Valid([]byte(s)) {
				return nil, false
			}
			return json.Number(s), true
		case "boolean":
			switch s {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		case "array", "object":
			var parsed interface{}
			if decodeJSONUseNumber(s, &parsed) == nil && jsonTypeOf(parsed) == typ {
				return parsed, true
			}
		}
	case json.Number:
		if typ == "string" {
			return v.String(), true
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
	case float64, json.Number:
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
// without the types the caller can only guess. params are the deployed ones
// when run-task already exported them; otherwise the deployed scheme is
// exported read-only here, which costs a call only on the failure path. A
// local file is never the source: its types may not be deployed yet.
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
