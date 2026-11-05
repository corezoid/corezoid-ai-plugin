package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// stripeDoc is a real 26-node sample process.
func stripeDoc(t *testing.T) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile("../samples/stripe-checkout.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeProcessDoc(raw)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func patchArgs(t *testing.T, ops string) map[string]interface{} {
	t.Helper()
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(`{"ops":`+ops+`}`), &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func TestApplyProcessPatchByIDAndTitle(t *testing.T) {
	doc := stripeDoc(t)
	p, err := parseProcessPatch(patchArgs(t, `[
		{"op":"set","node":"Configuration","set":{"description":"Stripe base URL","logics":[{"type":"go","to_node_id":"68cd4683e552e8d5700ce6a0"}]}},
		{"op":"add","value":{"title":"Audit","obj_type":0,"x":10,"y":20}},
		{"op":"set","node":"68cd4683e552e8d5700ce6a0","set":{"title":"Renamed"}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	p.process = map[string]interface{}{"description": "patched"}
	touched, err := applyProcessPatch(doc, p)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := schemeNodes(doc)
	if len(nodes) != 27 || len(touched) != 3 || touched[0] != "68cd3f94b677ac95d109fb49" || len(touched[1]) != 24 || doc["description"] != "patched" {
		t.Fatalf("%d nodes, touched %v, description %v", len(nodes), touched, doc["description"])
	}
	i, _ := findNode(nodes, "Configuration")
	cond := nodes[i]["condition"].(map[string]interface{})
	if nodes[i]["description"] != "Stripe base URL" || len(cond["logics"].([]interface{})) != 1 || cond["semaphors"] == nil {
		t.Fatalf("%+v", nodes[i])
	}
	added, _ := findNode(nodes, "Audit")
	if c, ok := nodes[added]["condition"].(map[string]interface{}); !ok || c["logics"] == nil {
		t.Fatalf("an added node got no empty condition: %+v", nodes[added])
	}
	if _, err := findNode(nodes, "Renamed"); err != nil {
		t.Fatal(err)
	}
	outline := processOutline(doc, []string{"Configuration"})
	if strings.Count(outline, "\n") != 1 || !strings.Contains(outline, `"kind":"node","logics":["go"],"to":["68cd4683e552e8d5700ce6a0"]`) {
		t.Fatalf("%s", outline)
	}
}

func TestApplyProcessPatchRefusesPrecisely(t *testing.T) {
	for _, c := range []struct{ ops, want string }{
		{`[{"op":"remove","node":"68cd4683e552e8d5700ce6a0"}]`, `logics[1].to_node_id points to 68cd4683e552e8d5700ce6a0, which is not in the process: repoint it in the same patch`},
		{`[{"op":"set","node":"Nope","set":{"title":"x"}}]`, `ops[0] (set): no node has id or title "Nope"; nodes: `},
		{`[{"op":"add","value":{"title":"Dup","obj_type":0,"id":"68cd3f94b677ac95d109fb49"}}]`, "already exists"},
	} {
		doc := stripeDoc(t)
		p, err := parseProcessPatch(patchArgs(t, c.ops))
		if err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(doc)
		if _, err := applyProcessPatch(doc, p); err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("want %q, got %v", c.want, err)
		}
		if after, _ := json.Marshal(doc); string(after) != string(before) {
			t.Errorf("a refused patch changed the document (%s)", c.ops)
		}
	}
	for _, c := range []struct{ ops, want string }{
		{`[{"op":"set","node":"a","set":{"id":"b"}}]`, "set.id is not a node key you can change"},
		{`[{"op":"set","node":"a","unset":["title"]}]`, "unset.title refused"},
		{`[{"op":"add","value":{"obj_type":0}}]`, "value.title (string) is required"},
		{`[{"op":"add","value":{"title":"x"}}]`, "value.obj_type is required"},
		{`[{"op":"rename","node":"a"}]`, `unknown op "rename"; allowed: set, add, remove`},
		{`[]`, "ops (1..100 node edits) or process is required"},
	} {
		if _, err := parseProcessPatch(patchArgs(t, c.ops)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
	if _, err := parseProcessPatch(map[string]interface{}{"process": map[string]interface{}{"conv_type": "state"}}); err == nil || !strings.Contains(err.Error(), "process.conv_type cannot be patched") {
		t.Error(err)
	}
}

// Two nodes with one title are named by id, not guessed.
func TestFindNodeAmbiguousTitle(t *testing.T) {
	nodes := []map[string]interface{}{{"id": "a", "title": "Step"}, {"id": "b", "title": "Step"}}
	if _, err := findNode(nodes, "Step"); err == nil || !strings.Contains(err.Error(), "2 nodes are titled \"Step\" (a, b)") {
		t.Fatal(err)
	}
	if i, err := findNode(nodes, "b"); err != nil || i != 1 {
		t.Fatal(i, err)
	}
}

func TestHandlePatchProcessLocal(t *testing.T) {
	sample, err := os.ReadFile("../samples/stripe-checkout.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	chdirTemp(t, dir)
	if err := os.WriteFile("123_stripe.conv.json", sample, 0644); err != nil {
		t.Fatal(err)
	}
	args := patchArgs(t, `[{"op":"set","node":"Configuration","set":{"title":"Config"}}]`)
	args["process_path"] = "123_stripe.conv.json"
	out, isErr := handlePatchProcess(context.Background(), args)
	if isErr || !strings.Contains(out, "Not deployed — run push-process") || !strings.Contains(out, `"title":"Config"`) || len(out) > 600 {
		t.Fatalf("isErr=%v %s", isErr, out)
	}
	written, _ := os.ReadFile("123_stripe.conv.json")
	doc, err := decodeProcessDoc(written)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := schemeNodes(doc)
	if _, err := findNode(nodes, "Config"); err != nil || len(nodes) != 26 {
		t.Fatal(err, len(nodes))
	}
	args = patchArgs(t, `[{"op":"remove","node":"Config"}]`)
	args["process_path"] = "123_stripe.conv.json"
	if out, isErr := handlePatchProcess(context.Background(), args); !isErr || !strings.Contains(out, "points to") {
		t.Fatalf("dangling remove accepted: %s", out)
	}
	if again, _ := os.ReadFile("123_stripe.conv.json"); string(again) != string(written) {
		t.Fatal("a refused patch rewrote the file")
	}
}

// Hosted patch-process refuses a malformed call before any Corezoid API call.
func TestHostedPatchProcessInputs(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()
	scope := map[string]interface{}{"company_id": "i1", "stage_id": 5}
	ops := []interface{}{map[string]interface{}{"op": "set", "node": "Start", "set": map[string]interface{}{"title": "Go"}}}
	for _, c := range []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"no base", map[string]interface{}{"process_id": 7, "ops": ops, "scope": scope}, "base is required"},
		{"base for another process", map[string]interface{}{"process_id": 7, "base": "v1:8:1:1", "ops": ops, "scope": scope}, "process #8"},
		{"no ops", map[string]interface{}{"process_id": 7, "base": "v1:7:1:1", "scope": scope}, "ops (1..100 node edits)"},
		{"bad op", map[string]interface{}{"process_id": 7, "base": "v1:7:1:1", "ops": []interface{}{map[string]interface{}{"op": "drop"}}, "scope": scope}, `unknown op "drop"`},
		{"process_path", map[string]interface{}{"process_id": 7, "base": "v1:7:1:1", "ops": ops, "process_path": "7_x.conv.json", "scope": scope}, "does not accept process_path"},
		{"no stage", map[string]interface{}{"process_id": 7, "base": "v1:7:1:1", "ops": ops, "scope": map[string]interface{}{"company_id": "i1"}}, "scope.stage_id"},
	} {
		_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("patch-process", c.args))
		text, isErr := toolText(t, out)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s: isError=%v %q, want %q", c.name, isErr, text, c.want)
		}
	}
	if n := len(api.snapshot()); n != 0 {
		t.Errorf("%d API calls for refused patches", n)
	}
}

func TestHostedPullView(t *testing.T) {
	doc := stripeDoc(t)
	if _, compact, err := hostedPullView(doc, map[string]interface{}{}); compact || err != nil {
		t.Fatal("a plain pull answered compact", err)
	}
	full, _ := json.Marshal(doc)
	view, compact, err := hostedPullView(doc, map[string]interface{}{"outline": true, "nodes": []interface{}{"Configuration"}})
	if err != nil || !compact || !strings.Contains(view, "Outline, 26 nodes") || !strings.Contains(view, `"set_param"`) || len(view)*3 > len(full) {
		t.Fatalf("%d of %d bytes: %s", len(view), len(full), view[:min(300, len(view))])
	}
	if _, _, err := hostedPullView(doc, map[string]interface{}{"nodes": []interface{}{"Missing"}}); err == nil {
		t.Fatal("an unknown node read as empty")
	}
}
