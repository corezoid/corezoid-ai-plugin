package main

import (
	"context"
	"fmt"
	"time"
)

// toolHandler is the signature every per-tool handler in mcp_handlers_*.go
// implements. Keeping the dispatcher table-driven means adding a new tool is
// one entry in toolHandlers plus one handler function — no edits to the
// switch-statement-as-router.
type toolHandler func(ctx context.Context, args map[string]interface{}) (result string, isError bool)

// toolHandlers maps MCP tool names to their handler functions. The actual
// implementations live in mcp_handlers_auth.go, mcp_handlers_process.go,
// mcp_handlers_discovery.go, mcp_handlers_tasks.go, and
// mcp_handlers_dashboard.go — grouped by concern so a change to e.g. dashboard
// behavior doesn't pull in unrelated diffs to login/push/etc.
var toolHandlers = map[string]toolHandler{
	// auth
	"login":  handleLogin,
	"logout": handleLogout,

	// process / folder / alias
	"pull-process":         handlePullProcess,
	"pull-folder":          handlePullFolder,
	"create-variable":      handleCreateVariable,
	"push-process":         handlePushProcess,
	"lint-process":         handleLintProcess,
	"run-task":             handleRunTask,
	"create-process":       handleCreateProcess,
	"create-state-diagram": handleCreateStateDiagram,
	"create-folder":        handleCreateFolder,
	"show-folder":          handleShowFolder,
	"list-folders":         handleListFolders,
	"modify-folder":        handleModifyFolder,
	"delete-folder":        handleDeleteFolder,
	"create-alias":         handleCreateAlias,

	// discovery
	"list-workspaces": handleListWorkspaces,
	"list-projects":   handleListProjects,
	"list-stages":     handleListStages,
	"create-project":  handleCreateProject,
	"modify-project":  handleModifyProject,
	"delete-project":  handleDeleteProject,
	"show-project":    handleShowProject,

	// tasks
	"list-task-history": handleListTaskHistory,
	"list-node-tasks":   handleListNodeTasks,
	"get-node-stat":     handleGetNodeStat,
	"modify-task":       handleModifyTask,
	"delete-task":       handleDeleteTask,

	// dashboards
	"create-dashboard":     handleCreateDashboard,
	"get-dashboard":        handleGetDashboard,
	"add-chart":            handleAddChart,
	"modify-chart":         handleModifyChart,
	"get-chart":            handleGetChart,
	"set-dashboard-layout": handleSetDashboardLayout,

	// access control (share, groups, api keys, invites)
	"share-object":       handleShareObject,
	"list-shares":        handleListShares,
	"create-group":       handleCreateGroup,
	"modify-group":       handleModifyGroup,
	"list-group-objects": handleListGroupObjects,
	"delete-group":       handleDeleteGroup,
	"add-to-group":       handleAddToGroup,
	"remove-from-group":  handleRemoveFromGroup,
	"list-groups":        handleListGroups,
	"create-api-key":     handleCreateAPIKey,
	"modify-api-key":     handleModifyAPIKey,
	"delete-api-key":     handleDeleteAPIKey,
	"list-api-keys":      handleListAPIKeys,
	"find-principal":     handleFindPrincipal,
	"invite-user":        handleInviteUser,

	// feedback
	"send-feedback": handleSendFeedback,
}

// noAuthTools don't need any credentials. lint runs entirely on local files;
// login/logout manage credentials themselves.
// send-feedback must not require auth so users can report problems that
// occurred before or during the login flow.
var noAuthTools = map[string]struct{}{
	"lint-process":  {},
	"login":         {},
	"logout":        {},
	"send-feedback": {},
}

// tokenOnlyTools need an OAuth token but not a fully configured workspace or
// stage — they help the user discover those values during initial setup.
var tokenOnlyTools = map[string]struct{}{
	"list-workspaces": {},
	"list-projects":   {},
	"list-stages":     {},
	"create-project":  {},
	"modify-project":  {},
	"delete-project":  {},
	"show-project":    {},
}

// handleToolCall dispatches an MCP tool invocation. ctx must be non-nil — it
// flows down through the Executor into every HTTP request, so callers can
// cancel a long-running tool (e.g. pull-folder on a large workspace) via the
// MCP notifications/cancelled message or an HTTP server timeout.
//
// The handler tables above keep this function deliberately small: it does
// auth gating, then a single map lookup. Per-tool logic lives in the
// mcp_handlers_*.go files alongside related tools.
func handleToolCall(ctx context.Context, name string, args map[string]interface{}) (result string, isError bool) {
	if ctx == nil {
		ctx = context.Background()
	}

	switch {
	case isInSet(name, noAuthTools):
		// no auth required
	case isInSet(name, tokenOnlyTools):
		if err := ensureTokenAuth(); err != nil {
			return err.Error(), true
		}
	default:
		if err := ensureAuth(); err != nil {
			return err.Error(), true
		}
	}

	h, ok := toolHandlers[name]
	if !ok {
		return fmt.Sprintf("Unknown tool: %s", name), true
	}

	start := time.Now()
	result, isError = h(ctx, args)

	if analyticsEnabled.Load() {
		apiURLv, _, _, _, _ := authSnapshot()
		e := AnalyticsEvent{
			Ts:             start.UTC().Format(time.RFC3339),
			Tool:           name,
			DurationMs:     time.Since(start).Milliseconds(),
			IsError:        isError,
			APIURL:         hostnameOnly(apiURLv),
			Transport:      analyticsTransport,
			ServerVersion:  mcpServerVersion,
			InstallationID: installationID,
			UserEmail:      telemetryEmail,
		}
		if isError {
			e.ErrorType = classifyError(result)
		}
		emitAnalyticsEvent(e)
	}

	return result, isError
}

func isInSet(name string, set map[string]struct{}) bool {
	_, ok := set[name]
	return ok
}
