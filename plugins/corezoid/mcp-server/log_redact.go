package main

import (
	"encoding/json"
	"strings"
)

// redactForLog masks secret-bearing values in an API payload/response before
// it is written to the debug log. The debug trace dumps full bodies, and two
// flows carry live secrets: create_api_key responses return the API-key
// secret in `logins[].key` (which CreateAPIKey deliberately writes only to a
// 0600 file), and env-var ops carry the variable value — the variable-manager
// skill explicitly directs tokens and API keys there. Debug logs are exactly
// what users paste into bug reports, so the trace must never contain them.
//
// Redaction is field-based, not method-based: env-var values also travel
// through the generic "json" method, so keying off the method name would
// miss them.
func redactForLog(data []byte) []byte {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		// Not JSON — don't risk dumping it raw.
		return []byte(`"(non-JSON payload omitted from debug log)"`)
	}
	redactValue(v, false)
	out, err := json.Marshal(v)
	if err != nil {
		return []byte(`"(payload could not be re-serialized for debug log)"`)
	}
	return out
}

// secretKeys are masked wherever they appear, at any nesting depth.
var secretKeys = map[string]bool{
	"key":          true, // create_api_key response: logins[].key
	"api_key":      true,
	"api_secret":   true,
	"secret":       true,
	"password":     true,
	"token":        true,
	"access_token": true,
}

// redactValue walks the decoded JSON. envVar is true inside an object that
// declared itself an env_var op — there the `value` field is the secret.
func redactValue(v interface{}, envVar bool) {
	switch node := v.(type) {
	case map[string]interface{}:
		if obj, _ := node["obj"].(string); strings.EqualFold(obj, "env_var") {
			envVar = true
		}
		for k, child := range node {
			lk := strings.ToLower(k)
			if (secretKeys[lk] || (envVar && lk == "value")) && child != nil {
				// Over-redaction is the safe direction for a debug log.
				node[k] = "***REDACTED***"
				continue
			}
			redactValue(child, envVar)
		}
	case []interface{}:
		for _, child := range node {
			redactValue(child, envVar)
		}
	}
}
