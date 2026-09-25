package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Issue #175: push-process uploaded the scheme and the params, reported
// success, and left the conv's own title and description at whatever the
// server already had. These tests pin the op that carries them.

func TestModifyConv_SendsTitleAndDescription(t *testing.T) {
	var captured []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = append(captured, ops...)
		return okResponse(ops)
	})
	e.WorkspaceID = "workspace"

	desc := "Routes an order to the right fulfilment queue."
	if err := e.ModifyConv(4242, "Order router", &desc); err != nil {
		t.Fatalf("ModifyConv returned error: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 op, got %d: %#v", len(captured), captured)
	}
	op := captured[0]
	if op["type"] != "modify" || op["obj"] != "conv" {
		t.Fatalf("expected a modify/conv op, got %#v", op)
	}
	if op["obj_id"] != float64(4242) {
		t.Fatalf("expected obj_id 4242, got %#v", op["obj_id"])
	}
	if op["title"] != "Order router" {
		t.Fatalf("expected the title to be sent, got %#v", op["title"])
	}
	if op["description"] != desc {
		t.Fatalf("expected the description to be sent, got %#v", op["description"])
	}
	if op["company_id"] != "workspace" {
		t.Fatalf("expected company_id to be sent, got %#v", op["company_id"])
	}
}

// A .conv.json with no description key must not wipe the description on the
// server — "absent" is not "cleared".
func TestModifyConv_NilDescriptionIsOmitted(t *testing.T) {
	var captured []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = append(captured, ops...)
		return okResponse(ops)
	})

	if err := e.ModifyConv(7, "Renamed", nil); err != nil {
		t.Fatalf("ModifyConv returned error: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 op, got %d", len(captured))
	}
	if _, present := captured[0]["description"]; present {
		t.Fatalf("absent description must not be sent, got %#v", captured[0])
	}
	if captured[0]["title"] != "Renamed" {
		t.Fatalf("expected the rename to be sent, got %#v", captured[0])
	}
}

// ...but a description the user deliberately emptied must reach the server.
func TestModifyConv_EmptyDescriptionClearsIt(t *testing.T) {
	var captured []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		captured = append(captured, ops...)
		return okResponse(ops)
	})

	empty := ""
	if err := e.ModifyConv(7, "Kept", &empty); err != nil {
		t.Fatalf("ModifyConv returned error: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 op, got %d", len(captured))
	}
	desc, present := captured[0]["description"]
	if !present || desc != "" {
		t.Fatalf("expected an explicit empty description, got %#v", captured[0])
	}
}

func TestModifyConv_NothingToChangeSkipsTheRequest(t *testing.T) {
	calls := 0
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		calls++
		return okResponse(ops)
	})

	if err := e.ModifyConv(7, "", nil); err != nil {
		t.Fatalf("ModifyConv returned error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no request when there is nothing to change, got %d", calls)
	}
}

// End-to-end through the push path: an existing process whose local file has a
// new description must leave a modify/conv op behind. This is the exact flow
// corezoid-describe documents, and the one that used to be a no-op.
func TestProcessJSON_PushesEditedDescriptionOfExistingProcess(t *testing.T) {
	content, doc := loadSampleDoc(t, map[string]interface{}{
		"obj_id":      float64(777),
		"title":       "Valid Process (renamed)",
		"description": "Description written by corezoid-describe.",
	})
	filePath := filepath.Join(t.TempDir(), "777_valid_process.conv.json")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var convModifies []map[string]interface{}
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) == 0 {
			return okResponse(ops)
		}
		op := ops[0]
		switch {
		case op["type"] == "list" && op["obj"] == "conv":
			return wrapOp(map[string]interface{}{
				"proc": "ok",
				"list": []interface{}{
					map[string]interface{}{
						"obj_id":   "eeeeeeeeeeeeeeeeeeee0001",
						"obj_type": float64(1),
						"title":    "Start",
					},
				},
			})
		case op["type"] == "create" && op["obj"] == "node":
			results := make([]interface{}, len(ops))
			for i, createOp := range ops {
				localID, _ := createOp["id"].(string)
				results[i] = map[string]interface{}{"proc": "ok", "id": localID, "obj_id": localID}
			}
			return map[string]interface{}{"request_proc": "ok", "ops": results}
		case op["type"] == "modify" && op["obj"] == "conv":
			convModifies = append(convModifies, ops...)
			return okResponse(ops)
		default:
			return okResponse(ops)
		}
	})
	e.ProcessID = 777
	e.Version = 1
	e.WorkspaceID = "workspace"

	if _, err := e.ProcessJSON(filePath, content); err != nil {
		t.Fatalf("ProcessJSON returned error: %v", err)
	}
	if len(convModifies) != 1 {
		t.Fatalf("expected exactly 1 modify/conv op, got %d: %#v", len(convModifies), convModifies)
	}
	if got := convModifies[0]["description"]; got != doc["description"] {
		t.Fatalf("description not pushed: got %#v, want %#v", got, doc["description"])
	}
	if got := convModifies[0]["title"]; got != doc["title"] {
		t.Fatalf("title not pushed: got %#v, want %#v", got, doc["title"])
	}
	if convModifies[0]["obj_id"] != float64(777) {
		t.Fatalf("modify op targeted the wrong conv: %#v", convModifies[0])
	}
}

// A brand-new process has no conv to modify yet, so its description has to ride
// along with the create op — and must not cost a second round trip afterwards.
func TestProcessJSON_CreateCarriesDescription(t *testing.T) {
	content, doc := loadSampleDoc(t, map[string]interface{}{
		"obj_id":      float64(0),
		"description": "Created with a description.",
	})
	filePath := filepath.Join(t.TempDir(), "valid_process.conv.json")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var createConv map[string]interface{}
	convModifies := 0
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) == 0 {
			return okResponse(ops)
		}
		op := ops[0]
		switch {
		case op["type"] == "create" && op["obj"] == "conv":
			createConv = op
			return wrapOp(map[string]interface{}{"proc": "ok", "obj_id": float64(778)})
		case op["type"] == "create" && op["obj"] == "node":
			results := make([]interface{}, len(ops))
			for i, createOp := range ops {
				localID, _ := createOp["id"].(string)
				results[i] = map[string]interface{}{"proc": "ok", "id": localID, "obj_id": localID}
			}
			return map[string]interface{}{"request_proc": "ok", "ops": results}
		case op["type"] == "modify" && op["obj"] == "conv":
			convModifies++
			return okResponse(ops)
		default:
			return okResponse(ops)
		}
	})
	e.Version = 1
	e.WorkspaceID = "workspace"

	if _, err := e.ProcessJSON(filePath, content); err != nil {
		t.Fatalf("ProcessJSON returned error: %v", err)
	}
	if createConv == nil {
		t.Fatal("expected a create/conv op")
	}
	if got := createConv["description"]; got != doc["description"] {
		t.Fatalf("create op dropped the description: got %#v, want %#v", got, doc["description"])
	}
	if convModifies != 0 {
		t.Fatalf("create already carried the metadata; expected no modify/conv op, got %d", convModifies)
	}
}

// loadSampleDoc reads samples/valid_process.json and applies overrides to its
// root, returning the serialized document and the decoded map.
func loadSampleDoc(t *testing.T, overrides map[string]interface{}) (string, map[string]interface{}) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("samples", "valid_process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for k, v := range overrides {
		doc[k] = v
	}
	content, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(content), doc
}
