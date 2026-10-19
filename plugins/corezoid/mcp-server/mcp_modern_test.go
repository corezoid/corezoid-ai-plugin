package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for the MCP 2026-07-28 behaviour added in AIC-170: per-request _meta,
// UnsupportedProtocolVersionError, resultType / serverInfo on every result,
// cacheable list results, Multi Round-Trip Requests and subscriptions/listen.

// ---- parseRequestMeta -------------------------------------------------------

func TestParseRequestMeta(t *testing.T) {
	cases := []struct {
		name     string
		params   string
		modern   bool
		wantCode int
	}{
		{"no params is legacy", ``, false, 0},
		{"no _meta is legacy", `{"name":"x"}`, false, 0},
		{"modern", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{"elicitation":{}}}}`, true, 0},
		{"unsupported version", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-03-26","io.modelcontextprotocol/clientCapabilities":{}}}`, false, errCodeUnsupportedProtocolVersion},
		{"missing capabilities", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`, false, -32602},
		{"non-string version", `{"_meta":{"io.modelcontextprotocol/protocolVersion":7}}`, false, -32602},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := parseRequestMeta(json.RawMessage(c.params))
			if c.wantCode != 0 {
				if err == nil || err.Code != c.wantCode {
					t.Fatalf("err = %+v, want code %d", err, c.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %+v", err)
			}
			if m.Modern != c.modern {
				t.Errorf("Modern = %v, want %v", m.Modern, c.modern)
			}
		})
	}

	m, _ := parseRequestMeta(json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"io.modelcontextprotocol/clientInfo":{"name":"C","version":"1"},"io.modelcontextprotocol/logLevel":"debug"}}`))
	if !m.supportsElicitation() || m.ClientName != "C" || m.ClientVersion != "1" || m.LogLevel != "debug" {
		t.Errorf("meta = %+v", m)
	}
}

func TestUnsupportedProtocolVersionErrorData(t *testing.T) {
	e := unsupportedProtocolVersionError("1999-01-01")
	raw, _ := json.Marshal(e)
	want := `{"code":-32022,"message":"Unsupported protocol version","data":{"requested":"1999-01-01","supported":["2026-07-28","2025-03-26"]}}`
	if string(raw) != want {
		t.Errorf("got %s\nwant %s", raw, want)
	}
}

// ---- results ----------------------------------------------------------------

func modernReq(t *testing.T, method string, params map[string]interface{}, caps map[string]interface{}) mcpRequest {
	t.Helper()
	if params == nil {
		params = map[string]interface{}{}
	}
	params["_meta"] = modernMeta("test", "0", caps)
	return mcpRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: mustMarshal(t, params)}
}

func resultFields(t *testing.T, v interface{}) map[string]json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(v)
	var resp struct {
		Result map[string]json.RawMessage `json:"result"`
		Error  *mcpError                  `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	return resp.Result
}

func TestEveryResultCarriesResultTypeAndServerInfo(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	reqs := map[string]mcpRequest{
		"server/discover": modernReq(t, "server/discover", nil, nil),
		"tools/list":      modernReq(t, "tools/list", nil, nil),
		"resources/list":  modernReq(t, "resources/list", nil, nil),
		"prompts/list":    modernReq(t, "prompts/list", nil, nil),
		"prompts/get":     modernReq(t, "prompts/get", map[string]interface{}{"name": "pull-workspace"}, nil),
		"tools/call":      modernReq(t, "tools/call", map[string]interface{}{"name": "lint-process", "arguments": map[string]interface{}{"process_path": "missing.json"}}, nil),
		"initialize":      {JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{}`)},
	}
	cacheable := map[string]string{"server/discover": "public", "tools/list": "public", "resources/list": "private", "prompts/list": "public"}
	for name, req := range reqs {
		res := resultFields(t, httpDispatch(context.Background(), req))
		if string(res["resultType"]) != `"complete"` {
			t.Errorf("%s: resultType = %s", name, res["resultType"])
		}
		if !strings.Contains(string(res["_meta"]), metaKeyServerInfo) {
			t.Errorf("%s: _meta lacks serverInfo: %s", name, res["_meta"])
		}
		if scope, ok := cacheable[name]; ok {
			if string(res["cacheScope"]) != `"`+scope+`"` || res["ttlMs"] == nil {
				t.Errorf("%s: cacheScope=%s ttlMs=%s, want %s", name, res["cacheScope"], res["ttlMs"], scope)
			}
		}
	}
	// resources/list returns [] rather than null when there is nothing.
	res := resultFields(t, httpDispatch(context.Background(), reqs["resources/list"]))
	if string(res["resources"]) != `[]` {
		t.Errorf("resources = %s, want []", res["resources"])
	}
}

func TestToolsListOrderIsDeterministic(t *testing.T) {
	first, _ := json.Marshal(resultFields(t, httpDispatch(context.Background(), modernReq(t, "tools/list", nil, nil)))["tools"])
	for i := 0; i < 20; i++ {
		again, _ := json.Marshal(resultFields(t, httpDispatch(context.Background(), modernReq(t, "tools/list", nil, nil)))["tools"])
		if string(again) != string(first) {
			t.Fatal("tools/list order changed between calls")
		}
	}
	var tools []mcpTool
	_ = json.Unmarshal(first, &tools)
	for i, tool := range tools {
		if tool.Name != toolRegistry[i].Name {
			t.Fatalf("tools/list[%d] = %s, want registry order %s", i, tool.Name, toolRegistry[i].Name)
		}
	}
}

func TestResourcesReadNotFoundIsInvalidParams(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	raw, _ := json.Marshal(httpDispatch(context.Background(), modernReq(t, "resources/read", map[string]interface{}{"uri": resourceURIPrefix + "nope.conv.json"}, nil)))
	if !strings.Contains(string(raw), `"code":-32602`) {
		t.Errorf("expected -32602 for a missing resource, got %s", raw)
	}
}

func TestRemovedMethodsAreNotFound(t *testing.T) {
	for _, m := range []string{"ping", "logging/setLevel", "resources/subscribe"} {
		raw, _ := json.Marshal(httpDispatch(context.Background(), modernReq(t, m, nil, nil)))
		if !strings.Contains(string(raw), `"code":-32601`) {
			t.Errorf("%s: expected -32601, got %s", m, raw)
		}
	}
	if r := httpDispatch(context.Background(), mcpRequest{JSONRPC: "2.0", Method: "notifications/roots/list_changed"}); r != nil {
		t.Errorf("notifications/roots/list_changed got a response: %v", r)
	}
}

// ---- Multi Round-Trip Requests ----------------------------------------------

const spyElicitToolName = "test-elicit-spy"

// withSpyElicitTool registers a tool that asks two questions in a row and
// reports both answers, mirroring login's multi-step flow.
func withSpyElicitTool(t *testing.T) {
	t.Helper()
	toolHandlers[spyElicitToolName] = func(ctx context.Context, _ map[string]interface{}) (string, bool) {
		if !clientCanElicit(ctx) {
			return "no-elicit", false
		}
		schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"v": map[string]interface{}{"type": "string"}}}
		a, action, err := elicitValues(ctx, "first?", schema)
		if err != nil || action != "accept" {
			return "aborted-1", true
		}
		b, action, err := elicitValues(ctx, "second?", schema)
		if err != nil || action != "accept" {
			return "aborted-2", true
		}
		return a["v"].(string) + "+" + b["v"].(string), false
	}
	noAuthTools[spyElicitToolName] = struct{}{}
	t.Cleanup(func() {
		delete(toolHandlers, spyElicitToolName)
		delete(noAuthTools, spyElicitToolName)
	})
}

type inputRequired struct {
	ResultType    string `json:"resultType"`
	RequestState  string `json:"requestState"`
	InputRequests map[string]struct {
		Method string `json:"method"`
		Params struct {
			Mode    string `json:"mode"`
			Message string `json:"message"`
		} `json:"params"`
	} `json:"inputRequests"`
	Content []mcpContent `json:"content"`
}

func callElicitSpy(t *testing.T, state string, responses map[string]interface{}) (inputRequired, *mcpError) {
	t.Helper()
	params := map[string]interface{}{"name": spyElicitToolName, "arguments": map[string]interface{}{}}
	if state != "" {
		params["requestState"] = state
		params["inputResponses"] = responses
	}
	raw, _ := json.Marshal(httpDispatch(context.Background(), modernReq(t, "tools/call", params, map[string]interface{}{"elicitation": map[string]interface{}{}})))
	var resp struct {
		Result inputRequired `json:"result"`
		Error  *mcpError     `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Result, resp.Error
}

func onlyKey(t *testing.T, ir inputRequired) string {
	t.Helper()
	if len(ir.InputRequests) != 1 {
		t.Fatalf("inputRequests = %+v, want exactly one", ir.InputRequests)
	}
	for k, v := range ir.InputRequests {
		if v.Method != "elicitation/create" || v.Params.Mode != "form" {
			t.Errorf("input request %q = %+v", k, v)
		}
		return k
	}
	return ""
}

func TestMRTR_MultiRoundToolCall(t *testing.T) {
	withSpyElicitTool(t)

	r1, err := callElicitSpy(t, "", nil)
	if err != nil {
		t.Fatalf("round 1: %+v", err)
	}
	if r1.ResultType != resultTypeInputRequired || r1.RequestState == "" {
		t.Fatalf("round 1 = %+v, want input_required with requestState", r1)
	}
	k1 := onlyKey(t, r1)

	// A retry that does not answer gets the same question again, under a
	// fresh requestState; the old one is spent.
	r1b, err := callElicitSpy(t, r1.RequestState, map[string]interface{}{})
	if err != nil {
		t.Fatalf("round 1 (unanswered retry): %+v", err)
	}
	if onlyKey(t, r1b) != k1 || r1b.RequestState == r1.RequestState {
		t.Fatalf("unanswered retry = %+v", r1b)
	}
	if _, err := callElicitSpy(t, r1.RequestState, map[string]interface{}{k1: map[string]interface{}{"action": "accept", "content": map[string]interface{}{"v": "x"}}}); err == nil || err.Code != -32602 {
		t.Fatalf("redeeming a spent requestState: err = %+v, want -32602", err)
	}

	r2, err := callElicitSpy(t, r1b.RequestState, map[string]interface{}{k1: map[string]interface{}{"action": "accept", "content": map[string]interface{}{"v": "a"}}})
	if err != nil {
		t.Fatalf("round 2: %+v", err)
	}
	k2 := onlyKey(t, r2)
	if k2 == k1 || r2.ResultType != resultTypeInputRequired {
		t.Fatalf("round 2 = %+v", r2)
	}

	r3, err := callElicitSpy(t, r2.RequestState, map[string]interface{}{k2: map[string]interface{}{"action": "accept", "content": map[string]interface{}{"v": "b"}}})
	if err != nil {
		t.Fatalf("round 3: %+v", err)
	}
	if r3.ResultType != resultTypeComplete || len(r3.Content) == 0 || r3.Content[0].Text != "a+b" {
		t.Fatalf("final = %+v, want complete \"a+b\"", r3)
	}
}

func TestMRTR_DeclineEndsCall(t *testing.T) {
	withSpyElicitTool(t)
	r1, _ := callElicitSpy(t, "", nil)
	r2, err := callElicitSpy(t, r1.RequestState, map[string]interface{}{onlyKey(t, r1): map[string]interface{}{"action": "decline"}})
	if err != nil {
		t.Fatal(err)
	}
	if r2.ResultType != resultTypeComplete || r2.Content[0].Text != "aborted-1" {
		t.Fatalf("after decline = %+v", r2)
	}
}

func TestMRTR_RequestStateBoundToTool(t *testing.T) {
	withSpyElicitTool(t)
	r1, _ := callElicitSpy(t, "", nil)
	params := map[string]interface{}{"name": "lint-process", "arguments": map[string]interface{}{}, "requestState": r1.RequestState}
	raw, _ := json.Marshal(httpDispatch(context.Background(), modernReq(t, "tools/call", params, map[string]interface{}{"elicitation": map[string]interface{}{}})))
	if !strings.Contains(string(raw), `"code":-32602`) {
		t.Fatalf("requestState redeemed against another tool: %s", raw)
	}
	// The handle still works for its own tool.
	if _, err := callElicitSpy(t, r1.RequestState, map[string]interface{}{onlyKey(t, r1): map[string]interface{}{"action": "decline"}}); err != nil {
		t.Fatalf("own tool after mismatch: %+v", err)
	}
}

func TestMRTR_NotOfferedWithoutElicitationCapability(t *testing.T) {
	withSpyElicitTool(t)
	params := map[string]interface{}{"name": spyElicitToolName, "arguments": map[string]interface{}{}}
	res := resultFields(t, httpDispatch(context.Background(), modernReq(t, "tools/call", params, nil)))
	if string(res["resultType"]) != `"complete"` || !strings.Contains(string(res["content"]), "no-elicit") {
		t.Errorf("without the elicitation capability: %v", res)
	}
}

func TestHostedNeverOffersMRTR(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	params := map[string]interface{}{"name": "list-processes", "arguments": map[string]interface{}{}, "requestState": "forged"}
	raw, _ := json.Marshal(hostedDispatch(context.Background(), modernReq(t, "tools/call", params, map[string]interface{}{"elicitation": map[string]interface{}{}}), "tok"))
	if !strings.Contains(string(raw), `"code":-32602`) {
		t.Errorf("hosted accepted a requestState: %s", raw)
	}
}

func TestHostedDiscoverIsToolsOnly(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	res := resultFields(t, hostedDispatch(context.Background(), modernReq(t, "server/discover", nil, nil), "tok"))
	if string(res["capabilities"]) != `{"tools":{}}` {
		t.Errorf("hosted capabilities = %s", res["capabilities"])
	}
	raw, _ := json.Marshal(hostedDispatch(context.Background(), modernReq(t, "ping", nil, nil), "tok"))
	if !strings.Contains(string(raw), `"code":-32601`) {
		t.Errorf("hosted still answers ping: %s", raw)
	}
}

// ---- subscriptions/listen ---------------------------------------------------

func withFastResourcePolling(t *testing.T) {
	t.Helper()
	prev, prevKA := resourcePollInterval, httpSubscriptionKeepAlive
	resourcePollInterval, httpSubscriptionKeepAlive = 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { resourcePollInterval, httpSubscriptionKeepAlive = prev, prevKA })
}

func TestParseSubscriptionFilterDropsForeignURIs(t *testing.T) {
	f, err := parseSubscriptionFilter(json.RawMessage(`{"notifications":{"toolsListChanged":true,"resourceSubscriptions":["corezoid://process/1.conv.json","file:///etc/passwd","corezoid://process/../x"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !f.ToolsListChanged || len(f.ResourceSubscriptions) != 1 || f.ResourceSubscriptions[0] != "corezoid://process/1.conv.json" {
		t.Errorf("filter = %+v", f)
	}
	if _, err := parseSubscriptionFilter(json.RawMessage(`{}`)); err == nil {
		t.Error("expected an error without params.notifications")
	}
}

func TestRunSubscription_AckThenResourceNotifications(t *testing.T) {
	withFastResourcePolling(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	_ = os.MkdirAll(".processes", 0o755)
	file := filepath.Join(".processes", "1_a.conv.json")
	_ = os.WriteFile(file, []byte(`{}`), 0o644)

	var mu sync.Mutex
	var got []map[string]interface{}
	emit := func(msg interface{}) error {
		raw, _ := json.Marshal(msg)
		var m map[string]interface{}
		_ = json.Unmarshal(raw, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runSubscription(ctx, "sub-1", subscriptionFilter{ResourcesListChanged: true, ResourceSubscriptions: []string{resourceURIPrefix + "1_a.conv.json"}}, emit)
	}()

	time.Sleep(60 * time.Millisecond)
	_ = os.WriteFile(file, []byte(`{"changed":true}`), 0o644)
	_ = os.WriteFile(filepath.Join(".processes", "2_b.conv.json"), []byte(`{}`), 0o644)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		methods := map[string]bool{}
		for _, m := range got {
			methods[m["method"].(string)] = true
		}
		mu.Unlock()
		if methods["notifications/resources/updated"] && methods["notifications/resources/list_changed"] {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || got[0]["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("first message = %v, want the acknowledgement", got)
	}
	seen := map[string]bool{}
	for _, m := range got {
		params := m["params"].(map[string]interface{})
		meta := params["_meta"].(map[string]interface{})
		if meta[metaKeySubscriptionID] != "sub-1" {
			t.Errorf("%s not tagged with the subscription id: %v", m["method"], params)
		}
		seen[m["method"].(string)] = true
	}
	for _, want := range []string{"notifications/resources/updated", "notifications/resources/list_changed"} {
		if !seen[want] {
			t.Errorf("never received %s (got %v)", want, got)
		}
	}
}

func TestHTTPSubscriptionsListen_SSEStream(t *testing.T) {
	withFastResourcePolling(t)
	srv := httptest.NewServer(http.HandlerFunc(corsWrap(httpMCPEndpoint)))
	defer srv.Close()

	body := modernBody(t, "subscriptions/listen", map[string]interface{}{"notifications": map[string]interface{}{"toolsListChanged": true}})
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpModernProtocolVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("missing X-Accel-Buffering: no")
	}

	sc := bufio.NewScanner(resp.Body)
	var sawAck, sawKeepAlive bool
	for sc.Scan() && !(sawAck && sawKeepAlive) {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "notifications/subscriptions/acknowledged") {
			sawAck = true
			if !strings.Contains(line, `"toolsListChanged":true`) {
				t.Errorf("ack does not echo the honoured filter: %s", line)
			}
		}
		if strings.HasPrefix(line, ":") {
			sawKeepAlive = true
		}
	}
	if !sawAck || !sawKeepAlive {
		t.Errorf("ack=%v keepAlive=%v", sawAck, sawKeepAlive)
	}
}

func TestSubscriptionsListen_LegacyIsNotFound(t *testing.T) {
	req := mcpRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "subscriptions/listen", Params: json.RawMessage(`{"notifications":{}}`)}
	raw, _ := json.Marshal(httpDispatch(context.Background(), req))
	if !strings.Contains(string(raw), `"code":-32601`) {
		t.Errorf("legacy subscriptions/listen = %s", raw)
	}
}

// ---- stdio end to end --------------------------------------------------------

// TestMCPProtocol_ModernStdioNoHandshake drives the real binary with modern
// requests only — no initialize — and checks version errors and results.
func TestMCPProtocol_ModernStdioNoHandshake(t *testing.T) {
	bin := buildTestBinary(t)
	sess := newMCPSession(t, bin)

	sess.send(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]interface{}{"_meta": modernMeta("t", "0", nil)}})
	resp := sess.recv()
	var res map[string]json.RawMessage
	_ = json.Unmarshal(resp["result"], &res)
	if string(res["resultType"]) != `"complete"` || res["tools"] == nil {
		t.Fatalf("tools/list without handshake: %s", resp["result"])
	}

	sess.send(map[string]interface{}{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]interface{}{"_meta": map[string]interface{}{
		metaKeyProtocolVersion: "2030-01-01", metaKeyClientCapabilities: map[string]interface{}{},
	}}})
	resp = sess.recv()
	if !strings.Contains(string(resp["error"]), `-32022`) {
		t.Fatalf("expected UnsupportedProtocolVersion, got %s", resp["error"])
	}

	sess.send(map[string]interface{}{"jsonrpc": "2.0", "id": 3, "method": "ping", "params": map[string]interface{}{"_meta": modernMeta("t", "0", nil)}})
	resp = sess.recv()
	if !strings.Contains(string(resp["error"]), `-32601`) {
		t.Fatalf("expected ping to be gone, got %s", resp)
	}
}
