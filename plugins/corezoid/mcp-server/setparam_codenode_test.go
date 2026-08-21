package main

import "testing"

// A set_param whose values are read by a Code node used to be reported dead,
// because the check only looked for the {{template}} form. This is the exact
// shape the middleware guidance recommends (namespace with set_param, then map
// the outcome in one Code node), so following the docs guaranteed the warning.
func TestUnusedSetParam_CodeNodeReadCounts(t *testing.T) {
	nodes := []processNode{
		{id: "n1", title: "Namespace", objType: 0, logics: []map[string]interface{}{
			{"type": "set_param",
				"extra":       map[string]interface{}{"auth_result": "{{result}}", "auth_code": "{{code}}"},
				"err_node_id": "e1"},
			{"type": "go", "to_node_id": "n2"},
		}},
		{id: "n2", title: "Map outcome", objType: 0, logics: []map[string]interface{}{
			{"type": "api_code", "lang": "js", "err_node_id": "e2",
				"src": "if (data.auth_result === \"success\") { data.respCode = \"302\"; }\nvar c = data[\"auth_code\"];"},
			{"type": "go", "to_node_id": "n3"},
		}},
	}
	_, unused := findNoopNodes(nodes)
	if len(unused) != 0 {
		t.Fatalf("set_param read by a Code node must not be reported unused, got: %+v", unused)
	}
}

// The check must still catch a genuinely dead set_param.
func TestUnusedSetParam_StillCatchesDead(t *testing.T) {
	nodes := []processNode{
		{id: "n1", title: "Setter", objType: 0, logics: []map[string]interface{}{
			{"type": "set_param", "extra": map[string]interface{}{"ghost": "1"}, "err_node_id": "e1"},
			{"type": "go", "to_node_id": "n2"},
		}},
		{id: "n2", title: "Code", objType: 0, logics: []map[string]interface{}{
			{"type": "api_code", "lang": "js", "src": "data.other = 2;", "err_node_id": "e2"},
		}},
	}
	_, unused := findNoopNodes(nodes)
	if len(unused) != 1 {
		t.Fatalf("expected the dead set_param to be reported, got: %+v", unused)
	}
}

// A prefix must not count: data.result_code does not read `result`.
func TestReferencesVar_NoPrefixFalseNegative(t *testing.T) {
	if referencesVar("data.result_code = 1;", "result") {
		t.Error("data.result_code must not count as a read of `result`")
	}
	if !referencesVar("data.result = 1;", "result") {
		t.Error("data.result must count as a read of `result`")
	}
	if !referencesVar("x = {{result}}", "result") {
		t.Error("template form must still count")
	}
	if !referencesVar("var v = data['result'];", "result") {
		t.Error("single-quoted bracket form must count")
	}
}
