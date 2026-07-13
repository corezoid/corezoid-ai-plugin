package main

// toolRegistry is the single source of truth for all MCP tool definitions.
// mcp_server.go returns this slice for "tools/list", and tests verify
// the README tools table stays in sync with it.
var toolRegistry = []mcpTool{
	{
		Name:        "pull-process",
		Description: "Export a single Corezoid process definitions to a JSON file. The file is saved to the folder path matching its location in Corezoid (resolved from parent_id).",
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
		Name:        "create-variable",
		Description: "Create an environment variable in a Corezoid folder.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"stage_id": map[string]interface{}{
					"type":        "string",
					"description": "Root folder ID where the variable will be created",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Variable name",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Variable description",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "Variable value",
				},
			},
			"required": []string{"stage_id", "name", "description", "value"},
		},
	},
	{
		Name:        "list-variables",
		Description: "List all environment variables (env_var) in a Corezoid stage: short_name, obj_id, data_type (raw/json), env_var_type (visible/secret), title, value and change time. Read-only. Secret variables are ALWAYS shown masked — their value is never retrievable after creation, only fingerprints. Returns the obj_id needed by modify-variable / delete-variable.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Stage (root folder) ID to list variables from. Defaults to COREZOID_STAGE_ID from .env.",
				},
			},
			"required": []string{},
		},
	},
	{
		Name:        "modify-variable",
		Description: "Modify a Corezoid environment variable: change its value, description (display title), data_type (raw/json), and/or rename it (new_name). CONSEQUENTIAL: renaming breaks every {{env_var[@old-name]}} reference in the stage's processes, and a value change takes effect immediately in running processes without redeploy. env_var_type (visible/secret) CANNOT be changed after creation — the server silently ignores such changes. Modify is partial: omitted fields keep their current value (a secret's value survives a modify that does not send value). SAFETY: apply=false (default) is a dry-run showing the current → new diff and, for renames, a local reference scan — nothing is changed. To apply you MUST show the diff to the user, get their explicit confirmation, then call with apply=true AND confirm=\"<short_name>#<obj_id>\" (the CURRENT short_name, before any rename). Never modify a variable without the user confirming.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Current short_name of the variable (as used in {{env_var[@name]}}).",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Optional numeric variable ID (from list-variables). If given together with name, both must refer to the same variable.",
				},
				"stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Stage the variable lives in. Defaults to COREZOID_STAGE_ID from .env.",
				},
				"new_name": map[string]interface{}{
					"type":        "string",
					"description": "New short_name (rename). WARNING: breaks all {{env_var[@old-name]}} references — the dry-run reports affected local .conv.json files.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New human-readable label (stored as the variable's title, same as create-variable). An empty string is ignored — titles cannot be cleared.",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "New value. For data_type=json, a JSON-encoded string. Omit to keep the current value (secrets survive).",
				},
				"data_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"raw", "json"},
					"description": "New data type (raw or json).",
				},
				"apply": map[string]interface{}{
					"type":        "boolean",
					"description": "false (default) = dry-run: show the current → new diff only. true = perform the modification (also requires a matching confirm).",
				},
				"confirm": map[string]interface{}{
					"type":        "string",
					"description": "Required when apply=true: must equal \"<short_name>#<obj_id>\" of the CURRENT variable (e.g. \"payment-api-url#2192\"). Guards against accidental and wrong-variable modifications.",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		Name:        "delete-variable",
		Description: "PERMANENTLY delete a Corezoid environment variable. DESTRUCTIVE AND IRREVERSIBLE: unlike processes/folders/projects there is NO recycle bin for variables — the value (secrets included) is gone immediately, and any process still referencing {{env_var[@name]}} will fail at runtime. SAFETY: apply=false (default) is a dry-run that shows the variable's full details plus a local reference scan — nothing is deleted. To delete you MUST show the user the dry-run warning block VERBATIM, get their explicit confirmation, then call with apply=true AND confirm=\"<short_name>#<obj_id>\". Never delete a variable without the user confirming.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "short_name of the variable to delete.",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Optional numeric variable ID (from list-variables). If given together with name, both must match.",
				},
				"stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Stage the variable lives in. Defaults to COREZOID_STAGE_ID from .env.",
				},
				"apply": map[string]interface{}{
					"type":        "boolean",
					"description": "false (default) = dry-run preview only. true = perform the permanent deletion (also requires a matching confirm).",
				},
				"confirm": map[string]interface{}{
					"type":        "string",
					"description": "Required when apply=true: must equal \"<short_name>#<obj_id>\" (e.g. \"stripe-key#2192\"). Guards against accidental and wrong-variable deletion.",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		Name:        "push-process",
		Description: "Validate and deploy a process file to Corezoid. Note: the server regenerates node IDs on every push and the local file is rewritten in place with the server's canonical scheme — reference nodes by title when iterating, and re-read the file after a push instead of reusing old node IDs.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the process JSON file, relative to the project root (absolute paths are accepted when they point inside the project).",
				},
			},
			"required": []string{"process_path"},
		},
	},
	{
		Name:        "layout-process",
		Description: "Auto-arrange a process's node coordinates into a clean, readable layout (waterfall for simple trees, layered+error-rail for meshes, aligned table/star grids for region bundles). Rewrites ONLY x/y and the extra.modeForm collapse flag in the file — edges, logic, conv_id and aliases stay intact, so a re-layout can never alter behaviour. Runs entirely on the local file (no API, no auth). The result always reports the chosen strategy, canvas size and overlap count; dry=true previews placements without writing.",
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
		Description: "Validate process structure. Reports orphaned nodes, noop conditions, unused set_params, passthrough escalations, and literal non-string values in api_rpc_reply res_data (a scheme shape that hangs the server commit on push).",
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
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
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
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
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
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
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
		Description: "Create a short alias for a Corezoid process.",
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
		Name:        "list-workspaces",
		Description: "Return the list of Corezoid workspaces (companies) available to the authenticated user.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "list-projects",
		Description: "Return the list of projects inside a Corezoid workspace (company), sorted by title.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID whose projects to list",
				},
			},
			"required": []string{"company_id"},
		},
	},
	{
		Name:        "list-stages",
		Description: "Return the list of stages (environments) inside a Corezoid project.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_id": map[string]interface{}{
					"type":        "integer",
					"description": "Project ID whose stages to list",
				},
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID the project belongs to",
				},
			},
			"required": []string{"project_id", "company_id"},
		},
	},
	{
		Name:        "deploy-stage",
		Description: "Deploy (promote) one stage's processes onto another within a Corezoid project — e.g. develop → production. Wraps the admin obj_scheme compare+merge that the UI \"Deploy\" button issues (on /api/2/compare and /api/2/merge). DESTRUCTIVE, and irreversible on an immutable target. SAFETY: apply=false (default) is a dry-run that only shows the diff and any conflicts — nothing is deployed. To actually deploy you MUST first get the user's explicit confirmation of the exact source→target, then call with apply=true AND confirm=\"<source_stage_id>-><target_stage_id>\". Never deploy without the user confirming. The merge is asynchronous; this tool waits for it to finish over the progress WebSocket.",
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
		Name:        "set-stage-immutable",
		Description: "Set a stage's immutable (read-only) flag. Immutable stages are the ONLY valid deploy/merge targets (see deploy-stage); an immutable stage can no longer be edited directly — only changed via deploy. Consequential: making a stage editable removes that protection. Requires explicit user confirmation — call with confirm=\"<stage_id>:<true|false>\" (e.g. \"684082:true\"). Never change immutability without the user confirming.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"stage_id": map[string]interface{}{
					"type":        "integer",
					"description": "Stage ID whose immutable flag to set.",
				},
				"project_id": map[string]interface{}{
					"type":        "integer",
					"description": "Project ID the stage belongs to.",
				},
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID the project belongs to.",
				},
				"immutable": map[string]interface{}{
					"type":        "boolean",
					"description": "true = make read-only (a valid deploy target); false = make editable again.",
				},
				"confirm": map[string]interface{}{
					"type":        "string",
					"description": "Required: must equal \"<stage_id>:<immutable>\" (e.g. \"684082:true\"). Guards against accidental read-only changes.",
				},
			},
			"required": []string{"stage_id", "project_id", "company_id", "immutable"},
		},
	},
	{
		Name:        "create-project",
		Description: "Create a new Corezoid project (with optional stages) inside a workspace. Returns the new project_id and the stage IDs that were created.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID where the project will be created",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Project title",
				},
				"short_name": map[string]interface{}{
					"type":        "string",
					"description": "Project short name (alphanumeric, used in URLs). If omitted the server derives one from the title.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional project description",
				},
				"stages": map[string]interface{}{
					"type":        "string",
					"description": `Optional JSON array of stages to create with the project: [{"title":"production","immutable":true},{"title":"develop","immutable":false}]`,
				},
			},
			"required": []string{"company_id", "title"},
		},
	},
	{
		Name:        "modify-project",
		Description: "Update a Corezoid project's title, short_name and/or description. At least one of title/short_name/description must be provided.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID the project belongs to",
				},
				"project_id": map[string]interface{}{
					"type":        "integer",
					"description": "Project ID (obj_id) to modify",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "New project title",
				},
				"short_name": map[string]interface{}{
					"type":        "string",
					"description": "New project short name",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New project description",
				},
			},
			"required": []string{"company_id", "project_id"},
		},
	},
	{
		Name:        "delete-project",
		Description: "Move a Corezoid project to the recycle bin (Trash). Use restore-project to undo. Use destroy via the Corezoid UI to permanently delete.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID the project belongs to",
				},
				"project_id": map[string]interface{}{
					"type":        "integer",
					"description": "Project ID (obj_id) to delete",
				},
			},
			"required": []string{"company_id", "project_id"},
		},
	},
	{
		Name:        "show-project",
		Description: "Show a Corezoid project's metadata and the stages available to the caller. Returns project obj_id, short_name, parent folder ID and the list of stage IDs/titles.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"company_id": map[string]interface{}{
					"type":        "string",
					"description": "Workspace (company) ID the project belongs to",
				},
				"project_id": map[string]interface{}{
					"type":        "integer",
					"description": "Project ID (obj_id) to show",
				},
			},
			"required": []string{"company_id", "project_id"},
		},
	},
	{
		Name:        "login",
		Description: "Authenticate with Corezoid via OAuth2 browser flow. Opens a browser window and saves the token so it persists across sessions. Optionally accepts account_url, workspace_id, and stage_id to skip interactive prompts.",
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
					"description": "Corezoid stage (root folder) ID",
				},
			},
		},
	},
	{
		Name:        "create-dashboard",
		Description: "Create a new Corezoid dashboard for visualizing process node metrics. Returns dashboard_id needed for adding charts.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Dashboard title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional dashboard description",
				},
				"timezone_offset": map[string]interface{}{
					"type":        "integer",
					"description": "UTC offset in minutes (e.g. -180 for UTC+3). Defaults to 0 (UTC).",
				},
				"folder_id": map[string]interface{}{
					"type":        "integer",
					"description": "Folder (stage) ID where the dashboard will be created. Defaults to COREZOID_STAGE_ID from .env.",
				},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        "get-dashboard",
		Description: "Get a Corezoid dashboard with its charts and series. Use after add-chart to verify series is populated.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dashboard_id": map[string]interface{}{
					"type":        "integer",
					"description": "Dashboard ID",
				},
			},
			"required": []string{"dashboard_id"},
		},
	},
	{
		Name:        "add-chart",
		Description: "Add a chart to a Corezoid dashboard. chart_type must be one of: column, pie, funnel, table. Use 'column' for bar/comparison charts — 'bar' is not a valid type.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dashboard_id": map[string]interface{}{
					"type":        "integer",
					"description": "Dashboard ID to add the chart to",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Chart name/title",
				},
				"chart_type": map[string]interface{}{
					"type":        "string",
					"description": "Chart type: column, pie, funnel, or table",
				},
				"series": map[string]interface{}{
					"type":        "string",
					"description": `JSON array of series: [{"conv_id": 123, "node_id": "<24-char-hex>", "title": "Label"}]`,
				},
			},
			"required": []string{"dashboard_id", "name", "chart_type", "series"},
		},
	},
	{
		Name:        "modify-chart",
		Description: "Modify an existing Corezoid chart. Always provide the full series array — partial updates are not supported.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"chart_id": map[string]interface{}{
					"type":        "string",
					"description": "Chart obj_id (hex string returned by add-chart or get-dashboard)",
				},
				"dashboard_id": map[string]interface{}{
					"type":        "integer",
					"description": "Dashboard ID that contains this chart",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Chart name/title",
				},
				"chart_type": map[string]interface{}{
					"type":        "string",
					"description": "Chart type: column, pie, funnel, or table",
				},
				"series": map[string]interface{}{
					"type":        "string",
					"description": `JSON array of series (full replacement): [{"conv_id": 123, "node_id": "<id>", "title": "Label"}]`,
				},
			},
			"required": []string{"chart_id", "dashboard_id", "name", "chart_type", "series"},
		},
	},
	{
		Name:        "get-chart",
		Description: "Get a single chart with its series data.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"chart_id": map[string]interface{}{
					"type":        "string",
					"description": "Chart obj_id (hex string)",
				},
				"dashboard_id": map[string]interface{}{
					"type":        "integer",
					"description": "Dashboard ID that contains this chart",
				},
			},
			"required": []string{"chart_id", "dashboard_id"},
		},
	},
	{
		Name:        "set-dashboard-layout",
		Description: "Save chart positions on a dashboard grid. Must be called after add-chart/modify-chart to make charts visible. Each grid entry positions one chart by its chart_id (hex string from add-chart).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dashboard_id": map[string]interface{}{
					"type":        "integer",
					"description": "Dashboard ID",
				},
				"grid": map[string]interface{}{
					"type":        "string",
					"description": `JSON array of chart positions: [{"chart_id":"<hex>","x":0,"y":0,"width":6,"height":4},...]. Standard width=6, height=4. Grid is 12 columns wide.`,
				},
				"timezone_offset": map[string]interface{}{
					"type":        "integer",
					"description": "UTC offset in minutes (e.g. -180 for UTC+3). Defaults to 0.",
				},
			},
			"required": []string{"dashboard_id", "grid"},
		},
	},
	{
		Name:        "logout",
		Description: "Remove saved Corezoid credentials from disk.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "list-task-history",
		Description: "Return the execution history (node path) for a task. Shows each node transition with node_id, node_prev_id, create_time_ms.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid process (conv) ID",
				},
				"task_id": map[string]interface{}{
					"type":        "string",
					"description": "Task ID (obj_id) to retrieve history for",
				},
			},
			"required": []string{"process_id", "task_id"},
		},
	},
	{
		Name:        "list-node-tasks",
		Description: "Return tasks currently sitting in a specific node of a process.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid process (conv) ID",
				},
				"node_id": map[string]interface{}{
					"type":        "string",
					"description": "24-character hex node ID",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of tasks to return (default 50)",
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "Pagination offset (default 0)",
				},
			},
			"required": []string{"process_id", "node_id"},
		},
	},
	{
		Name:        "get-node-stat",
		Description: "Return time-series statistics (in/out counts) for a node over a time range. node_id is the ID shown in the Corezoid UI archive URL (/diagram/{node_id}/archive). ops[0]['data'] contains [{\"date\":\"YYYY-MM-DD\",\"in\":N,\"out\":M}] for non-zero buckets. ops[0]['title'] is the node title.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid process (conv) ID",
				},
				"node_id": map[string]interface{}{
					"type":        "string",
					"description": "Node ID from the Corezoid UI archive URL",
				},
				"start": map[string]interface{}{
					"type":        "integer",
					"description": "Unix timestamp — start of the period",
				},
				"end": map[string]interface{}{
					"type":        "integer",
					"description": "Unix timestamp — end of the period",
				},
				"interval": map[string]interface{}{
					"type":        "string",
					"description": "Aggregation bucket: 'day' or 'hour' (default: 'day')",
				},
				"timezone_offset": map[string]interface{}{
					"type":        "integer",
					"description": "UTC offset in minutes, negative westward (e.g. -180 for UTC+3, default: 0)",
				},
			},
			"required": []string{"process_id", "node_id", "start", "end"},
		},
	},
	{
		Name:        "modify-task",
		Description: "Modify an existing task's data. The task will continue from the node where it was paused with the updated data. At least one of task_id or ref must be provided.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid process (conv) ID",
				},
				"data": map[string]interface{}{
					"type":        "string",
					"description": "JSON string with fields to merge into the task",
				},
				"task_id": map[string]interface{}{
					"type":        "string",
					"description": "Task ID (obj_id)",
				},
				"ref": map[string]interface{}{
					"type":        "string",
					"description": "Task reference string",
				},
			},
			"required": []string{"process_id", "data"},
		},
	},
	{
		Name:        "delete-task",
		Description: "Delete a task from a process. At least one of task_id or ref must be provided. If only ref is given, the task_id and node_id are resolved automatically.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Corezoid process (conv) ID",
				},
				"task_id": map[string]interface{}{
					"type":        "string",
					"description": "Task ID (obj_id)",
				},
				"ref": map[string]interface{}{
					"type":        "string",
					"description": "Task reference string",
				},
			},
			"required": []string{"process_id"},
		},
	},
	{
		Name:        "share-object",
		Description: "Grant or revoke access to a Corezoid object (process/folder/stage/project) for a user, API key, or group. To revoke, pass privs=\"none\" — that's the same wire operation as a share with empty privs. API keys share as obj_to=\"user\" with the api key's obj_id.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"obj": map[string]interface{}{
					"type":        "string",
					"description": "Object kind: conv | folder | stage | project",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Numeric ID of the object being shared",
				},
				"obj_to": map[string]interface{}{
					"type":        "string",
					"description": "Recipient kind: user (includes API keys) | group",
				},
				"obj_to_id": map[string]interface{}{
					"type":        "integer",
					"description": "Recipient obj_id (resolve via find-principal)",
				},
				"privs": map[string]interface{}{
					"type":        "string",
					"description": "Comma-separated list, JSON array, or keyword. Allowed values: view, create (task management), modify, delete, all (default), none (revoke all access).",
				},
				"notify": map[string]interface{}{
					"type":        "boolean",
					"description": "Send notification to recipient (default true). Ignored when revoking.",
				},
			},
			"required": []string{"obj", "obj_id", "obj_to", "obj_to_id"},
		},
	},
	{
		Name:        "list-shares",
		Description: "List users, API keys and groups that currently have access to a Corezoid object.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"obj": map[string]interface{}{
					"type":        "string",
					"description": "Object kind: conv | folder | stage | project",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Object ID",
				},
			},
			"required": []string{"obj", "obj_id"},
		},
	},
	{
		Name:        "create-group",
		Description: "Create a new user group in the current workspace. Returns the group's obj_id (use as obj_to_id when sharing).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Group title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional group description",
				},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        "modify-group",
		Description: "Rename a user group and/or update its description. At least one of title or description must be supplied.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "New group title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New group description",
				},
			},
			"required": []string{"group_id"},
		},
	},
	{
		Name:        "list-group-objects",
		Description: "List the processes (conv objects) currently shared with a group. Used to audit group impact before destructive operations. Note: folders/stages/projects shared to the group are not retrievable via this endpoint.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
			},
			"required": []string{"group_id"},
		},
	},
	{
		Name:        "delete-group",
		Description: "Delete a user group. By default refuses to delete if the group still has active shares — pass force=true to override. Existing share links are revoked when the group is deleted.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"force": map[string]interface{}{
					"type":        "boolean",
					"description": "Delete even if the group still has active shares (default false).",
				},
			},
			"required": []string{"group_id"},
		},
	},
	{
		Name:        "add-to-group",
		Description: "Add a user (or API key user) to a group.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"user_id": map[string]interface{}{
					"type":        "integer",
					"description": "User or API-key user obj_id",
				},
			},
			"required": []string{"group_id", "user_id"},
		},
	},
	{
		Name:        "remove-from-group",
		Description: "Remove a user from a group.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"user_id": map[string]interface{}{
					"type":        "integer",
					"description": "User or API-key user obj_id",
				},
			},
			"required": []string{"group_id", "user_id"},
		},
	},
	{
		Name:        "list-groups",
		Description: "List user groups visible in the current workspace.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Optional substring filter on group title",
				},
			},
		},
	},
	{
		Name:        "create-api-key",
		Description: "Create a new API key in the workspace. The secret is written to ~/.corezoid/api-keys/<slug>-<obj_id>.json (mode 0600) and the chat output reports only the file path — the secret is never printed in agent responses.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "API key title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional API key description",
				},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        "modify-api-key",
		Description: "Update title and/or description of an existing API key. Does not regenerate the secret.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"api_key_id": map[string]interface{}{
					"type":        "integer",
					"description": "API key obj_id",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "New title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New description",
				},
			},
			"required": []string{"api_key_id"},
		},
	},
	{
		Name:        "delete-api-key",
		Description: "Delete an API key. The secret is invalidated immediately — subsequent requests return 401. Objects owned by the key are reassigned to the workspace owner.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"api_key_id": map[string]interface{}{
					"type":        "integer",
					"description": "API-key user obj_id",
				},
			},
			"required": []string{"api_key_id"},
		},
	},
	{
		Name:        "list-api-keys",
		Description: "List API keys visible in the current workspace.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Optional substring filter on key title",
				},
			},
		},
	},
	{
		Name:        "find-principal",
		Description: "Search users, groups or API keys in the workspace by substring. Returns obj_ids to pass as obj_to_id in share-object.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Substring to match against title (omit to list all)",
				},
				"kind": map[string]interface{}{
					"type":        "string",
					"description": "What to search: user | group | api_key | shared. Defaults to user.",
				},
			},
		},
	},
	{
		Name:        "invite-user",
		Description: "Invite an external email to the workspace AND share a process/folder/stage/project with them in one call. Returns the invite URL the recipient must open.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"email": map[string]interface{}{
					"type":        "string",
					"description": "Invitee email",
				},
				"login_type": map[string]interface{}{
					"type":        "string",
					"description": "Login type: google | corezoid | phone (defaults to google)",
				},
				"obj": map[string]interface{}{
					"type":        "string",
					"description": "Object to share: conv | folder | stage | project",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Object ID",
				},
				"privs": map[string]interface{}{
					"type":        "string",
					"description": "Privs to grant (view, create, modify, delete, all). Defaults to view.",
				},
			},
			"required": []string{"email", "obj", "obj_id"},
		},
	},
	{
		Name:        "send-feedback",
		Description: "Submit user feedback about plugin behavior to Corezoid. Use only after the user has explicitly confirmed sending. Returns a feedback ticket id.",
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
	{
		Name:        "create-snapshot",
		Description: "Create a snapshot of the current server state of a process before making changes. Useful as a manual checkpoint before experiments. Auto-snapshot is also created automatically before every push-process on existing processes.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the .conv.json file.",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Optional snapshot title. Defaults to 'manual snapshot <ProcessName> <datetime>'.",
				},
			},
			"required": []string{"process_path"},
		},
	},
	{
		Name:        "list-snapshots",
		Description: "List all snapshots for a process. Returns version, title, author and creation time for each snapshot.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the .conv.json file.",
				},
			},
			"required": []string{"process_path"},
		},
	},
	{
		Name:        "delete-snapshot",
		Description: "Delete a snapshot by its obj_id. Use list-snapshots to find the snapshot_id.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the .conv.json file.",
				},
				"snapshot_id": map[string]interface{}{
					"type":        "integer",
					"description": "The obj_id of the snapshot to delete (from list-snapshots).",
				},
			},
			"required": []string{"process_path", "snapshot_id"},
		},
	},
	{
		Name:        "get-snapshot",
		Description: "Get the node list of a specific snapshot for diff comparison against the current process state. Returns all nodes as they existed at snapshot time.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the .conv.json file.",
				},
				"snapshot_id": map[string]interface{}{
					"type":        "integer",
					"description": "The obj_id of the snapshot to retrieve (from list-snapshots).",
				},
			},
			"required": []string{"process_path", "snapshot_id"},
		},
	},
}
