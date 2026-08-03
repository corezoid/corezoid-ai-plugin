package main

// Process alias tools. Handlers live in mcp_handlers_process.go.

// aliasTools manage process aliases.
var aliasTools = []mcpTool{
	{
		Name:        "create-alias",
		Description: "Create a short alias for a Corezoid process. Aliases are stage-scoped; the stage is derived from the process file's parent_id (walking up folders until a stage is reached), so a stale COREZOID_STAGE_ID in .env no longer produces the cryptic \"Object is not in stage\" error. Pass stage_id explicitly to override.",
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
