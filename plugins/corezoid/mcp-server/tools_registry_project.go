package main

// Workspace, project and stage tools. Handlers live in mcp_handlers_process.go
// and mcp_handlers_deploy.go.

// projectTools manage workspaces, projects and stages.
var projectTools = []mcpTool{
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
}
