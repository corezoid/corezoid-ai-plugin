package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// processTargetTools are the tools that identify their target with
// process_path or process_id.
var processTargetTools = []string{
	"run-task", "create-snapshot", "list-snapshots", "delete-snapshot", "get-snapshot",
}

// compileAdvertisedSchema compiles a tool's InputSchema exactly as an MCP host
// receives it: through JSON, so a Go-typed schema that does not survive
// serialization is caught here rather than in a client.
func compileAdvertisedSchema(t *testing.T, tool string) *jsonschema.Schema {
	t.Helper()
	var mt *mcpTool
	for i := range toolRegistry {
		if toolRegistry[i].Name == tool {
			mt = &toolRegistry[i]
			break
		}
	}
	if mt == nil {
		t.Fatalf("tool %q is not in toolRegistry", tool)
	}
	raw, err := json.Marshal(mt.InputSchema)
	if err != nil {
		t.Fatalf("marshal %s InputSchema: %v", tool, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse %s InputSchema: %v", tool, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem:///tool.json", doc); err != nil {
		t.Fatalf("register %s InputSchema: %v", tool, err)
	}
	sch, err := c.Compile("mem:///tool.json")
	if err != nil {
		t.Fatalf("compile %s InputSchema: %v", tool, err)
	}
	return sch
}

// A host that serializes declared-but-unset optionals as "" or null must still
// pass client-side validation when it supplies a valid process_id. Under a
// presence-based `oneOf` such a call satisfied both branches and was rejected
// before reaching the server — precisely the no-local-repository hosts
// process_id exists for, and precisely the shape the runtime accepts (see
// TestResolveProcessID_EmptyOrNullProcessPathIsNotAConflict).
func TestAdvertisedSchema_AcceptsWhatResolveProcessIDAccepts(t *testing.T) {
	cases := map[string]string{
		"process_id only":            `{"process_id": 456}`,
		"process_path only":          `{"process_path": "111_p.conv.json"}`,
		"empty process_path plus id": `{"process_path": "", "process_id": 456}`,
		"null process_path plus id":  `{"process_path": null, "process_id": 456}`,
		"null process_id plus path":  `{"process_path": "111_p.conv.json", "process_id": null}`,
	}
	// Arguments the individual tools require on top of the target.
	extras := map[string]string{
		"run-task":        `"data": "{}"`,
		"delete-snapshot": `"snapshot_id": 7`,
		"get-snapshot":    `"snapshot_id": 7`,
	}
	for _, tool := range processTargetTools {
		sch := compileAdvertisedSchema(t, tool)
		for name, target := range cases {
			t.Run(tool+"/"+name, func(t *testing.T) {
				payload := target
				if extra := extras[tool]; extra != "" {
					payload = target[:len(target)-1] + ", " + extra + "}"
				}
				var doc any
				if err := json.Unmarshal([]byte(payload), &doc); err != nil {
					t.Fatalf("bad test payload %s: %v", payload, err)
				}
				if err := sch.Validate(doc); err != nil {
					t.Errorf("%s must accept %s, got: %v", tool, payload, err)
				}
			})
		}
	}
}

// The schema still has to reject a call that names no target at all — that is
// the one thing client-side validation can catch better than the server.
func TestAdvertisedSchema_RejectsNoProcessTarget(t *testing.T) {
	payloads := map[string]string{
		"run-task":        `{"data": "{}"}`,
		"create-snapshot": `{}`,
		"list-snapshots":  `{}`,
		"delete-snapshot": `{"snapshot_id": 7}`,
		"get-snapshot":    `{"snapshot_id": 7}`,
	}
	for _, tool := range processTargetTools {
		sch := compileAdvertisedSchema(t, tool)
		var doc any
		if err := json.Unmarshal([]byte(payloads[tool]), &doc); err != nil {
			t.Fatalf("bad test payload for %s: %v", tool, err)
		}
		if err := sch.Validate(doc); err == nil {
			t.Errorf("%s must reject a call naming neither process_path nor process_id", tool)
		}
	}
}

// Guards the fix itself: a presence-based oneOf on this pair is what broke the
// empty-optional hosts, so no process-target tool may reintroduce it.
func TestAdvertisedSchema_ProcessTargetUsesAnyOf(t *testing.T) {
	for _, tool := range processTargetTools {
		for i := range toolRegistry {
			if toolRegistry[i].Name != tool {
				continue
			}
			schema, ok := toolRegistry[i].InputSchema.(map[string]interface{})
			if !ok {
				t.Fatalf("%s InputSchema is not an object", tool)
			}
			if _, bad := schema["oneOf"]; bad {
				t.Errorf("%s must express the process_path/process_id pair as anyOf, not oneOf: "+
					"`required` matches on key presence, so a host sending an empty process_path "+
					"alongside a valid process_id satisfies both branches and the call is rejected client-side", tool)
			}
			if _, ok := schema["anyOf"]; !ok {
				t.Errorf("%s declares no anyOf for the process_path/process_id pair", tool)
			}
		}
	}
}
