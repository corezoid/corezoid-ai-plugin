package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The hosted variants of the file-based process tools advertise schemas with
// no filesystem arguments.
func TestHostedProcessToolSchemas(t *testing.T) {
	enableHostedForTest(t, "https://admin.corezoid.com")
	byName := map[string]mcpTool{}
	for _, d := range hostedToolRegistry() {
		byName[d.Name] = d
	}
	for name := range hostedHandlers {
		d, ok := byName[name]
		if !ok {
			t.Errorf("%s not listed in hosted mode", name)
			continue
		}
		props := d.InputSchema.(map[string]interface{})["properties"].(map[string]interface{})
		for k := range props {
			if strings.Contains(k, "path") || k == "force" || k == "overwrite" {
				t.Errorf("%s advertises local argument %q in hosted mode", name, k)
			}
		}
	}
}

func TestHostedLintProcessContent(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	sample, err := os.ReadFile("samples/" + firstSample(t))
	if err != nil {
		t.Fatal(err)
	}
	_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("lint-process", map[string]interface{}{"content": string(sample)}))
	text, isErr := toolText(t, out)
	if isErr || !strings.Contains(strings.ToLower(text), "node") {
		t.Errorf("lint of a sample process: isError=%v %q", isErr, text[:min(200, len(text))])
	}
	if n := len(api.snapshot()); n != 0 {
		t.Errorf("lint made %d API calls; it must work on the content alone", n)
	}

	_, _, out = hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("lint-process", map[string]interface{}{"process_path": "x.conv.json"}))
	if text, isErr := toolText(t, out); !isErr || !strings.Contains(text, "does not accept process_path") {
		t.Errorf("process_path accepted by hosted lint: %q", text)
	}
}

func TestHostedRunTaskRefusesPath(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	for _, args := range []map[string]interface{}{
		{"process_path": "1_x.conv.json", "data": "{}"},
		{"data": "{}"},
		{"process_id": 0, "data": "{}"},
	} {
		_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("run-task", args))
		if text, isErr := toolText(t, out); !isErr {
			t.Errorf("run-task %v accepted: %q", args, text)
		}
	}
	if n := len(api.snapshot()); n != 0 {
		t.Errorf("%d API calls for refused run-task calls", n)
	}
}

func TestHostedCreateProcessNeedsFolder(t *testing.T) {
	api := &fakeCorezoidAPI{}
	enableHostedForTest(t, api.server(t).URL)
	ts := httptest.NewServer(newHostedHandler(testHostedCfg))
	defer ts.Close()

	_, _, out := hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("create-process", map[string]interface{}{"process_name": "x"}))
	if _, isErr := toolText(t, out); !isErr {
		t.Error("create-process without folder_id accepted")
	}
	_, _, out = hostedPost(t, ts.URL+"/mcp", "Bearer tok", callBody("create-process", map[string]interface{}{"process_name": "x", "folder_id": 5, "folder_path": "."}))
	if text, isErr := toolText(t, out); !isErr || !strings.Contains(text, "folder_path") {
		t.Errorf("folder_path accepted by hosted create-process: %q", text)
	}
}

func TestHostedBaseToken(t *testing.T) {
	b := baselineEntry{ChangeTime: 1700000000, Version: 42}
	tok := hostedBaseToken(77, b)
	id, got, err := parseHostedBaseToken(tok)
	if err != nil || id != 77 || got.ChangeTime != b.ChangeTime || got.Version != b.Version || got.Source != baselineSourceDetail {
		t.Fatalf("round trip %q -> %d %+v %v", tok, id, got, err)
	}
	for _, bad := range []string{"", "v1:1:2", "v2:1:2:3", "v1:a:2:3", "v1:1:-2:3", "v1:1:2:3:4"} {
		if _, _, err := parseHostedBaseToken(bad); err == nil {
			t.Errorf("parseHostedBaseToken(%q) accepted", bad)
		}
	}
}

// firstSample returns a bundled sample process file name.
func firstSample(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir("samples")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".conv.json") || strings.HasSuffix(e.Name(), ".json") {
			var probe map[string]interface{}
			if b, err := os.ReadFile("samples/" + e.Name()); err == nil && json.Unmarshal(b, &probe) == nil {
				if _, err := getNodes(probe); err == nil {
					return e.Name()
				}
			}
		}
	}
	t.Skip("no sample process in samples/")
	return ""
}
