package main

import (
	"encoding/json"
	"testing"
)

// toolsListLineBudget bounds the serialized tools/list payload.
//
// The whole list goes out as ONE line on stdio, on every session, and it is
// already ~65 KB of descriptions — which is how a 349-byte description edit
// managed to break TestMCPProtocol_ToolsList: bufio.Scanner, the reader this
// repo's own harness uses and a common default in line-framed clients, caps a
// token at 64 KiB. The cap is not in the MCP spec, so this is a budget rather
// than a hard limit — but crossing it silently is what makes it dangerous, and
// growth here should be a decision, not a surprise. When this fails, shorten
// descriptions rather than raising the number.
const toolsListLineBudget = 64 * 1024

func TestMCPProtocol_ToolsListFitsLineBudget(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"tools": toolRegistry})
	if err != nil {
		t.Fatalf("marshal tool registry: %v", err)
	}
	// One JSON-RPC envelope's worth of framing sits around the list on the wire.
	const envelope = 64
	got := len(payload) + envelope
	t.Logf("tools/list ≈ %d bytes for %d tools (%d bytes of headroom)",
		got, len(toolRegistry), toolsListLineBudget-got)
	if got > toolsListLineBudget {
		t.Errorf("tools/list is ~%d bytes, over the %d-byte budget by %d: "+
			"the list is sent as a single line and a 64 KiB-buffered reader "+
			"drops it — shorten tool descriptions instead of raising the budget",
			got, toolsListLineBudget, got-toolsListLineBudget)
	}
}
