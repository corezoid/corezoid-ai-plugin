package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGenerateVerifier_Length(t *testing.T) {
	v, err := generateVerifier()
	if err != nil {
		t.Fatalf("generateVerifier error: %v", err)
	}
	// 32 random bytes base64url-encoded → at least 40 chars
	if len(v) < 40 {
		t.Errorf("verifier too short: %d", len(v))
	}
}

func TestGenerateVerifier_Unique(t *testing.T) {
	v1, _ := generateVerifier()
	v2, _ := generateVerifier()
	if v1 == v2 {
		t.Error("two verifiers should not be equal")
	}
}

func TestGenerateChallenge_S256(t *testing.T) {
	// RFC 7636: challenge = BASE64URL(SHA256(verifier))
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := generateChallenge(verifier)
	// Must be non-empty and base64url-safe (no +, /, =)
	if strings.ContainsAny(challenge, "+/=") {
		t.Errorf("challenge contains non-URL-safe characters: %s", challenge)
	}
	if len(challenge) == 0 {
		t.Error("challenge is empty")
	}
}

func TestFindFreePort(t *testing.T) {
	port, err := findFreePort()
	if err != nil {
		t.Fatalf("findFreePort error: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Errorf("invalid port: %d", port)
	}
}

func TestParseJWTExpiry_Valid(t *testing.T) {
	// Build a minimal JWT with exp claim 1 hour in the future.
	exp := time.Now().Add(time.Hour).Unix()
	claims := map[string]interface{}{"exp": float64(exp)}
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := "header." + encoded + ".sig"

	result := parseJWTExpiry(token)
	if result.IsZero() {
		t.Error("expected non-zero time for valid JWT")
	}
	diff := result.Unix() - exp
	if diff < -1 || diff > 1 {
		t.Errorf("parsed expiry %d differs from expected %d", result.Unix(), exp)
	}
}

func TestParseJWTExpiry_InvalidToken(t *testing.T) {
	result := parseJWTExpiry("not.a.jwt.with.five.parts")
	// For a 5-part token the split will give 5 parts, part[1] base64-decodes to something invalid.
	// We just expect a zero time — no panic.
	_ = result
}

func TestParseJWTExpiry_MalformedBase64(t *testing.T) {
	result := parseJWTExpiry("hdr.!!!.sig")
	if !result.IsZero() {
		t.Error("expected zero time for malformed base64 payload")
	}
}

func TestParseJWTExpiry_NoExpClaim(t *testing.T) {
	claims := map[string]interface{}{"sub": "user"}
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := "header." + encoded + ".sig"
	result := parseJWTExpiry(token)
	if !result.IsZero() {
		t.Error("expected zero time when exp claim absent")
	}
}

func TestOAuthPageHTML_Success(t *testing.T) {
	html := oauthPageHTML("title", "success", "heading", "detail", "action")
	if !strings.Contains(html, "heading") {
		t.Error("success page should contain heading")
	}
	if strings.Contains(html, "e05252") {
		t.Error("success page should not contain error color")
	}
}

func TestOAuthPageHTML_Error(t *testing.T) {
	html := oauthPageHTML("title", "error", "heading", "detail", "action")
	if !strings.Contains(html, "e05252") {
		t.Error("error page should contain error color")
	}
}

func TestParseJWTExpiry_AlreadyExpired(t *testing.T) {
	// Build a JWT whose exp is already in the past — simulates a token minted
	// from a stale SSO session (the root cause of the reported login deadlock).
	exp := time.Now().Add(-24 * time.Hour).Unix()
	claims := map[string]interface{}{"exp": float64(exp)}
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := "header." + encoded + ".sig"

	result := parseJWTExpiry(token)
	if result.IsZero() {
		t.Fatal("expected non-zero time for an expired JWT")
	}
	if !result.Before(time.Now()) {
		t.Errorf("expected expiry in the past, got %v", result)
	}
}

func TestCheckTokenExpiry_Future(t *testing.T) {
	// A token with exp in the future — must not return an error.
	expiry := time.Now().Add(time.Hour)
	if err := checkTokenExpiry(expiry, "https://account.corezoid.com"); err != nil {
		t.Errorf("unexpected error for future expiry: %v", err)
	}
}

func TestCheckTokenExpiry_Past(t *testing.T) {
	// A token with exp already in the past — must return an error containing
	// the account URL so the user knows where to log out.
	expiry := time.Now().Add(-time.Hour)
	err := checkTokenExpiry(expiry, "https://account.corezoid.com")
	if err == nil {
		t.Fatal("expected error for already-expired token, got nil")
	}
	if !strings.Contains(err.Error(), "account.corezoid.com") {
		t.Errorf("error should mention account URL, got: %v", err)
	}
	if !strings.Contains(err.Error(), "already expired") {
		t.Errorf("error should mention 'already expired', got: %v", err)
	}
}

func TestCheckTokenExpiry_Zero(t *testing.T) {
	// Zero expiry means we could not parse it — should be allowed through.
	if err := checkTokenExpiry(time.Time{}, "https://account.corezoid.com"); err != nil {
		t.Errorf("zero expiry should not produce an error, got: %v", err)
	}
}
