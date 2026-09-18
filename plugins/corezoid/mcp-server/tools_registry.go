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
// process_path or process_id" contract shared by run-task and the snapshot
// tools.
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
		Description: "Recursively export all processes from a Corezoid folder/stage to a local directory. Pass folder_id=0 for \"No Project\" mode (workspace-root pull): downloads every top-level folder / process / dashboard in the workspace.",
		Annotations: toolHints(hintReadOnly, hintSafe, hintIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid folder(stage) ID to export. Use 0 to pull the entire workspace root (\"No Project\" mode).",
				},
			},
			"required": []string{"folder_id"},
		},
	},
	{
		Name:        "push-process",
		Description: "Validate and deploy a process file to Corezoid. Runs lint-process first; deploy-breaking findings block, advisory ones do not. Also blocks when the process changed on the server since pull, reporting local edits, server changes, true overlap and the last known author — resolve by re-pulling, by merge=true (writes a reviewable local 3-way merge plus a .pre-merge backup without deploying), or by overwrite_server_change=true after being shown that report. force=true is the generic-lint override ONLY: it never waives the concurrency gate, never confirms Stub Mode, and never bypasses structural lint findings (broken links, old-format nodes, self-referencing api_copy/api_rpc) — those describe an invalid graph the server rejects and must be fixed in the design. A pre-push snapshot is always attempted for existing processes; overwriting never-compared live state (overwrite_server_change or adopt_existing) without one, or when the snapshot call itself failed, is refused unless allow_no_snapshot=true AND the stage resolves as mutable and non-production-like — that combination is irreversible, so the flag is ignored on immutable, production-like or unresolvable stages, including installations whose API has no snapshot object at all. Never-deployed processes are exempt. A server-state fetch failing for anything but a genuine 'not found' also blocks, since the deploy is about to call that same API. Active Call Process Stub Mode (obj_type:4) is warning-only on a resolved mutable non-production-like stage; immutable/prod/unknown stages require allow_active_stub_mode=true after explicit confirmation. Every waived gate is reported in the push result, not only the server log. The server regenerates node IDs and rewrites the local file with the canonical scheme, so reference nodes by title and re-read the file after push.",
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
					"description": "Deploy despite generic blocking lint findings. LINT ONLY: does not overwrite a concurrent server change (use overwrite_server_change), confirm active Stub Mode (allow_active_stub_mode) or waive the snapshot requirement (allow_no_snapshot). Advisory findings never block. Does NOT bypass pre-deployment validation errors such as self-referencing api_copy/api_rpc nodes — fix those in the design. Default false.",
				},
				"overwrite_server_change": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy over a process that changed on the server since your pull, dropping those changes. Pass it only in reply to the block report describing what would be lost — never speculatively, never as a default: set ahead of time it authorises overwriting a concurrent change nobody has seen. Unlike force (which overrides lint findings) this overrides another person's edit. Refused when no pre-push snapshot exists unless allow_no_snapshot=true is also passed (never-deployed processes are exempt). Default false.",
				},
				"allow_active_stub_mode": map[string]interface{}{
					"type":        "boolean",
					"description": "Explicitly allow deploying active Call Process Stub Mode (obj_type:4) when the target stage is immutable, production-like, or cannot be resolved. Use only after confirming that temporary mock replies are intentionally being deployed.",
				},
				"merge": map[string]interface{}{
					"type":        "boolean",
					"description": "On a concurrent-change conflict, perform a 3-way merge: preserve the original as <process>.pre-merge and graft non-conflicting server node/process-field changes into the local file for review (does not deploy). Values changed differently on both sides are kept as yours and listed to resolve. Default false.",
				},
				"allow_no_snapshot": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy over an existing process although no pre-push snapshot could be taken — project_id/stage_id unresolvable, or the CreateSnapshot call itself failed — i.e. accept that the overwritten version cannot be restored. Honoured ONLY on a stage that resolves and is mutable; refused on immutable, production-like or unresolvable ones. Where force and overwrite_server_change override findings or a shown conflict, this waives the ability to undo; combining it with overwrite_server_change or adopt_existing is the deliberate way to make an irreversible overwrite. Prefer fixing the workspace config (corezoid-init) or retrying once the API recovers. Default false.",
				},
				"adopt_existing": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy a file that has no pull baseline over a process that already has a deployed version — overwriting server state without knowing what it contains. Use only when the local file is deliberately authoritative (an import or a restored copy); otherwise pull-process first so real conflicts surface. Not needed for never-deployed processes. Where force and overwrite_server_change resolve a conflict you were shown, this declares you do not know what is on the server. Refused when no pre-push snapshot exists unless allow_no_snapshot=true is also passed (a never-deployed process is exempt). Default false.",
				},
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
					"description": "Relative path to the process JSON file. Optional when the working directory contains exactly one .conv.json.",
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
		Description: "Validate process structure. Reports orphaned nodes, noop conditions, unused set_params, passthrough escalations, shared error clusters (an error node fed by several different failing nodes — each needs its own Reply/Error cluster), old-format nodes (obj_type:0 err_node_id targets, or action logic mixed with go_if_const — the UI would force-convert the process), finals reachable without api_rpc_reply in a process that replies elsewhere (an RPC caller would hang), nodes whose logics do not end with a default go and time semaphors under the 30s server minimum (both reject the deploy), literal non-string values in api_rpc_reply res_data (a scheme shape that hangs the server commit on push), active Call Process Stub Mode nodes (obj_type:4) that bypass the real called process, self-referencing api_copy/api_rpc nodes (valid in the Corezoid UI but always blocked by push-process — force=true does not bypass this), and git_call (api_git) usage (advisory: ~60s execution deadline, 50 MB/0.1 CPU shared defaults, ephemeral local storage — see the corezoid-gitcall skill).",
		Annotations: toolHints(hintReadOnly, hintSafe, hintIdempotent, hintLocal),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the process JSON file.",
				},
			},
			"required": []string{"process_path"},
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
		Description: "Run a task on an already-deployed Corezoid process (without re-deploying) and wait for it to reach a final node. Never commits or deploys, so it needs only run access and works on immutable stages; if the deployed node list is unreadable the task is still sent, just reported without node names. Identify the target with EXACTLY ONE of process_path (a local .conv.json) or process_id (the numeric ID, as in show-task/list-task-history) — process_id needs no local file, so it works in hosts with no process repository; passing both is rejected as ambiguous. Polls up to wait_sec (default 30), so tasks crossing async nodes (api, api_rpc, db_call, delay) still return their final result. On timeout reports the node the task is parked at, plus TaskRef/TaskID for follow-up via list-task-history.",
		Annotations: toolHints(hintMutates, hintSafe, hintNonIdempotent, hintOpenWorld),
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        []string{"string", "null"},
					"description": "Relative path to the process JSON file. Omit and pass process_id instead when there is no local process repository (e.g. a host console with no filesystem). Mutually exclusive with process_id.",
				},
				"process_id": map[string]interface{}{
					"type":        []string{"integer", "null"},
					"description": "Corezoid process (conv) ID, > 0. Alternative to process_path — use this to run a task without a local .conv.json file, without a preceding pull-process. Mutually exclusive with process_path.",
				},
				"data": map[string]interface{}{
					"type":        "string",
					"description": "JSON string with task input parameters",
				},
				"ref": map[string]interface{}{
					"type":        "string",
					"description": "Optional custom task ref (lookup key, not a guaranteed idempotency key — duplicate-ref behavior depends on the target process/state-diagram). Use this to create a task with a specific, lookup-able ref — e.g. matching an external ID a downstream process keys off of. If omitted, an auto-generated ref (\"<unix_ts>_<rand>\") is used, same as before.",
				},
				"wait_sec": map[string]interface{}{
					"type":        "integer",
					"description": "How long to wait (seconds) for the task to reach a final node before reporting it as in progress. Default 30, max 600. Raise it for processes with slow external calls or delay nodes.",
				},
			},
			"required": []string{"data"},
			"anyOf":    processTargetAnyOf(),
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
		Description: "Authenticate with Corezoid. Supports two auth methods: (1) OAuth2 browser flow — opens a browser window and saves the token so it persists across sessions; (2) API key — provide api_login and api_secret to skip the browser flow. All credentials are saved per-folder to ~/.corezoid/config.json (file mode 0600, keyed by the working directory). Optionally accepts account_url, workspace_id, and stage_id to skip interactive prompts.",
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
		Description: "Remove saved Corezoid credentials from disk.",
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
// It is NOT the set of callable tools — the 54 definitions behind the routers
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
