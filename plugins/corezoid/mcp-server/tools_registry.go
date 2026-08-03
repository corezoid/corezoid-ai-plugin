package main

// toolRegistry is the single source of truth for all MCP tool definitions.
// mcp_server.go and mcp_http.go return this slice for "tools/list",
// tool_args_validation.go derives the per-tool argument allow-list from it,
// and tests verify the README tools table stays in sync with it.
//
// The definitions themselves live in per-domain tools_registry_<domain>.go
// files, next to the mcp_handlers_<domain>.go that implements them. This file
// is the spine: the only place that decides ORDER.
//
// Order matters. Hosts (Claude Code, Codex, Kiro) may enumerate tools/list
// positionally or memoise tools by index, so the sequence below is frozen by
// TestToolsList_Snapshot against testdata/golden/tools_list.json. Moving a
// definition between domain files without changing this concatenation is a
// pure code move; anything that adds, removes, reorders or rewords a tool has
// to regenerate the golden explicitly:
//
//	go test -run TestToolsList_Snapshot -update ./...
//
// The groups are concatenated rather than appended from init(), because the
// historical order interleaves domains — env-var tools sit between two process
// groups, run-task splits the process CRUD block, dashboards sit between login
// and logout. Alphabetical init() ordering cannot reproduce that; an explicit
// concatenation keeps the whole sequence visible and reviewable in one place.
var toolRegistry = concatTools(
	processExportTools,    // pull-process, pull-folder
	variableTools,         // env_var CRUD
	processQualityTools,   // push / layout / lint
	taskRunTools,          // run-task
	processCreateTools,    // create-process
	stateDiagramTools,     // create-state-diagram
	processStructureTools, // folder CRUD + delete-process
	aliasTools,            // process aliases
	projectTools,          // workspaces, projects, stages, stage deploy
	authLoginTools,        // login
	dashboardTools,        // dashboards and charts
	authLogoutTools,       // logout
	taskInspectTools,      // task history, node stats, modify/delete task
	accessTools,           // sharing, groups, API keys
	feedbackTools,         // send-feedback
	snapshotTools,         // process snapshots
	gitContextTools,       // git mirror
)

// concatTools flattens the per-domain groups into the single ordered registry.
func concatTools(groups ...[]mcpTool) []mcpTool {
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	out := make([]mcpTool, 0, n)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}
