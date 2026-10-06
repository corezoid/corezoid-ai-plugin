package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCorezoidAPI records the Authorization header and the company_id of
// every op it receives, and answers each op with an empty successful list.
type fakeCorezoidAPI struct {
	mu    sync.Mutex
	calls []fakeAPICall
}

type fakeAPICall struct {
	Auth      string
	CompanyID string
	Path      string
}

func (f *fakeCorezoidAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		_ = json.Unmarshal(body, &payload)
		company := ""
		if len(payload.Ops) > 0 {
			company, _ = payload.Ops[0]["company_id"].(string)
		}
		f.mu.Lock()
		f.calls = append(f.calls, fakeAPICall{Auth: r.Header.Get("Authorization"), CompanyID: company, Path: r.URL.Path})
		f.mu.Unlock()
		ops := make([]map[string]interface{}, len(payload.Ops))
		for i := range ops {
			ops[i] = map[string]interface{}{"proc": "ok", "list": []interface{}{}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"request_proc": "ok", "ops": ops})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeCorezoidAPI) snapshot() []fakeAPICall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeAPICall(nil), f.calls...)
}

// enableHostedForTest switches the package into hosted mode against apiURL and
// restores everything afterwards.
func enableHostedForTest(t *testing.T, apiURL string) {
	t.Helper()
	prevMode, prevURL, prevAnalytics := hostedMode, hostedAPIURL, analyticsEnabled.Load()
	hostedMode, hostedAPIURL = true, apiURL
	analyticsEnabled.Store(false)
	t.Cleanup(func() {
		hostedMode, hostedAPIURL = prevMode, prevURL
		analyticsEnabled.Store(prevAnalytics)
	})
}

var testHostedCfg = hostedConfig{
	ResourceURL:   "https://mcp.corezoid.com",
	AuthServerURL: "https://account.corezoid.com",
}

func hostedPost(t *testing.T, url, auth, body string) (int, http.Header, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, resp.Header, out
}

func toolText(t *testing.T, resp map[string]interface{}) (string, bool) {
	t.Helper()
	res, _ := resp["result"].(map[string]interface{})
	content, _ := res["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("no tool content in %v", resp)
	}
	text, _ := content[0].(map[string]interface{})["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func callBody(tool string, args map[string]interface{}) string {
	b, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": tool, "arguments": args},
	})
	return string(b)
}

func TestHostedRefusesAnonymous(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	for _, auth := range []string{"", "Bearer", "Bearer ", "simulator"} {
		code, hdr, _ := hostedPost(t, ts.URL+"/mcp", auth, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
		if code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: status %d, want 401", auth, code)
		}
		want := `Bearer resource_metadata="https://mcp.corezoid.com/.well-known/oauth-protected-resource"`
		if got := hdr.Get("WWW-Authenticate"); got != want {
			t.Errorf("Authorization %q: WWW-Authenticate %q, want %q", auth, got, want)
		}
	}

	resp, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var meta map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&meta)
	if meta["resource"] != "https://mcp.corezoid.com" {
		t.Errorf("metadata %v", meta)
	}
}

func TestHostedInitializeIsStatelessAndToolsOnly(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	code, hdr, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if code != http.StatusOK {
		t.Fatalf("initialize: %d", code)
	}
	if hdr.Get("Mcp-Session-Id") != "" {
		t.Error("hosted mode minted a session id")
	}
	caps := out["result"].(map[string]interface{})["capabilities"].(map[string]interface{})
	if _, ok := caps["resources"]; ok {
		t.Error("resources advertised: they are local workspace files")
	}
	if _, ok := caps["prompts"]; ok {
		t.Error("prompts advertised: they drive local workflows")
	}
	for _, m := range []string{"resources/list", "resources/read", "prompts/list"} {
		_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", `{"jsonrpc":"2.0","id":2,"method":"`+m+`","params":{"uri":"corezoid://x"}}`)
		if out["error"] == nil {
			t.Errorf("%s answered in hosted mode: %v", m, out)
		}
	}
}

func TestHostedToolListOnlyAPITools(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	tools := hostedToolRegistry()
	seen := map[string]mcpTool{}
	for _, d := range tools {
		seen[d.Name] = d
	}
	for _, banned := range []string{"login", "logout", "pull-folder", "layout-process", "cz-git-context", "cz-snapshots", "send-feedback"} {
		if _, ok := seen[banned]; ok {
			t.Errorf("%s listed in hosted mode", banned)
		}
	}
	for _, d := range tools {
		if strings.HasPrefix(d.Name, "cz-") {
			t.Errorf("router %s listed in hosted mode; actions must be flat tools", d.Name)
		}
		if _, ok := hostedAllowedTools[d.Name]; !ok {
			t.Errorf("%s listed but not hosted-allowed", d.Name)
		}
	}
	if len(tools) != len(hostedAllowedTools) {
		t.Errorf("hosted tools/list has %d tools, want one per allowed tool (%d)", len(tools), len(hostedAllowedTools))
	}
	for _, d := range tools {
		props := d.InputSchema.(map[string]interface{})["properties"].(map[string]interface{})
		if _, ok := props[hostedScopeArg]; !ok {
			t.Errorf("%s has no scope argument", d.Name)
		}
	}
	// The shared registry must not have been mutated.
	for _, d := range toolRegistry {
		if p, ok := d.InputSchema.(map[string]interface{})["properties"].(map[string]interface{}); ok {
			if _, has := p[hostedScopeArg]; has {
				t.Fatalf("withScopeArg mutated the shared registry (%s)", d.Name)
			}
		}
	}
}

// A disallowed tool is refused however it is reached: directly by name or as
// a router action.
func TestHostedRefusesLocalTools(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	scope := map[string]interface{}{"company_id": "i1", "stage_id": 5}
	for _, c := range []struct {
		tool string
		args map[string]interface{}
	}{
		{"layout-process", map[string]interface{}{"process_path": "1_x.conv.json", "scope": scope}},
		{"login", map[string]interface{}{}},
		{"create-folder", map[string]interface{}{"title": "x", "scope": scope}},
		{"cz-structure", map[string]interface{}{"action": "create-folder", "args": map[string]interface{}{"title": "x"}, "scope": scope}},
		{"cz-git-context", map[string]interface{}{"action": "read-context-file", "args": map[string]interface{}{"path": "../../etc/passwd"}, "scope": scope}},
	} {
		_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody(c.tool, c.args))
		text, isErr := toolText(t, out)
		if !isErr || !strings.Contains(text, "not available on the hosted") {
			t.Errorf("%s: got %q (isError=%v), want a hosted refusal", c.tool, text, isErr)
		}
	}
}

// Every API call carries the caller's own token and company, and two callers
// never see each other's identity — not even with credentials sitting in the
// process globals.
func TestHostedUsesRequestIdentityOnly(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	authStateMu.Lock()
	prevTok, prevWS, prevStage, prevLogin, prevSecret := apiToken, workspaceID, stageID, apiLogin, apiSecret
	apiToken, workspaceID, stageID, apiLogin, apiSecret = "GLOBAL-TOKEN", "GLOBAL-COMPANY", 999, "GLOBAL-LOGIN", "GLOBAL-SECRET"
	authStateMu.Unlock()
	t.Cleanup(func() {
		authStateMu.Lock()
		apiToken, workspaceID, stageID, apiLogin, apiSecret = prevTok, prevWS, prevStage, prevLogin, prevSecret
		authStateMu.Unlock()
	})

	var wg sync.WaitGroup
	for _, caller := range []struct{ tok, company string }{{"tok-A", "i-A"}, {"tok-B", "i-B"}} {
		wg.Add(1)
		go func(tok, company string) {
			defer wg.Done()
			_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer "+tok, callBody("cz-structure", map[string]interface{}{
				"action": "list-folders", "args": map[string]interface{}{"folder_id": 10},
				"scope": map[string]interface{}{"company_id": company, "stage_id": 7},
			}))
			if text, isErr := toolText(t, out); isErr {
				t.Errorf("%s: list-folders failed: %s", tok, text)
			}
		}(caller.tok, caller.company)
	}
	wg.Wait()

	calls := api.snapshot()
	if len(calls) == 0 {
		t.Fatal("no API call reached the fake Corezoid API")
	}
	for _, c := range calls {
		if strings.Contains(c.Auth, "GLOBAL") || c.CompanyID == "GLOBAL-COMPANY" || strings.Contains(c.Path, "GLOBAL") {
			t.Errorf("hosted call used process globals: %+v", c)
		}
		ok := (c.Auth == "Simulator tok-A" && c.CompanyID == "i-A") || (c.Auth == "Simulator tok-B" && c.CompanyID == "i-B")
		if !ok {
			t.Errorf("call mixed identities or lost them: %+v", c)
		}
	}
}

func TestHostedRequiresStageForScopedTools(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("cz-structure", map[string]interface{}{
		"action": "list-folders", "args": map[string]interface{}{"folder_id": 10},
	}))
	if text, isErr := toolText(t, out); !isErr || !strings.Contains(text, "scope.stage_id") {
		t.Errorf("got %q (isError=%v), want a scope.stage_id hint", text, isErr)
	}
	if n := len(api.snapshot()); n != 0 {
		t.Errorf("%d API calls made without a stage", n)
	}

	// Discovery tools need no stage.
	_, _, out = hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("cz-structure", map[string]interface{}{"action": "list-workspaces"}))
	if text, isErr := toolText(t, out); isErr {
		t.Errorf("list-workspaces without scope failed: %s", text)
	}
}

func TestHostedProjectIDIsNotCached(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	authStateMu.Lock()
	prev := cachedProjectID
	cachedProjectID = 4242
	authStateMu.Unlock()
	t.Cleanup(func() { authStateMu.Lock(); cachedProjectID = prev; authStateMu.Unlock() })

	v := &Executor{Ctx: t.Context(), APIUrl: hostedAPIURL, Token: "tok", StageID: 0}
	if id, _ := resolveAndCacheProjectID(v); id == 4242 {
		t.Error("hosted mode returned the process-wide cached project id")
	}
}

func TestBearerToken(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "Bearer": "", "Bearer ": "", "bearer   ": "", "Simulator": "",
		"Bearer abc": "abc", "SIMULATOR abc": "abc", "abc": "abc", "  Bearer  abc ": "abc",
		"Bearer a b": "",
	} {
		if got := bearerToken(in); got != want {
			t.Errorf("bearerToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTakeHostedScope(t *testing.T) {
	good := map[string]interface{}{"scope": map[string]interface{}{"company_id": " i1 ", "stage_id": float64(7)}, "x": 1}
	c, s, err := takeHostedScope(good)
	if err != nil || c != "i1" || s != 7 {
		t.Fatalf("got %q %d %v", c, s, err)
	}
	if _, still := good["scope"]; still {
		t.Error("scope not removed from args")
	}
	for name, bad := range map[string]interface{}{
		"not an object": "i1",
		"unknown key":   map[string]interface{}{"token": "x"},
		"negative":      map[string]interface{}{"stage_id": float64(-1)},
		"fraction":      map[string]interface{}{"stage_id": 1.5},
		"company type":  map[string]interface{}{"company_id": 5},
	} {
		if _, _, err := takeHostedScope(map[string]interface{}{"scope": bad}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if c, s, err := takeHostedScope(map[string]interface{}{}); err != nil || c != "" || s != 0 {
		t.Errorf("absent scope: %q %d %v", c, s, err)
	}
}

func TestLoadHostedConfigRequiresHTTPS(t *testing.T) {
	for _, u := range []string{"http://admin.corezoid.com", "https://user@admin.corezoid.com", "admin.corezoid.com"} {
		t.Setenv("COREZOID_HOSTED_API_URL", u)
		if _, err := loadHostedConfig(":0"); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	t.Setenv("COREZOID_HOSTED_API_URL", "")
	cfg, err := loadHostedConfig(":0")
	if err != nil || cfg.APIURL != hostedDefaultAPIURL || cfg.AuthServerURL != hostedDefaultAuthServerURL {
		t.Errorf("defaults: %+v %v", cfg, err)
	}
}

func TestHostedOpenAIAppsChallenge(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	get := func(cfg hostedConfig) (int, string, string) {
		ts := httptest.NewServer(newHostedHandler(cfg))
		defer ts.Close()
		resp, err := http.Get(ts.URL + "/.well-known/openai-apps-challenge")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Content-Type")
	}
	if code, _, _ := get(testHostedCfg); code != http.StatusNotFound {
		t.Errorf("unset: %d, want 404", code)
	}
	cfg := testHostedCfg
	cfg.OpenAIAppsChallenge = "tok-xyz"
	if code, body, ct := get(cfg); code != http.StatusOK || body != "tok-xyz" || !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("set: %d %q %q", code, body, ct)
	}
}

func TestHostedToolsHaveTitles(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	for _, d := range hostedToolRegistry() {
		if d.Annotations == nil || d.Annotations.Title == "" || d.Title == "" {
			t.Errorf("%s has no title", d.Name)
		}
	}
	for in, want := range map[string]string{"pull-process": "Pull process", "cz-structure": "Corezoid structure", "run-task": "Run task"} {
		if got := humanizeToolName(in); got != want {
			t.Errorf("humanizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, d := range toolRegistry {
		if d.Title != "" || (d.Annotations != nil && d.Annotations.Title != "") {
			t.Fatalf("hosted titles leaked into the shared registry (%s)", d.Name)
		}
	}
}

// Hosted descriptions must not send the model to the local workspace: the
// review scanners compare each description with what the tool can do there.
func TestHostedDescriptionsHaveNoLocalReferences(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	local := []string{".stage.json", "marker", "pull-folder", "to disk", "process_path", ".conv.json", "modify-variable", "delete-variable"}
	for _, d := range hostedToolRegistry() {
		schema, _ := json.Marshal(d.InputSchema)
		for _, l := range local {
			if strings.Contains(d.Description, l) || strings.Contains(string(schema), l) {
				t.Errorf("%s description or schema mentions %q", d.Name, l)
			}
		}
	}
	// The overrides work on copies.
	for _, d := range allToolDefs() {
		if d.Name == "create-dashboard" {
			schema, _ := json.Marshal(d.InputSchema)
			if !strings.Contains(string(schema), ".stage.json") {
				t.Fatal("hosted argument descriptions mutated the shared registry")
			}
		}
	}
}
