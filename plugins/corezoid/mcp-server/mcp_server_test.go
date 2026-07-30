package main

import (
	"fmt"
	"sync"
	"testing"
)

// ---- parseInitializeParams --------------------------------------------------

func TestParseInitializeParams_SetsClientIdentityAndElicitation(t *testing.T) {
	prevElicit, prevName, prevVersion := clientSupportsElicitation, clientName, clientVersion
	t.Cleanup(func() {
		clientSupportsElicitation, clientName, clientVersion = prevElicit, prevName, prevVersion
	})

	raw := []byte(`{
		"capabilities": {"elicitation": {}},
		"clientInfo": {"name": "Claude Code", "version": "1.2.3"}
	}`)
	parseInitializeParams(raw)

	if !clientSupportsElicitation {
		t.Error("expected clientSupportsElicitation=true")
	}
	if clientName != "Claude Code" {
		t.Errorf("clientName = %q, want %q", clientName, "Claude Code")
	}
	if clientVersion != "1.2.3" {
		t.Errorf("clientVersion = %q, want %q", clientVersion, "1.2.3")
	}
}

func TestParseInitializeParams_MissingClientInfoClearsIdentity(t *testing.T) {
	prevElicit, prevName, prevVersion := clientSupportsElicitation, clientName, clientVersion
	t.Cleanup(func() {
		clientSupportsElicitation, clientName, clientVersion = prevElicit, prevName, prevVersion
	})

	parseInitializeParams([]byte(`{"capabilities": {}}`))

	if clientSupportsElicitation {
		t.Error("expected clientSupportsElicitation=false when the client omits it")
	}
	if clientName != "" || clientVersion != "" {
		t.Errorf("expected empty client identity when clientInfo is omitted, got name=%q version=%q", clientName, clientVersion)
	}
}

func TestParseInitializeParams_MalformedJSONLeavesGlobalsUnchanged(t *testing.T) {
	t.Cleanup(func() {
		clientSupportsElicitation, clientName, clientVersion = false, "", ""
	})
	clientSupportsElicitation, clientName, clientVersion = true, "Preexisting", "9.9.9"

	parseInitializeParams([]byte(`not json`))

	if !clientSupportsElicitation || clientName != "Preexisting" || clientVersion != "9.9.9" {
		t.Errorf("expected globals untouched on parse error, got elicit=%v name=%q version=%q",
			clientSupportsElicitation, clientName, clientVersion)
	}
}

// ---- protocol version -------------------------------------------------------

func TestParseInitializeProtocolVersion(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"declared", fmt.Sprintf(`{"protocolVersion": %q, "capabilities": {}}`, mcpProtocolVersion), mcpProtocolVersion},
		{"unsupported still returned verbatim", `{"protocolVersion": "1900-01-01"}`, "1900-01-01"},
		{"omitted", `{"capabilities": {}}`, ""},
		{"explicitly empty", `{"protocolVersion": ""}`, ""},
		{"malformed JSON", `not json`, ""},
		{"wrong type", `{"protocolVersion": 42}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseInitializeProtocolVersion([]byte(tc.raw)); got != tc.want {
				t.Errorf("parseInitializeProtocolVersion(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestIsSupportedProtocolVersion(t *testing.T) {
	if !isSupportedProtocolVersion(mcpProtocolVersion) {
		t.Errorf("expected %q to be supported", mcpProtocolVersion)
	}
	for _, v := range []string{"", "1900-01-01", "2026-07-28", "2025-03-25"} {
		if isSupportedProtocolVersion(v) {
			t.Errorf("expected %q to be unsupported", v)
		}
	}
}

// TestSupportedProtocolVersions_NotSharedAcrossCalls guards the reason the
// list is built per call: it is embedded in JSON responses, and a shared
// package-level slice would be mutable through every caller that holds one.
func TestSupportedProtocolVersions_NotSharedAcrossCalls(t *testing.T) {
	first := supportedProtocolVersions()
	first[0] = "tampered"

	second := supportedProtocolVersions()
	if len(second) != 1 || second[0] != mcpProtocolVersion {
		t.Errorf("supportedProtocolVersions() = %v, want [%q]", second, mcpProtocolVersion)
	}
}

// ---- concurrency (HTTP mode runs one goroutine per request) ---------------

// TestParseInitializeParams_ConcurrentHTTPInitializes reproduces the scenario
// flagged in review: net/http dispatches each request on its own goroutine,
// so two clients connecting at once race on the shared client-identity state
// unless it's lock-protected. Run with -race — before clientStateMu existed,
// this reliably tripped the race detector. It also asserts every snapshot is
// internally consistent (name and version always come from the same client),
// which a bare mutex-free "last write wins" design would not guarantee: two
// unguarded assignments (name, then version) could interleave into a torn
// pair from two different clients.
func TestParseInitializeParams_ConcurrentHTTPInitializes(t *testing.T) {
	prevElicit, prevName, prevVersion := clientSupportsElicitation, clientName, clientVersion
	t.Cleanup(func() {
		clientSupportsElicitation, clientName, clientVersion = prevElicit, prevName, prevVersion
	})

	clientA := []byte(`{"capabilities":{"elicitation":{}},"clientInfo":{"name":"Client-A","version":"1.0.0"}}`)
	clientB := []byte(`{"capabilities":{},"clientInfo":{"name":"Client-B","version":"2.0.0"}}`)

	isCoherent := func(name, version string) bool {
		return (name == "" && version == "") ||
			(name == "Client-A" && version == "1.0.0") ||
			(name == "Client-B" && version == "2.0.0")
	}

	var wg sync.WaitGroup
	errCh := make(chan string, 400)
	for i := 0; i < 100; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			_, name, version := parseInitializeParams(clientA)
			if !isCoherent(name, version) {
				errCh <- fmt.Sprintf("torn snapshot from parseInitializeParams(A): name=%q version=%q", name, version)
			}
		}()
		go func() {
			defer wg.Done()
			_, name, version := parseInitializeParams(clientB)
			if !isCoherent(name, version) {
				errCh <- fmt.Sprintf("torn snapshot from parseInitializeParams(B): name=%q version=%q", name, version)
			}
		}()
		// Concurrent readers, matching how mcp_handlers.go (analytics) and
		// mcp_handlers_auth.go (login) read this state from other goroutines.
		go func() {
			defer wg.Done()
			_ = clientElicitationSupported()
		}()
		go func() {
			defer wg.Done()
			name, version := clientIdentitySnapshot()
			if !isCoherent(name, version) {
				errCh <- fmt.Sprintf("torn snapshot from clientIdentitySnapshot: name=%q version=%q", name, version)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}
