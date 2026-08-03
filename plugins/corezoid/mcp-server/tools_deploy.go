package main

// deployTools — Stage deployment tools (mcp_handlers_deploy.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var deployTools = []mcpTool{
	{
		Name:        "deploy-stage",
		Description: "Deploy (promote) one stage's processes onto another within a Corezoid project — e.g. develop → production. Wraps the admin obj_scheme compare+merge that the UI \"Deploy\" button issues (on /api/2/compare and /api/2/merge). DESTRUCTIVE, and irreversible on an immutable target. SAFETY: apply=false (default) is a dry-run that only shows the diff and any conflicts — nothing is deployed. To actually deploy you MUST first get the user's explicit confirmation of the exact source→target, then call with apply=true AND confirm=\"<source_stage_id>-><target_stage_id>\". Never deploy without the user confirming. The merge is asynchronous; this tool waits for it to finish over the progress WebSocket.",
		Annotations: annDestructiveRemote,
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
		Annotations: annDestructiveRemote,
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
}
