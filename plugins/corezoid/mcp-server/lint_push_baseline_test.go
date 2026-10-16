package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// recordPulledAncestor stores the file as it is now as the scheme a pull
// recorded, i.e. what is already deployed.
func recordPulledAncestor(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAncestorScheme(".", 123, string(raw)); err != nil {
		t.Fatal(err)
	}
}

// An edit to a process that already had a blocking-class finding in its
// deployed version must not need force: the finding is not part of the change.
// Before the fix every such push was rejected, and force became the default.
func TestHandlePushProcess_InheritedLintFindingDoesNotBlock(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	addShortTimer(t, name)
	recordPulledAncestor(t, name)
	snapshotCapableServer(t, deployedProcessOp)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if isErr {
		t.Fatalf("a finding the pulled version already had must not block, got:\n%s", result)
	}
	for _, want := range []string{
		"Process deployed successfully",
		"already in the pulled version",
		"SHORT", // the finding is still reported
	} {
		if !strings.Contains(strings.ToUpper(result), strings.ToUpper(want)) {
			t.Errorf("result must contain %q, got:\n%s", want, result)
		}
	}
	if strings.Contains(result, "force=true was used") {
		t.Errorf("no waiver was used, none may be reported:\n%s", result)
	}
}

// The same finding introduced by the edit still blocks, and the block report
// lists only what blocks.
func TestHandlePushProcess_IntroducedLintFindingStillBlocks(t *testing.T) {
	resetGlobals(t)
	t.Cleanup(resetSnapshotSupportCache)
	name := writeDeployedConv(t)
	recordPulledAncestor(t, name)
	addShortTimer(t, name)
	snapshotCapableServer(t, deployedProcessOp)

	result, isErr := handlePushProcess(context.Background(), map[string]interface{}{
		"process_path":   name,
		"adopt_existing": true,
	})
	if !isErr || !strings.Contains(result, "introduced by this push") || !strings.Contains(result, "re-run with force=true") {
		t.Fatalf("a finding the edit introduced must block, got isErr=%v:\n%s", isErr, result)
	}
	if strings.Contains(result, "already in the pulled version") {
		t.Errorf("nothing was inherited, got:\n%s", result)
	}
}

func TestSplitInheritedLint(t *testing.T) {
	cur := &LintResult{
		ShortTimers:        []ShortTimer{{ID: "a"}, {ID: "b"}},
		RpcReplyMismatches: []RpcReplyMismatch{{NodeID: "r", UntypedKeys: []string{"x", "y"}}},
		UnrepliedTerminals: []UnrepliedTerminal{{ID: "u"}},
	}
	base := &LintResult{
		ShortTimers:        []ShortTimer{{ID: "a"}},
		RpcReplyMismatches: []RpcReplyMismatch{{NodeID: "r", UntypedKeys: []string{"x"}}},
		UnrepliedTerminals: []UnrepliedTerminal{{ID: "u"}},
	}
	introduced, inherited := splitInheritedLint(cur, base)
	if _, n, _ := categorizeLintForPush(introduced); n != 2 {
		t.Errorf("want 2 introduced (timer b, reply node with a new untyped key), got %d: %+v", n, introduced)
	}
	if _, n, _ := categorizeLintForPush(inherited); n != 2 {
		t.Errorf("want 2 inherited (timer a, terminal u), got %d: %+v", n, inherited)
	}

	introduced, inherited = splitInheritedLint(cur, nil)
	if _, n, _ := categorizeLintForPush(introduced); n != 4 {
		t.Errorf("without a baseline every finding is introduced, got %d", n)
	}
	if _, n, _ := categorizeLintForPush(inherited); n != 0 {
		t.Errorf("without a baseline nothing is inherited, got %d", n)
	}
}
