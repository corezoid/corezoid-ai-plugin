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

// The server adds service fields with default values to go_if_const condition
// items. All four authored fields are required, so a misspelling of any of them
// still fails as a missing property; extra keys only come from the server.
func TestSchema_AcceptsServerEmittedConditionField(t *testing.T) {
	cond := `{"fun": "eq", "const": "1", "param": "x", "cast": "string", "__service__": ""}`
	if err := ValidateJSONSchemaData(pulledProcess("active", cond), false); err != nil {
		t.Fatalf("a condition carrying a server-emitted field must validate, got: %v", err)
	}
	res, err := lintProcessData(pulledProcess("active", cond))
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !res.SchemaValid {
		t.Errorf("lint must agree with push validation, got: %s", res.SchemaError)
	}

	typo := `{"fun": "eq", "const": "1", "param": "x", "cats": "string"}`
	err = ValidateJSONSchemaData(pulledProcess("active", typo), false)
	if err == nil || !strings.Contains(err.Error(), "cast") {
		t.Fatalf("a misspelled cast must still fail as missing 'cast', got: %v", err)
	}
}
