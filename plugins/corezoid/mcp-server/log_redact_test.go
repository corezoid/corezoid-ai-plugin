package main

import (
	"strings"
	"testing"
)

func TestRedactForLog_APIKeySecret(t *testing.T) {
	in := `{"ops":[{"proc":"ok","logins":[{"obj_id":1,"key":"SUPER-SECRET-KEY"}]}]}`
	out := string(redactForLog([]byte(in)))
	if strings.Contains(out, "SUPER-SECRET-KEY") {
		t.Fatalf("api-key secret leaked into debug output: %s", out)
	}
	if !strings.Contains(out, "***REDACTED***") {
		t.Errorf("expected redaction marker, got: %s", out)
	}
}

func TestRedactForLog_EnvVarValue(t *testing.T) {
	in := `{"ops":[{"type":"create","obj":"env_var","name":"payment-token","value":"tok-12345"}]}`
	out := string(redactForLog([]byte(in)))
	if strings.Contains(out, "tok-12345") {
		t.Fatalf("env-var value leaked into debug output: %s", out)
	}
	if !strings.Contains(out, "payment-token") {
		t.Errorf("non-secret name must survive redaction: %s", out)
	}
}

func TestRedactForLog_OrdinaryValueSurvives(t *testing.T) {
	// `value` outside an env_var op is ordinary data (set_param etc.) and must
	// not be masked; ordinary fields must round-trip untouched.
	in := `{"ops":[{"type":"create","obj":"task","data":{"value":"42","title":"hello"}}]}`
	out := string(redactForLog([]byte(in)))
	for _, want := range []string{`"42"`, "hello"} {
		if !strings.Contains(out, want) {
			t.Errorf("ordinary field lost: want %s in %s", want, out)
		}
	}
}

func TestRedactForLog_NonJSON(t *testing.T) {
	out := string(redactForLog([]byte("not json at all")))
	if strings.Contains(out, "not json") {
		t.Fatalf("raw non-JSON payload must not be echoed: %s", out)
	}
}
