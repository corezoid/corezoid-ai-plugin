package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Corezoid builds a Communications Orchestrator asynchronously: the
// `bot_wizzard` create op only queues the job (result "queued") and hands back
// an obj_id, so the caller has to poll `type: "check"` until the wizard
// reports ok or error. The knobs are package-level vars so tests can dial the
// interval down to microseconds instead of waiting the full 30 seconds.
var (
	botWizardPollEvery    = 3 * time.Second
	botWizardPollAttempts = 10
)

// commsChannelTokenField maps a messenger channel to the one credential field
// its wizard entry must carry. A channel that is not in this map is rejected
// before the API call — bot_wizzard answers an unknown channel with a generic
// "Value is not valid" that tells the user nothing about which entry is wrong.
var commsChannelTokenField = map[string]string{
	"telegram":    "key",
	"viber":       "viber_token",
	"fbmessenger": "page_access_token",
	"abc":         "abc_token",
}

// commsChannelOptFields lists the extra fields a channel accepts beyond its
// token. Only Apple Messages for Business has any: the wizard registers the
// brand contact alongside the token. Every other key in a messenger entry is
// an error rather than a silent no-op, so a misspelled "viber_key" surfaces
// here instead of producing a bot with no Viber binding.
var commsChannelOptFields = map[string][]string{
	"abc": {"user_id", "email", "name"},
}

// knownCommsChannels returns the supported channel names, sorted, for error
// messages.
func knownCommsChannels() []string {
	names := make([]string, 0, len(commsChannelTokenField))
	for name := range commsChannelTokenField {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parseMessengers normalises the `messengers` argument into the op payload.
// It accepts both a JSON string (the registry's declared form, and what the
// CLI can pass) and an already-decoded array, because MCP clients differ on
// whether they hand structured arguments through as JSON text or as values.
func parseMessengers(raw interface{}) ([]map[string]any, error) {
	if raw == nil {
		return nil, fmt.Errorf("missing required argument: messengers")
	}

	var decoded []interface{}
	switch v := raw.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, fmt.Errorf("messengers is empty — at least one messenger channel is required")
		}
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, fmt.Errorf("messengers must be a JSON array of channel objects: %w", err)
		}
	case []interface{}:
		decoded = v
	default:
		return nil, fmt.Errorf("messengers must be a JSON array of channel objects, got %T", raw)
	}

	if len(decoded) == 0 {
		return nil, fmt.Errorf("messengers is empty — at least one messenger channel is required")
	}

	out := make([]map[string]any, 0, len(decoded))
	seen := make(map[string]bool, len(decoded))
	for i, item := range decoded {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("messengers[%d] must be an object, got %T", i, item)
		}
		normalised, channel, err := normaliseMessenger(i, entry)
		if err != nil {
			return nil, err
		}
		if seen[channel] {
			return nil, fmt.Errorf("messengers[%d]: channel %q is listed twice — one entry per channel", i, channel)
		}
		seen[channel] = true
		out = append(out, normalised)
	}
	return out, nil
}

// normaliseMessenger validates one messenger entry and returns the op form of
// it plus its channel name.
func normaliseMessenger(idx int, entry map[string]interface{}) (map[string]any, string, error) {
	channel, _ := entry["channel"].(string)
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		return nil, "", fmt.Errorf("messengers[%d]: missing \"channel\" (one of: %s)",
			idx, strings.Join(knownCommsChannels(), ", "))
	}
	tokenField, ok := commsChannelTokenField[channel]
	if !ok {
		return nil, "", fmt.Errorf("messengers[%d]: unsupported channel %q (supported: %s)",
			idx, channel, strings.Join(knownCommsChannels(), ", "))
	}

	token := commsFieldString(entry[tokenField])
	if token == "" {
		return nil, "", fmt.Errorf("messengers[%d]: channel %q requires a non-empty %q",
			idx, channel, tokenField)
	}

	op := map[string]any{"channel": channel, tokenField: token}

	allowed := map[string]bool{"channel": true, tokenField: true}
	for _, field := range commsChannelOptFields[channel] {
		allowed[field] = true
		raw, present := entry[field]
		if !present {
			continue
		}
		if field == "user_id" {
			userID, err := commsFieldInt(raw)
			if err != nil {
				return nil, "", fmt.Errorf("messengers[%d]: channel %q: user_id must be an integer, got %v", idx, channel, raw)
			}
			op[field] = userID
			continue
		}
		if s := commsFieldString(raw); s != "" {
			op[field] = s
		}
	}

	var unknown []string
	for key := range entry {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		accepted := []string{"channel", tokenField}
		accepted = append(accepted, commsChannelOptFields[channel]...)
		return nil, "", fmt.Errorf("messengers[%d]: channel %q got unknown field(s) %s (accepted: %s)",
			idx, channel, strings.Join(unknown, ", "), strings.Join(accepted, ", "))
	}

	return op, channel, nil
}

// commsFieldString reads a messenger field as a trimmed string. Numbers are
// accepted too — a Telegram bot key or an ABC user id pasted as a bare number
// arrives as float64 through JSON.
func commsFieldString(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// commsFieldInt reads a messenger field as an int, accepting the JSON-number
// and string-encoded forms. A fractional value is an error rather than a
// truncation: this is an identity (the ABC brand contact), and 68381.5 silently
// becoming 68381 registers the bot against a different user than the caller
// named. resolveProcessID refuses fractional ids for the same reason.
func commsFieldInt(raw interface{}) (int, error) {
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("must be a whole number, got %v", v)
		}
		return int(v), nil
	case int:
		return v, nil
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	}
	return 0, fmt.Errorf("unexpected type %T", raw)
}

// commsCheckTargetID validates an optional stage_id/project_id argument before
// it is used to resolve a build target. An absent or null key is fine — both
// ids fall back to the workspace's configured stage — but a supplied one has to
// be a whole number greater than zero. argInt would truncate 12345.6 and
// happily accept 0 or a negative, and the resulting build lands somewhere the
// caller did not name, with no undo.
func commsCheckTargetID(args map[string]interface{}, key string) string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return ""
	}
	if f, isFloat := raw.(float64); isFloat && f != math.Trunc(f) {
		return fmt.Sprintf("Error: %s must be a whole number, got %v", key, f)
	}
	id, ok := argInt(args, key)
	if !ok {
		// argInt reports "not usable" the same way it reports "absent", so a
		// value it cannot parse would otherwise fall through to the configured
		// stage and build the bot somewhere the caller never named.
		return fmt.Sprintf("Error: %s must be an integer, got %v", key, raw)
	}
	if id <= 0 {
		return fmt.Sprintf("Error: %s must be greater than zero, got %d", key, id)
	}
	return ""
}

// handleCreateCommsOrchestrator creates a Communications Orchestrator — the
// multi-platform message-handling robot bot_wizzard builds from one folder of
// processes per messenger channel — and waits for the build to finish.
//
// The tool only reports success once the wizard hands back a folder_url: the
// queued job can still fail on a bad channel token, and "created" without the
// folder link is indistinguishable from a wizard that silently built nothing.
func handleCreateCommsOrchestrator(ctx context.Context, args map[string]interface{}) (string, bool) {
	messengers, err := parseMessengers(args["messengers"])
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)

	// argInt truncates a JSON float, so a mistyped 12345.6 would resolve to a
	// real but different stage — and this tool's whole output is "a folder of
	// ~150 processes now exists over there". Both target ids are validated
	// before anything is queued.
	if msg := commsCheckTargetID(args, "stage_id"); msg != "" {
		return msg, true
	}
	if msg := commsCheckTargetID(args, "project_id"); msg != "" {
		return msg, true
	}

	stageID := v.StageID
	if id, ok := argInt(args, "stage_id"); ok && id != 0 {
		stageID = id
	}
	if stageID == 0 {
		return "Error: no stage to create the orchestrator in — pass stage_id, or run login/init so the workspace has a configured stage", true
	}

	projectID, ok := argInt(args, "project_id")
	if !ok || projectID == 0 {
		projectID = v.GetProjectIDByStageID(stageID)
	}
	if projectID == 0 {
		return fmt.Sprintf("Error: could not resolve the project for stage %d — pass project_id explicitly", stageID), true
	}

	lang := strings.TrimSpace(optStrArg(args, "lang"))
	if lang == "" {
		lang = "en"
	}

	createOp := map[string]any{
		"obj":        "bot_wizzard",
		"type":       "create",
		"messengers": messengers,
		"company_id": v.WorkspaceID,
		"project_id": projectID,
		"stage_id":   stageID,
		"lang":       lang,
		"version":    2,
		"async":      true,
	}

	resp, err := v.req("json", []map[string]any{createOp})
	if err != nil {
		return fmt.Sprintf("Error: could not queue the Communications Orchestrator: %v", err), true
	}
	op, err := firstOp(resp)
	if err != nil {
		return fmt.Sprintf("Error: could not queue the Communications Orchestrator: %v", err), true
	}
	objID := stringValue(op, "obj_id")
	if objID == "" {
		return "Error: Corezoid queued no job — the create response carried no obj_id", true
	}

	channels := make([]string, 0, len(messengers))
	for _, m := range messengers {
		channels = append(channels, fmt.Sprint(m["channel"]))
	}

	return waitForCommsOrchestrator(v, objID, channels)
}

// waitForCommsOrchestrator polls `bot_wizzard` `check` until the build
// finishes. Transient check failures do not abort the wait — the job is
// already queued server-side, so a single failed status call is worth retrying
// rather than reporting as a failed build; the last error is surfaced only if
// the attempts run out.
func waitForCommsOrchestrator(v *Executor, objID string, channels []string) (string, bool) {
	var lastErr error
	var lastResult string

	for attempt := 1; attempt <= botWizardPollAttempts; attempt++ {
		if err := commsSleep(v.Ctx, botWizardPollEvery); err != nil {
			return fmt.Sprintf("Error: cancelled while waiting for orchestrator %s: %v", objID, err), true
		}

		resp, err := v.req("json", []map[string]any{{
			"obj":    "bot_wizzard",
			"type":   "check",
			"obj_id": objID,
		}})
		if err != nil {
			lastErr = err
			continue
		}
		op, err := firstOp(resp)
		if err != nil {
			lastErr = err
			continue
		}
		lastErr = nil

		lastResult = strings.ToLower(stringValue(op, "result"))
		switch lastResult {
		case "ok":
			folderURL := stringValue(op, "folder_url")
			if folderURL == "" {
				return fmt.Sprintf("Error: orchestrator %s reported result=ok but returned no folder_url — nothing was linked, report this to the Corezoid team", objID), true
			}
			out := map[string]any{
				"status":     "ok",
				"obj_id":     objID,
				"folder_url": folderURL,
				"channels":   channels,
				"checks":     attempt,
			}
			if url := stringValue(op, "dashboard_url"); url != "" {
				out["dashboard_url"] = url
			}
			if hooks := commsWebhooks(op["webhooks_url"]); len(hooks) > 0 {
				out["webhooks_url"] = hooks
			}
			if desc := commsDescription(op); desc != "" {
				out["description"] = desc
			}
			data, _ := json.MarshalIndent(out, "", "  ")
			return string(data), false
		case "error", "fail", "failed":
			desc := commsDescription(op)
			if desc == "" {
				desc = "the wizard reported an error without a description"
			}
			return fmt.Sprintf("Error: Communications Orchestrator %s failed to build: %s", objID, desc), true
		}
	}

	msg := fmt.Sprintf("Error: Communications Orchestrator %s is still building after %d checks (%s)",
		objID, botWizardPollAttempts, (time.Duration(botWizardPollAttempts) * botWizardPollEvery).String())
	if lastResult != "" {
		msg += fmt.Sprintf("; last status: %s", lastResult)
	}
	if lastErr != nil {
		msg += fmt.Sprintf("; last check error: %v", lastErr)
	}
	return msg + ". The build may still finish on the server — check the target stage in Corezoid before creating another one.", true
}

// commsSleep waits d but returns early with the context error on
// cancellation, so a /cancel notification is not held up for the full poll
// interval.
func commsSleep(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		time.Sleep(d)
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// commsDescription reads the op description, dropping the literal "undefined"
// bot_wizzard sends on success — echoing it back reads as a real message.
func commsDescription(op map[string]any) string {
	desc := strings.TrimSpace(stringValue(op, "description"))
	if desc == "undefined" {
		return ""
	}
	return desc
}

// commsWebhooks keeps only the webhook entries that actually carry a URL.
// bot_wizzard returns a fixed row per channel it knows about, so the
// unconfigured ones come back with url:"" and would otherwise read as
// "configured but broken".
func commsWebhooks(raw interface{}) []map[string]string {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var out []map[string]string
	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		url := strings.TrimSpace(stringValue(entry, "url"))
		if url == "" {
			continue
		}
		out = append(out, map[string]string{
			"channel": stringValue(entry, "channel"),
			"url":     url,
		})
	}
	return out
}
