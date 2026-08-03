package main

import (
	"strings"
	"testing"
)

// TestResolveHTTPBindAddr_Loopback covers the addresses that need no opt-in.
func TestResolveHTTPBindAddr_Loopback(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
	}{
		{"unset defaults to loopback", "", "127.0.0.1:8080"},
		{"whitespace-only defaults to loopback", "   ", "127.0.0.1:8080"},
		{"explicit loopback IPv4", "127.0.0.1", "127.0.0.1:8080"},
		{"other loopback IPv4", "127.0.0.2", "127.0.0.2:8080"},
		{"loopback IPv6", "::1", "[::1]:8080"},
		{"bracketed loopback IPv6", "[::1]", "[::1]:8080"},
		{"localhost", "localhost", "localhost:8080"},
		{"localhost is case-insensitive", "LocalHost", "LocalHost:8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, warning, err := resolveHTTPBindAddr(tc.host, "8080", "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr != tc.want {
				t.Errorf("addr = %q, want %q", addr, tc.want)
			}
			if warning != "" {
				t.Errorf("unexpected warning for a loopback bind: %q", warning)
			}
		})
	}
}

// TestResolveHTTPBindAddr_RemoteRefused is the fail-closed guard: without the
// opt-in, a non-loopback bind must not produce an address at all. A wildcard
// bind is the dangerous case — it is what a naive container config produces.
func TestResolveHTTPBindAddr_RemoteRefused(t *testing.T) {
	hosts := []string{"0.0.0.0", "::", "[::]", "192.168.1.10", "10.0.0.5", "example.com"}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			addr, warning, err := resolveHTTPBindAddr(host, "8080", "")
			if err == nil {
				t.Fatalf("expected refusal for %q, got addr %q", host, addr)
			}
			if addr != "" {
				t.Errorf("addr must be empty on refusal, got %q", addr)
			}
			if warning != "" {
				t.Errorf("warning must be empty on refusal, got %q", warning)
			}
			// The message has to be actionable: name the env var and the
			// exact value, or the operator will just guess.
			for _, want := range []string{httpRemoteOptInEnv, httpRemoteOptInValue, "NO authentication"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal message missing %q: %v", want, err)
				}
			}
		})
	}
}

// TestResolveHTTPBindAddr_RemoteRequiresExactOptIn verifies a truthy-looking
// value is not enough — the whole point of the sentence-shaped sentinel.
func TestResolveHTTPBindAddr_RemoteRequiresExactOptIn(t *testing.T) {
	for _, optIn := range []string{"1", "true", "yes", "YES-I-KNOW-THERE-IS-NO-AUTH", " yes-i-know-there-is-no-auth"} {
		t.Run(optIn, func(t *testing.T) {
			if _, _, err := resolveHTTPBindAddr("0.0.0.0", "8080", optIn); err == nil {
				t.Errorf("opt-in value %q should not unlock a remote bind", optIn)
			}
		})
	}
}

// TestResolveHTTPBindAddr_RemoteAllowedWithOptIn verifies the escape hatch
// works and shouts about it.
func TestResolveHTTPBindAddr_RemoteAllowedWithOptIn(t *testing.T) {
	addr, warning, err := resolveHTTPBindAddr("0.0.0.0", "18080", httpRemoteOptInValue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "0.0.0.0:18080" {
		t.Errorf("addr = %q, want 0.0.0.0:18080", addr)
	}
	if !strings.Contains(warning, "UNAUTHENTICATED") {
		t.Errorf("warning should spell out the exposure, got %q", warning)
	}
}

// TestResolveHTTPBindAddr_NoPort guards the caller contract.
func TestResolveHTTPBindAddr_NoPort(t *testing.T) {
	if _, _, err := resolveHTTPBindAddr("127.0.0.1", "", ""); err == nil {
		t.Error("expected an error when no port is configured")
	}
}
