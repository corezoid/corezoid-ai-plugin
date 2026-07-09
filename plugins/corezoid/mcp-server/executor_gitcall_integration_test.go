package main

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// gitCallLang is one language case for the multi-language build test. The Code
// field doubles as living documentation of the git_call handler contract for
// each supported runtime — it is exactly what a user writes in a git_call node.
type gitCallLang struct {
	lang string
	code string
	want string // the value handle() must place in data["r"]
}

// gitCallLangs covers several runtimes end-to-end. Each is built on the
// container build service and run, and the Code is exactly what a user writes in
// a git_call node — so this table doubles as a per-language usage reference.
// (Go is exercised via repo mode / go.mod elsewhere: an inline Go snippet can't
// resolve the external gitcall-go-runner import without a module file.)
var gitCallLangs = []gitCallLang{
	{
		lang: "js",
		code: "module.exports = (data) => { data.r = 'js-ok'; return data; };",
		want: "js-ok",
	},
	{
		lang: "python",
		code: "def handle(data):\n    data['r'] = 'python-ok'\n    return data\n",
		want: "python-ok",
	},
	{
		lang: "php",
		code: "<?php\nfunction handle($data) {\n    $data['r'] = 'php-ok';\n    return $data;\n}\n",
		want: "php-ok",
	},
}

// TestGitCallBuildIntegration exercises the real push build path
// (modify -> BuildGitCallNodes over the live WebSocket -> Commit -> run) against
// a live Corezoid workspace, once per supported language (js, python, go, php).
// It both proves multi-language build/deploy works and documents each runtime's
// handler contract. Skipped unless COREZOID_GITCALL_IT=1 and the connection env
// is present, so `go test ./...` stays offline/deterministic.
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

	for _, c := range gitCallLangs {
		c := c
		t.Run(c.lang, func(t *testing.T) {
			deployAndRunGitCall(t, v, conv, ws, start, final, c)
		})
	}
}

// deployAndRunGitCall creates a fresh git_call node for one language, builds it
// over the live WebSocket (a no-op for interpreted runtimes), commits, fires a
// task and asserts handle() ran — the exact path push-process takes for a
// git_call node. Create + modify + build + commit all share one version so the
// draft is consistent.
func deployAndRunGitCall(t *testing.T, v *Executor, conv int, ws, start, final string, c gitCallLang) {
	v.Version = int(time.Now().Unix())
	gitCallDiscardDraft(v, conv, ws)

	localID := "it-gitcall-" + c.lang
	cr, err := v.req("it_create", []map[string]any{{"id": localID, "type": "create", "obj": "node", "conv_id": conv, "title": "it-gitcall", "obj_type": 0, "version": v.Version}})
	if err != nil {
		t.Fatalf("[%s] create node: %v", c.lang, err)
	}
	serverID, _ := gitCallFirstOp(cr)["obj_id"].(string)
	if serverID == "" {
		t.Fatalf("[%s] no server node id in create response: %v", c.lang, cr)
	}
	v.NodeIDMap[localID] = NodeInfo{ServerID: serverID}

	gitLogic := map[string]any{"type": "git_call", "version": 2, "lang": c.lang, "code": c.code, "src": c.code,
		"repo": "", "commit": "", "path": "", "script": "", "log": map[string]any{}, "err_node_id": final, "code_error": false}
	if _, err := v.req("it_modify", []map[string]any{
		{"type": "modify", "obj": "node", "obj_id": serverID, "company_id": ws, "conv_id": conv, "title": "it-gitcall", "obj_type": 0, "options": nil,
			"logics": []any{gitLogic, map[string]any{"type": "go", "to_node_id": final}}, "semaphors": []any{}, "position": []int{400, 120}, "extra": map[string]any{"modeForm": "expand", "icon": ""}, "version": v.Version},
		{"type": "modify", "obj": "node", "obj_id": start, "company_id": ws, "conv_id": conv, "title": "Start", "obj_type": 1, "options": nil,
			"logics": []any{map[string]any{"type": "go", "to_node_id": serverID}}, "semaphors": []any{}, "extra": map[string]any{"modeForm": "expand", "icon": ""}, "version": v.Version},
	}); err != nil {
		t.Fatalf("[%s] modify nodes: %v", c.lang, err)
	}

	// The unit under test: build the git_call node the way push-process does.
	nodes := []interface{}{map[string]interface{}{
		"id": localID, "title": "it-gitcall",
		"condition": map[string]interface{}{"logics": []interface{}{gitLogic}},
	}}
	buildStart := time.Now()
	if err := v.BuildGitCallNodes(nodes); err != nil {
		t.Fatalf("[%s] BuildGitCallNodes: %v", c.lang, err)
	}
	t.Logf("[%s] build finished in %s", c.lang, time.Since(buildStart).Round(time.Second))

	if resp := v.Commit(); resp == nil || gitCallFirstOp(resp)["proc"] == "error" {
		t.Fatalf("[%s] commit rejected after build: %v", c.lang, resp)
	}

	ref := c.lang + "-it-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := v.req("it_task", []map[string]any{{"type": "create", "obj": "task", "conv_id": conv, "ref": ref, "data": map[string]any{"in": "x"}}}); err != nil {
		t.Fatalf("[%s] create task: %v", c.lang, err)
	}
	for i := 0; i < 12; i++ {
		time.Sleep(3 * time.Second)
		g, err := v.req("it_show", []map[string]any{{"type": "show", "obj": "task", "conv_id": conv, "ref": ref}})
		if err != nil {
			continue
		}
		if data, ok := gitCallFirstOp(g)["data"].(map[string]interface{}); ok {
			if res, ok := data["r"].(string); ok && res != "" {
				if res != c.want {
					t.Fatalf("[%s] unexpected handle() result: got %q, want %q", c.lang, res, c.want)
				}
				t.Logf("✅ [%s] git_call executed end-to-end: r=%q", c.lang, res)
				return
			}
		}
	}
	t.Fatalf("[%s] task did not produce a git_call result in time", c.lang)
}

// gitCallDiscardDraft drops any leftover uncommitted draft so the next commit is clean.
func gitCallDiscardDraft(v *Executor, conv int, ws string) {
	lst, err := v.req("it_list", []map[string]any{{"type": "list", "obj": "conv", "obj_id": conv, "company_id": ws}})
	if err != nil {
		return
	}
	if cm, ok := gitCallFirstOp(lst)["commits"].(map[string]interface{}); ok {
		if ver, ok := cm["version"].(float64); ok && ver > 0 {
			_, _ = v.req("it_del", []map[string]any{{"type": "delete", "obj": "commits", "company_id": ws, "conv_id": conv, "version": int(ver)}})
		}
	}
}

// gitCallFirstOp returns ops[0] of a Corezoid API response, or an empty map.
func gitCallFirstOp(m map[string]interface{}) map[string]interface{} {
	if ops, ok := m["ops"].([]interface{}); ok && len(ops) > 0 {
		if x, ok := ops[0].(map[string]interface{}); ok {
			return x
		}
	}
	return map[string]interface{}{}
}
