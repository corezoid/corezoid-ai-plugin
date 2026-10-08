package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Every /mcp request leaves one access line with the method, tool and outcome,
// and never the token, the arguments or the result.
func TestHostedAccessLog(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	var buf bytes.Buffer
	prev := logger.writer
	logger.writer = &buf
	t.Cleanup(func() { logger.writer = prev })

	cfg := testHostedCfg
	cfg.AccessLog = true
	ts := httptest.NewServer(newHostedHandler(cfg))
	defer ts.Close()

	sample, err := os.ReadFile("samples/" + firstSample(t))
	if err != nil {
		t.Fatal(err)
	}
	hostedPost(t, ts.URL+"/mcp", "Bearer secret-token-123", callBody("lint-process", map[string]interface{}{"content": string(sample)}))
	hostedPost(t, ts.URL+"/mcp", "Bearer secret-token-123", callBody("lint-process", map[string]interface{}{"process_path": "x.conv.json"}))
	hostedPost(t, ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	var got []hostedAccess
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.HasPrefix(l, "INFO:access ") {
			continue
		}
		var a hostedAccess
		if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "INFO:access ")), &a); err != nil {
			t.Fatalf("bad access line %q: %v", l, err)
		}
		got = append(got, a)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 access lines, got %d:\n%s", len(got), buf.String())
	}
	if a := got[0]; a.Method != "tools/call" || a.Tool != "lint-process" || a.IsError || a.Status != 200 || a.Caller != callerHash("secret-token-123") || len(a.Caller) != 12 {
		t.Errorf("successful call: %+v", a)
	}
	if a := got[1]; !a.IsError || a.Tool != "lint-process" {
		t.Errorf("refused call not marked is_error: %+v", a)
	}
	if a := got[2]; a.Status != 401 || a.Caller != "" || a.Method != "" {
		t.Errorf("anonymous request: %+v", a)
	}
	for _, leak := range []string{"secret-token-123", "x.conv.json", "\"nodes\""} {
		if strings.Contains(buf.String(), leak) {
			t.Errorf("log contains %q", leak)
		}
	}
}
