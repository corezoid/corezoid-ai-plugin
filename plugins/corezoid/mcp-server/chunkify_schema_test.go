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

// logicItemDefs are the definitions used as items of condition.logics — every one
// of them can come back from pull-process carrying the server's chunkify flag.
// condition/semaphors/stub/semaphore_* are excluded: they are not logics entries.
var logicItemDefs = []string{
	"go", "go_if_const", "set_param", "api", "api_callback", "api_sum",
	"api_code", "api_copy", "api_rpc", "api_rpc_reply", "api_queue",
	"api_get_task", "api_form", "api_git", "db_call",
}

// Declaring chunkify on api_code alone only moved the failure to the next node
// type: a pulled Condition node blew up with "additional properties 'chunkify'
// not allowed" at /condition/logics/0. Every closed logic schema must declare it,
// so the round-trip cannot fail on whichever node type Corezoid stamps next.
func TestSchema_EveryLogicTypeDeclaresChunkify(t *testing.T) {
	byName := make(map[string]string, len(schemaDefinitions))
	for _, d := range schemaDefinitions {
		byName[d.name] = d.path
	}
	for _, name := range logicItemDefs {
		path, ok := byName[name]
		if !ok {
			t.Fatalf("logic definition %q is missing from schemaDefinitions", name)
		}
		data, err := schemaFS.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc struct {
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if doc.AdditionalProperties == nil || *doc.AdditionalProperties {
			continue // open schema, nothing to declare
		}
		if _, ok := doc.Properties["chunkify"]; !ok {
			t.Errorf("%s is closed but does not declare chunkify: a pulled process carrying it will fail push", path)
		}
	}
}

// The concrete regression: a Condition node pulled from a deployed process.
func TestSchema_AcceptsChunkifyOnConditionNode(t *testing.T) {
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
	    {"id": "aaaaaaaaaaaaaaaaaaaaaaa2", "obj_type": 3, "title": "Condition",
	     "description": "", "x": 0, "y": 0, "extra": "{}", "options": null,
	     "condition": {"logics": [
	        {"type": "go_if_const", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3", "chunkify": false,
	         "conditions": [{"fun": "eq", "const": "1", "param": "x", "cast": "string"}]},
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
		t.Fatalf("a pulled Condition node with chunkify must validate, got: %v", err)
	}
}
