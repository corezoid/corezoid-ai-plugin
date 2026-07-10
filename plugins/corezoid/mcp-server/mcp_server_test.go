package main

import "testing"

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
