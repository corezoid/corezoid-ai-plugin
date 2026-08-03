package main

// Process snapshot tools. Handlers live in mcp_handlers_snapshots.go.

// snapshotTools manage server-side process snapshots.
var snapshotTools = []mcpTool{
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
