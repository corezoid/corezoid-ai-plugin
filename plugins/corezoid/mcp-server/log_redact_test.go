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

// Every messenger channel names its credential differently, and the
// create-communications-orchestrator payload carries a live one per channel.
// Only Telegram's was masked — by collision with the create_api_key `key`
// field, not by intent — so a debug trace attached to a bug report handed over
// working Viber, Facebook and Apple Messages bot tokens.
func TestRedactForLog_MessengerChannelTokens(t *testing.T) {
	in := `{"ops":[{"obj":"bot_wizzard","type":"create","messengers":[
	  {"channel":"telegram","key":"TG-SECRET"},
	  {"channel":"viber","viber_token":"VIBER-SECRET"},
	  {"channel":"fbmessenger","page_access_token":"FB-SECRET"},
	  {"channel":"abc","abc_token":"ABC-SECRET","email":"me@example.com"}]}]}`
	out := string(redactForLog([]byte(in)))
	for _, secret := range []string{"TG-SECRET", "VIBER-SECRET", "FB-SECRET", "ABC-SECRET"} {
		if strings.Contains(out, secret) {
			t.Errorf("messenger credential %s leaked into debug output: %s", secret, out)
		}
	}
	// The entries must still be identifiable in the trace — which channel was
	// rejected is the whole point of reading the log.
	for _, want := range []string{"telegram", "viber", "fbmessenger", "abc", "me@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("non-secret field lost: want %s in %s", want, out)
		}
	}
}

// The suffix rule, not the list, is what keeps a channel added later covered:
// an exhaustive list is how the Viber/FB/ABC tokens got logged in the first
// place.
func TestRedactForLog_UnlistedTokenSuffix(t *testing.T) {
	in := `{"ops":[{"obj":"bot_wizzard","type":"create","messengers":[
	  {"channel":"whatsapp","whatsapp_business_token":"WA-SECRET","some_secret":"S2"}]}]}`
	out := string(redactForLog([]byte(in)))
	for _, secret := range []string{"WA-SECRET", "S2"} {
		if strings.Contains(out, secret) {
			t.Errorf("credential %s leaked into debug output: %s", secret, out)
		}
	}
}
