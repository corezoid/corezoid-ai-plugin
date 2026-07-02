package main

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestGitCallBuildIntegration exercises the real push build path
// (modify -> BuildGitCallNodes over the live WebSocket -> Commit -> run) against
// a live Corezoid workspace. It is skipped unless COREZOID_GITCALL_IT=1 and the
// connection env is present, so `go test ./...` stays offline/deterministic.
//
// Required env:
//
//	COREZOID_GITCALL_IT=1
//	ACCESS_TOKEN, COREZOID_API_URL, WORKSPACE_ID
//	COREZOID_IT_CONV     — a throwaway process id with a Start->Final scaffold
//	COREZOID_IT_START    — Start node server id
//	COREZOID_IT_FINAL    — Final node server id
func TestGitCallBuildIntegration(t *testing.T) {
	if os.Getenv("COREZOID_GITCALL_IT") != "1" {
		t.Skip("set COREZOID_GITCALL_IT=1 (and connection env) to run the live git_call build test")
	}
	token := os.Getenv("ACCESS_TOKEN")
	apiURL := os.Getenv("COREZOID_API_URL")
	ws := os.Getenv("WORKSPACE_ID")
	convStr := os.Getenv("COREZOID_IT_CONV")
	start := os.Getenv("COREZOID_IT_START")
	final := os.Getenv("COREZOID_IT_FINAL")
	if token == "" || apiURL == "" || convStr == "" || start == "" || final == "" {
		t.Fatal("missing integration env (ACCESS_TOKEN/COREZOID_API_URL/WORKSPACE_ID/COREZOID_IT_CONV/COREZOID_IT_START/COREZOID_IT_FINAL)")
	}
	conv, err := strconv.Atoi(convStr)
	if err != nil {
		t.Fatalf("bad COREZOID_IT_CONV: %v", err)
	}

	v := &Executor{
		Ctx:         context.Background(),
		Token:       token,
		APIUrl:      apiURL,
		WorkspaceID: ws,
		ProcessID:   conv,
		Version:     int(time.Now().Unix()),
		NodeIDMap:   map[string]NodeInfo{},
	}
	first := func(m map[string]interface{}) map[string]interface{} {
		if ops, ok := m["ops"].([]interface{}); ok && len(ops) > 0 {
			if x, ok := ops[0].(map[string]interface{}); ok {
				return x
			}
		}
		return map[string]interface{}{}
	}

	// Discard any leftover uncommitted draft so version/commit is clean.
	if lst, err := v.req("it_list", []map[string]any{{"type": "list", "obj": "conv", "obj_id": conv, "company_id": ws}}); err == nil {
		if cm, ok := first(lst)["commits"].(map[string]interface{}); ok {
			if ver, ok := cm["version"].(float64); ok && ver > 0 {
				_, _ = v.req("it_del", []map[string]any{{"type": "delete", "obj": "commits", "company_id": ws, "conv_id": conv, "version": int(ver)}})
			}
		}
	}

	// Create a fresh python git_call node (compiled -> requires a real build).
	code := "import base64\ndef handle(data):\n    data['result']='it-'+base64.b64encode(b'ok').decode()\n    return data\n"
	localID := "it-gitcall-node"
	cr, err := v.req("it_create", []map[string]any{{"id": localID, "type": "create", "obj": "node", "conv_id": conv, "title": "it-gitcall", "obj_type": 0, "version": v.Version}})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	serverID, _ := first(cr)["obj_id"].(string)
	if serverID == "" {
		t.Fatalf("no server node id in create response: %v", cr)
	}
	v.NodeIDMap[localID] = NodeInfo{ServerID: serverID}

	gitLogic := map[string]any{"type": "git_call", "version": 2, "lang": "python", "code": code, "src": code,
		"repo": "", "commit": "", "path": "", "script": "", "log": map[string]any{}, "err_node_id": final, "code_error": false}
	if _, err := v.req("it_modify", []map[string]any{
		{"type": "modify", "obj": "node", "obj_id": serverID, "company_id": ws, "conv_id": conv, "title": "it-gitcall", "obj_type": 0, "options": nil,
			"logics": []any{gitLogic, map[string]any{"type": "go", "to_node_id": final}}, "semaphors": []any{}, "position": []int{400, 120}, "extra": map[string]any{"modeForm": "expand", "icon": ""}, "version": v.Version},
		{"type": "modify", "obj": "node", "obj_id": start, "company_id": ws, "conv_id": conv, "title": "Start", "obj_type": 1, "options": nil,
			"logics": []any{map[string]any{"type": "go", "to_node_id": serverID}}, "semaphors": []any{}, "extra": map[string]any{"modeForm": "expand", "icon": ""}, "version": v.Version},
	}); err != nil {
		t.Fatalf("modify nodes: %v", err)
	}

	// The unit under test: build the compiled git_call node over the live WS.
	nodes := []interface{}{map[string]interface{}{
		"id": localID, "title": "it-gitcall",
		"condition": map[string]interface{}{"logics": []interface{}{gitLogic}},
	}}
	buildStart := time.Now()
	if err := v.BuildGitCallNodes(nodes); err != nil {
		t.Fatalf("BuildGitCallNodes: %v", err)
	}
	t.Logf("git_call build finished in %s", time.Since(buildStart).Round(time.Second))

	if resp := v.Commit(); resp == nil || first(resp)["proc"] == "error" {
		t.Fatalf("commit rejected after build: %v", resp)
	}

	// Run a task through the node and confirm handle() executed.
	ref := "it-run-" + strconv.FormatInt(time.Now().Unix(), 10)
	if _, err := v.req("it_task", []map[string]any{{"type": "create", "obj": "task", "conv_id": conv, "ref": ref, "data": map[string]any{"in": "x"}}}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	for i := 0; i < 10; i++ {
		time.Sleep(3 * time.Second)
		g, err := v.req("it_show", []map[string]any{{"type": "show", "obj": "task", "conv_id": conv, "ref": ref}})
		if err != nil {
			continue
		}
		if data, ok := first(g)["data"].(map[string]interface{}); ok {
			if res, ok := data["result"].(string); ok && res != "" {
				if res != "it-b2s=" {
					t.Fatalf("unexpected handle() result: %q", res)
				}
				t.Logf("✅ git_call executed end-to-end: result=%q", res)
				return
			}
		}
	}
	t.Fatal("task did not produce a git_call result in time")
}
