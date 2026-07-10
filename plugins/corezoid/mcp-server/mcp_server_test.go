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
