package main

// Task execution and inspection tools. Handlers live in mcp_handlers_tasks.go.

// taskRunTools execute tasks against a deployed process.
var taskRunTools = []mcpTool{
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
}

// taskInspectTools inspect, modify and delete existing tasks.
var taskInspectTools = []mcpTool{
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
}
