package main

// dashboardTools — Dashboard and chart tools (mcp_handlers_dashboard.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var dashboardTools = []mcpTool{
	{
		Name:        "create-dashboard",
		Description: "Create a new Corezoid dashboard for visualizing process node metrics. Returns dashboard_id needed for adding charts.",
		Annotations: annCreateRemote,
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
		Annotations: annReadOnlyRemote,
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
		Annotations: annCreateRemote,
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
		Annotations: annDestructiveRemote,
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
		Annotations: annReadOnlyRemote,
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
		Annotations: annDestructiveRemote,
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
}
