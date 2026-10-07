package main

// Hosted variants of the process tools that locally work through files. Same
// tool names, different contract: content travels in the request and the
// response instead of the working directory, which a hosted server does not
// have per caller. The local handlers are untouched; handleToolCall picks
// these only in hosted mode.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// hostedHandlers override the local handler of the same name in hosted mode.
var hostedHandlers = map[string]toolHandler{
	"pull-process":   hostedPullProcess,
	"lint-process":   hostedLintProcess,
	"create-process": hostedCreateProcess,
	"run-task":       hostedRunTask,
	"push-process":   hostedPushProcess,
}

// hostedMaxContentBytes caps process JSON accepted in a request. Large
// workspaces reach a few MB; the HTTP body cap is far above this.
const hostedMaxContentBytes = 8 << 20

// hostedToolDefs are the schemas the hosted server advertises for the tools in
// hostedHandlers. They replace the local schemas, which describe files.
var hostedToolDefs = map[string]mcpTool{
	"pull-process": {
		Name: "pull-process",
		Description: "Fetch a Corezoid process as JSON. Returns the process scheme and a `base` token naming the server version it was read at — keep it: a later deploy of this process compares it with the server to detect concurrent changes. " +
			"Read-only; nothing is written anywhere.",
		Annotations: toolHints(hintReadOnly, hintSafe, hintIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{"type": "integer", "description": "Corezoid process (conv) ID."},
			},
			"required": []string{"process_id"},
		},
	},
	"lint-process": {
		Name:        "lint-process",
		Description: "Lint a Corezoid process given as JSON text: structural problems, unreachable or no-op nodes, missing error branches, literal values that should be env variables, schema validity. Runs locally on the content; calls nothing.",
		Annotations: toolHints(hintReadOnly, hintSafe, hintIdempotent, hintLocal),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"content": map[string]interface{}{"type": "string", "description": "Process JSON (as returned by pull-process)."},
			},
			"required": []string{"content"},
		},
	},
	"create-process": {
		Name:        "create-process",
		Description: "Create an empty Corezoid process in a folder. Returns the new process ID, its JSON scheme and a `base` token for the first deploy.",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id":    map[string]interface{}{"type": "integer", "description": "Corezoid folder (or stage root) ID to create the process in. Find it with list-folders / show-project."},
				"process_name": map[string]interface{}{"type": "string", "description": "Name of the new process."},
			},
			"required": []string{"folder_id", "process_name"},
		},
	},
	"push-process": {
		Name: "push-process",
		Description: "Validate and deploy a Corezoid process from JSON. Runs the same gates as the local push: structure fix, schema, lint (force=true overrides blocking findings, never structural ones), a concurrency check against `base`, a pre-push snapshot, and the irreversibility check. " +
			"Pass the `base` token from pull-process / create-process: if someone changed the process on the server since, the push is blocked with a report — pull again and re-apply your edits (merging is not available on the hosted server). " +
			"Needs scope.stage_id for the snapshot. Returns the deployed scheme with the server's node IDs and a new `base` for the next edit.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"content":                 map[string]interface{}{"type": "string", "description": "Process JSON to deploy. Its obj_id names the process (create it first with create-process)."},
				"base":                    map[string]interface{}{"type": "string", "description": "Base token from pull-process / create-process / the previous push."},
				"force":                   map[string]interface{}{"type": "boolean", "description": "Deploy despite blocking lint findings (not structural ones, not conflicts)."},
				"overwrite_server_change": map[string]interface{}{"type": "boolean", "description": "Deploy over a server change made since `base`, dropping it. Pass only in reply to the block report."},
				"adopt_existing":          map[string]interface{}{"type": "boolean", "description": "Deploy without a base token over a process that already has a deployed version, overwriting it blind."},
				"allow_active_stub_mode":  map[string]interface{}{"type": "boolean", "description": "Allow active Stub Mode nodes on a stage that otherwise refuses them."},
				"allow_no_snapshot":       map[string]interface{}{"type": "boolean", "description": "Deploy although no pre-push snapshot could be taken (mutable stages only)."},
			},
			"required": []string{"content"},
		},
	},
	"run-task": {
		Name:        "run-task",
		Description: "Create a task in a deployed Corezoid process and wait for it to reach a final node. Returns the task's final node and data.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld), // runs the process: its side effects are arbitrary
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{"type": "integer", "description": "Corezoid process (conv) ID."},
				"data":       map[string]interface{}{"type": "string", "description": "Task input as a JSON object string, e.g. {\"amount\": 21}."},
				"ref":        map[string]interface{}{"type": "string", "description": "Optional task ref (lookup key)."},
				"wait_sec":   map[string]interface{}{"type": "integer", "description": "How long to wait for a final node, seconds (default 30, max 600)."},
			},
			"required": []string{"process_id", "data"},
		},
	},
}

// hostedBaseToken encodes the server version a process was read at. It is a
// concurrency marker, not a credential: forging it only weakens the caller's
// own conflict check on a process they can already write.
func hostedBaseToken(processID int, b baselineEntry) string {
	return fmt.Sprintf("v1:%d:%d:%d", processID, b.ChangeTime, b.Version)
}

// parseHostedBaseToken reverses hostedBaseToken.
func parseHostedBaseToken(tok string) (processID int, b baselineEntry, err error) {
	parts := strings.Split(strings.TrimSpace(tok), ":")
	if len(parts) != 4 || parts[0] != "v1" {
		return 0, baselineEntry{}, fmt.Errorf("malformed base token")
	}
	nums := make([]int64, 3)
	for i, p := range parts[1:] {
		n, perr := strconv.ParseInt(p, 10, 64)
		if perr != nil || n < 0 {
			return 0, baselineEntry{}, fmt.Errorf("malformed base token")
		}
		nums[i] = n
	}
	return int(nums[0]), baselineEntry{ChangeTime: nums[1], Version: nums[2], Source: baselineSourceDetail}, nil
}

// exportProcessJSON exports v.ProcessID as indented JSON, unwrapping the
// single-element array the export API returns.
func exportProcessJSON(v *Executor) ([]byte, map[string]interface{}, error) {
	exported, err := v.ExportProcess()
	if err != nil {
		return nil, nil, err
	}
	doc := exported
	if arr, ok := exported.([]interface{}); ok && len(arr) > 0 {
		doc = arr[0]
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	m, _ := doc.(map[string]interface{})
	return data, m, nil
}

func hostedPullProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil || processID <= 0 {
		return "Error: process_id must be a positive integer", true
	}
	v := NewValidator(ctx, processID)
	// Version first, then content — as in the local pull: a commit landing
	// in between leaves an older base, so the next deploy sees the change.
	baseProc, baseErr := v.GetProcessByID(processID)
	data, proc, err := exportProcessJSON(v)
	if err != nil {
		return fmt.Sprintf("Error fetching process: %v", err), true
	}
	var b strings.Builder
	title, _ := proc["title"].(string)
	fmt.Fprintf(&b, "Process %d %q.\n", processID, title)
	if baseErr == nil {
		fmt.Fprintf(&b, "base: %s\n", hostedBaseToken(processID, baselineFromServer(baseProc)))
	} else {
		fmt.Fprintf(&b, "base: unavailable (%v) — a later deploy cannot check for concurrent changes.\n", baseErr)
	}
	b.WriteString("\n")
	b.Write(data)
	return b.String(), false
}

func hostedLintProcess(_ context.Context, args map[string]interface{}) (string, bool) {
	content, err := strArg(args, "content")
	if err != nil || strings.TrimSpace(content) == "" {
		return "Error: content (process JSON) is required", true
	}
	if len(content) > hostedMaxContentBytes {
		return fmt.Sprintf("Error: content is larger than %d bytes", hostedMaxContentBytes), true
	}
	result, err := lintProcessData([]byte(content))
	if err != nil {
		return fmt.Sprintf("Error: lint failed: %v", err), true
	}
	return FormatLintResult(result), false
}

func hostedCreateProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil || folderID <= 0 {
		return "Error: folder_id must be a positive integer", true
	}
	name, err := strArg(args, "process_name")
	if err != nil || strings.TrimSpace(name) == "" {
		return "Error: process_name is required", true
	}
	v := NewValidator(ctx, 0)
	processID, cerr := v.CreateEmptyConv(folderID, name, "", "process")
	if processID == 0 {
		return fmt.Sprintf("Error: failed to create process %q in folder #%d: %v", name, folderID, cerr), true
	}
	v.ProcessID = processID
	baseProc, baseErr := v.GetProcessByID(processID)
	data, _, err := exportProcessJSON(v)
	if err != nil {
		return fmt.Sprintf("Process %d created in folder #%d, but exporting it failed: %v", processID, folderID, err), true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Process %q created in folder #%d, ProcessID: %d.\n", name, folderID, processID)
	if baseErr == nil {
		fmt.Fprintf(&b, "base: %s\n", hostedBaseToken(processID, baselineFromServer(baseProc)))
	}
	b.WriteString("\n")
	b.Write(data)
	return b.String(), false
}

// hostedRunTask runs a task by process_id only; the local handler's
// process_path route reads the working directory.
func hostedRunTask(ctx context.Context, args map[string]interface{}) (string, bool) {
	if _, has := args["process_path"]; has {
		return "Error: process_path is not available on the hosted server; pass process_id", true
	}
	if id, err := intArg(args, "process_id"); err != nil || id <= 0 {
		return "Error: process_id must be a positive integer", true
	}
	return handleRunTask(ctx, args)
}

// hostedUnknownArgsError rejects arguments a hosted tool schema does not
// declare, like unknownArgsError does for the local schemas.
func hostedUnknownArgsError(tool string, args map[string]interface{}) string {
	def, ok := hostedToolDefs[tool]
	if !ok {
		return unknownArgsError(tool, args)
	}
	props, _ := def.InputSchema.(map[string]interface{})["properties"].(map[string]interface{})
	var unknown []string
	for k := range args {
		if _, ok := props[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	return fmt.Sprintf("Error: %s does not accept %s on the hosted server. Accepted: %s.", tool, strings.Join(unknown, ", "), strings.Join(sortedKeys(props), ", "))
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hostedPushProcess deploys a process sent as JSON, through the same gates as
// the local push (pushProcessCore) with a hosted source: no files, the base
// token as the concurrency baseline, no merge.
func hostedPushProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	content, err := strArg(args, "content")
	if err != nil || strings.TrimSpace(content) == "" {
		return "Error: content (process JSON) is required", true
	}
	if len(content) > hostedMaxContentBytes {
		return fmt.Sprintf("Error: content is larger than %d bytes", hostedMaxContentBytes), true
	}
	procID := extractObjIDFromJSON(content)
	if procID <= 0 {
		return "Error: content has no obj_id — create the process with create-process first and deploy the JSON it returns", true
	}
	var base *baselineEntry
	if tok := optStrArg(args, "base"); tok != "" {
		tokID, b, perr := parseHostedBaseToken(tok)
		if perr != nil {
			return "Error: " + perr.Error() + " — pass the base token exactly as pull-process returned it", true
		}
		if tokID != procID {
			return fmt.Sprintf("Error: base token is for process #%d, but the content is process #%d", tokID, procID), true
		}
		base = &b
	}
	v := NewValidator(ctx, procID)
	result, isErr := pushProcessCore(ctx, v, procID, content, args, hostedPushSource{base: base, content: content})
	if isErr && strings.Contains(result, "merge=true") {
		// The shared conflict report lists the local resolutions too.
		result += "\n\nOn the hosted server merge=true is not available: pull-process again, re-apply your edits to the JSON it returns, and push that with its new base — or pass overwrite_server_change=true to drop the server change."
	}
	return result, isErr
}
