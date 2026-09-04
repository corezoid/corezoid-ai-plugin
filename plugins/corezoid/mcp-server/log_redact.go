package main

import (
	"encoding/json"
	"strings"
)

// redactForLog masks secret-bearing values in an API payload/response before
// it is written to the debug log. The debug trace dumps full bodies, and three
// flows carry live secrets: create_api_key responses return the API-key
// secret in `logins[].key` (which CreateAPIKey deliberately writes only to a
// 0600 file), env-var ops carry the variable value — the variable-manager
// skill explicitly directs tokens and API keys there — and the bot_wizzard op
// behind create-communications-orchestrator carries one live messenger
// credential per channel. Debug logs are exactly what users paste into bug
// reports, so the trace must never contain them.
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

// isSecretKey decides whether a field name names a credential. The exact-match
// list above is not enough on its own: every messenger channel bot_wizzard
// accepts names its credential differently — `viber_token`,
// `page_access_token`, `abc_token` — so an exhaustive list silently stops
// covering the payload the moment a channel is added, which is exactly how
// those three came to be logged in the clear while Telegram's `key` was masked
// only because it collides with the create_api_key field name. A `*_token` or
// `*_secret` suffix is a credential by construction, so the suffix rule keeps
// new channels and new ops covered by default; over-redaction is the safe
// direction for a debug log.
func isSecretKey(lowerKey string) bool {
	return secretKeys[lowerKey] ||
		strings.HasSuffix(lowerKey, "_token") ||
		strings.HasSuffix(lowerKey, "_secret")
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
			if (isSecretKey(lk) || (envVar && lk == "value")) && child != nil {
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
