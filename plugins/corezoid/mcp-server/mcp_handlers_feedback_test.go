package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// --- redactSecrets ---

func TestRedactSecrets_BearerToken(t *testing.T) {
	input := "Authorization: Bearer eyABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890abc"
	out := redactSecrets(input)
	if strings.Contains(out, "eyABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890abc") {
		t.Errorf("bearer token not redacted: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker, got: %q", out)
	}
}

func TestRedactSecrets_JWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	out := redactSecrets("token: " + jwt)
	if strings.Contains(out, jwt) {
		t.Errorf("JWT not redacted: %q", out)
	}
}

func TestRedactSecrets_APIKey(t *testing.T) {
	cases := []string{
		`api_key: "supersecretvalue123"`,
		`"token": "abc123xyz456def789"`,
		`password=hunter2secret`,
		`secret=deadbeefdeadbeef1234567890abcdef`,
	}
	for _, c := range cases {
		out := redactSecrets(c)
		if strings.Contains(out, "supersecretvalue123") || strings.Contains(out, "abc123xyz456def789") ||
			strings.Contains(out, "hunter2secret") || strings.Contains(out, "deadbeefdeadbeef") {
			t.Errorf("secret not redacted in %q → %q", c, out)
		}
	}
}

func TestRedactSecrets_LongHex(t *testing.T) {
	longHex := "5b76d006818d63730bc18a5b0e7d8d091e82d2a2"
	out := redactSecrets("endpoint key: " + longHex)
	if strings.Contains(out, longHex) {
		t.Errorf("long hex not redacted: %q", out)
	}
}

func TestRedactSecrets_ShortHexPreserved(t *testing.T) {
	// 24-char hex node IDs should NOT be redacted (< 32 chars)
	nodeID := "5b76d006818d63730bc18a5b"
	out := redactSecrets("node_id: " + nodeID)
	if !strings.Contains(out, nodeID) {
		t.Errorf("short hex (node ID) should be preserved, got: %q", out)
	}
}

func TestRedactSecrets_EmptyString(t *testing.T) {
	if got := redactSecrets(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// --- parseObjID ---

func TestParseObjID_SingleObject(t *testing.T) {
	body := []byte(`{"request_proc":"ok","ops":{"proc":"ok","obj":"task","ref":null,"obj_id":"6a3b8b6ab677ac777074794f"}}`)
	id, err := parseObjID(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "6a3b8b6ab677ac777074794f" {
		t.Errorf("expected 6a3b8b6ab677ac777074794f, got %q", id)
	}
}

func TestParseObjID_Array(t *testing.T) {
	body := []byte(`{"request_proc":"ok","ops":[{"proc":"ok","obj":"task","ref":null,"obj_id":"6a3b8b6ab677ac777074794f"}]}`)
	id, err := parseObjID(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "6a3b8b6ab677ac777074794f" {
		t.Errorf("expected 6a3b8b6ab677ac777074794f, got %q", id)
	}
}

func TestParseObjID_ErrorResponse(t *testing.T) {
	body := []byte(`{"request_proc":"fail","ops":{}}`)
	_, err := parseObjID(body)
	if err == nil {
		t.Error("expected error for request_proc=fail, got nil")
	}
}

func TestParseObjID_MissingObjID(t *testing.T) {
	body := []byte(`{"request_proc":"ok","ops":{"proc":"ok"}}`)
	_, err := parseObjID(body)
	if err == nil {
		t.Error("expected error when obj_id is absent, got nil")
	}
}

func TestParseObjID_InvalidJSON(t *testing.T) {
	_, err := parseObjID([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// --- handleSendFeedback ---

func TestHandleSendFeedback_MissingProblem(t *testing.T) {
	_, isErr := handleSendFeedback(context.Background(), map[string]interface{}{})
	if !isErr {
		t.Error("expected isError=true when problem is missing")
	}
}

func TestHandleSendFeedback_DisabledByEnv(t *testing.T) {
	t.Setenv("COREZOID_FEEDBACK_DISABLED", "1")
	result, isErr := handleSendFeedback(context.Background(), map[string]interface{}{
		"problem": "something went wrong",
	})
	if isErr {
		t.Errorf("expected isError=false when feedback disabled, got true. result=%q", result)
	}
	if !strings.Contains(result, "COREZOID_FEEDBACK_DISABLED") {
		t.Errorf("expected message about COREZOID_FEEDBACK_DISABLED, got: %q", result)
	}
}

// TestHandleSendFeedback_RedactsSecrets verifies that secrets in input fields
// are not present in the serialized payload (checked via redactSecrets directly).
func TestHandleSendFeedback_RedactsSecrets(t *testing.T) {
	secret := "supersecrettoken12345678"
	redacted := redactSecrets("token: " + secret)
	if strings.Contains(redacted, secret) {
		t.Errorf("secret leaked through redactSecrets: %q", redacted)
	}
}

// TestTelemetryConfigDefaults verifies that the built-in defaults are correct.
func TestTelemetryConfigDefaults(t *testing.T) {
	// Ensure no override env vars interfere.
	os.Unsetenv("COREZOID_ANALYTICS_ENDPOINT")
	os.Unsetenv("COREZOID_ANALYTICS_CONV_ID")
	os.Unsetenv("COREZOID_FEEDBACK_ENDPOINT")
	os.Unsetenv("COREZOID_FEEDBACK_CONV_ID")

	cfg := loadTelemetryConfig()
	if cfg.AnalyticsConvID != 1852976 {
		t.Errorf("expected analytics conv_id 1852976, got %d", cfg.AnalyticsConvID)
	}
	if cfg.FeedbackConvID != 1871779 {
		t.Errorf("expected feedback conv_id 1871779, got %d", cfg.FeedbackConvID)
	}
}

// TestTelemetryConfigEnvOverride verifies that env vars override the defaults.
func TestTelemetryConfigEnvOverride(t *testing.T) {
	t.Setenv("COREZOID_ANALYTICS_CONV_ID", "9999999")
	t.Setenv("COREZOID_FEEDBACK_CONV_ID", "8888888")
	t.Setenv("COREZOID_ANALYTICS_ENDPOINT", "https://example.com/analytics")
	t.Setenv("COREZOID_FEEDBACK_ENDPOINT", "https://example.com/feedback")

	cfg := loadTelemetryConfig()
	if cfg.AnalyticsConvID != 9999999 {
		t.Errorf("expected analytics conv_id 9999999, got %d", cfg.AnalyticsConvID)
	}
	if cfg.FeedbackConvID != 8888888 {
		t.Errorf("expected feedback conv_id 8888888, got %d", cfg.FeedbackConvID)
	}
	if cfg.AnalyticsEndpoint != "https://example.com/analytics" {
		t.Errorf("unexpected analytics endpoint: %q", cfg.AnalyticsEndpoint)
	}
	if cfg.FeedbackEndpoint != "https://example.com/feedback" {
		t.Errorf("unexpected feedback endpoint: %q", cfg.FeedbackEndpoint)
	}
}
