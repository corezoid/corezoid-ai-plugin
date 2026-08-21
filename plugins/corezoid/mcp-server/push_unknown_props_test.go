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
func TestUnknownPropsPushBlock_AllowFlagWaives(t *testing.T) {
	res := &LintResult{UnknownLogicProps: []UnknownLogicProp{
		{NodeID: "n2", NodeTitle: "Code", LogicType: "api_code", Props: []string{"someNewField"}},
	}}
	if msg := unknownPropsPushBlock(res, true); msg != "" {
		t.Errorf("allow_unknown_logic_props=true must waive the block, got: %s", msg)
	}
}

// The waiver must NOT be force. force also waives the lost-update concurrency
// gate, and since there is no catalogue of server-emitted fields, routing this
// waiver through force would make it routine for pull -> edit -> push — so the
// first push after a colleague touched the process on the server would silently
// discard their work. The block message is what steers the user, so it has to
// name the right flag and rule out the wrong one.
func TestUnknownPropsPushBlock_MessageDoesNotSendUserToForce(t *testing.T) {
	res := &LintResult{UnknownLogicProps: []UnknownLogicProp{
		{NodeID: "n2", NodeTitle: "Code", LogicType: "api_code", Props: []string{"someNewField"}},
	}}
	msg := unknownPropsPushBlock(res, false)
	if !strings.Contains(msg, "allow_unknown_logic_props=true") {
		t.Errorf("message must name the dedicated waiver, got: %s", msg)
	}
	if !strings.Contains(msg, "Not force=true") {
		t.Errorf("message must rule out force, which would also waive the concurrency gate, got: %s", msg)
	}
}

// The flag has to be reachable through the tool schema, or the message above
// tells the caller to pass an argument that does not exist.
func TestPushProcess_DeclaresUnknownPropsWaiver(t *testing.T) {
	var props map[string]interface{}
	var toolDesc string
	for _, tool := range toolRegistry {
		if tool.Name != "push-process" {
			continue
		}
		toolDesc = tool.Description
		schema, _ := tool.InputSchema.(map[string]interface{})
		props, _ = schema["properties"].(map[string]interface{})
	}
	if props == nil {
		t.Fatal("push-process not found in the tool registry")
	}
	waiver, ok := props["allow_unknown_logic_props"].(map[string]interface{})
	if !ok {
		t.Fatal("push-process must declare allow_unknown_logic_props, or the block message " +
			"points at an argument callers cannot pass")
	}
	if desc, _ := waiver["description"].(string); !strings.Contains(desc, "force") {
		t.Error("the waiver's description must say how it differs from force")
	}
	force, _ := props["force"].(map[string]interface{})
	if desc, _ := force["description"].(string); !strings.Contains(desc, "allow_unknown_logic_props") {
		t.Error("force's description must disclaim this waiver, as it already does for Stub Mode")
	}
	if !strings.Contains(toolDesc, "allow_unknown_logic_props") {
		t.Error("the push-process description must mention the new gate — it is a blocking behaviour change")
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
