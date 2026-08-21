package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// A Code node pulled from a deployed process carries `chunkify`, which Corezoid
// sets on deploy and nobody authors by hand. api_code declared
// `additionalProperties: false`, so the plugin's own pull -> edit -> lint -> push
// loop failed on its own output for every process containing a Code node:
// "additional properties 'chunkify' not allowed". Declaring the field fixes the
// round-trip without opening the schema to anything else.
func TestSchema_AcceptsServerAddedChunkify(t *testing.T) {
	sch, err := loadCompiledSchema()
	if err != nil {
		t.Fatalf("loadCompiledSchema: %v", err)
	}
	raw := []byte(`{
	  "obj_type": 1, "obj_id": 1, "parent_id": 2, "title": "T", "description": "",
	  "status": "active", "params": [], "ref_mask": true, "conv_type": "process",
	  "scheme": {"nodes": [
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa1", "obj_type": 1, "title": "Start",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [{"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa2"}], "semaphors": []}},
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa2", "obj_type": 0, "title": "Code",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [
	        {"type": "api_code", "lang": "js", "src": "data.x = 1;",
	         "err_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3", "chunkify": false},
	        {"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3"}], "semaphors": []}},
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa3", "obj_type": 2, "title": "End",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [], "semaphors": []}}
	  ], "web_settings": [[], []]}
	}`)
	var doc any
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := sch.Validate(doc); err != nil {
		t.Fatalf("a pulled process with chunkify must validate, got: %v", err)
	}
}

// The schema stays closed: chunkify validates because it is declared, not because
// unknown properties became tolerated. A misspelled property is in no `required`
// list, so schema validation is the only thing that catches it.
func TestSchema_StillRejectsUndeclaredLogicProp(t *testing.T) {
	sch, err := loadCompiledSchema()
	if err != nil {
		t.Fatalf("loadCompiledSchema: %v", err)
	}
	raw := []byte(`{
	  "obj_type": 1, "obj_id": 1, "parent_id": 2, "title": "T", "description": "",
	  "status": "active", "params": [], "ref_mask": true, "conv_type": "process",
	  "scheme": {"nodes": [
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa1", "obj_type": 1, "title": "Start",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [{"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa2"}], "semaphors": []}},
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa2", "obj_type": 0, "title": "Code",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [
	        {"type": "api_code", "lang": "js", "src": "data.x = 1;",
	         "err_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3", "chunkfy": false},
	        {"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3"}], "semaphors": []}},
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa3", "obj_type": 2, "title": "End",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [], "semaphors": []}}
	  ], "web_settings": [[], []]}
	}`)
	var doc any
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := sch.Validate(doc); err == nil {
		t.Fatal("a typo of chunkify must still fail schema validation")
	}
}
