package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

	// Drive the tool past its apply/confirm gate by default so the tests below
	// keep asserting build behaviour rather than the gate. Tests that exercise
	// the gate itself pass apply/confirm explicitly and are left alone.
	if _, set := args["apply"]; !set {
		args["apply"] = true
	}
	if _, set := args["confirm"]; !set {
		args["confirm"] = commsConfirmTokenForArgs(args)
	}

	return handleToolCall(context.Background(), "create-communications-orchestrator", args)
}

// commsConfirmTokenForArgs rebuilds the token the handler will demand for these
// arguments, mirroring how callCommsTool configures the stage. Tests that care
// about the token's exact shape assert a literal instead of calling this.
func commsConfirmTokenForArgs(args map[string]interface{}) string {
	stage := 647738
	if v, ok := argInt(args, "stage_id"); ok && v != 0 {
		stage = v
	}
	ms, err := parseMessengers(args["messengers"])
	if err != nil {
		return "" // malformed input is rejected before the gate is reached
	}
	channels := make([]string, 0, len(ms))
	for _, m := range ms {
		channels = append(channels, fmt.Sprint(m["channel"]))
	}
	return commsConfirmToken(stage, channels, commsCredentialFingerprint(ms))
}

const commsTelegramOnly = `[{"channel":"telegram","key":"1234"}]`

// End-to-end guard that the handler uses reqOnce and not req. The doWithRetry
// unit tests prove maxAttempts=1 works; this proves the orchestrator actually
// asks for it, so swapping the call back to v.req fails here instead of turning
// one 503 into two ~150-process builds in production.
func TestCommsOrchestrator_CreateIsNeverRetried(t *testing.T) {
	shortenRetryDelays(t)
	resetGlobals(t)
	t.Chdir(t.TempDir())

	var createCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if len(body.Ops) > 0 {
			typ, _ := body.Ops[0]["type"].(string)
			obj, _ := body.Ops[0]["obj"].(string)
			if typ == "create" && obj == "bot_wizzard" {
				// Corezoid accepted the job and then failed to answer — the
				// exact shape where a retry duplicates the build.
				atomic.AddInt32(&createCalls, 1)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"request_proc": "ok",
			"ops": []interface{}{map[string]interface{}{
				"proc": "ok", "obj_id": float64(647738), "parent_obj_id": float64(647737),
				"title": "dev", "obj_type": float64(3),
			}},
		})
	}))
	t.Cleanup(srv.Close)

	setProjectAuth(t, srv.URL)
	origAccount, origStage := accountURL, stageID
	accountURL = "https://account.test"
	stageID = 647738
	t.Cleanup(func() { accountURL = origAccount; stageID = origStage })

	args := map[string]interface{}{
		"messengers": commsTelegramOnly,
		"apply":      true,
	}
	args["confirm"] = commsConfirmTokenForArgs(args)
	out, isErr := handleToolCall(context.Background(), "create-communications-orchestrator", args)
	if !isErr {
		t.Fatalf("a 503 on create must surface as an error, got: %s", out)
	}
	if n := atomic.LoadInt32(&createCalls); n != 1 {
		t.Fatalf("create was delivered %d times, want exactly 1 — every extra delivery is another ~150-process build", n)
	}
	// The user has to be told the build may exist anyway, or they will re-run
	// the tool and get the duplicate the no-retry rule just prevented.
	if !strings.Contains(out, "check stage") {
		t.Errorf("the error should tell the user to check the stage before retrying, got: %s", out)
	}
}

// The gate is the only thing standing between a stray tool call and ~150
// irreversible processes, so it gets tested for the property that matters:
// nothing reaches the API until an exact, matching token arrives.
func TestCommsOrchestrator_GateBlocksTheBuild(t *testing.T) {
	cases := []struct {
		name  string
		args  map[string]interface{}
		isErr bool
		want  string
	}{
		{
			name:  "no apply is a dry-run, not an error",
			args:  map[string]interface{}{"messengers": commsTelegramOnly, "apply": false, "confirm": ""},
			isErr: false,
			want:  "DRY-RUN",
		},
		{
			name:  "apply without a confirm token is refused",
			args:  map[string]interface{}{"messengers": commsTelegramOnly, "apply": true, "confirm": ""},
			isErr: true,
			want:  "Confirmation required",
		},
		{
			name:  "a token for a different channel set is refused",
			args:  map[string]interface{}{"messengers": commsTelegramOnly, "apply": true, "confirm": "orchestrator@stage#647738:telegram+viber"},
			isErr: true,
			want:  "Confirmation required",
		},
		{
			name:  "a token for a different stage is refused",
			args:  map[string]interface{}{"messengers": commsTelegramOnly, "apply": true, "confirm": "orchestrator@stage#999999:telegram"},
			isErr: true,
			want:  "Confirmation required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &commsMock{checkResults: []map[string]interface{}{commsOKCheck("https://admin.corezoid.com/folder/9")}}
			out, isErr := callCommsTool(t, m, tc.args)
			if isErr != tc.isErr {
				t.Errorf("isErr = %v, want %v; out = %s", isErr, tc.isErr, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output should mention %q, got: %s", tc.want, out)
			}
			if m.createOp != nil {
				t.Error("the build was queued despite the gate — this is the failure the gate exists to prevent")
			}
		})
	}
}

// The dry-run has to carry the token verbatim: an agent that cannot copy it out
// of the preview will either invent one or give up mid-build.
func TestCommsOrchestrator_DryRunCarriesTheConfirmToken(t *testing.T) {
	m := &commsMock{checkResults: []map[string]interface{}{commsOKCheck("https://admin.corezoid.com/folder/9")}}
	out, isErr := callCommsTool(t, m, map[string]interface{}{
		"messengers": `[{"channel":"viber","viber_token":"v"},{"channel":"telegram","key":"1234"}]`,
		"apply":      false,
		"confirm":    "",
	})
	if isErr {
		t.Fatalf("a dry-run is not an error: %s", out)
	}
	// Channels are sorted, so the token does not depend on the order the caller
	// happened to list them in. The trailing segment is the credential
	// fingerprint, asserted by shape here and by behaviour in
	// TestCommsOrchestrator_ConfirmTokenIsBoundToCredentials.
	prefix := `confirm="orchestrator@stage#647738:telegram+viber/`
	if !strings.Contains(out, prefix) {
		t.Errorf("dry-run should print %s<fingerprint>\", got: %s", prefix, out)
	}
	if fp := commsCredentialFingerprint(mustParseMessengers(t, `[{"channel":"viber","viber_token":"v"},{"channel":"telegram","key":"1234"}]`)); !strings.Contains(out, prefix+fp+`"`) {
		t.Errorf("dry-run token should end in the credential fingerprint %q, got: %s", fp, out)
	}
	for _, phrase := range []string{"NO UNDO", "webhook", "~150 processes"} {
		if !strings.Contains(out, phrase) {
			t.Errorf("preview should warn about %q, got: %s", phrase, out)
		}
	}
}

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

// A mistyped target id must not resolve to a real-but-different stage. argInt
// truncates a JSON float and reports an unparseable value the same way it
// reports an absent key, so without these checks 12345.6 would build the bot
// in stage 12345 and "abc" would build it in whatever stage the workspace has
// configured — both irreversibly, and neither named by the caller.
func TestCommsOrchestrator_RejectsBadTargetIDs(t *testing.T) {
	valid := `[{"channel":"telegram","key":"tg-token"}]`
	cases := map[string]struct {
		args map[string]interface{}
		want string
	}{
		"fractional stage_id":   {map[string]interface{}{"messengers": valid, "stage_id": 12345.6}, "whole number"},
		"fractional project_id": {map[string]interface{}{"messengers": valid, "project_id": 777.25}, "whole number"},
		"negative stage_id":     {map[string]interface{}{"messengers": valid, "stage_id": float64(-5)}, "greater than zero"},
		"zero project_id":       {map[string]interface{}{"messengers": valid, "project_id": float64(0)}, "greater than zero"},
		"non-numeric stage_id":  {map[string]interface{}{"messengers": valid, "stage_id": "abc"}, "must be an integer"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, isErr := handleCreateCommsOrchestrator(context.Background(), tc.args)
			if !isErr {
				t.Fatalf("expected an error, got success: %s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("error must explain the problem (%q), got: %s", tc.want, out)
			}
		})
	}
}

// The ABC brand contact is an identity: 68381.5 quietly becoming 68381
// registers the bot against a different user than the caller named.
func TestCommsOrchestrator_RejectsFractionalUserID(t *testing.T) {
	_, err := parseMessengers([]interface{}{
		map[string]interface{}{"channel": "abc", "abc_token": "t", "user_id": 68381.5},
	})
	if err == nil {
		t.Fatal("expected a fractional user_id to be rejected")
	}
	if !strings.Contains(err.Error(), "user_id") {
		t.Errorf("error must name user_id, got: %v", err)
	}
}

// mustParseMessengers parses a messengers payload the way the handler does, so
// a test can compute the fingerprint the gate will demand.
func mustParseMessengers(t *testing.T, raw string) []map[string]any {
	t.Helper()
	ms, err := parseMessengers(raw)
	if err != nil {
		t.Fatalf("parseMessengers(%s): %v", raw, err)
	}
	return ms
}

// TestCommsOrchestrator_ConfirmTokenIsBoundToCredentials covers the gap an
// inbound review found: the token used to be derived from the stage and the
// channel NAMES only, so a confirm token issued for a preview of bot A was
// accepted for a build against bot B. Channel names are the one thing that
// does not change when the credentials are swapped, and taking over the wrong
// live bot's webhook is the exact harm the gate exists to prevent.
func TestCommsOrchestrator_ConfirmTokenIsBoundToCredentials(t *testing.T) {
	const previewed = `[{"channel":"telegram","key":"BOT-A"}]`
	const swapped = `[{"channel":"telegram","key":"BOT-B"}]`

	tokenFor := func(raw string) string {
		return commsConfirmToken(647738, []string{"telegram"}, commsCredentialFingerprint(mustParseMessengers(t, raw)))
	}
	if tokenFor(previewed) == tokenFor(swapped) {
		t.Fatal("swapping the bot token must change the confirm token")
	}

	m := &commsMock{checkResults: []map[string]interface{}{commsOKCheck("https://admin.corezoid.com/folder/9")}}
	out, isErr := callCommsTool(t, m, map[string]interface{}{
		"messengers": swapped,
		"apply":      true,
		"confirm":    tokenFor(previewed), // approved for BOT-A
	})
	if !isErr {
		t.Fatalf("a token issued for different credentials must be refused, got: %s", out)
	}
	if !strings.Contains(out, "Confirmation required") {
		t.Errorf("the refusal should be the confirm gate, got: %s", out)
	}
	if m.createOp != nil {
		t.Error("the build was queued against credentials the user never approved")
	}
}

// The preview and the build are two separate calls, so the fingerprint has to
// survive an agent re-serialising the payload between them. Only the values
// matter — not the order of the entries, nor the order of keys within one.
func TestCommsCredentialFingerprint_IgnoresOrderingOnly(t *testing.T) {
	base := commsCredentialFingerprint(mustParseMessengers(t, `[{"channel":"telegram","key":"1234"},{"channel":"viber","viber_token":"v"}]`))
	reordered := commsCredentialFingerprint(mustParseMessengers(t, `[{"viber_token":"v","channel":"viber"},{"key":"1234","channel":"telegram"}]`))
	if base != reordered {
		t.Errorf("reordering entries or keys must not change the fingerprint: %q vs %q", base, reordered)
	}
	changed := commsCredentialFingerprint(mustParseMessengers(t, `[{"channel":"telegram","key":"1235"},{"channel":"viber","viber_token":"v"}]`))
	if base == changed {
		t.Error("a one-character change to a credential must change the fingerprint")
	}
}
