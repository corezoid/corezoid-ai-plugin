package main

import "testing"

// categorizeLintForPush is the source of the "force can override this" policy
// for handlePushProcess. These tests pin the split so a future edit that moves
// (say) BrokenLinks from structural back into overridable trips CI — a broken
// graph must never be force-pushable, because the server itself rejects it and
// the "override" path silently produces a stuck deploy.

func TestCategorizeLintForPush_StructuralNotOverridable(t *testing.T) {
	// Every structural finding shows up under `structural` and none of them
	// leak into `overridable` — force cannot waive them.
	cases := []struct {
		name string
		res  *LintResult
	}{
		{"BrokenLinks", &LintResult{BrokenLinks: []BrokenLink{{ID: "x"}}}},
		{"OldFormatNodes", &LintResult{OldFormatNodes: []OldFormatNode{{ID: "x"}}}},
		{"SelfReferenceCopies", &LintResult{SelfReferenceCopies: []SelfReferenceCopy{{NodeID: "x"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			structural, overridable, advisory := categorizeLintForPush(c.res)
			if structural != 1 {
				t.Fatalf("%s must count as 1 structural, got %d", c.name, structural)
			}
			if overridable != 0 || advisory != 0 {
				t.Fatalf("%s must not spill into overridable/advisory (got %d/%d)", c.name, overridable, advisory)
			}
		})
	}
}

func TestCategorizeLintForPush_OverridableStaysOverridable(t *testing.T) {
	// Contract/warning-level findings — force=true is a legitimate escape hatch
	// for these because the user, not the server, decides whether the shape is
	// acceptable for this push.
	cases := []struct {
		name string
		res  *LintResult
	}{
		{"MissingDefaultGo", &LintResult{MissingDefaultGo: []MissingDefaultGo{{ID: "x"}}}},
		{"ShortTimers", &LintResult{ShortTimers: []ShortTimer{{ID: "x"}}}},
		{"RpcReplyMismatches", &LintResult{RpcReplyMismatches: []RpcReplyMismatch{{NodeID: "x"}}}},
		{"LiteralReplyValues", &LintResult{LiteralReplyValues: []LiteralReplyValue{{ID: "x"}}}},
		{"UnrepliedTerminals", &LintResult{UnrepliedTerminals: []UnrepliedTerminal{{ID: "x"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			structural, overridable, advisory := categorizeLintForPush(c.res)
			if overridable != 1 {
				t.Fatalf("%s must count as 1 overridable, got %d", c.name, overridable)
			}
			if structural != 0 || advisory != 0 {
				t.Fatalf("%s must not spill into structural/advisory (got %d/%d)", c.name, structural, advisory)
			}
		})
	}
}

func TestCategorizeLintForPush_AdvisoryNeverBlocks(t *testing.T) {
	cases := []struct {
		name string
		res  *LintResult
	}{
		{"NoopConditions", &LintResult{NoopConditions: []NoopCondition{{ID: "x"}}}},
		{"UnusedSetParams", &LintResult{UnusedSetParams: []UnusedSetParam{{ID: "x"}}}},
		{"OrphanedNodes", &LintResult{OrphanedNodes: []OrphanedNode{{ID: "x"}}}},
		{"PassthroughEscalations", &LintResult{PassthroughEscalations: []PassthroughEscalation{{ID: "x"}}}},
		{"SharedErrorClusters", &LintResult{SharedErrorClusters: []SharedErrorCluster{{ID: "x"}}}},
		{"GitCallUsages", &LintResult{GitCallUsages: []GitCallUsage{{ID: "x"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			structural, overridable, advisory := categorizeLintForPush(c.res)
			if advisory != 1 {
				t.Fatalf("%s must count as 1 advisory, got %d", c.name, advisory)
			}
			if structural != 0 || overridable != 0 {
				t.Fatalf("%s must not spill into structural/overridable (got %d/%d)", c.name, structural, overridable)
			}
		})
	}
}
