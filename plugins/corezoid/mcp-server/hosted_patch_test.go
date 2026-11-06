package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// scriptedAPI answers each Corezoid op by "type obj" (e.g. "list conv") from
// a table, records the ops it saw, and serves GET /download as the exported
// scheme. An op missing from the table fails with proc=error.
type scriptedAPI struct {
	mu      sync.Mutex
	answers map[string]map[string]interface{}
	export  interface{}
	seen    []string
}

func (s *scriptedAPI) start(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(s.export)
			return
		}
		var payload struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		out := make([]interface{}, 0, len(payload.Ops))
		for _, op := range payload.Ops {
			key := fmt.Sprintf("%v %v", op["type"], op["obj"])
			s.mu.Lock()
			s.seen = append(s.seen, key)
			ans, ok := s.answers[key]
			s.mu.Unlock()
			if key == "download obj_scheme" && s.export != nil {
				ans, ok = map[string]interface{}{"proc": "ok", "download_url": srv.URL + "/download"}, true
			}
			if !ok {
				ans = map[string]interface{}{"proc": "error", "description": "no answer for " + key}
			}
			out = append(out, ans)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"request_proc": "ok", "ops": out})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *scriptedAPI) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func scriptedExecutor(t *testing.T, s *scriptedAPI) *Executor {
	t.Helper()
	return &Executor{APIUrl: s.start(t).URL, Token: "tok", Ctx: context.Background(), ProcessID: 7, WorkspaceID: "i1", NodeIDMap: map[string]NodeInfo{}}
}

func draftGuardAnswers(conv, child map[string]interface{}) map[string]map[string]interface{} {
	a := map[string]map[string]interface{}{
		"show conv": {"proc": "ok", "obj_id": 7, "status": "active", "stage_id": 5, "parent_obj_id": 40},
	}
	if conv != nil {
		a["list conv"] = conv
	}
	if child != nil {
		a["list folder"] = map[string]interface{}{"proc": "ok", "list": []interface{}{
			map[string]interface{}{"obj": "conv", "obj_id": 8, "is_deployed": false, "version": 1790000001},
			child,
		}}
	}
	return a
}

func TestHostedDraftGuard(t *testing.T) {
	deployed := map[string]interface{}{"proc": "ok", "change_time": 100, "last_confirmed_version": 12}
	draft := map[string]interface{}{"obj": "conv", "obj_id": 7, "is_deployed": false, "version": 1790000000}
	clean := map[string]interface{}{"obj": "conv", "obj_id": 7, "is_deployed": true}
	for _, c := range []struct {
		name    string
		answers map[string]map[string]interface{}
		want    string // "" = deploy allowed
	}{
		{"deployed, no draft", draftGuardAnswers(deployed, clean), ""},
		{"deployed, draft pending", draftGuardAnswers(deployed, draft), "not deployed (draft version 1790000000)"},
		// Installations that send commits.version and no last_confirmed_version.
		{"commits.version only, draft pending", draftGuardAnswers(map[string]interface{}{"proc": "ok", "commits": map[string]interface{}{"version": 12}}, draft), "not deployed"},
		{"never deployed", draftGuardAnswers(map[string]interface{}{"proc": "ok", "commits": map[string]interface{}{}}, nil), ""},
		{"list conv error", draftGuardAnswers(map[string]interface{}{"proc": "error", "description": "access denied"}, clean), "access denied"},
		{"list conv missing", draftGuardAnswers(nil, clean), "could not read process #7"},
		{"list folder error", draftGuardAnswers(deployed, nil), "could not list folder #40"},
		{"process not in folder", draftGuardAnswers(deployed, map[string]interface{}{"obj": "folder", "obj_id": 7}), "not in the listing of its folder #40"},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := &scriptedAPI{answers: c.answers}
			msg := hostedDraftGuard(scriptedExecutor(t, api), 7)
			if c.want == "" {
				if msg != "" {
					t.Fatalf("refused: %s", msg)
				}
				return
			}
			if !strings.Contains(msg, c.want) || !strings.Contains(msg, "discard_draft=true") {
				t.Fatalf("got %q, want %q and the discard_draft way out", msg, c.want)
			}
		})
	}
}

// A never-deployed process is settled by list conv; the folder is not listed.
func TestHostedDraftGuardNeverDeployedSkipsFolder(t *testing.T) {
	api := &scriptedAPI{answers: draftGuardAnswers(map[string]interface{}{"proc": "ok", "commits": map[string]interface{}{}}, nil)}
	if msg := hostedDraftGuard(scriptedExecutor(t, api), 7); msg != "" {
		t.Fatal(msg)
	}
	for _, c := range api.calls() {
		if c == "list folder" {
			t.Fatalf("listed the folder for a never-deployed process: %v", api.calls())
		}
	}
}

// Through the hosted handler: a pending draft stops the patch before the
// scheme is exported or anything is deployed; discard_draft skips the check.
func TestHostedPatchProcessDraftRefusedBeforeExport(t *testing.T) {
	api := &scriptedAPI{answers: draftGuardAnswers(
		map[string]interface{}{"proc": "ok", "change_time": 100, "last_confirmed_version": 12},
		map[string]interface{}{"obj": "conv", "obj_id": 7, "is_deployed": false, "version": 1790000000})}
	enableHostedForTest(t, api.start(t).URL)
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()
	args := map[string]interface{}{
		"process_id": 7, "base": "v1:7:100:12",
		"ops":   []interface{}{map[string]interface{}{"op": "set", "node": "Start", "set": map[string]interface{}{"title": "Go"}}},
		"scope": map[string]interface{}{"company_id": "i1", "stage_id": 5},
	}
	_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("patch-process", args))
	text, isErr := toolText(t, out)
	if !isErr || !strings.Contains(text, "not deployed") || !strings.Contains(text, "discard_draft=true") {
		t.Fatalf("isError=%v %q", isErr, text)
	}
	if got := strings.Join(api.calls(), ","); got != "show conv,show conv,list conv,list folder" {
		t.Fatalf("API calls before the refusal = %s", got)
	}

	args["discard_draft"] = true
	_, _, _ = hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("patch-process", args))
	calls := api.calls()[4:]
	if len(calls) < 2 || calls[0] != "show conv" || calls[1] == "list conv" {
		t.Fatalf("discard_draft still ran the draft check: %v", calls)
	}
}

func TestPatchPushSourceAfterDeploy(t *testing.T) {
	doc := stripeDoc(t)
	api := &scriptedAPI{
		answers: map[string]map[string]interface{}{"list conv": {"proc": "ok", "change_time": 200, "last_confirmed_version": 13}},
		export:  []interface{}{doc},
	}
	v := scriptedExecutor(t, api)
	src := patchPushSource{changed: []string{"68cd3f94b677ac95d109fb49"}, added: []string{"Create params"}, addedCount: 2, removed: 1}
	notes, out := src.afterDeploy(context.Background(), v)
	if notes != nil {
		t.Fatalf("stale notes: %v", notes)
	}
	for _, want := range []string{
		"base: v1:7:200:13\n",
		"Deployed: 26 nodes, 1 removed.",
		`"id":"68cd3f94b677ac95d109fb49","title":"Configuration"`,
		`"title":"Create params"`,
		"1 added node(s) without a title of their own",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"title":"Prepare Request"`) {
		t.Errorf("answered a node the patch did not touch:\n%s", out)
	}
}

func TestPatchPushSourceAfterDeployReadBackFails(t *testing.T) {
	api := &scriptedAPI{answers: map[string]map[string]interface{}{}}
	_, out := patchPushSource{}.afterDeploy(context.Background(), scriptedExecutor(t, api))
	if !strings.Contains(out, "base: unavailable") || !strings.Contains(out, "could not be read back") || !strings.Contains(out, "pull-process outline=true") {
		t.Fatalf("%q", out)
	}
	if _, out := (patchPushSource{}).afterDeploy(context.Background(), &Executor{}); out != "" {
		t.Fatalf("no process id answered %q", out)
	}
}
