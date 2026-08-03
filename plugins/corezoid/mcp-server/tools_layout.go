package main

// layoutTools — Local layout tooling (mcp_handlers_layout.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var layoutTools = []mcpTool{
	{
		Name:        "layout-process",
		Description: "Auto-arrange a process's node coordinates into a clean, readable layout (waterfall for simple trees, layered+error-rail for meshes, aligned table/star grids for region bundles). Rewrites ONLY x/y; collapse/expand state, extra, edges, logic, conv_id and aliases stay intact. Runs entirely on the local file (no API, no auth). The result always reports the chosen strategy, canvas size and overlap count; dry=true previews placements without writing.",
		Annotations: annDestructiveLocal,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"process_path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the process JSON file. Optional when the working directory contains exactly one .conv.json.",
				},
				"density": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"compact", "medium", "roomy"},
					"description": "Spacing mode: compact | medium (default) | roomy (keeps the coarse block rhythm, skips compaction).",
				},
				"dry": map[string]interface{}{
					"type":        "boolean",
					"description": "Preview the planned coordinates without modifying the file.",
				},
			},
		},
	},
}
