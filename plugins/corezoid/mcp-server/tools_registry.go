package main

// Safety-hint constants. They exist so the 65 Annotations lines below read
// as prose instead of as four anonymous booleans. The argument order of
// toolHints is fixed: (readOnly, destructive, idempotent, openWorld).
//
// The taxonomy, applied uniformly across the registry:
//
//   - readOnly: the tool changes no Corezoid state. pull-* counts as
//     read-only even though it writes an export to disk — the file is a
//     mirror of server state, not a mutation of it.
//   - destructive: the tool can delete state, or overwrite it in a way the
//     caller cannot trivially undo (deploys, pushes, revoking access,
//     changing a variable's name or value). Additive or metadata-only
//     changes are not destructive.
//   - idempotent: repeating the call with the same arguments leaves the same
//     end state. Anything marked destructive is deliberately reported as
//     non-idempotent — retrying a destructive call is never free.
//   - openWorld: the tool talks to the Corezoid API or the remote git
//     mirror. Tools that only touch local workspace files are closed-world.
//
// tools_registry_annotations_test.go enforces these invariants.
const (
	hintReadOnly = true  // changes no Corezoid state
	hintMutates  = false // changes state

	hintDestructive = true  // can delete or irreversibly overwrite state
	hintSafe        = false // additive or metadata-only changes

	hintIdempotent    = true  // repeating the call leaves the same end state
	hintNonIdempotent = false // repeating the call accumulates or re-fires

	hintOpenWorld = true  // talks to the Corezoid API or the git mirror
	hintLocal     = false // touches only local files
)

// toolHints builds the annotations object for one tool entry.
func toolHints(readOnly, destructive, idempotent, openWorld bool) *toolAnnotations {
	return &toolAnnotations{
		ReadOnlyHint:    boolPtr(readOnly),
		DestructiveHint: boolPtr(destructive),
		IdempotentHint:  boolPtr(idempotent),
		OpenWorldHint:   boolPtr(openWorld),
	}
}

// processTargetAnyOf advertises the "identify the process by EXACTLY ONE of
// process_path or process_id" contract shared by the router-fronted snapshot
// tools (create-, list-, get- and delete-snapshot).
//
// It must never go on a tool in tools/list. The Anthropic Messages API
// rejects an input_schema with oneOf/anyOf/allOf at the top level ("tools.N.
// custom.input_schema: input_schema does not support oneOf, allOf, or anyOf
// at the top level"), and it rejects the whole request, so one such schema
// fails every message in the session. run-task is advertised, so it states
// the contract in its description and leaves enforcement to
// resolveProcessID; router-fronted schemas are only served as help text.
// TestToolsList_NoTopLevelSchemaCombinators enforces the split.
//
// It is anyOf, not oneOf, on purpose. JSON Schema `required` is satisfied by
// the mere PRESENCE of a key, whatever its value, while the runtime check in
// resolveProcessID treats "" and null as absent — deliberately, because some
// MCP hosts serialize a declared-but-unset optional field instead of omitting
// it (see TestResolveProcessID_EmptyOrNullProcessPathIsNotAConflict). Under
// oneOf such a client sends {process_path: "", process_id: N}, satisfies BOTH
// branches, and a host that pre-validates arguments against the advertised
// schema rejects the call before the server ever sees it — breaking exactly
// the no-local-repository hosts process_id was added for. anyOf still rejects
// a call that names neither target, and the genuine both-given conflict is
// caught by resolveProcessID, which can say which two arguments disagreed
// instead of emitting a schema error. Same shape show-task already uses for
// its task_id/ref pair.
//
// For the same reason the two properties are typed ["string", "null"] and
// ["integer", "null"] at each call site: a host that fills an unset optional
// with an explicit null would otherwise fail client-side validation on the
// property TYPE even with anyOf in place, and the runtime accepts that form
// too (TestResolveProcessID_NullProcessIDFallsBackToProcessPath). A null is
// "not supplied", never a target — resolveProcessID still rejects a call that
// nulls both.
func processTargetAnyOf() []map[string]interface{} {
	return []map[string]interface{}{
		{"required": []string{"process_path"}},
		{"required": []string{"process_id"}},
	}
}

// coreToolDefs are the tools advertised individually in tools/list — the ones
// whose descriptions carry the rules a model has to read BEFORE it calls
// them, and which are called often enough that a flat call shape is worth its
// bytes. Everything CRUD-shaped lives in collapsedToolRegistry behind a
// domain router instead (see tools_router.go).
var coreToolDefs = []mcpTool{
	{
		Name:        "pull-process",
		Description: "Export a single Corezoid process definitions to a JSON file. The file is saved to the folder path matching its location in Corezoid (resolved from parent_id).",
		Annotations: toolHints(hintReadOnly, hintSafe, hintIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid process ID to export",
				},
			},
			"required": []string{"process_id"},
		},
	},
	{
		Name:        "pull-folder",
		Description: "Recursively export all processes from a Corezoid folder/stage to a local directory. Pass folder_id=0 for \"No Project\" mode (workspace-root pull): downloads every top-level folder / process / dashboard in the workspace. Omit folder_id to re-pull the stage selected at login.",
		Annotations: toolHints(hintReadOnly, hintSafe, hintIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid folder(stage) ID to export. Omit to pull the stage selected at login (an error if none is selected). Use 0 to pull the entire workspace root (\"No Project\" mode).",
				},
			},
		},
	},
	{
		Name:        "push-process",
		Description: "Validate (lint-process) and deploy a process file to Corezoid. Gates, each reported when it blocks or is waived: (1) Lint: deploy-breaking findings block, advisory ones do not; force=true overrides generic findings only, never structural ones (broken links, old-format nodes, self-referencing api_copy/api_rpc), which must be fixed in the design. (2) Server change since pull: the report lists local edits, server changes, overlap and the last author; re-pull, use merge=true, or pass overwrite_server_change=true after that report was shown. If the server state cannot be read, or the process was deleted since the pull, the push blocks and no flag waives it: retry or re-pull. (3) Snapshot: one is attempted before an existing process is overwritten (installations without snapshots skip it, and the result says so). If it fails, or overwrite_server_change/adopt_existing would overwrite live state without one, the push needs allow_no_snapshot=true, honoured only on a resolved mutable stage that is not production-like; a process in another stage or workspace blocks regardless. Never-deployed processes are exempt. (4) Active Call Process Stub Mode (obj_type:4): a warning on a resolved mutable stage that is not production-like; immutable, production-like or unresolved stages need allow_active_stub_mode=true after explicit confirmation. The root title and description deploy too, so a stale file renames the process. The server regenerates node IDs and rewrites the local file: reference nodes by title and re-read the file after push.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the process JSON file, relative to the project root (absolute paths are accepted when they point inside the project).",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Process JSON to write before validating and deploying, for hosts whose agent cannot write files. Without process_path it targets the pulled file for its obj_id. Omit to deploy the file on disk.",
				},
				"force": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy despite generic blocking lint findings. LINT ONLY: it does not override a server change (overwrite_server_change), confirm Stub Mode (allow_active_stub_mode), waive the snapshot (allow_no_snapshot), or bypass structural findings. Default false.",
				},
				"overwrite_server_change": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy over a server change made since your pull, dropping it. It overrides another person's edit: pass it only in reply to the block report that showed what would be lost, never ahead of time or by default. Without a pre-push snapshot it also needs allow_no_snapshot=true (never-deployed processes are exempt). Default false.",
				},
				"allow_active_stub_mode": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy active Call Process Stub Mode (obj_type:4) to an immutable, production-like or unresolved stage. Pass it only after the user confirmed that these temporary mock replies are meant to be deployed.",
				},
				"merge": map[string]interface{}{
					"type":        "boolean",
					"description": "On a server-change block, write a 3-way merge into the local file for review instead of deploying: the original is kept as <process>.pre-merge, non-conflicting server node and process-field changes are grafted in, and values changed on both sides stay yours and are listed. Default false.",
				},
				"allow_no_snapshot": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy over an existing process although no pre-push snapshot could be taken (project/stage unresolved, or the snapshot call failed): the overwritten version cannot be restored. Ignored unless the stage resolves as mutable and not production-like. Prefer fixing the workspace config (corezoid-init) or retrying once the API recovers. Default false.",
				},
				"adopt_existing": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy a file with no pull baseline over a process that already has a deployed version, overwriting server state you have not seen. Only when the local file is deliberately authoritative (an import, a restored copy); otherwise pull-process first so real conflicts surface. Not needed for never-deployed processes. Without a pre-push snapshot it also needs allow_no_snapshot=true. Default false.",
				},
			},
			"required": []string{"process_path"},
		},
	},
	{
		Name:        "patch-process",
		Description: "Change a few nodes of a pulled process file in place, by node id or title, instead of rewriting the file or sending the whole JSON as push-process content: ops set/add/remove and process keys. A logic or semaphore left pointing at a missing node is refused. Writes only the local file (no API) and answers the changed nodes; run push-process on the file to deploy.",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintLocal),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the pulled process JSON file, relative to the project root.",
				},
				"ops":     patchOpsSchema,
				"process": patchProcessSchema,
			},
			"required": []string{"process_path"},
		},
	},
	{
		Name:        "layout-process",
		Description: "Auto-arrange a process's node coordinates into a clean, readable layout (waterfall for simple trees, layered+error-rail for meshes, aligned table/star grids for region bundles). Rewrites ONLY x/y; collapse/expand state, extra, edges, logic, conv_id and aliases stay intact. Runs entirely on the local file (no API, no auth). The result always reports the chosen strategy, canvas size and overlap count; dry=true previews placements without writing.",
		Annotations: toolHints(hintMutates, hintSafe, hintIdempotent, hintLocal),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the process .conv.json file, relative to the project root (the server's working directory) or absolute inside it. Optional when the working directory contains exactly one .conv.json.",
				},
				"density": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"compact", "medium", "roomy"},
					"description": "Spacing mode: compact | medium (default) | roomy (keeps the coarse block rhythm, skips compaction).",
				},
				"dry": map[string]interface{}{
					"type":        "boolean",
					"description": "Preview the planned coordinates without modifying the file.",
				},
			},
		},
	},
	{
		Name:        "lint-process",
		Description: "Check a local process file without calling the server (pull-process first; pass process_path or process_id). Findings that block push-process include an invalid or old-format graph, schema errors, logics without a default go, semaphors under 30 s, finals that skip api_rpc_reply while other paths reply, literal non-string values in api_rpc_reply res_data/extra, incomplete params or Call Process extra/extra_type, self-referencing api_copy/api_rpc, and active Stub Mode on production-like stages. Advisory: orphaned, no-op or passthrough nodes, unused set_params, shared error clusters, git_call limits (see the corezoid-gitcall skill). Each finding explains itself; push-process runs this first.",
		Annotations: toolHints(hintReadOnly, hintSafe, hintIdempotent, hintLocal),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the local .conv.json, relative to the project root (not to a previously opened file) or absolute inside it. Give this or process_id.",
				},
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Process ID: lints the pulled copy <ID>_<title>.conv.json in the workspace. Never fetches from the server.",
				},
			},
		},
	},
	{
		Name:        "clean-process",
		Description: "Remove nodes with no traffic in the last N days (default 90) from a Corezoid process, saving a reviewable proposal as <ID>_<title>.cleaned.json — NOT a .conv.json, so it never collides with the pulled process; pass that path explicitly to lint-process/push-process to deploy it. Never deploys. Structurally required inactive nodes are kept (escalation chains, unconditional-go and set_param targets), references to removed nodes are redirected to their go-successor, and only delay→final nodes this cleanup rewired are dropped — hand-authored delays stay. Refuses to write if no node shows traffic, if the start node would be lost, or if the result fails validation. Reports counts per step plus any outgoing branch that could not be redirected.",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Numeric ID of the process to clean.",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Look-back period in days for node activity statistics. Nodes with no traffic in this window are considered inactive. Default 90.",
				},
				"overwrite": map[string]interface{}{
					"type":        "boolean",
					"description": "Allow overwriting an existing <ID>_<title>.cleaned.json file. Default false — the tool refuses to overwrite to protect manual edits made to a previously cleaned file.",
				},
			},
			"required": []string{"process_id"},
		},
	},
	{
		Name:        "run-task",
		Description: "Run a task on a deployed process and wait for it to reach a final node. Never deploys, so run access is enough and immutable stages work. Pass exactly one of process_path or process_id. Waits up to wait_sec, also across async nodes (api, api_rpc, db_call, delay). At the deadline the result is still a success: Status in progress, the current node (flagged as slow or stuck unless it is a state, callback, delay or storage node) and TaskRef/TaskID for list-task-history; if the task was never found by then, check list-task-history before re-running. Each data value must have the type of the process param it fills (the \"params\" list of the .conv.json); with process_path, obvious mismatches are converted and reported, and a rejected task lists the expected types. Data over 4 KB is summarised by size and keys: read it with show-task instead of re-running. If the deployed node list cannot be read, the task still runs, the wait is capped at 5 s and the reported data may not be final.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld), // runs the process: its side effects are arbitrary
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        []string{"string", "null"},
					"description": "Relative path to the process JSON file. Omit it and pass process_id when there is no local file.",
				},
				"process_id": map[string]interface{}{
					"type":        []string{"integer", "null"},
					"description": "Process (conv) ID, > 0, as in show-task/list-task-history. Needs no local file and no pull-process.",
				},
				"data": map[string]interface{}{
					"type":        "string",
					"description": "Task input as a JSON object string; each value must have the JSON type its process input parameter declares, e.g. {\"amount\": 21, \"active\": true, \"tags\": [\"a\"], \"name\": \"Ann\"} — not {\"amount\": \"21\", \"tags\": \"[\\\"a\\\"]\"}.",
				},
				"ref": map[string]interface{}{
					"type":        "string",
					"description": "Optional task ref: a lookup key, such as an external ID a downstream process keys off. Not an idempotency key: duplicate refs behave as the target process decides. Default: auto-generated.",
				},
				"wait_sec": map[string]interface{}{
					"type":        "integer",
					"description": "Seconds to wait for a final node. Default 30, max 600; raise it for slow external calls or delays. 0 = create the task and return its ref without waiting (state diagrams, long async flows).",
				},
				"mode":             runTaskModeSchema,
				"fields":           runTaskFieldsSchema,
				"max_value_length": runTaskMaxValueSchema,
			},
			"required": []string{"data"},
		},
	},
	{
		Name:        "create-process",
		Description: "Create a new empty process (conv_type \"process\") inside a Corezoid folder.",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit Corezoid folder/stage ID to create in; overrides folder_path resolution",
				},
				"folder_path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the folder directory. Omit to use the current directory.",
				},
				"process_name": map[string]interface{}{
					"type":        "string",
					"description": "Name for the new process",
				},
			},
			"required": []string{"process_name"},
		},
	},
	{
		Name:        "create-state-diagram",
		Description: "Create a new empty state diagram (conv_type \"state\") inside a Corezoid folder. Use this for status / lifecycle storage instead of create-process.",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit Corezoid folder/stage ID to create in; overrides folder_path resolution",
				},
				"folder_path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the folder directory. Omit to use the current directory.",
				},
				"process_name": map[string]interface{}{
					"type":        "string",
					"description": "Name for the new state diagram",
				},
			},
			"required": []string{"process_name"},
		},
	},
	{
		Name:        "delete-process",
		Description: "Move a Corezoid process (or state diagram) to the recycle bin (Trash). Can be restored from the Corezoid UI. Use pull-process first if you want a local backup before deleting.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid process ID to delete",
				},
			},
			"required": []string{"process_id"},
		},
	},
	{
		Name:        "pause-process",
		Description: "Pause one Corezoid process without changing or deploying its graph. CONSEQUENTIAL: Corezoid rejects NEW task creation for a paused process with conveyor_is_not_active — that is admission control, not proof that tasks already running or parked in nodes have stopped; inspect those separately. EXPLICIT-INTENT ONLY: never invoke because a process looks unused, during a review/refactor, or as an inferred safety step — only when the user asks to pause this exact process. SAFETY: apply=false (default) reads live status and returns a dry-run; after showing it and receiving explicit approval, call apply=true with confirm=\"process#<id>:<live_status>->paused\". The live status is re-read on every invocation and the result is post-verified.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "Exact Corezoid process/state-diagram ID to pause.",
				},
				"apply": map[string]interface{}{
					"type":        "boolean",
					"description": "false (default) = read live state and preview only. true = pause (also requires the exact confirm token from a fresh dry-run).",
				},
				"confirm": map[string]interface{}{
					"type":        "string",
					"description": "Required when apply=true. Exact form: process#<id>:<current_status>->paused, for example process#123:active->paused.",
				},
			},
			"required": []string{"process_id"},
		},
	},
	{
		Name:        "resume-process",
		Description: "Resume (activate) one paused/debug Corezoid process without changing or deploying its graph. CONSEQUENTIAL: after activation, API clients, schedules, callbacks and other processes may create new tasks immediately. EXPLICIT-INTENT ONLY: never resume automatically after edits, tests, a review or a previous pause — only when the user asks to resume this exact process and accepts incoming traffic. SAFETY: apply=false (default) reads live status and returns a dry-run; after showing it and receiving explicit approval, call apply=true with confirm=\"process#<id>:<live_status>->active\". The live status is re-read on every invocation and the result is post-verified.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "Exact Corezoid process/state-diagram ID to resume.",
				},
				"apply": map[string]interface{}{
					"type":        "boolean",
					"description": "false (default) = read live state and preview only. true = activate (also requires the exact confirm token from a fresh dry-run).",
				},
				"confirm": map[string]interface{}{
					"type":        "string",
					"description": "Required when apply=true. Exact form: process#<id>:<current_status>->active, for example process#123:paused->active.",
				},
			},
			"required": []string{"process_id"},
		},
	},
	{
		Name:        "create-alias",
		Description: "Create a short alias for a Corezoid process. Aliases are stage-scoped; the stage is derived from the process file's parent_id (walking up folders until a stage is reached), so a stale marker no longer produces the cryptic \"Object is not in stage\" error. LLM does not need to supply a stage.",
		Annotations: toolHints(hintMutates, hintSafe, hintIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the process JSON file.",
				},
				"short_name": map[string]interface{}{
					"type":        "string",
					"description": "Short alias name for the process",
				},
			},
			"required": []string{"process_path", "short_name"},
		},
	},
	{
		Name:        "deploy-stage",
		Description: "Deploy (promote) one stage's processes onto another within a Corezoid project — e.g. develop → production. Wraps the admin obj_scheme compare+merge behind the UI's \"Deploy\" button (/api/2/compare, /api/2/merge). DESTRUCTIVE, and irreversible on an immutable target. SAFETY: apply=false (default) is a dry-run showing only the diff and any conflicts — nothing is deployed. To deploy you MUST first get the user's explicit confirmation of the exact source→target, then call with apply=true AND confirm=\"<source_stage_id>-><target_stage_id>\". Never deploy without the user confirming. The merge is asynchronous; this tool waits for it to finish over the progress WebSocket.",
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{
					"type":        "integer",
					"description": "Project ID that both stages belong to.",
				},
				"source_stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Stage to deploy FROM (the source of truth, e.g. develop).",
				},
				"target_stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Stage to deploy INTO (e.g. production). Its scheme is overwritten with the source's.",
				},
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID the project belongs to.",
				},
				"apply": map[string]interface{}{
					"type":        "boolean",
					"description": "false (default) = dry-run: show the diff/conflicts only. true = perform the deploy (also requires a matching confirm).",
				},
				"confirm": map[string]interface{}{
					"type":        "string",
					"description": "Required when apply=true: must equal \"<source_stage_id>-><target_stage_id>\" (e.g. \"684083->684082\"). Guards against accidental and wrong-stage deploys.",
				},
			},
			"required": []string{"project_id", "source_stage_id", "target_stage_id", "company_id"},
		},
	},
	{
		Name:        "login",
		Description: "Authenticate with Corezoid. Supports two auth methods: (1) OAuth2 browser flow — opens a browser window and saves the token so it persists across sessions; (2) API key — provide api_login and api_secret to skip the browser flow. All credentials are saved per-folder to ~/.corezoid/config.json (file mode 0600, keyed by the working directory). Optionally accepts account_url, workspace_id, and stage_id to skip interactive prompts. Every parameter is optional: for a normal setup call it with no arguments ({}).",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"account_url": map[string]interface{}{
					"type":        "string",
					"description": "Account API URL, e.g. https://account.corezoid.com",
				},
				"workspace_id": map[string]interface{}{
					"type":        "string",
					"description": "Corezoid workspace (company) ID",
				},
				"stage_id": map[string]interface{}{
					"type":        "string",
					"description": "Corezoid stage (root folder) ID. Pass it only when the user dictates a stage ID or asks to switch stages — otherwise omit it and let the interactive picker choose. The chosen stage lands on disk as the <id>_<name>.stage.json marker, which every other MCP tool reads.",
				},
				"api_login": map[string]interface{}{
					"type":        "string",
					"description": "API key login (alternative to the OAuth2 browser flow). Providing both api_login and api_secret skips browser authentication.",
				},
				"api_secret": map[string]interface{}{
					"type":        "string",
					"description": "API key secret (alternative to OAuth2 browser flow). Must be paired with api_login. Stored per-folder in ~/.corezoid/config.json (file mode 0600).",
				},
			},
		},
	},
	{
		Name:        "create-communications-orchestrator",
		Description: "Create a Communications Orchestrator: a multi-platform robot handling Telegram, Facebook Messenger, Viber and Apple Messages for Business. Corezoid builds one folder of processes per channel asynchronously; this tool queues the build and polls it (every 3s, up to 10 checks), returning the generated folder_url or the wizard's error. At least one messenger is required. NO UNDO (~150 processes; a rebuild on a live channel token steals that bot's webhook): apply=false (default) returns a dry-run carrying the confirm token needed to build.",
		// destructiveHint, even though the build only ADDS objects: the flag is
		// what an MCP host reads to decide whether to ask the user first, and
		// this call earns the prompt twice over: there is no undo for the ~150
		// processes it creates, and a second build against a channel token that
		// already serves a bot silently steals that bot's webhook — destroying
		// a working integration without deleting a single object.
		//
		// The annotation is advice to the host, not a check, so the handler also
		// demands the apply/confirm handshake the other irreversible tools use.
		// The corezoid-gen-bot skill has its own confirmation step, but the tool
		// is callable without the skill, and that path previously had no
		// server-side gate at all.
		Annotations: toolHints(hintMutates, hintDestructive, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"messengers": map[string]interface{}{
					"type": "string",
					"description": "JSON array of channel objects, at least one. One entry per channel with its own credential field: " +
						`telegram {"channel":"telegram","key":"<bot token>"}, ` +
						`viber {"channel":"viber","viber_token":"<token>"}, ` +
						`fbmessenger {"channel":"fbmessenger","page_access_token":"<token>"}, ` +
						`abc {"channel":"abc","abc_token":"<token>","user_id":68381,"email":"me@example.com","name":"My Name"} — abc = Apple Messages for Business; user_id/email/name are an optional brand contact.`,
				},
				"stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Optional. Stage/folder ID to build in. Defaults to the current stage (from the <id>_<name>.stage.json marker).",
				},
				"project_id": map[string]interface{}{
					"type":        "integer",
					"description": "Optional. Project ID owning stage_id. Resolved from the stage when omitted.",
				},
				"lang": map[string]interface{}{
					"type":        "string",
					"description": "Optional. Language of generated processes and bot replies (e.g. \"en\", \"uk\", \"ru\"). Default \"en\".",
				},
				"apply": map[string]interface{}{
					"type":        "boolean",
					"description": "false (default) = preview only, nothing is created. true = build (also requires confirm).",
				},
				"confirm": map[string]interface{}{
					"type":        "string",
					"description": "Required when apply=true. Copy the exact token printed by the apply=false dry-run.",
				},
			},
			"required": []string{"messengers"},
		},
	},
	{
		Name:        "logout",
		Description: "Log out: delete the entry for the current directory in ~/.corezoid/config.json (token or API key, workspace, stage, git settings). Entries of other directories and the .stage.json file stay. Tell the user the result in their language, including the browser SSO warning. To reconnect, call login.",
		Annotations: toolHints(hintMutates, hintSafe, hintIdempotent, hintLocal),
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "send-feedback",
		Description: "Submit user feedback about plugin behavior to Corezoid. Use only after the user has explicitly confirmed sending. Returns a feedback ticket id.",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"problem": map[string]interface{}{
					"type":        "string",
					"description": "What went wrong, in the user's words.",
				},
				"expected": map[string]interface{}{
					"type":        "string",
					"description": "What the user expected to happen.",
				},
				"proposed_solution": map[string]interface{}{
					"type":        "string",
					"description": "How the user thinks it should work.",
				},
				"tool": map[string]interface{}{
					"type":        "string",
					"description": "Tool or skill involved, if known.",
				},
				"transcript_excerpt": map[string]interface{}{
					"type":        "string",
					"description": "Short, already-redacted excerpt of the relevant dialog.",
				},
				"contact": map[string]interface{}{
					"type":        "string",
					"description": "Optional contact for follow-up.",
				},
			},
			"required": []string{"problem"},
		},
	},
}

// toolRegistry is what tools/list returns: the core tools plus one entry per
// domain router. mcp_server.go and mcp_http.go marshal this slice directly.
//
// It is NOT the set of callable tools — the 56 definitions behind the routers
// are callable too, by action name (and, unchanged, from the CLI and any
// direct client). Use allToolDefs when you need every definition; use
// toolHandlers when you need every callable name.
var toolRegistry = buildToolRegistry()

// collapsedToolByName indexes the router-fronted definitions by tool name.
// Declared as a var initializer rather than filled in init() on purpose:
// buildToolRegistry runs during package-variable initialization (routers
// aggregate their annotations from these entries), which happens before any
// init() body would have run.
var collapsedToolByName = indexToolDefs(collapsedToolRegistry)

func buildToolRegistry() []mcpTool {
	defs := make([]mcpTool, 0, len(coreToolDefs)+len(toolRouters))
	defs = append(defs, coreToolDefs...)
	defs = append(defs, routerToolDefs()...)
	return defs
}

// allToolDefs returns every tool definition — advertised and router-fronted.
// This is the set the argument validator, the README sync check and the
// annotation taxonomy test work against: collapsing a tool moved its call
// shape, not its contract.
func allToolDefs() []mcpTool {
	defs := make([]mcpTool, 0, len(coreToolDefs)+len(collapsedToolRegistry))
	defs = append(defs, coreToolDefs...)
	defs = append(defs, collapsedToolRegistry...)
	return defs
}

func indexToolDefs(defs []mcpTool) map[string]mcpTool {
	byName := make(map[string]mcpTool, len(defs))
	for _, d := range defs {
		byName[d.Name] = d
	}
	return byName
}
