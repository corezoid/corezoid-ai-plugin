package main

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// stubWSHostResolves makes only the given hosts resolve for the test.
func stubWSHostResolves(t *testing.T, hosts ...string) {
	t.Helper()
	orig := wsHostResolves
	t.Cleanup(func() { wsHostResolves = orig })
	known := map[string]bool{}
	for _, h := range hosts {
		known[h] = true
	}
	wsHostResolves = func(_ context.Context, host string) bool { return known[host] }
}

func TestGitCallWSURL(t *testing.T) {
	stubWSHostResolves(t, "ws.corezoid.com", "ws.eu.corezoid.com", "ws.corp-corezoid.example.org")
	configured := func() string { return "wss://configured.example.com/api/1/sock_json" }

	// COREZOID_WS_URL override wins, even over ws_url from the config.
	t.Setenv("COREZOID_WS_URL", "wss://custom.example.com/api/1/sock_json")
	if got := resolveGitCallWSTarget(nil, configured, "https://admin.corezoid.com").url; got != "wss://custom.example.com/api/1/sock_json" {
		t.Fatalf("override not honored: %q", got)
	}
	_ = os.Unsetenv("COREZOID_WS_URL")

	// ws_url from the config wins over the derived address.
	if got := resolveGitCallWSTarget(nil, configured, "https://admin.corezoid.com").url; got != "wss://configured.example.com/api/1/sock_json" {
		t.Fatalf("configured ws_url not honored: %q", got)
	}

	cases := map[string]string{
		"https://admin.corezoid.com":        "wss://ws.corezoid.com/api/1/sock_json",
		"https://admin.corezoid.com/":       "wss://ws.corezoid.com/api/1/sock_json",
		"https://admin.eu.corezoid.com":     "wss://ws.eu.corezoid.com/api/1/sock_json",
		"https://corp-corezoid.example.org": "wss://ws.corp-corezoid.example.org/api/1/sock_json",
		"":                                  "wss://ws.corezoid.com/api/1/sock_json",
	}
	for api, want := range cases {
		if got := resolveGitCallWSTarget(nil, nil, api).url; got != want {
			t.Errorf("resolveGitCallWSTarget(%q) = %q, want %q", api, got, want)
		}
	}
}

// TestGitCallWSURL_SelfHostedFallsBackToAPIHost reproduces issue #237: a
// self-hosted install has no ws.<domain> record, so the derived sibling host
// cannot be dialled. The API host itself must be used instead.
func TestGitCallWSURL_SelfHostedFallsBackToAPIHost(t *testing.T) {
	t.Setenv("COREZOID_WS_URL", "")
	stubWSHostResolves(t, "corezoid.corp.example", "admin.onprem.example")

	cases := map[string]string{
		"https://corezoid.corp.example":      "wss://corezoid.corp.example/api/1/sock_json",
		"https://admin.onprem.example":       "wss://admin.onprem.example/api/1/sock_json",
		"https://corezoid.corp.example:8443": "wss://corezoid.corp.example:8443/api/1/sock_json",
	}
	for api, want := range cases {
		if got := resolveGitCallWSTarget(nil, nil, api).url; got != want {
			t.Errorf("resolveGitCallWSTarget(%q) = %q, want %q", api, got, want)
		}
	}

	// Nothing resolves: keep the primary guess, and say so in the source.
	stubWSHostResolves(t)
	got := resolveGitCallWSTarget(nil, nil, "https://corezoid.corp.example")
	if got.url != "wss://ws.corezoid.corp.example/api/1/sock_json" || !strings.Contains(got.source, "did not resolve") {
		t.Errorf("unresolvable target = %+v", got)
	}
}

// TestBuildGitCallNode_DialErrorNamesAddressAndSetting: when the build socket
// cannot be reached the error must name the address used and the settings
// that override it, so the agent stops guessing (issue #237).
func TestBuildGitCallNode_DialErrorNamesAddressAndSetting(t *testing.T) {
	t.Setenv("COREZOID_WS_URL", "")
	v := &Executor{Ctx: context.Background(), APIUrl: "https://corezoid.corp.example"}
	target := gitCallWSTarget{url: "ws://127.0.0.1:1/api/1/sock_json", source: `derived from the API URL "https://corezoid.corp.example"`}
	err := v.buildGitCallNode(target, gitCallBuild{nodeServerID: "n1", lang: "python"})
	if err == nil {
		t.Fatal("expected a dial error")
	}
	for _, want := range []string{"ws://127.0.0.1:1/api/1/sock_json", "derived from the API URL", `"ws_url"`, "~/.corezoid/config.json", "COREZOID_WS_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("dial error missing %q:\n%v", want, err)
		}
	}
}

// TestWSTarget_ReadsConfiguredWSURL checks ws_url is taken from the current
// workspace's config entry at use time.
func TestWSTarget_ReadsConfiguredWSURL(t *testing.T) {
	t.Setenv("COREZOID_WS_URL", "")
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COREZOID_WORK_DIR", work)
	if err := UpdateCurrent(func(f *Folder) { f.WSURL = "wss://socket.corp.example/api/1/sock_json" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	v := &Executor{Ctx: context.Background(), APIUrl: "https://corezoid.corp.example"}
	got := v.wsTarget()
	if got.url != "wss://socket.corp.example/api/1/sock_json" || !strings.Contains(got.source, "ws_url") {
		t.Errorf("wsTarget = %+v, want the configured ws_url", got)
	}
}

func TestGitCallWSOrigin(t *testing.T) {
	if got := gitCallWSOrigin("https://admin.corezoid.com"); got != "https://admin.corezoid.com" {
		t.Errorf("origin = %q", got)
	}
	if got := gitCallWSOrigin(""); got != "https://admin.corezoid.com" {
		t.Errorf("fallback origin = %q", got)
	}
}

func TestParseGitCallBuildLog(t *testing.T) {
	cases := []struct {
		name       string
		frame      string
		wantStatus string
		wantMsg    string
	}{
		{"stdout", `{"request_proc":"ok","ops":[{"type":"monitor_show","obj":"git_call","obj_type":"function_build","obj_id":"x","log":{"type":"stdout","message":"Building"}}]}`, "stdout", "Building"},
		{"done", `{"request_proc":"ok","ops":[{"type":"monitor_show","obj":"git_call","obj_type":"function_build","obj_id":"x","log":{"type":"done"}}]}`, "done", ""},
		{"error", `{"ops":[{"obj_type":"function_build","log":{"type":"error","message":"boom"}}]}`, "error", "boom"},
		{"unrelated", `{"ops":[{"obj_type":"something_else","log":{"type":"done"}}]}`, "", ""},
		{"garbage", `not json`, "", ""},
		{"keepalive", `1`, "", ""},
	}
	for _, c := range cases {
		st, msg := parseGitCallBuildLog([]byte(c.frame))
		if st != c.wantStatus || msg != c.wantMsg {
			t.Errorf("%s: got (%q,%q), want (%q,%q)", c.name, st, msg, c.wantStatus, c.wantMsg)
		}
	}
}

func TestNewBuildID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newBuildID()
		if !re.MatchString(id) {
			t.Fatalf("not a UUIDv4: %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate build id: %q", id)
		}
		seen[id] = true
	}
}

func TestGitCallNeedsBuild(t *testing.T) {
	cases := map[string]bool{"js": true, "JS": true, "python": true, "golang": true, "php": true, "java": true, "": false}
	for lang, want := range cases {
		if got := (gitCallBuild{lang: lang}).needsBuild(); got != want {
			t.Errorf("needsBuild(lang=%q) = %v, want %v", lang, got, want)
		}
	}
}

func TestCollectGitCallBuilds(t *testing.T) {
	v := &Executor{NodeIDMap: map[string]NodeInfo{"local-1": {ServerID: "server-abc"}}}
	nodes := []interface{}{
		map[string]interface{}{
			"id": "local-1", "title": "parse",
			"condition": map[string]interface{}{"logics": []interface{}{
				map[string]interface{}{"type": "git_call", "lang": "python", "code": "def handle(d): return d", "repo": ""},
				map[string]interface{}{"type": "go", "to_node_id": "n2"},
			}},
		},
		// api_git alias, source in src, repo mode
		map[string]interface{}{
			"id": "local-2", "title": "fetch",
			"condition": map[string]interface{}{"logics": []interface{}{
				map[string]interface{}{"type": "api_git", "lang": "js", "src": "module.exports=(d)=>d", "repo": "https://x/y.git", "commit": "main"},
			}},
		},
		// non-git node ignored
		map[string]interface{}{"id": "local-3", "condition": map[string]interface{}{"logics": []interface{}{
			map[string]interface{}{"type": "api_code", "lang": "js", "src": "x"},
		}}},
	}
	got := v.collectGitCallBuilds(nodes)
	if len(got) != 2 {
		t.Fatalf("expected 2 git_call builds, got %d", len(got))
	}
	if got[0].nodeServerID != "server-abc" || got[0].lang != "python" || got[0].code == "" {
		t.Errorf("first build resolved wrong: %+v", got[0])
	}
	if !got[0].needsBuild() {
		t.Errorf("python must need build")
	}
	if got[1].nodeServerID != "local-2" { // no NodeIDMap entry -> falls back to local id
		t.Errorf("second build server id = %q, want fallback local-2", got[1].nodeServerID)
	}
	if !got[1].needsBuild() {
		t.Errorf("js must need a container build")
	}
	if got[1].repo != "https://x/y.git" || got[1].commit != "main" {
		t.Errorf("repo-mode fields lost: %+v", got[1])
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", "", "z") != "z" {
		t.Error("want z")
	}
	if firstNonEmpty("a", "b") != "a" {
		t.Error("want a")
	}
	if firstNonEmpty("", "") != "" {
		t.Error("want empty")
	}
}

// TestFixStructSetsGitCallCodeError verifies fixStruct marks git_call/api_git
// logic as built (code_error:false) so Commit accepts the node, without
// clobbering an explicit value.
func TestFixStructSetsGitCallCodeError(t *testing.T) {
	in := `{"obj_type":1,"scheme":{"nodes":[{"id":"n1","obj_type":0,"condition":{"logics":[` +
		`{"type":"git_call","lang":"python","code":"x"},` +
		`{"type":"api_git","lang":"js","src":"y","code_error":true}` +
		`]}}]}}`
	out, _ := fixStruct(in, 0)

	var parsed struct {
		Scheme struct {
			Nodes []struct {
				Condition struct {
					Logics []map[string]interface{} `json:"logics"`
				} `json:"condition"`
			} `json:"nodes"`
		} `json:"scheme"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("fixStruct output is not valid JSON: %v\n%s", err, out)
	}
	logics := parsed.Scheme.Nodes[0].Condition.Logics
	byType := map[string]map[string]interface{}{}
	for _, l := range logics {
		if lt, ok := l["type"].(string); ok {
			byType[lt] = l
		}
	}
	if ce, ok := byType["git_call"]["code_error"]; !ok || ce != false {
		t.Errorf("git_call code_error should be false, got %v (present=%v)", ce, ok)
	}
	if ce := byType["api_git"]["code_error"]; ce != true {
		t.Errorf("explicit api_git code_error:true was clobbered, got %v", ce)
	}
}
