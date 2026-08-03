package main

// snapshotTools — Process snapshot tools (mcp_handlers_snapshots.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var snapshotTools = []mcpTool{
	{
		Name:        "create-snapshot",
		Description: "Create a snapshot of the current server state of a process before making changes. Useful as a manual checkpoint before experiments. Auto-snapshot is also created automatically before every push-process on existing processes.",
		Annotations: annCreateRemote,
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
		Annotations: annReadOnlyRemote,
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
		Annotations: annDestructiveRemote,
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
		Annotations: annReadOnlyRemote,
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
