package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `issync` instead of `is_sync` — a plausible typo that appears in no `required`
// list, so nothing but the undeclared-property check can catch it.
const apiRPCTypoProcess = `{
  "obj_type": 1, "obj_id": 7001, "parent_id": 7000, "title": "typo", "description": "",
  "status": "active", "params": [], "ref_mask": true, "conv_type": "process",
  "scheme": {"nodes": [
    {"id": "aaaaaaaaaaaaaaaaaaaaaaa1", "obj_type": 1, "title": "Start", "description": "",
     "x": 0, "y": 0, "extra": "{}", "options": null,
     "condition": {"logics": [{"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa2"}], "semaphors": []}},
    {"id": "aaaaaaaaaaaaaaaaaaaaaaa2", "obj_type": 0, "title": "Call", "description": "",
     "x": 0, "y": 160, "extra": "{}", "options": null,
     "condition": {"logics": [
        {"type": "api_rpc", "conv_id": 123, "extra": {}, "extra_type": {},
         "err_node_id": "aaaaaaaaaaaaaaaaaaaaaaa4", "issync": true},
        {"type": "go", "to_node_id": "aaaaaaaaaaaaaaaaaaaaaaa3"}], "semaphors": []}},
    {"id": "aaaaaaaaaaaaaaaaaaaaaaa3", "obj_type": 2, "title": "Done", "description": "",
     "x": 0, "y": 320, "extra": "{}", "options": "{\"save_task\":true}",
     "condition": {"logics": [], "semaphors": []}},
    {"id": "aaaaaaaaaaaaaaaaaaaaaaa4", "obj_type": 2, "title": "Call failed", "description": "",
     "x": 260, "y": 320, "extra": "{}", "options": "{\"save_task\":true}",
     "condition": {"logics": [], "semaphors": []}}
  ], "web_settings": [[], []]}
}`

func writeTempProcess(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "7001_typo.conv.json")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// `semaphores` instead of Corezoid's misspelled `semaphors`. The key sits on the
// condition object, not on a logic, so only a closed container schema refuses it.
const misspelledSemaphorsProcess = `{
  "obj_type": 1, "obj_id": 7002, "parent_id": 7000, "title": "sem typo", "description": "",
  "status": "active", "params": [], "ref_mask": true, "conv_type": "process",
  "scheme": {"nodes": [
    {"id": "bbbbbbbbbbbbbbbbbbbbbbb1", "obj_type": 1, "title": "Start", "description": "",
     "x": 0, "y": 0, "extra": "{}", "options": null,
     "condition": {"logics": [{"type": "go", "to_node_id": "bbbbbbbbbbbbbbbbbbbbbbb2"}], "semaphors": []}},
    {"id": "bbbbbbbbbbbbbbbbbbbbbbb2", "obj_type": 0, "title": "Wait", "description": "",
     "x": 0, "y": 160, "extra": "{}", "options": null,
     "condition": {"logics": [{"type": "go", "to_node_id": "bbbbbbbbbbbbbbbbbbbbbbb3"}],
       "semaphores": [{"type": "time", "value": 60, "dimension": "sec",
                       "to_node_id": "bbbbbbbbbbbbbbbbbbbbbbb4"}]}},
    {"id": "bbbbbbbbbbbbbbbbbbbbbbb3", "obj_type": 2, "title": "Done", "description": "",
     "x": 0, "y": 320, "extra": "{}", "options": "{\"save_task\":true}",
     "condition": {"logics": [], "semaphors": []}},
    {"id": "bbbbbbbbbbbbbbbbbbbbbbb4", "obj_type": 2, "title": "Timed out", "description": "",
     "x": 260, "y": 320, "extra": "{}", "options": "{\"save_task\":true}",
     "condition": {"logics": [], "semaphors": []}}
  ], "web_settings": [[], []]}
}`
