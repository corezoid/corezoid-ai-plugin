package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// commsMock answers the ops create-communications-orchestrator issues:
// show-folder (which feeds GetProjectIDByStageID), bot_wizzard create, and
// bot_wizzard check. checkResults is consumed one entry per check call so a
// test can script "queued, queued, ok".
type commsMock struct {
	createOp     map[string]interface{}
	checkOps     []map[string]interface{}
	checkResults []map[string]interface{}
	createErr    bool
	omitObjID    bool
	checkHTTPErr int // fail this many check calls with proc=error first
}

func (m *commsMock) fn(ops []map[string]interface{}) interface{} {
	if len(ops) == 0 {
		return wrapCommsOp(map[string]interface{}{"proc": "ok"})
	}
	op := ops[0]
	typ, _ := op["type"].(string)
	obj, _ := op["obj"].(string)

	switch {
	case typ == "show" && obj == "folder":
		id, _ := op["obj_id"].(float64)
		return wrapCommsOp(map[string]interface{}{
			"proc": "ok", "obj_id": id, "parent_obj_id": float64(647737),
			"title": "dev", "obj_type": float64(3),
		})

	case typ == "create" && obj == "bot_wizzard":
		m.createOp = op
		if m.createErr {
			return wrapCommsOp(map[string]interface{}{"proc": "error", "description": "Value is not valid / messengers"})
		}
		res := map[string]interface{}{
			"proc": "ok", "obj": "bot_wizzard", "id": "", "result": "queued",
			"obj_id": "6a97d166e552e8f782614ea7",
		}
		if m.omitObjID {
			delete(res, "obj_id")
		}
		return wrapCommsOp(res)

	case typ == "check" && obj == "bot_wizzard":
		m.checkOps = append(m.checkOps, op)
		if m.checkHTTPErr > 0 {
			m.checkHTTPErr--
			return wrapCommsOp(map[string]interface{}{"proc": "error", "description": "temporary glitch"})
		}
		if len(m.checkResults) == 0 {
			return wrapCommsOp(map[string]interface{}{"proc": "ok", "obj": "bot_wizzard", "result": "queued"})
		}
		next := m.checkResults[0]
		if len(m.checkResults) > 1 {
			m.checkResults = m.checkResults[1:]
		}
		return wrapCommsOp(next)
	}
	return wrapCommsOp(map[string]interface{}{"proc": "ok"})
}

func wrapCommsOp(op map[string]interface{}) interface{} {
	return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{op}}
}

// commsOKCheck is the shape bot_wizzard returns for a finished build.
func commsOKCheck(folderURL string) map[string]interface{} {
	return map[string]interface{}{
		"proc": "ok", "obj": "bot_wizzard", "id": "",
		"dashboard_url": "",
		"folder_url":    folderURL,
		"webhooks_url": []interface{}{
			map[string]interface{}{"channel": "fbmessenger", "url": "https://hook.example/fb"},
			map[string]interface{}{"channel": "skype", "url": ""},
		},
		"result":      "ok",
		"description": "undefined",
	}
}

// callCommsTool runs the tool against the mock with the poll interval dialled
// down, so the 3s production cadence does not turn every test into a 30s wait.
func callCommsTool(t *testing.T, m *commsMock, args map[string]interface{}) (string, bool) {
	t.Helper()
	resetGlobals(t)
	t.Chdir(t.TempDir())

	origEvery, origAttempts := botWizardPollEvery, botWizardPollAttempts
	botWizardPollEvery = time.Millisecond
	t.Cleanup(func() { botWizardPollEvery, botWizardPollAttempts = origEvery, origAttempts })

	srv, _ := mockAPIServer(t, m.fn)
	setProjectAuth(t, srv.URL)
	origAccount, origStage := accountURL, stageID
	accountURL = "https://account.test"
	stageID = 647738
	t.Cleanup(func() { accountURL = origAccount; stageID = origStage })

	return handleToolCall(context.Background(), "create-communications-orchestrator", args)
}

const commsTelegramOnly = `[{"channel":"telegram","key":"1234"}]`

// The whole point of the tool: the caller gets the folder the wizard built,
// not just "queued". A result without folder_url is not a success.
func TestCommsOrchestrator_ReturnsFolderURL(t *testing.T) {
	m := &commsMock{checkResults: []map[string]interface{}{
		{"proc": "ok", "obj": "bot_wizzard", "result": "queued"},
		commsOKCheck("https://admin.corezoid.com/folder/691905"),
	}}

	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": commsTelegramOnly})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, out)
	}
	if got["folder_url"] != "https://admin.corezoid.com/folder/691905" {
		t.Errorf("folder_url = %v, want the wizard's folder URL", got["folder_url"])
	}
	if got["obj_id"] != "6a97d166e552e8f782614ea7" {
		t.Errorf("obj_id = %v, want the queued job id", got["obj_id"])
	}
	if got["status"] != "ok" {
		t.Errorf("status = %v, want ok", got["status"])
	}
	// "undefined" is the wizard's placeholder on success — echoing it back
	// would read as a real message.
	if _, ok := got["description"]; ok {
		t.Errorf("description must be dropped when it is the literal \"undefined\", got %v", got["description"])
	}
	// Empty rows (dashboard_url, the skype webhook) are noise, not state.
	if _, ok := got["dashboard_url"]; ok {
		t.Errorf("empty dashboard_url must be omitted, got %v", got["dashboard_url"])
	}
	hooks, _ := got["webhooks_url"].([]interface{})
	if len(hooks) != 1 {
		t.Fatalf("webhooks_url = %v, want only the entry that carries a URL", got["webhooks_url"])
	}

	if len(m.checkOps) != 2 {
		t.Fatalf("check calls = %d, want 2 (queued then ok)", len(m.checkOps))
	}
	if m.checkOps[0]["obj_id"] != "6a97d166e552e8f782614ea7" || m.checkOps[0]["obj"] != "bot_wizzard" {
		t.Errorf("check op = %v, want {obj:bot_wizzard, type:check, obj_id:<queued id>}", m.checkOps[0])
	}
}

// The create payload is the contract with bot_wizzard — a wrong obj/version/
// async triple fails server-side with an unhelpful message, so pin it here.
func TestCommsOrchestrator_CreatePayload(t *testing.T) {
	m := &commsMock{checkResults: []map[string]interface{}{commsOKCheck("https://admin.corezoid.com/folder/1")}}

	messengers := `[
	  {"channel":"abc","abc_token":"1234","user_id":68381,"email":"salimov.artem@corezoid.com","name":"Artem Salimov"},
	  {"channel":"viber","viber_token":"1234"},
	  {"channel":"fbmessenger","page_access_token":"1234"},
	  {"channel":"telegram","key":"1234"}
	]`
	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": messengers})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}

	op := m.createOp
	if op == nil {
		t.Fatal("no create op reached the API")
	}
	for key, want := range map[string]interface{}{
		"obj":        "bot_wizzard",
		"type":       "create",
		"lang":       "en",
		"version":    float64(2),
		"async":      true,
		"company_id": "i260836082",
		"stage_id":   float64(647738),
		"project_id": float64(647737),
	} {
		if op[key] != want {
			t.Errorf("create op %s = %v (%T), want %v", key, op[key], op[key], want)
		}
	}

	list, _ := op["messengers"].([]interface{})
	if len(list) != 4 {
		t.Fatalf("messengers = %v, want 4 entries", op["messengers"])
	}
	abc, _ := list[0].(map[string]interface{})
	if abc["channel"] != "abc" || abc["abc_token"] != "1234" ||
		abc["user_id"] != float64(68381) || abc["email"] != "salimov.artem@corezoid.com" || abc["name"] != "Artem Salimov" {
		t.Errorf("abc entry = %v, want token plus the brand contact fields", abc)
	}
	tg, _ := list[3].(map[string]interface{})
	if tg["channel"] != "telegram" || tg["key"] != "1234" {
		t.Errorf("telegram entry = %v, want {channel, key}", tg)
	}
}

// stage_id/project_id/lang override the workspace defaults, and project_id is
// then not resolved by walking the folder tree.
func TestCommsOrchestrator_ExplicitTargetSkipsFolderWalk(t *testing.T) {
	m := &commsMock{checkResults: []map[string]interface{}{commsOKCheck("https://admin.corezoid.com/folder/2")}}

	out, isErr := callCommsTool(t, m, map[string]interface{}{
		"messengers": commsTelegramOnly,
		"stage_id":   float64(999001),
		"project_id": float64(999000),
		"lang":       "uk",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if m.createOp["stage_id"] != float64(999001) || m.createOp["project_id"] != float64(999000) {
		t.Errorf("target = stage %v / project %v, want the explicit arguments", m.createOp["stage_id"], m.createOp["project_id"])
	}
	if m.createOp["lang"] != "uk" {
		t.Errorf("lang = %v, want uk", m.createOp["lang"])
	}
}

// A failed build must surface the wizard's own diagnosis — that string names
// which channel token was rejected.
func TestCommsOrchestrator_BuildError(t *testing.T) {
	m := &commsMock{checkResults: []map[string]interface{}{{
		"proc": "ok", "obj": "bot_wizzard", "result": "error",
		"folder_url":  "",
		"description": "ERRORS: -Telegram response: Not Found- -Viber response: invalidAuthToken- -Wrong ABC token-",
	}}}

	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": commsTelegramOnly})
	if !isErr {
		t.Fatalf("a failed build must be reported as an error, got: %s", out)
	}
	if !strings.Contains(out, "invalidAuthToken") {
		t.Errorf("error must carry the wizard's description, got: %s", out)
	}
	if len(m.checkOps) != 1 {
		t.Errorf("check calls = %d, want 1 — polling must stop at a terminal error", len(m.checkOps))
	}
}

// result=ok with no folder_url is not a success: there is nothing for the user
// to open, so reporting "created" would be a lie.
func TestCommsOrchestrator_OKWithoutFolderURLIsError(t *testing.T) {
	m := &commsMock{checkResults: []map[string]interface{}{{
		"proc": "ok", "obj": "bot_wizzard", "result": "ok", "folder_url": "",
	}}}

	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": commsTelegramOnly})
	if !isErr {
		t.Fatalf("result=ok without folder_url must be an error, got: %s", out)
	}
	if !strings.Contains(out, "folder_url") {
		t.Errorf("error should name the missing field, got: %s", out)
	}
}

// A build that never finishes stops after the attempt budget and hands back
// the job id, so the user can look for the folder instead of queueing a
// duplicate build.
func TestCommsOrchestrator_PollBudgetExhausted(t *testing.T) {
	m := &commsMock{} // always "queued"

	origAttempts := botWizardPollAttempts
	botWizardPollAttempts = 3
	t.Cleanup(func() { botWizardPollAttempts = origAttempts })

	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": commsTelegramOnly})
	if !isErr {
		t.Fatalf("an unfinished build must be reported as an error, got: %s", out)
	}
	if len(m.checkOps) != 3 {
		t.Errorf("check calls = %d, want exactly the attempt budget (3)", len(m.checkOps))
	}
	if !strings.Contains(out, "6a97d166e552e8f782614ea7") {
		t.Errorf("timeout error must carry the job id so the build can be traced, got: %s", out)
	}
}

// The job is already queued server-side, so one failed status call is worth
// retrying rather than reporting as a failed build.
func TestCommsOrchestrator_TransientCheckFailureIsRetried(t *testing.T) {
	m := &commsMock{
		checkHTTPErr: 2,
		checkResults: []map[string]interface{}{commsOKCheck("https://admin.corezoid.com/folder/3")},
	}

	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": commsTelegramOnly})
	if isErr {
		t.Fatalf("transient check failures must not fail the build: %s", out)
	}
	if len(m.checkOps) != 3 {
		t.Errorf("check calls = %d, want 3 (two failures then success)", len(m.checkOps))
	}
}

// A rejected create op is reported straight away — there is no job to poll.
func TestCommsOrchestrator_CreateRejected(t *testing.T) {
	m := &commsMock{createErr: true}

	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": commsTelegramOnly})
	if !isErr {
		t.Fatalf("a rejected create must be an error, got: %s", out)
	}
	if len(m.checkOps) != 0 {
		t.Errorf("check calls = %d, want 0 — nothing was queued", len(m.checkOps))
	}
}

func TestCommsOrchestrator_CreateWithoutObjIDIsError(t *testing.T) {
	m := &commsMock{omitObjID: true}

	out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": commsTelegramOnly})
	if !isErr {
		t.Fatalf("a create response with no obj_id must be an error, got: %s", out)
	}
	if len(m.checkOps) != 0 {
		t.Errorf("check calls = %d, want 0 — there is no job id to poll", len(m.checkOps))
	}
}

// Bad input is rejected locally: bot_wizzard answers every malformed
// messengers list with a generic "Value is not valid" that does not say which
// entry is wrong.
func TestCommsOrchestrator_MessengerValidation(t *testing.T) {
	cases := []struct {
		name       string
		messengers interface{}
		wantSubstr string
	}{
		{"empty array", `[]`, "at least one messenger"},
		{"empty string", `   `, "at least one messenger"},
		{"not JSON", `telegram`, "JSON array"},
		{"not an object", `["telegram"]`, "must be an object"},
		{"no channel", `[{"key":"1234"}]`, `missing "channel"`},
		{"unknown channel", `[{"channel":"whatsapp","key":"1234"}]`, "unsupported channel"},
		{"missing token", `[{"channel":"telegram"}]`, `requires a non-empty "key"`},
		{"blank token", `[{"channel":"viber","viber_token":"  "}]`, `requires a non-empty "viber_token"`},
		{"wrong token field", `[{"channel":"viber","key":"1234"}]`, `requires a non-empty "viber_token"`},
		{"unknown field", `[{"channel":"telegram","key":"1","secret":"x"}]`, "unknown field(s) secret"},
		{"duplicate channel", `[{"channel":"telegram","key":"1"},{"channel":"telegram","key":"2"}]`, "listed twice"},
		{"non-integer user_id", `[{"channel":"abc","abc_token":"1","user_id":"abc"}]`, "user_id must be an integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &commsMock{}
			out, isErr := callCommsTool(t, m, map[string]interface{}{"messengers": tc.messengers})
			if !isErr {
				t.Fatalf("expected a validation error, got: %s", out)
			}
			if !strings.Contains(out, tc.wantSubstr) {
				t.Errorf("error = %q, want it to mention %q", out, tc.wantSubstr)
			}
			if m.createOp != nil {
				t.Error("invalid input must not reach the API")
			}
		})
	}
}

// MCP clients differ on whether structured arguments arrive as JSON text or as
// decoded values; both must work, and a channel name is matched case- and
// whitespace-insensitively.
func TestCommsOrchestrator_AcceptsDecodedArrayAndNormalisesChannel(t *testing.T) {
	m := &commsMock{checkResults: []map[string]interface{}{commsOKCheck("https://admin.corezoid.com/folder/4")}}

	out, isErr := callCommsTool(t, m, map[string]interface{}{
		"messengers": []interface{}{
			map[string]interface{}{"channel": " Telegram ", "key": float64(1234)},
		},
	})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	list, _ := m.createOp["messengers"].([]interface{})
	entry, _ := list[0].(map[string]interface{})
	if entry["channel"] != "telegram" {
		t.Errorf("channel = %v, want it lower-cased and trimmed", entry["channel"])
	}
	// A numeric key pasted without quotes must still reach the API as a string.
	if entry["key"] != "1234" {
		t.Errorf("key = %v (%T), want the string \"1234\"", entry["key"], entry["key"])
	}
}

// Cancelling the request must not hold the caller for a full poll interval.
func TestCommsSleep_HonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := commsSleep(ctx, time.Hour); err == nil {
		t.Error("commsSleep must return the context error instead of sleeping")
	}
}
