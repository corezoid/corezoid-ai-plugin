package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A process pulled from Corezoid carries server-added execution fields on its
// deployed nodes — `chunkify` on api_code is the one observed in the wild. Before
// this was handled, the plugin's own pull -> edit -> lint -> push loop failed on
// its own output for EVERY process containing a Code node, with the misleading
// message "additional properties 'chunkify' not allowed".
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

// The relaxation must not silently swallow a typo: an undeclared property is
// still surfaced, just as an advisory instead of a hard failure.
func TestFindUnknownLogicProps_NamesTypo(t *testing.T) {
	if _, err := loadCompiledSchema(); err != nil {
		t.Fatalf("loadCompiledSchema: %v", err)
	}
	nodes := []processNode{{
		id: "n1", title: "Setter", objType: 0,
		logics: []map[string]interface{}{
			{"type": "set_param", "extra": map[string]interface{}{"a": "1"}, "err_nod_id": "oops"},
		},
	}}
	got := findUnknownLogicProps(nodes)
	if len(got) != 1 {
		t.Fatalf("expected 1 advisory, got %d (%+v)", len(got), got)
	}
	if !strings.Contains(got[0].Issue, "err_nod_id") {
		t.Errorf("advisory must name the offending property, got: %s", got[0].Issue)
	}
	// chunkify on api_code is declared, so it must NOT be reported.
	clean := []processNode{{
		id: "n2", title: "Code", objType: 0,
		logics: []map[string]interface{}{
			{"type": "api_code", "lang": "js", "src": "x", "err_node_id": "e", "chunkify": false},
		},
	}}
	if adv := findUnknownLogicProps(clean); len(adv) != 0 {
		t.Errorf("chunkify is declared and must not be flagged, got: %+v", adv)
	}
}

// An advisory must never make an otherwise-clean file report as dirty — that is
// the regression that would bring back the round-trip breakage.
func TestFormatLintResult_AdvisoryNotCounted(t *testing.T) {
	res := &LintResult{
		ProcessTitle: "P", TotalNodes: 1, SchemaValid: true,
		UnknownLogicProps: []UnknownLogicProp{
			{NodeID: "n1", NodeTitle: "Code", LogicType: "api_code",
				Props: []string{"newServerField"}, Issue: "api_code carries [newServerField]"},
		},
	}
	out := FormatLintResult(res)
	if !strings.Contains(out, "No issues found.") {
		t.Errorf("advisory alone must still read as clean, got: %s", out)
	}
	if strings.Contains(out, "Total issues") {
		t.Errorf("advisory must not be counted, got: %s", out)
	}
	if !strings.Contains(out, "UNKNOWN LOGIC PROPERTIES (1)") {
		t.Errorf("advisory section must still be printed, got: %s", out)
	}
}

// End-to-end on the shape pull-process actually writes: the whole document, not
// a hand-trimmed fragment, so a schema change anywhere in the process envelope
// is caught too.
func TestSchema_RealPulledProcessWithChunkify(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pulled_with_chunkify.conv.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sch, err := loadCompiledSchema()
	if err != nil {
		t.Fatalf("loadCompiledSchema: %v", err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := sch.Validate(doc); err != nil {
		t.Fatalf("real pulled process must validate, got: %v", err)
	}
}
