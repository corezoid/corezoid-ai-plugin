package main

// toolRegistry is the single source of truth for all MCP tool definitions
// returned by "tools/list". The definitions themselves live in the per-domain
// tools_*.go files, mirroring the mcp_handlers_*.go split — a 1300-line single
// literal was a merge-conflict magnet and made per-domain review impractical.
//
// The concatenation order below is the wire order clients see. It is frozen by
// testdata/golden/tools_list.json (TestToolsList_Snapshot); regenerate that
// snapshot deliberately with:
//
//	go test -run TestToolsList_Snapshot -update ./...
var toolRegistry = concatTools(
	authTools,
	processTools,
	variableTools,
	layoutTools,
	discoveryTools,
	deployTools,
	taskTools,
	dashboardTools,
	accessTools,
	feedbackTools,
	snapshotTools,
	gitTools,
)

// concatTools flattens the per-domain tool slices in the given order.
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
