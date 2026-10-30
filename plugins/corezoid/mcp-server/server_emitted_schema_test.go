package main

import (
	"strings"
	"testing"
)

// pulledProcess is a minimal process as pull-process writes it, with the
// process status and the go_if_const condition item left for the caller.
func pulledProcess(status, condition string) []byte {
	return []byte(`{
	  "obj_type": 1, "obj_id": 1, "parent_id": 2, "title": "T", "description": "",
	  "status": "` + status + `", "params": [], "ref_mask": true, "conv_type": "process",
	  "scheme": {"nodes": [
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa1", "obj_type": 1, "title": "Start",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [{"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa2"}], "semaphors": []}},
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa2", "obj_type": 3, "title": "Condition",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [
	        {"type": "go_if_const", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3",
	         "conditions": [` + condition + `]},
	        {"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3"}], "semaphors": []}},
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa3", "obj_type": 2, "title": "End",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [], "semaphors": []}}
	  ], "web_settings": [[], []]}
	}`)
}

const plainCondition = `{"fun": "eq", "const": "1", "param": "x", "cast": "string"}`

// A process pulled while in debug (or paused) mode carries that status, and
// push used to reject it with an enum error until the file was hand-edited.
// Lint runs the same schema, so it must accept the status too.
func TestSchema_AcceptsServerEmittedProcessStatus(t *testing.T) {
	for _, status := range []string{"active", "inactive", "paused", "debug"} {
		if err := ValidateJSONSchemaData(pulledProcess(status, plainCondition), false); err != nil {
			t.Errorf("status %q: pulled process must validate, got: %v", status, err)
		}
		res, err := lintProcessData(pulledProcess(status, plainCondition))
		if err != nil {
			t.Fatalf("lint: %v", err)
		}
		if !res.SchemaValid {
			t.Errorf("status %q: lint must agree with push validation, got: %s", status, res.SchemaError)
		}
	}
	if err := ValidateJSONSchemaData(pulledProcess("debugg", plainCondition), false); err == nil {
		t.Error("an unknown status must still fail schema validation")
	}
}

// go_if_const condition items stay closed. The server-emitted field reported
// in #239 was chunkify on the logic itself (condition.logics[N]), which is
// declared on every logic type (see chunkify_schema_test.go); nothing the
// server writes inside conditions[] is known. An undeclared key there is a
// model invention, so it must fail, and a misspelled cast must still fail.
func TestSchema_RejectsUndeclaredConditionField(t *testing.T) {
	extra := `{"fun": "eq", "const": "1", "param": "x", "cast": "string", "operator": "eq"}`
	err := ValidateJSONSchemaData(pulledProcess("active", extra), false)
	if err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("an undeclared condition field must fail schema validation, got: %v", err)
	}

	typo := `{"fun": "eq", "const": "1", "param": "x", "cats": "string"}`
	if err := ValidateJSONSchemaData(pulledProcess("active", typo), false); err == nil {
		t.Fatal("a misspelled cast must still fail schema validation")
	}
}
