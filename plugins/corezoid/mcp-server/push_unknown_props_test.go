package main

import (
	"strings"
	"testing"
)

// Relaxing additionalProperties removed the only thing standing between a typo
// and a live process. `issync` for `is_sync` on an api_rpc is in no `required`
// list, so it used to fail ValidateJSONSchema and block the push; afterwards it
// validated, lint saw it, and the push printed nothing and deployed — because the
// finding was in none of the categorizeLintForPush sums that gate the report.
//
// These tests cover the gate itself rather than the sums, since the sums are
// precisely where this finding does not live.
func TestUnknownPropsPushBlock_BlocksByDefault(t *testing.T) {
	res := &LintResult{UnknownLogicProps: []UnknownLogicProp{
		{NodeID: "n2", NodeTitle: "Call", LogicType: "api_rpc",
			Props: []string{"issync"}, Issue: "api_rpc carries [issync]"},
	}}
	msg := unknownPropsPushBlock(res, false)
	if msg == "" {
		t.Fatal("an undeclared property must stop a push, not deploy silently")
	}
	// The message has to serve both readings, or it sends half its readers the
	// wrong way.
	for _, want := range []string{"Push blocked", "issync", "typo", "force=true", "api_rpc carries [issync]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message must mention %q, got: %s", want, msg)
		}
	}
}

// Waivable, unlike the closed schema it replaced: a genuine platform field must
// not be a dead end.
func TestUnknownPropsPushBlock_ForceWaives(t *testing.T) {
	res := &LintResult{UnknownLogicProps: []UnknownLogicProp{
		{NodeID: "n2", NodeTitle: "Code", LogicType: "api_code", Props: []string{"someNewField"}},
	}}
	if msg := unknownPropsPushBlock(res, true); msg != "" {
		t.Errorf("force=true must waive the block, got: %s", msg)
	}
}

func TestUnknownPropsPushBlock_CleanProcessPasses(t *testing.T) {
	if msg := unknownPropsPushBlock(&LintResult{}, false); msg != "" {
		t.Errorf("nothing undeclared must not block, got: %s", msg)
	}
}

// On a forced push the finding is the whole reason the report matters, so it must
// still be printed — the promise that non-blocking findings are surfaced.
func TestLintNoteWanted_RendersForUnknownPropsAlone(t *testing.T) {
	res := &LintResult{UnknownLogicProps: []UnknownLogicProp{
		{NodeID: "n2", NodeTitle: "Code", LogicType: "api_code", Props: []string{"someNewField"}},
	}}
	if !lintNoteWanted(res, 0, 0, 0) {
		t.Error("an undeclared property alone must still print the lint report")
	}
	if lintNoteWanted(&LintResult{}, 0, 0, 0) {
		t.Error("a clean process must not print a lint report")
	}
}

// End-to-end over the real path: the typo passes the relaxed schema, so the gate
// is the only thing left. Pins the whole chain rather than the gate in isolation.
func TestUnknownProps_TypoSurvivesSchemaButNotTheGate(t *testing.T) {
	path := writeTempProcess(t, apiRPCTypoProcess)

	if err := ValidateJSONSchema(path, false); err != nil {
		t.Fatalf("precondition: the relaxed schema is expected to accept the typo, got: %v", err)
	}
	res, err := lintProcess(path)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if len(res.UnknownLogicProps) != 1 {
		t.Fatalf("lint must see the typo, got %d findings", len(res.UnknownLogicProps))
	}
	if msg := unknownPropsPushBlock(res, false); msg == "" {
		t.Fatal("the gate is the only stop left and it did not fire")
	}
}
