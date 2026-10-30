package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Issue #250: push failed with a bare "invalid parameters" (or an empty
// message) after a clean lint. Lint must name the two causes seen in those
// sessions, push must block on them, and a rejection the server still returns
// must name the node, what was sent and the usual causes.

func lintConv(t *testing.T, params string, rpcLogic string) *LintResult {
	t.Helper()
	conv := `{"conv_type":"process","obj_id":0,"obj_type":1,"ref_mask":true,"title":"t","status":"active","params":` + params + `,"scheme":{"nodes":[
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaa1","obj_type":1,"title":"Start","x":0,"y":0,"condition":{"logics":[{"type":"go","to_node_id":"aaaaaaaaaaaaaaaaaaaaaaa2"}],"semaphors":[]}},
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaa2","obj_type":0,"title":"Call child","x":0,"y":200,"condition":{"logics":[` + rpcLogic + `,
	   {"type":"go","to_node_id":"aaaaaaaaaaaaaaaaaaaaaaa3"}],"semaphors":[]}},
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaa3","obj_type":2,"title":"Done","x":0,"y":400,"condition":{"logics":[],"semaphors":[]}},
	 {"id":"aaaaaaaaaaaaaaaaaaaaaaa4","obj_type":2,"title":"Error","x":300,"y":200,"condition":{"logics":[],"semaphors":[]}}
	],"web_settings":[[],[]]}}`
	res, err := lintProcessData([]byte(conv))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const goodRPC = `{"type":"api_rpc","conv_id":123,"err_node_id":"aaaaaaaaaaaaaaaaaaaaaaa4","extra":{"a":"{{a}}"},"extra_type":{"a":"string"},"group":""}`

func TestLint_IncompleteParams(t *testing.T) {
	res := lintConv(t, `[{"name":"x","type":"string","flags":["input"]},
		{"name":"y","type":"string","descr":"","flags":[],"regex":"","regex_error_text":""}]`, goodRPC)
	if len(res.IncompleteParams) != 1 {
		t.Fatalf("want 1 incomplete param, got %+v", res.IncompleteParams)
	}
	got := res.IncompleteParams[0]
	if got.Index != 0 || got.Name != "x" || strings.Join(got.Missing, ",") != "descr,regex,regex_error_text" {
		t.Errorf("unexpected finding %+v", got)
	}
	if out := FormatLintResult(res); !strings.Contains(out, "INCOMPLETE PROCESS PARAMS") || !strings.Contains(out, `params[0] ("x") is missing [descr, regex, regex_error_text]`) {
		t.Errorf("report does not name the param and keys:\n%s", out)
	}
	if _, overridable, _ := categorizeLintForPush(res); overridable != 1 {
		t.Errorf("incomplete params must block the push, overridable=%d", overridable)
	}
}

func TestLint_EmptyParamsAccepted(t *testing.T) {
	if res := lintConv(t, `[]`, goodRPC); len(res.IncompleteParams)+len(res.IncompleteCallProcess) != 0 {
		t.Errorf("valid process flagged: %+v %+v", res.IncompleteParams, res.IncompleteCallProcess)
	}
}

func TestLint_IncompleteCallProcess(t *testing.T) {
	cases := map[string]struct{ logic, want string }{
		"missing data block": {`{"type":"api_rpc","conv_id":123,"err_node_id":"aaaaaaaaaaaaaaaaaaaaaaa4","group":"all"}`, "extra is missing; extra_type is missing"},
		"null extra":         {`{"type":"api_rpc","conv_id":123,"err_node_id":"aaaaaaaaaaaaaaaaaaaaaaa4","extra":null,"extra_type":{}}`, "extra is null, not an object"},
		"untyped key":        {`{"type":"api_rpc","conv_id":123,"err_node_id":"aaaaaaaaaaaaaaaaaaaaaaa4","extra":{"a":"1"},"extra_type":{}}`, `extra key "a" has no matching extra_type entry`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			res := lintConv(t, `[]`, c.logic)
			if len(res.IncompleteCallProcess) != 1 {
				t.Fatalf("want 1 finding, got %+v", res.IncompleteCallProcess)
			}
			f := res.IncompleteCallProcess[0]
			if f.ID != "aaaaaaaaaaaaaaaaaaaaaaa2" || f.Title != "Call child" || !strings.Contains(f.Issue, c.want) {
				t.Errorf("unexpected finding %+v", f)
			}
			// The server rejects these on every deploy, so a pulled version
			// that had one does not make the next push succeed.
			introduced, _ := splitInheritedLint(res, res)
			if _, overridable, _ := categorizeLintForPush(introduced); overridable != 1 {
				t.Errorf("must block even when inherited, overridable=%d", overridable)
			}
		})
	}
	// extra {} / extra_type {} is the documented "pass nothing" shape.
	if res := lintConv(t, `[]`, `{"type":"api_rpc","conv_id":123,"err_node_id":"aaaaaaaaaaaaaaaaaaaaaaa4","extra":{},"extra_type":{},"group":"all"}`); len(res.IncompleteCallProcess) != 0 {
		t.Errorf("empty objects flagged: %+v", res.IncompleteCallProcess)
	}
}

func TestCheckError_RejectedNodeNamesNodeAndCause(t *testing.T) {
	e := &Executor{NodeIDMap: map[string]NodeInfo{"aaaaaaaaaaaaaaaaaaaaaaa2": {ServerID: "bbbbbbbbbbbbbbbbbbbbbbb2"}}}
	var rpc map[string]interface{}
	if err := json.Unmarshal([]byte(`{"type":"api_rpc","conv_id":123,"err_node_id":"x","group":"all"}`), &rpc); err != nil {
		t.Fatal(err)
	}
	ops := []map[string]any{
		{"type": "modify", "obj": "node", "obj_id": "bbbbbbbbbbbbbbbbbbbbbbb1", "title": "Start", "logics": []map[string]interface{}{{"type": "go", "to_node_id": "bbbbbbbbbbbbbbbbbbbbbbb2"}}},
		{"type": "modify", "obj": "node", "obj_id": "bbbbbbbbbbbbbbbbbbbbbbb2", "title": "Call child", "logics": []map[string]interface{}{rpc, {"type": "go", "to_node_id": "x"}}},
	}
	for _, desc := range []string{"invalid parameters", ""} {
		rsp := map[string]interface{}{
			"request_proc": "ok",
			"ops": []interface{}{
				map[string]interface{}{"proc": "ok"},
				map[string]interface{}{"proc": "error", "description": desc},
			},
		}
		err := e.checkError(rsp, ops)
		if err == nil {
			t.Fatal("expected an error")
		}
		msg := err.Error()
		for _, want := range []string{
			`node aaaaaaaaaaaaaaaaaaaaaaa2 ("Call child"), server id bbbbbbbbbbbbbbbbbbbbbbb2`,
			"api_rpc{conv_id, err_node_id, group}",
			"extra is missing; extra_type is missing",
			"Typical causes:",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("description %q: error missing %q:\n%s", desc, want, msg)
			}
		}
		if desc == "" && !strings.Contains(msg, "API op error: unknown error") {
			t.Errorf("empty description must not leave an empty message:\n%s", msg)
		}
	}
}

func TestCheckError_RejectedParamsNamesMissingKeys(t *testing.T) {
	e := &Executor{}
	params := []interface{}{map[string]interface{}{"name": "x", "type": "string"}}
	rsp := map[string]interface{}{
		"request_proc": "ok",
		"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "Params are not valid"}},
	}
	err := e.checkError(rsp, []map[string]any{{"type": "modify", "obj": "conv_params", "params": params}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if msg := err.Error(); !strings.Contains(msg, `params[0] ("x") is missing [descr, flags, regex, regex_error_text]`) {
		t.Errorf("error does not name the param and keys:\n%s", msg)
	}
}

func TestCheckError_OtherOpsUnchanged(t *testing.T) {
	e := &Executor{}
	rsp := map[string]interface{}{
		"request_proc": "ok",
		"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "access denied"}},
	}
	err := e.checkError(rsp, []map[string]any{{"type": "show", "obj": "conv"}})
	if err == nil || err.Error() != "API op error: access denied" {
		t.Errorf("non-deploy op error changed: %v", err)
	}
}
