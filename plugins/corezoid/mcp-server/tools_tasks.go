package main

// taskTools — Task inspection tools (mcp_handlers_tasks.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var taskTools = []mcpTool{
	{
		Name:        "list-task-history",
		Description: "Return the execution history (node path) for a task. Shows each node transition with node_id, node_prev_id, create_time_ms.",
		Annotations: annReadOnlyRemote,
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
		Annotations: annReadOnlyRemote,
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
		Annotations: annReadOnlyRemote,
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
		Annotations: annDestructiveRemote,
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
		Annotations: annDestructiveRemote,
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
}
