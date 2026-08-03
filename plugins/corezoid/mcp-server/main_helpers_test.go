package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- getNodes --------------------------------------------------------------

func TestGetNodes_OK(t *testing.T) {
	data := map[string]interface{}{
		"scheme": map[string]interface{}{
			"nodes": []interface{}{"a", "b"},
		},
	}
	nodes, err := getNodes(data)
	if err != nil {
		t.Fatalf("getNodes error: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(nodes))
	}
}

func TestGetNodes_NoScheme(t *testing.T) {
	_, err := getNodes(map[string]interface{}{})
	if err == nil {
		t.Error("expected error when scheme missing, got nil")
	}
}

func TestGetNodes_NoNodes(t *testing.T) {
	data := map[string]interface{}{
		"scheme": map[string]interface{}{},
	}
	_, err := getNodes(data)
	if err == nil {
		t.Error("expected error when nodes missing, got nil")
	}
}

// ---- LoadBinFromFile -------------------------------------------------------

func TestLoadBinFromFile_OK(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.json")
	os.WriteFile(p, []byte(`{"x":1}`), 0644) //nolint:errcheck
	got, err := LoadBinFromFile(p)
	if err != nil || got != `{"x":1}` {
		t.Errorf("got (%q, %v)", got, err)
	}
}

func TestLoadBinFromFile_NotFound(t *testing.T) {
	_, err := LoadBinFromFile("/nonexistent_file_xyz.json")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// ---- Logger ----------------------------------------------------------------

func TestLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{writer: &buf}
	l.Info("hello %s", "world")
	if !strings.Contains(buf.String(), "INFO:hello world") {
		t.Errorf("unexpected log output: %q", buf.String())
	}
}

func TestLogger_Debug_WhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{writer: &buf, IsDebug: true}
	l.Debug("dbg %d", 42)
	if !strings.Contains(buf.String(), "DEBUG:dbg 42") {
		t.Errorf("unexpected log output: %q", buf.String())
	}
}

func TestLogger_Debug_WhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{writer: &buf, IsDebug: false}
	l.Debug("should not appear")
	if buf.Len() != 0 {
		t.Errorf("expected no output when debug disabled, got %q", buf.String())
	}
}

func TestLogger_Warn(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{writer: &buf}
	l.Warn("caution")
	if !strings.Contains(buf.String(), "WARN:caution") {
		t.Errorf("unexpected log output: %q", buf.String())
	}
}

func TestLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{writer: &buf}
	l.Error("boom")
	if !strings.Contains(buf.String(), "ERROR:boom") {
		t.Errorf("unexpected log output: %q", buf.String())
	}
}

func TestLogger_DefaultWriter(t *testing.T) {
	// Logger with nil writer should not panic (uses stderr).
	l := &Logger{}
	l.Info("no panic")
}

// ---- fixStruct -------------------------------------------------------------

func TestFixStruct_SetsObjID(t *testing.T) {
	input := `{"title":"p","scheme":{"nodes":[]}}`
	out, msgs := fixStruct(input, 99)
	if len(msgs) != 0 {
		t.Logf("messages: %v", msgs)
	}
	if !strings.Contains(out, `"obj_id"`) {
		t.Error("expected obj_id in output")
	}
}

func TestFixStruct_InvalidJSON(t *testing.T) {
	out, _ := fixStruct("{invalid}", 1)
	if out != "{invalid}" {
		t.Errorf("expected passthrough for invalid JSON, got %q", out)
	}
}

func TestFixStruct_OptionsStringified(t *testing.T) {
	input := `{"obj_id":1,"scheme":{"nodes":[{"id":"aabbcc001122334455667788","obj_type":0,"condition":{"logics":[]},"options":{"key":"val"}}]}}`
	out, _ := fixStruct(input, 1)
	// options object should be turned into a JSON string
	if strings.Contains(out, `"options":{"key"`) {
		t.Error("expected options to be stringified, but found raw object")
	}
}

func TestFixStruct_FunAliasReplaced(t *testing.T) {
	input := `{
		"obj_id":1,
		"scheme":{"nodes":[{
			"id":"aabbcc001122334455667788",
			"obj_type":0,
			"condition":{"logics":[{
				"type":"go_if_const",
				"conditions":[{"fun":"gte","value":"5"}]
			}]}
		}]}
	}`
	out, msgs := fixStruct(input, 1)
	if !strings.Contains(out, `"more_or_eq"`) {
		t.Errorf("expected gte->more_or_eq replacement, output: %s", out)
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "more_or_eq") {
			found = true
		}
	}
	if !found {
		t.Error("expected replacement message in msgs")
	}
}

func TestFixStruct_ApiExtraBodyFixed(t *testing.T) {
	input := `{
		"obj_id":1,
		"scheme":{"nodes":[{
			"id":"aabbcc001122334455667788",
			"obj_type":0,
			"condition":{"logics":[{
				"type":"api",
				"extra":{"body":"{{some_var}}"}
			}]}
		}]}
	}`
	out, msgs := fixStruct(input, 1)
	if !strings.Contains(out, `"raw_body"`) {
		t.Errorf("expected raw_body in output, got: %s", out)
	}
	if len(msgs) == 0 {
		t.Error("expected fix message for api extra body")
	}
}

func TestFixStruct_ConvIDStringToInt(t *testing.T) {
	input := `{
		"obj_id":1,
		"scheme":{"nodes":[{
			"id":"aabbcc001122334455667788",
			"obj_type":0,
			"condition":{"logics":[{
				"type":"go",
				"conv_id":"12345"
			}]}
		}]}
	}`
	out, _ := fixStruct(input, 1)
	// conv_id should be integer 12345, not string "12345"
	if strings.Contains(out, `"conv_id":"12345"`) {
		t.Error("expected conv_id to be integer, found string")
	}
	if !strings.Contains(out, `"conv_id": 12345`) {
		t.Errorf("expected conv_id: 12345 in output, got: %s", out)
	}
}

func TestFixStruct_StubObjTypeLogicNormalized(t *testing.T) {
	input := `{
		"obj_id":1,
		"scheme":{"nodes":[{
			"id":"aabbcc001122334455667788",
			"obj_type":4,
			"condition":{
				"logics":[{
					"type":"api_rpc",
					"conv_id":"12345",
					"err_node_id":"bbbbcc001122334455667788",
					"extra":{},
					"extra_type":{},
					"group":"all"
				},{"type":"go","to_node_id":"cccccc001122334455667788"}],
				"stub":{"logics":[[
					{"type":"go_if_const","conditions":[{"param":"{{amount}}","const":"10","fun":"gt","cast":"number"}]},
					{"type":"api_rpc_reply","mode":"key_value","res_data":{"status":"ok"},"res_data_type":{"status":"string"}}
				]]}
			}
		}]}
	}`
	out, msgs := fixStruct(input, 1)
	if strings.Contains(out, `"conv_id":"12345"`) {
		t.Error("expected obj_type:4 conv_id to be integer, found string")
	}
	if !strings.Contains(out, `"conv_id": 12345`) {
		t.Errorf("expected conv_id: 12345 in output, got: %s", out)
	}
	if !strings.Contains(out, `"fun": "more"`) {
		t.Errorf("expected Stub go_if_const gt->more replacement, output: %s", out)
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m, `"fun":"gt" replaced with "fun":"more"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Stub fun alias replacement message, got: %+v", msgs)
	}
}
