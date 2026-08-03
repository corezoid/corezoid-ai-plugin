package main

// variableTools — Environment-variable tools (mcp_handlers_variables.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var variableTools = []mcpTool{
	{
		Name:        "create-variable",
		Description: "Create an environment variable in a Corezoid folder.",
		Annotations: annCreateRemote,
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
		Annotations: annReadOnlyRemote,
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
		Annotations: annDestructiveRemote,
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
		Annotations: annDestructiveRemote,
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
}
