package main

// discoveryTools — Workspace/project/stage discovery tools (mcp_handlers_discovery.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var discoveryTools = []mcpTool{
	{
		Name:        "list-workspaces",
		Description: "Return the list of Corezoid workspaces (companies) available to the authenticated user.",
		Annotations: annReadOnlyRemote,
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "list-projects",
		Description: "Return the list of projects inside a Corezoid workspace (company), sorted by title.",
		Annotations: annReadOnlyRemote,
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
		Annotations: annReadOnlyRemote,
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
		Name:        "create-project",
		Description: "Create a new Corezoid project (with optional stages) inside a workspace. Returns the new project_id and the stage IDs that were created.",
		Annotations: annCreateRemote,
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
		Annotations: annDestructiveRemote,
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
		Annotations: annDestructiveRemote,
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
		Annotations: annReadOnlyRemote,
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
