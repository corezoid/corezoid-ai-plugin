package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envVarMock answers the ops the variable handlers issue: show-folder (feeds
// GetProjectIDByStageID), env_var list/modify/delete. It records op types in
// seen and captures the last modify/delete payload.
type envVarMock struct {
	vars      []map[string]interface{}
	listErr   bool
	modifyErr bool
	deleteErr bool
	// dropOnDelete: remove the deleted obj_id from vars so the post-verify
	// re-list sees it gone (the real server behavior).
	dropOnDelete bool
	seen         *[]string
	captured     *map[string]interface{}
}

func (m *envVarMock) fn(ops []map[string]interface{}) interface{} {
	if len(ops) == 0 {
		return wrapVarOp(map[string]interface{}{"proc": "ok"})
	}
	op := ops[0]
	typ, _ := op["type"].(string)
	obj, _ := op["obj"].(string)
	if m.seen != nil {
		*m.seen = append(*m.seen, typ+":"+obj)
	}
	switch {
	case typ == "show" && obj == "folder":
		// stage folder 400 belongs to project 100
		id, _ := op["obj_id"].(float64)
		return wrapVarOp(map[string]interface{}{"proc": "ok", "obj_id": id, "parent_obj_id": float64(100), "title": "dev", "obj_type": float64(3)})
	case typ == "list" && obj == "env_var":
		if m.listErr {
			return wrapVarOp(map[string]interface{}{"proc": "error", "description": "list boom"})
		}
		list := make([]interface{}, len(m.vars))
		for i, d := range m.vars {
			list[i] = d
		}
		return wrapVarOp(map[string]interface{}{"proc": "ok", "list": list})
	case typ == "modify" && obj == "env_var":
		if m.captured != nil {
			*m.captured = op
		}
		if m.modifyErr {
			return wrapVarOp(map[string]interface{}{"proc": "error", "description": "env_var name already exists"})
		}
		return wrapVarOp(map[string]interface{}{"proc": "ok"})
	case typ == "delete" && obj == "env_var":
		if m.captured != nil {
			*m.captured = op
		}
		if m.deleteErr {
			return wrapVarOp(map[string]interface{}{"proc": "error", "description": "Object env_var with id 9 does not exist"})
		}
		if m.dropOnDelete {
			id, _ := op["obj_id"].(float64)
			kept := m.vars[:0]
			for _, d := range m.vars {
				if d["obj_id"].(float64) != id {
					kept = append(kept, d)
				}
			}
			m.vars = kept
		}
		return wrapVarOp(map[string]interface{}{"proc": "ok"})
	}
	return wrapVarOp(map[string]interface{}{"proc": "ok"})
}

func wrapVarOp(op map[string]interface{}) interface{} {
	return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{op}}
}

func envVarItem(objID int, name, title, dataType, varType, value string) map[string]interface{} {
	item := map[string]interface{}{
		"obj_id": float64(objID), "short_name": name, "title": title,
		"data_type": dataType, "env_var_type": varType,
		"create_time": float64(1700000000), "change_time": float64(1700000500),
		"uuid": "u-" + name,
	}
	if varType == "secret" {
		item["value"] = nil // the real server returns null for secrets
	} else {
		item["value"] = value
	}
	return item
}

// setVarTestAuth completes the auth globals setProjectAuth leaves unset and
// restores them afterwards.
func setVarTestAuth(t *testing.T) {
	t.Helper()
	origAccount, origStage := accountURL, stageID
	accountURL = "https://account.test"
	stageID = 400
	t.Cleanup(func() { accountURL = origAccount; stageID = origStage })
}

func callVarTool(t *testing.T, m *envVarMock, tool string, args map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	t.Chdir(t.TempDir())
	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	setVarTestAuth(t)
	base := map[string]interface{}{}
	for k, v := range args {
		base[k] = v
	}
	return handleToolCall(context.Background(), tool, base)
}

const secretRaw = "SUPER-SECRET-RAW-VALUE"

func stdVars() []map[string]interface{} {
	return []map[string]interface{}{
		envVarItem(11, "api-url", "API URL", "raw", "visible", "https://api.example.com"),
		envVarItem(22, "stripe-key", "Stripe Key", "raw", "secret", secretRaw),
	}
}

// ---- list-variables -----------------------------------------------------------

func TestListVariables_OK(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, isErr := callVarTool(t, m, "list-variables", nil)
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	for _, want := range []string{"@api-url", "11", "@stripe-key", "22", "https://api.example.com"} {
		if !strings.Contains(res, want) {
			t.Errorf("list output missing %q:\n%s", want, res)
		}
	}
}

func TestListVariables_SecretMasked(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, _ := callVarTool(t, m, "list-variables", nil)
	if strings.Contains(res, secretRaw) {
		t.Fatalf("SECRET VALUE LEAKED into list output:\n%s", res)
	}
	if !strings.Contains(res, "••••••") {
		t.Errorf("expected mask marker for the secret, got:\n%s", res)
	}
}

func TestListVariables_Empty(t *testing.T) {
	m := &envVarMock{}
	res, isErr := callVarTool(t, m, "list-variables", nil)
	if isErr || !strings.Contains(res, "No environment variables") {
		t.Fatalf("expected empty message, got isErr=%v: %s", isErr, res)
	}
}

func TestListVariables_NoStage(t *testing.T) {
	m := &envVarMock{}
	resetGlobals(t)
	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	setVarTestAuth(t)
	stageID = 0
	res, isErr := handleToolCall(context.Background(), "list-variables", map[string]interface{}{})
	if !isErr || !strings.Contains(res, "COREZOID_STAGE_ID") {
		t.Fatalf("expected stage error, got isErr=%v: %s", isErr, res)
	}
}

// ---- resolution ----------------------------------------------------------------

func TestModifyVariable_NotFound_ListsNames(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{"name": "api-ur", "value": "x"})
	if !isErr {
		t.Fatalf("expected not-found error: %s", res)
	}
	if !strings.Contains(res, "Did you mean") || !strings.Contains(res, "@api-url") {
		t.Errorf("expected near-match suggestion, got: %s", res)
	}
}

func TestModifyVariable_ObjIDNameMismatch(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{"name": "api-url", "obj_id": 22, "value": "x"})
	if !isErr || !strings.Contains(res, "disagree") {
		t.Fatalf("expected mismatch refusal, got isErr=%v: %s", isErr, res)
	}
}

// ---- modify-variable gate -------------------------------------------------------

func TestModifyVariable_NothingToChange(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{"name": "api-url"})
	if !isErr || !strings.Contains(res, "nothing to modify") {
		t.Fatalf("expected nothing-to-modify error, got isErr=%v: %s", isErr, res)
	}
	// The refusal must also teach that env_var_type is immutable.
	if !strings.Contains(res, "env_var_type") {
		t.Errorf("expected type-immutability note: %s", res)
	}
}

func TestModifyVariable_DryRunDefault(t *testing.T) {
	var seen []string
	m := &envVarMock{vars: stdVars(), seen: &seen}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{"name": "api-url", "value": "https://new.example.com"})
	if isErr {
		t.Fatalf("dry-run must not be an error: %s", res)
	}
	for _, want := range []string{"DRY-RUN", "https://api.example.com", "https://new.example.com", `confirm="api-url#11"`} {
		if !strings.Contains(res, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, res)
		}
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "modify:") {
			t.Fatalf("modify op must NOT run in dry-run; seen=%v", seen)
		}
	}
}

func TestModifyVariable_WrongConfirm(t *testing.T) {
	var seen []string
	m := &envVarMock{vars: stdVars(), seen: &seen}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{
		"name": "api-url", "value": "v2", "apply": true, "confirm": "wrong"})
	if !isErr || !strings.Contains(res, "confirmation required") {
		t.Fatalf("expected confirm refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "modify:") {
			t.Fatalf("modify op must NOT run with wrong confirm; seen=%v", seen)
		}
	}
}

func TestModifyVariable_OK_PartialPayload(t *testing.T) {
	captured := map[string]interface{}{}
	m := &envVarMock{vars: stdVars(), captured: &captured}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{
		"name": "api-url", "value": "v2", "apply": true, "confirm": "api-url#11"})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if !strings.Contains(res, "✅") || !strings.Contains(res, "Verified on server") {
		t.Errorf("expected success + verification, got: %s", res)
	}
	// Partial semantics: only the required identity keys + the changed field.
	for _, key := range []string{"obj_id", "short_name", "company_id", "project_id", "stage_id", "value"} {
		if _, ok := captured[key]; !ok {
			t.Errorf("modify payload missing %q: %v", key, captured)
		}
	}
	for _, absent := range []string{"title", "data_type", "env_var_type", "scopes"} {
		if _, ok := captured[absent]; ok {
			t.Errorf("modify payload must NOT send unchanged/forbidden field %q: %v", absent, captured)
		}
	}
	if captured["project_id"] != float64(100) {
		t.Errorf("project_id not resolved via stage walk: %v", captured["project_id"])
	}
}

func TestModifyVariable_SecretValueKeptWhenOmitted(t *testing.T) {
	// Modifying a secret's title WITHOUT value must be allowed (server keeps
	// the value — verified live) and must never echo the secret.
	captured := map[string]interface{}{}
	m := &envVarMock{vars: stdVars(), captured: &captured}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{
		"name": "stripe-key", "description": "Stripe Live Key", "apply": true, "confirm": "stripe-key#22"})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if _, ok := captured["value"]; ok {
		t.Errorf("value must NOT be sent when not requested (secret would be overwritten): %v", captured)
	}
	if strings.Contains(res, secretRaw) {
		t.Fatalf("SECRET VALUE LEAKED into modify output:\n%s", res)
	}
}

func TestModifyVariable_RenameCollision(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{"name": "api-url", "new_name": "stripe-key"})
	if !isErr || !strings.Contains(res, "already exists") {
		t.Fatalf("expected collision refusal, got isErr=%v: %s", isErr, res)
	}
}

func TestModifyVariable_RenameScansReferences(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	resetGlobals(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "1_proc.conv.json"), []byte(`{"x":"{{env_var[@api-url]}}"}`), 0644); err != nil {
		t.Fatal(err)
	}
	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	setVarTestAuth(t)
	res, isErr := handleToolCall(context.Background(), "modify-variable", map[string]interface{}{
		"name": "api-url", "new_name": "api-base-url"})
	if isErr {
		t.Fatalf("dry-run failed: %s", res)
	}
	for _, want := range []string{"RENAME", "1_proc.conv.json", "WILL BREAK"} {
		if !strings.Contains(res, want) {
			t.Errorf("rename dry-run missing %q:\n%s", want, res)
		}
	}
}

func TestModifyVariable_BadDataType(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{"name": "api-url", "data_type": "xml"})
	if !isErr || !strings.Contains(res, "raw") {
		t.Fatalf("expected data_type validation error, got isErr=%v: %s", isErr, res)
	}
}

// ---- delete-variable gate -------------------------------------------------------

func TestDeleteVariable_DryRun_RedBlock(t *testing.T) {
	var seen []string
	m := &envVarMock{vars: stdVars(), seen: &seen}
	res, isErr := callVarTool(t, m, "delete-variable", map[string]interface{}{"name": "stripe-key"})
	if isErr {
		t.Fatalf("dry-run must not be an error: %s", res)
	}
	for _, want := range []string{"🔴", "PERMANENT DELETION", "NOT RECOVERABLE", "Trash", "VERBATIM", `confirm="stripe-key#22"`, "••••••"} {
		if !strings.Contains(res, want) {
			t.Errorf("red block missing %q:\n%s", want, res)
		}
	}
	if strings.Contains(res, secretRaw) {
		t.Fatalf("SECRET VALUE LEAKED into delete preview:\n%s", res)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "delete:") {
			t.Fatalf("delete op must NOT run in dry-run; seen=%v", seen)
		}
	}
}

func TestDeleteVariable_NoLocalFiles_SaysCannotVerify(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, _ := callVarTool(t, m, "delete-variable", map[string]interface{}{"name": "api-url"})
	if !strings.Contains(res, "CANNOT be verified") {
		t.Errorf("empty checkout must be reported as unverifiable, got:\n%s", res)
	}
}

func TestDeleteVariable_WrongConfirm(t *testing.T) {
	var seen []string
	m := &envVarMock{vars: stdVars(), seen: &seen}
	res, isErr := callVarTool(t, m, "delete-variable", map[string]interface{}{
		"name": "api-url", "apply": true, "confirm": "api-url#999"})
	if !isErr || !strings.Contains(res, "confirmation required") {
		t.Fatalf("expected confirm refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "delete:") {
			t.Fatalf("delete op must NOT run with wrong confirm; seen=%v", seen)
		}
	}
}

func TestDeleteVariable_ApplyWithoutConfirm(t *testing.T) {
	var seen []string
	m := &envVarMock{vars: stdVars(), seen: &seen}
	res, isErr := callVarTool(t, m, "delete-variable", map[string]interface{}{"name": "api-url", "apply": true})
	if !isErr || !strings.Contains(res, "confirmation required") {
		t.Fatalf("expected refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "delete:") {
			t.Fatalf("delete op must NOT run without confirm; seen=%v", seen)
		}
	}
}

func TestDeleteVariable_OK(t *testing.T) {
	captured := map[string]interface{}{}
	m := &envVarMock{vars: stdVars(), captured: &captured, dropOnDelete: true}
	resetGlobals(t)
	dir := t.TempDir()
	t.Chdir(dir)
	// seed the cache so removal can be asserted
	if err := os.MkdirAll(".processes", 0755); err != nil {
		t.Fatal(err)
	}
	cache := `[{"name":"api-url","description":"API URL","value":"https://api.example.com"}]`
	if err := os.WriteFile(".processes/variables.json", []byte(cache), 0644); err != nil {
		t.Fatal(err)
	}
	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	setVarTestAuth(t)
	res, isErr := handleToolCall(context.Background(), "delete-variable", map[string]interface{}{
		"name": "api-url", "apply": true, "confirm": "api-url#11"})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if !strings.Contains(res, "permanently deleted") || !strings.Contains(res, "Verified gone") {
		t.Errorf("expected verified deletion, got: %s", res)
	}
	for _, key := range []string{"obj_id", "company_id", "project_id", "stage_id"} {
		if _, ok := captured[key]; !ok {
			t.Errorf("delete payload missing %q (server requires it): %v", key, captured)
		}
	}
	data, _ := os.ReadFile(".processes/variables.json")
	var remaining []map[string]string
	_ = json.Unmarshal(data, &remaining)
	for _, vr := range remaining {
		if vr["name"] == "api-url" {
			t.Errorf("cache entry not removed: %s", data)
		}
	}
}

func TestDeleteVariable_StillListedAfterDelete(t *testing.T) {
	// Server said ok but the re-list still shows the variable — the tool must
	// NOT claim success.
	m := &envVarMock{vars: stdVars(), dropOnDelete: false}
	res, isErr := callVarTool(t, m, "delete-variable", map[string]interface{}{
		"name": "api-url", "apply": true, "confirm": "api-url#11"})
	if !isErr || !strings.Contains(res, "STILL listed") {
		t.Fatalf("expected still-listed warning as error, got isErr=%v: %s", isErr, res)
	}
}

// ---- review-round additions ------------------------------------------------------

func TestModifyVariable_ApplyWithoutConfirm(t *testing.T) {
	var seen []string
	m := &envVarMock{vars: stdVars(), seen: &seen}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{
		"name": "api-url", "value": "v2", "apply": true})
	if !isErr || !strings.Contains(res, "confirmation required") {
		t.Fatalf("expected refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "modify:") {
			t.Fatalf("modify op must NOT run without confirm; seen=%v", seen)
		}
	}
}

// A non-string value must be refused — a silently failed assertion used to
// yield "" and would have irrecoverably wiped a secret the user believed was
// being replaced.
func TestModifyVariable_NonStringValueRefused(t *testing.T) {
	var seen []string
	m := &envVarMock{vars: stdVars(), seen: &seen}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{
		"name": "stripe-key", "value": 8080, "apply": true, "confirm": "stripe-key#22"})
	if !isErr || !strings.Contains(res, "must be a string") {
		t.Fatalf("expected type refusal, got isErr=%v: %s", isErr, res)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "modify:") {
			t.Fatalf("modify op must NOT run with a non-string value; seen=%v", seen)
		}
	}
}

func TestModifyVariable_SecretValueChangeMasksBothSides(t *testing.T) {
	m := &envVarMock{vars: stdVars()}
	res, isErr := callVarTool(t, m, "modify-variable", map[string]interface{}{
		"name": "stripe-key", "value": "NEW-PLAINTEXT-SECRET"})
	if isErr {
		t.Fatalf("dry-run failed: %s", res)
	}
	if strings.Contains(res, "NEW-PLAINTEXT-SECRET") || strings.Contains(res, secretRaw) {
		t.Fatalf("secret value leaked into modify diff:\n%s", res)
	}
	if !strings.Contains(res, "••••••") {
		t.Errorf("expected masked diff for secret value change:\n%s", res)
	}
}

// CLI passes booleans as strings; the gate must fire for apply="true" too.
func TestDeleteVariable_CLIStringApply(t *testing.T) {
	m := &envVarMock{vars: stdVars(), dropOnDelete: true}
	res, isErr := callVarTool(t, m, "delete-variable", map[string]interface{}{
		"name": "api-url", "apply": "true", "confirm": "api-url#11"})
	if isErr {
		t.Fatalf("unexpected error: %s", res)
	}
	if !strings.Contains(res, "permanently deleted") {
		t.Errorf("expected deletion with string apply, got: %s", res)
	}
}

// Plugin-created skeletons are named <id>_<name>.json (no .conv) — the
// reference scan must see them too.
func TestScanEnvVarRefs_PluginSkeletonFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "123_new-proc.json"), []byte(`{"x":"{{env_var[@api-url]}}"}`), 0644); err != nil {
		t.Fatal(err)
	}
	matches, scanned := scanEnvVarRefs("api-url")
	if scanned != 1 || len(matches) != 1 {
		t.Fatalf("skeleton file not scanned: matches=%v scanned=%d", matches, scanned)
	}
}

// Unknown/empty env_var_type must mask fail-safe.
func TestMaskedEnvVarValue_FailSafe(t *testing.T) {
	ev := EnvVar{EnvVarType: "", Value: "raw-thing"}
	if got := maskedEnvVarValue(ev); strings.Contains(got, "raw-thing") {
		t.Fatalf("unknown type must be masked, got %q", got)
	}
}

// A modify whose post-verify re-list fails must not read as a clean success —
// same honesty rule as the delete path (parity pinned by this test).
func TestModifyVariable_VerifyFailureIsSurfaced(t *testing.T) {
	calls := 0
	m := &envVarMock{vars: stdVars()}
	resetGlobals(t)
	t.Chdir(t.TempDir())
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		if len(ops) > 0 {
			if typ, _ := ops[0]["type"].(string); typ == "list" {
				calls++
				if calls > 1 { // resolution list ok, post-verify list fails
					return wrapVarOp(map[string]interface{}{"proc": "error", "description": "list boom"})
				}
			}
		}
		return m.fn(ops)
	})
	setProjectAuth(t, srv.URL)
	setVarTestAuth(t)
	res, isErr := handleToolCall(context.Background(), "modify-variable", map[string]interface{}{
		"name": "api-url", "value": "v2", "apply": true, "confirm": "api-url#11"})
	if isErr {
		t.Fatalf("modify itself succeeded — must not be an error: %s", res)
	}
	if !strings.Contains(res, "could not be re-verified") {
		t.Errorf("failed re-verify must be surfaced, got: %s", res)
	}
	if strings.Contains(res, "Verified on server") {
		t.Errorf("must not claim verification that never ran: %s", res)
	}
}

// A dead session must read as an auth problem with a recovery hint — not as a
// mysterious "could not resolve project for stage" (field incident).
func TestListVariables_DeadSessionSurfacesAuthHint(t *testing.T) {
	m := &envVarMock{}
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "error", "description": "cookie or headers are not valid"}}}
	})
	_ = m
	setProjectAuth(t, srv.URL)
	setVarTestAuth(t)
	res, isErr := handleToolCall(context.Background(), "list-variables", map[string]interface{}{})
	if !isErr {
		t.Fatalf("expected error, got: %s", res)
	}
	if !strings.Contains(res, "cookie or headers are not valid") || !strings.Contains(res, "re-run login") {
		t.Errorf("dead session must surface the auth cause + hint, got: %s", res)
	}
}
