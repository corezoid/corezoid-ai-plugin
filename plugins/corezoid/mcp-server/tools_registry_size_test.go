package main

import (
	"encoding/json"
	"testing"
)

// toolsListLineBudget bounds the serialized tools/list payload.
//
// The whole list goes out as ONE line on stdio, on every session, and a
// bufio.Scanner — the reader this repo's own harness uses and a common
// default in line-framed clients — caps a token at 64 KiB. The cap is not in
// the MCP spec, so this is a budget rather than a hard limit, but crossing it
// silently is what makes it dangerous.
//
// The list sat at 65 400 bytes with 136 bytes of headroom before the CRUD
// domains moved behind routers (tools_router.go). Headroom is not spare room:
// the descriptions here are also ~16k tokens of every session's context, so
// the right response to pressure on this budget is still to move text out of
// tools/list — into a router action's help, or into the skill that teaches
// the workflow — rather than to raise the number.
const toolsListLineBudget = 64 * 1024

// perToolByteBudget bounds ONE advertised entry. A single tool that grows
// past this is what used to eat the shared budget silently, forcing unrelated
// descriptions to be trimmed (33cd553) and losing operative rules in the
// process (8c24756). push-process is deliberately the exception: its
// description IS the deploy safety contract.
const perToolByteBudget = 4 * 1024

var perToolBudgetExempt = map[string]string{
	"push-process": "its description is the deploy safety contract: concurrency gate, snapshot gate, stub mode, and which flag waives which",
}

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
			"drops it — move text into a router action's help instead of raising the budget",
			got, toolsListLineBudget, got-toolsListLineBudget)
	}
}

func TestMCPProtocol_NoToolHogsTheLineBudget(t *testing.T) {
	for _, tool := range toolRegistry {
		payload, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal %s: %v", tool.Name, err)
		}
		if len(payload) <= perToolByteBudget {
			continue
		}
		if why, exempt := perToolBudgetExempt[tool.Name]; exempt {
			t.Logf("%s: %d bytes, exempt — %s", tool.Name, len(payload), why)
			continue
		}
		t.Errorf("%s is %d bytes, over the %d-byte per-tool budget by %d: "+
			"detail this deep belongs in a router action's help or in the skill "+
			"that teaches the workflow, not in every session's context",
			tool.Name, len(payload), perToolByteBudget, len(payload)-perToolByteBudget)
	}
}

// TestCollapsedToolsAreNotOnTheWire pins the saving itself: the router-fronted
// definitions must stay out of tools/list. Re-advertising one is how the list
// would drift back toward the 64 KiB ceiling one "just this tool" at a time.
func TestCollapsedToolsAreNotOnTheWire(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"tools": toolRegistry})
	if err != nil {
		t.Fatalf("marshal tool registry: %v", err)
	}
	collapsed, err := json.Marshal(map[string]any{"tools": collapsedToolRegistry})
	if err != nil {
		t.Fatalf("marshal collapsed registry: %v", err)
	}
	t.Logf("advertised: %d bytes for %d tools; kept off the wire: %d bytes for %d definitions",
		len(payload), len(toolRegistry), len(collapsed), len(collapsedToolRegistry))
	advertised := make(map[string]bool, len(toolRegistry))
	for _, tool := range toolRegistry {
		advertised[tool.Name] = true
	}
	for _, def := range collapsedToolRegistry {
		if advertised[def.Name] {
			t.Errorf("%s is advertised individually again — it belongs behind %s",
				def.Name, actionRouter[def.Name])
		}
	}
}
