package main

// processTools — Process, folder and alias tools (mcp_handlers_process.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var processTools = []mcpTool{
	{
		Name:        "pull-process",
		Description: "Export a single Corezoid process definitions to a JSON file. The file is saved to the folder path matching its location in Corezoid (resolved from parent_id).",
		Annotations: annDestructiveRemote,
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
		Description: "Recursively export all processes from a Corezoid folder/stage to a local directory.",
		Annotations: annDestructiveRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid folder(stage) ID to export",
				},
			},
			"required": []string{"folder_id"},
		},
	},
	{
		Name:        "push-process",
		Description: "Validate and deploy a process file to Corezoid. Runs lint-process first and blocks the deploy on issues that would break it (broken node links, old-format nodes, RPC paths without reply, nodes missing a default go, sub-30s timers, literal reply values); advisory findings are shown but do not block. Active Call Process Stub Mode (obj_type:4) is allowed as a warning only when the target stage is resolved as mutable and non-production-like; immutable/prod/unknown stages are blocked because Stub Mode bypasses the real called process. Pass allow_active_stub_mode=true only after explicit confirmation that the temporary mock behavior is intentional. Pass force=true to deploy despite other blocking lint issues. Note: the server regenerates node IDs on every push and the local file is rewritten in place with the server's canonical scheme — reference nodes by title when iterating, and re-read the file after a push instead of reusing old node IDs.",
		Annotations: annDestructiveRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the process JSON file, relative to the project root (absolute paths are accepted when they point inside the project).",
				},
				"force": map[string]interface{}{
					"type":        "boolean",
					"description": "Deploy even if the pre-push lint finds generic blocking issues. Does not confirm active Stub Mode; use allow_active_stub_mode for that. Advisory findings never block. Default false.",
				},
				"allow_active_stub_mode": map[string]interface{}{
					"type":        "boolean",
					"description": "Explicitly allow deploying active Call Process Stub Mode (obj_type:4) when the target stage is immutable, production-like, or cannot be resolved. Use only after confirming that temporary mock replies are intentionally being deployed.",
				},
			},
			"required": []string{"process_path"},
		},
	},
	{
		Name:        "lint-process",
		Description: "Validate process structure. Reports orphaned nodes, noop conditions, unused set_params, passthrough escalations, shared error clusters (an error node fed by several different failing nodes — each needs its own Reply/Error cluster), old-format nodes (obj_type:0 err_node_id targets, or action logic mixed with go_if_const — the UI would force-convert the process), finals reachable without api_rpc_reply in a process that replies elsewhere (an RPC caller would hang), nodes whose logics do not end with a default go and time semaphors under the 30s server minimum (both reject the deploy), literal non-string values in api_rpc_reply res_data (a scheme shape that hangs the server commit on push), and active Call Process Stub Mode nodes (obj_type:4) that bypass the real called process.",
		Annotations: annReadOnlyLocal,
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
		Name:        "run-task",
		Description: "Run a task on an already-deployed Corezoid process (without re-deploying) and wait for it to reach a final node. Polls up to wait_sec (default 30), so tasks that cross async nodes (api, api_rpc, db_call, delay) still return their final result. On timeout reports the node the task is parked at, plus TaskRef/TaskID for follow-up via list-task-history.",
		Annotations: annCreateRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the process JSON file.",
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
			"required": []string{"process_path", "data"},
		},
	},
	{
		Name:        "create-process",
		Description: "Create a new empty process (conv_type \"process\") inside a Corezoid folder.",
		Annotations: annCreateRemote,
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
		Annotations: annCreateRemote,
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
		Name:        "create-folder",
		Description: "Create a new folder inside a parent Corezoid folder.",
		Annotations: annCreateRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit Corezoid folder/stage ID to create the folder in; overrides parent_path resolution",
				},
				"parent_path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the parent folder directory. Omit to use the current directory.",
				},
				"folder_name": map[string]interface{}{
					"type":        "string",
					"description": "Name for the new folder",
				},
			},
			"required": []string{"folder_name"},
		},
	},
	{
		Name:        "show-folder",
		Description: "Show metadata for a single Corezoid folder: title, obj_type (0 normal, 2 project, 3 stage), parent folder ID and parent type.",
		Annotations: annReadOnlyRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid folder ID to show",
				},
			},
			"required": []string{"folder_id"},
		},
	},
	{
		Name:        "list-folders",
		Description: "List the immediate children of a Corezoid folder (subfolders + processes + state diagrams). Lighter than pull-folder — does not write anything to disk.",
		Annotations: annReadOnlyRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid folder ID whose children to list",
				},
			},
			"required": []string{"folder_id"},
		},
	},
	{
		Name:        "modify-folder",
		Description: "Rename a Corezoid folder and/or update its description. At least one of title or description must be supplied.",
		Annotations: annDestructiveRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid folder ID to modify",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "New folder title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New folder description",
				},
			},
			"required": []string{"folder_id"},
		},
	},
	{
		Name:        "delete-folder",
		Description: "Move a Corezoid folder to the recycle bin (Trash). Can be restored from the Corezoid UI.",
		Annotations: annDestructiveRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid folder ID to delete",
				},
			},
			"required": []string{"folder_id"},
		},
	},
	{
		Name:        "delete-process",
		Description: "Move a Corezoid process (or state diagram) to the recycle bin (Trash). Can be restored from the Corezoid UI. Use pull-process first if you want a local backup before deleting.",
		Annotations: annDestructiveRemote,
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
		Name:        "create-alias",
		Description: "Create a short alias for a Corezoid process. Aliases are stage-scoped; the stage is derived from the process file's parent_id (walking up folders until a stage is reached), so a stale COREZOID_STAGE_ID in .env no longer produces the cryptic \"Object is not in stage\" error. Pass stage_id explicitly to override.",
		Annotations: annCreateRemote,
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
				"stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Optional. Stage the alias should be created in. Defaults to the stage derived from the process file's parent_id, then to COREZOID_STAGE_ID from .env.",
				},
			},
			"required": []string{"process_path", "short_name"},
		},
	},
}
