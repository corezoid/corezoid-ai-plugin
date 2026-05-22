package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MCP JSON-RPC types

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// oauthClientID is the OAuth2 client ID used for PKCE flow.
// Resolved from COREZOID_OAUTH_CLIENT_ID env var, falling back to the built-in default.
var oauthClientID string

// serverWriter is the MCP stdout writer, shared across all goroutines.
var serverWriter *bufio.Writer
var serverWriteMu sync.Mutex

// pendingReqs maps elicitation request IDs to response channels.
var pendingReqs sync.Map

// reqCounter generates unique IDs for server-initiated requests.
var reqCounter int64

// clientSupportsElicitation is set during initialize based on the client's declared capabilities.
var clientSupportsElicitation bool

// serverSend marshals msg to JSON and writes it to stdout, thread-safe.
func serverSend(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	serverWriteMu.Lock()
	defer serverWriteMu.Unlock()
	fmt.Fprintf(serverWriter, "%s\n", data)
	serverWriter.Flush()
}

// elicitValues sends an MCP elicitation/create request to the client and waits
// for the user's response. Returns the filled content map, action string
// ("accept", "deny", or "cancel"), and any transport error.
func elicitValues(message string, schema map[string]interface{}) (content map[string]interface{}, action string, err error) {
	id := fmt.Sprintf("elicit-%d", atomic.AddInt64(&reqCounter, 1))
	ch := make(chan []byte, 1)
	pendingReqs.Store(id, ch)
	defer pendingReqs.Delete(id)

	serverSend(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "elicitation/create",
		"params": map[string]interface{}{
			"message":         message,
			"requestedSchema": schema,
		},
	})

	select {
	case raw := <-ch:
		var resp struct {
			Result *struct {
				Action  string                 `json:"action"`
				Content map[string]interface{} `json:"content"`
			} `json:"result"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if jsonErr := json.Unmarshal(raw, &resp); jsonErr != nil {
			return nil, "", fmt.Errorf("failed to parse elicitation response: %w", jsonErr)
		}
		if resp.Error != nil {
			return nil, "", fmt.Errorf("elicitation not supported or failed: %s", resp.Error.Message)
		}
		if resp.Result == nil {
			return nil, "", fmt.Errorf("empty elicitation response")
		}
		return resp.Result.Content, resp.Result.Action, nil
	case <-time.After(10 * time.Minute):
		return nil, "", fmt.Errorf("elicitation timed out")
	}
}

// wsItem holds a single workspace entry returned by the list-workspaces API.
type wsItem struct {
	companyID string
	title     string
	role      string // "owner", "admin", or "member"
}

type projectItem struct {
	projectID int64
	title     string
	shortName string
}

type stageItem struct {
	stageID   int64
	title     string
	shortName string
	immutable bool
}

// fetchWorkspaceList calls the Corezoid API and returns the list of workspaces
// available to the authenticated user. Requires apiURL and apiToken to be set.
func fetchWorkspaceList() ([]wsItem, error) {
	v := NewValidator(0)
	ops := []map[string]any{{"type": "list", "obj": "company"}}
	resp, err := v.req("list_workspaces", ops)
	if err != nil {
		return nil, err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	list, _ := opMap["list"].([]interface{})

	var result []wsItem
	for _, item := range list {
		ws, _ := item.(map[string]interface{})
		companyID, _ := ws["company_id"].(string)
		title, _ := ws["title"].(string)
		isOwner, _ := ws["is_owner"].(bool)
		isAdmin, _ := ws["is_admin"].(bool)
		if companyID == "" {
			continue
		}
		role := "member"
		if isOwner {
			role = "owner"
		} else if isAdmin {
			role = "admin"
		}
		result = append(result, wsItem{companyID: companyID, title: title, role: role})
	}
	return result, nil
}

// fetchProjectList calls the Corezoid API and returns the projects in a workspace.
func fetchProjectList(companyID string) ([]projectItem, error) {
	v := NewValidator(0)
	ops := []map[string]any{
		{"type": "list", "obj": "projects", "obj_id": 0, "id": companyID, "company_id": companyID, "sort": "title"},
	}
	resp, err := v.req("list_projects", ops)
	if err != nil {
		return nil, err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		return nil, fmt.Errorf("%s", desc)
	}
	list, _ := opMap["list"].([]interface{})

	var result []projectItem
	for _, item := range list {
		p, _ := item.(map[string]interface{})
		projectID := int64(0)
		if f, ok := p["project_id"].(float64); ok {
			projectID = int64(f)
		}
		if projectID == 0 {
			continue
		}
		title, _ := p["title"].(string)
		shortName, _ := p["short_name"].(string)
		result = append(result, projectItem{projectID: projectID, title: title, shortName: shortName})
	}
	return result, nil
}

// fetchStageList calls the Corezoid API and returns the stages (folders) in a project.
func fetchStageList(companyID string, projectID int64) ([]stageItem, error) {
	v := NewValidator(0)
	ops := []map[string]any{
		{"type": "list", "obj": "project", "obj_id": projectID, "id": companyID, "company_id": companyID, "sort": "date", "order": "asc"},
	}
	resp, err := v.req("list_stages", ops)
	if err != nil {
		return nil, err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		return nil, fmt.Errorf("%s", desc)
	}
	list, _ := opMap["list"].([]interface{})

	var result []stageItem
	for _, item := range list {
		s, _ := item.(map[string]interface{})
		sid := int64(0)
		if f, ok := s["obj_id"].(float64); ok {
			sid = int64(f)
		}
		if sid == 0 {
			continue
		}
		title, _ := s["title"].(string)
		shortName, _ := s["short_name"].(string)
		immutable, _ := s["immutable"].(bool)
		result = append(result, stageItem{stageID: sid, title: title, shortName: shortName, immutable: immutable})
	}
	return result, nil
}

// ensureTokenAuth checks that a valid API token is present (minimum auth for discovery tools).
func ensureTokenAuth() error {
	if apiToken == "" {
		creds, err := loadCredentials()
		if err == nil && creds != nil && !isCredentialsExpired(creds) {
			apiToken = creds.AccessToken
		}
	}
	if apiToken == "" {
		return fmt.Errorf("[Error] Not authenticated: missing [ACCESS_TOKEN]. Invoke the 'corezoid-init' skill to set up credentials (use the Skill tool with skill=\"corezoid-init\").")
	}
	return nil
}

// ensureAuth checks that all required credentials are set.
// Returns an error with instructions if any value is missing.
func ensureAuth() error {
	if err := ensureTokenAuth(); err != nil {
		return err
	}

	var missing []string
	if accountURL == "" {
		missing = append(missing, "ACCOUNT_URL")
	}
	// WORKSPACE_ID is optional: personal-workspace accounts have no companyID.
	// `Executor.req` strips the empty placeholder from outbound ops in that case.
	if stageID == 0 {
		missing = append(missing, "COREZOID_STAGE_ID")
	}

	if len(missing) > 0 {
		return fmt.Errorf("[Error] Not authenticated: missing %v. Invoke the 'corezoid-init' skill to set up credentials (use the Skill tool with skill=\"corezoid-init\").", missing)
	}
	return nil
}

// runMCPServer starts an MCP server over stdin/stdout using newline-delimited JSON-RPC 2.0.
func runMCPServer() {
	oauthClientID = oauthDefaultClientID
	serverWriter = bufio.NewWriter(os.Stdout)

	// Auto-load saved credentials if no token is configured via env.
	// loadCredentials reads from env vars already populated by findAndLoadDotEnv().
	if apiToken == "" {
		if creds, err := loadCredentials(); err == nil && creds != nil && !isCredentialsExpired(creds) {
			apiToken = creds.AccessToken
			expiry := ""
			if !creds.ExpiresAt.IsZero() {
				expiry = ", expires " + creds.ExpiresAt.Format("2006-01-02 15:04")
			}
			logger.Info("startup: loaded saved credentials%s", expiry)
		}
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)

	sendError := func(id interface{}, code int, msg string) {
		serverSend(mcpResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &mcpError{Code: code, Message: msg},
		})
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Detect whether this is a response to a server-initiated request (e.g. elicitation).
		// Responses have no "method" field; requests do.
		var rawMsg map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &rawMsg); err != nil {
			sendError(nil, -32700, "parse error: "+err.Error())
			continue
		}
		if _, hasMethod := rawMsg["method"]; !hasMethod {
			// It's a response — route to the goroutine waiting on this ID.
			if idRaw, ok := rawMsg["id"]; ok {
				var idStr string
				if json.Unmarshal(idRaw, &idStr) == nil {
					if ch, ok := pendingReqs.Load(idStr); ok {
						ch.(chan []byte) <- []byte(line)
					}
				}
			}
			continue
		}

		var req mcpRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			sendError(nil, -32700, "parse error: "+err.Error())
			continue
		}

		switch req.Method {
		case "initialize":
			// Read client capabilities to detect elicitation support.
			var initParams struct {
				Capabilities map[string]json.RawMessage `json:"capabilities"`
			}
			if err := json.Unmarshal(req.Params, &initParams); err == nil {
				_, clientSupportsElicitation = initParams.Capabilities["elicitation"]
			}
			logger.Info("initialize: clientSupportsElicitation=%v", clientSupportsElicitation)

			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"protocolVersion": "2025-03-26",
					"capabilities": map[string]interface{}{
						"tools": map[string]interface{}{},
					},
					"serverInfo": map[string]interface{}{
						"name":    "convctl-mcp",
						"version": "1.0.4",
					},
				},
			})

		case "notifications/initialized":
			// no response needed for notifications

		case "tools/list":
			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"tools": []mcpTool{
						{
							Name:        "pull-process",
							Description: "Export a single Corezoid process definition to a JSON file. The file is saved to the folder path matching its location in Corezoid (resolved from parent_id).",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"process_id": map[string]interface{}{
										"type":        "integer",
										"description": "Corezoid process ID to export",
									},
								},
								"required": []string{"process_id"},
							},
						},
						{
							Name:        "pull-folder",
							Description: "Recursively export all processes from a Corezoid folder/stage to a local directory.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"folder_id": map[string]interface{}{
										"type":        "integer",
										"description": "Corezoid folder(stage) ID to export",
									},
								},
								"required": []string{"folder_id"},
							},
						},
						{
							Name:        "create-variable",
							Description: "Create an environment variable in a Corezoid folder.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"stage_id": map[string]interface{}{
										"type":        "string",
										"description": "Root folder ID where the variable will be created",
									},
									"name": map[string]interface{}{
										"type":        "string",
										"description": "Variable name",
									},
									"description": map[string]interface{}{
										"type":        "string",
										"description": "Variable description",
									},
									"value": map[string]interface{}{
										"type":        "string",
										"description": "Variable value",
									},
								},
								"required": []string{"stage_id", "name", "description", "value"},
							},
						},
						{
							Name:        "push-process",
							Description: "Validate and deploy a process file to Corezoid.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"process_path": map[string]interface{}{
										"type":        "string",
										"description": "Relative path to the process JSON file.",
									},
								},
								"required": []string{"process_path"},
							},
						},
						{
							Name:        "lint-process",
							Description: "Validate process structure. Reports orphaned nodes, noop conditions, and unused set_params.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"process_path": map[string]interface{}{
										"type":        "string",
										"description": "Relative path to the process JSON file.",
									},
								},
								"required": []string{"process_path"},
							},
						},
						{
							Name:        "run-task",
							Description: "Run a task on an already-deployed Corezoid process (without re-deploying).",
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
								},
								"required": []string{"process_path", "data"},
							},
						},
						{
							Name:        "create-process",
							Description: "Create a new empty process inside a Corezoid folder.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"folder_path": map[string]interface{}{
										"type":        "string",
										"description": "Relative path to the folder directory. Omit to use the current directory.",
									},
									"process_name": map[string]interface{}{
										"type":        "string",
										"description": "Name for the new process",
									},
								},
								"required": []string{"process_name"},
							},
						},
						{
							Name:        "create-folder",
							Description: "Create a new folder inside a parent Corezoid folder.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"parent_path": map[string]interface{}{
										"type":        "string",
										"description": "Relative path to the parent folder directory. Omit to use the current directory.",
									},
									"folder_name": map[string]interface{}{
										"type":        "string",
										"description": "Name for the new folder",
									},
								},
								"required": []string{"folder_name"},
							},
						},
						{
							Name:        "create-alias",
							Description: "Create a short alias for a Corezoid process.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"process_path": map[string]interface{}{
										"type":        "string",
										"description": "Relative path to the process JSON file.",
									},
									"short_name": map[string]interface{}{
										"type":        "string",
										"description": "Short alias name for the process",
									},
								},
								"required": []string{"process_path", "short_name"},
							},
						},
						{
							Name:        "list-workspaces",
							Description: "Return the list of Corezoid workspaces (companies) available to the authenticated user.",
							InputSchema: map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
						{
							Name:        "list-projects",
							Description: "Return the list of projects inside a Corezoid workspace (company), sorted by title.",
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
							Name:        "login",
							Description: "Authenticate with Corezoid via OAuth2 browser flow. Opens a browser window and saves the token so it persists across sessions. Optionally accepts account_url, workspace_id, and stage_id to skip interactive prompts.",
							InputSchema: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"account_url": map[string]interface{}{
										"type":        "string",
										"description": "Account API URL, e.g. https://account.corezoid.com",
									},
									"workspace_id": map[string]interface{}{
										"type":        "string",
										"description": "Corezoid workspace (company) ID",
									},
									"stage_id": map[string]interface{}{
										"type":        "string",
										"description": "Corezoid stage (root folder) ID",
									},
								},
							},
						},
						{
							Name:        "create-dashboard",
							Description: "Create a new Corezoid dashboard for visualizing process node metrics. Returns dashboard_id needed for adding charts.",
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
						{
							Name:        "logout",
							Description: "Remove saved Corezoid credentials from disk.",
							InputSchema: map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
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
					},
				},
			})

		case "tools/call":
			var params struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				sendError(req.ID, -32602, "invalid params: "+err.Error())
				continue
			}

			// Run in a goroutine so the scanner loop can continue reading —
			// this is required to route elicitation responses back to the handler.
			go func(reqID interface{}, name string, args map[string]interface{}) {
				result, isErr := handleToolCall(name, args)
				serverSend(mcpResponse{
					JSONRPC: "2.0",
					ID:      reqID,
					Result: mcpToolResult{
						Content: []mcpContent{{Type: "text", Text: result}},
						IsError: isErr,
					},
				})
			}(req.ID, params.Name, params.Arguments)

		default:
			sendError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func handleToolCall(name string, args map[string]interface{}) (result string, isError bool) {
	// lint works on local files only; login/logout manage auth — skip auth check for these.
	// Discovery tools only need a token, not a fully configured workspace/stage.
	switch name {
	case "lint-process", "login", "logout":
		// no auth required
	case "list-workspaces", "list-projects", "list-stages":
		if err := ensureTokenAuth(); err != nil {
			return err.Error(), true
		}
	default:
		if err := ensureAuth(); err != nil {
			return err.Error(), true
		}
	}

	switch name {
	case "login":
		envPath := envFilePath()

		// Re-read .env so that values updated since server startup are honoured.
		// This matters when the user swaps `.env` to switch between environments
		// (e.g. dev <-> prod) within a single session: the values in the new
		// file must override the in-memory state captured at startup, not the
		// other way around. Otherwise every subsequent API call keeps hitting
		// the host that was active when the MCP process first started.
		findAndLoadDotEnv()
		accountURL = os.Getenv("ACCOUNT_URL")
		workspaceID = os.Getenv("WORKSPACE_ID")
		stageID, _ = strconv.Atoi(os.Getenv("COREZOID_STAGE_ID"))
		apiURL = os.Getenv("COREZOID_API_URL")
		// apiToken keeps its conditional refresh: if a token is already in
		// memory we want to *reuse* it (and skip OAuth) rather than overwrite
		// from .env, which may have just been cleared by `logout`.
		if apiToken == "" {
			apiToken = os.Getenv("ACCESS_TOKEN")
		}

		// Record initial stageID to detect if it gets set during this call.
		stageIDAtStart := stageID

		// Apply any values passed directly as arguments. These represent the
		// caller's explicit intent and override values previously loaded from
		// `.env` or captured at startup — this is what makes login args a
		// viable path to switch environments mid-session.
		if v := optStrArg(args, "account_url"); v != "" {
			if v != accountURL {
				// The account URL changed — the previously derived API URL is
				// no longer valid for the new host. Clear it so it gets
				// re-derived (or re-elicited) for the new account.
				apiURL = ""
				os.Unsetenv("COREZOID_API_URL")
				if err := removeEnvKey(envPath, "COREZOID_API_URL"); err != nil {
					logger.Warn("login: could not clear stale COREZOID_API_URL: %v", err)
				}
			}
			accountURL = v
			os.Setenv("ACCOUNT_URL", v)
			if err := updateEnvFile(envPath, "ACCOUNT_URL", v); err != nil {
				logger.Warn("login: could not save ACCOUNT_URL from arg: %v", err)
			}
		}
		if v := optStrArg(args, "workspace_id"); v != "" {
			workspaceID = v
			os.Setenv("WORKSPACE_ID", v)
			if err := updateEnvFile(envPath, "WORKSPACE_ID", v); err != nil {
				logger.Warn("login: could not save WORKSPACE_ID from arg: %v", err)
			}
		}
		if v := optStrArg(args, "stage_id"); v != "" {
			if id, err := strconv.Atoi(v); err == nil && id != 0 {
				stageID = id
				os.Setenv("COREZOID_STAGE_ID", v)
				if err := updateEnvFile(envPath, "COREZOID_STAGE_ID", v); err != nil {
					logger.Warn("login: could not save COREZOID_STAGE_ID from arg: %v", err)
				}
			}
		}

		// If we already have a token but no derived API URL (e.g. ACCESS_TOKEN
		// was supplied directly in `.env` without going through the OAuth flow,
		// or the account just changed via the `account_url` arg above), derive
		// the corezoid host now so subsequent API calls hit the right server.
		// Without this, requests fall back to the stale apiURL captured at
		// startup and either time out or hit the wrong environment.
		if apiToken != "" && apiURL == "" && accountURL != "" {
			corezoidURL, fetchErr := fetchCorezoidAPIURL(accountURL, apiToken)
			if fetchErr != nil {
				logger.Warn("login: fetchCorezoidAPIURL failed: %v", fetchErr)
			} else {
				apiURL = corezoidURL
				os.Setenv("COREZOID_API_URL", corezoidURL)
				if err := updateEnvFile(envPath, "COREZOID_API_URL", corezoidURL); err != nil {
					logger.Warn("login: could not save COREZOID_API_URL: %v", err)
				}
				logger.Info("login: derived COREZOID_API_URL=%q from clients API", corezoidURL)
			}
		}

		logger.Info("login: accountURL=%q workspaceID=%q stageID=%d", accountURL, workspaceID, stageID)

		// Step 1: ensure Account API URL.
		if accountURL == "" {
			if clientSupportsElicitation {
				content, action, err := elicitValues(
					"Enter your Account API URL to get started:",
					map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"account_url": map[string]interface{}{
								"type":        "string",
								"title":       "Account API URL",
								"description": "e.g. https://account.corezoid.com",
								"default":     "https://account.corezoid.com",
							},
						},
						"required": []string{"account_url"},
					},
				)
				if err != nil {
					logger.Warn("login: elicitation error for ACCOUNT_URL: %v — using default", err)
					accountURL = "https://account.corezoid.com"
				} else if action != "accept" {
					logger.Info("login: user cancelled ACCOUNT_URL elicitation (action=%q)", action)
					return "Please ask the user for their Corezoid Account URL (e.g. https://account.corezoid.com), then call the login tool again with account_url=<value>.", false
				} else {
					if v, _ := content["account_url"].(string); v != "" {
						accountURL = v
					} else {
						accountURL = "https://account.corezoid.com"
					}
				}
			} else {
				return "Please ask the user for their Corezoid Account URL (e.g. https://account.corezoid.com), then call the login tool again with account_url=<value>.", false
			}
			os.Setenv("ACCOUNT_URL", accountURL)
			if err := updateEnvFile(envPath, "ACCOUNT_URL", accountURL); err != nil {
				logger.Warn("login: could not save ACCOUNT_URL: %v", err)
			}
		}

		// Step 2: OAuth2 PKCE browser flow (skipped if already authenticated).
		var tokenExpiry time.Time
		if apiToken == "" {
			res, err := oauthPKCEFlow(accountURL, oauthClientID)
			if err != nil {
				return fmt.Sprintf("Authentication failed: %v", err), true
			}
			creds := &Credentials{
				AccessToken: res.AccessToken,
				ExpiresAt:   res.ExpiresAt,
				TokenType:   "Simulator",
			}
			if saveErr := saveCredentials(creds); saveErr != nil {
				logger.Warn("login: failed to save credentials: %v", saveErr)
			}
			apiToken = res.AccessToken
			tokenExpiry = res.ExpiresAt

			// Step 2.5: derive COREZOID_API_URL from the account clients endpoint.
			if apiURL == "" {
				corezoidURL, fetchErr := fetchCorezoidAPIURL(accountURL, res.AccessToken)
				if fetchErr != nil {
					logger.Warn("login: fetchCorezoidAPIURL failed: %v", fetchErr)
				} else {
					apiURL = corezoidURL
					os.Setenv("COREZOID_API_URL", corezoidURL)
					if err := updateEnvFile(envPath, "COREZOID_API_URL", corezoidURL); err != nil {
						logger.Warn("login: could not save COREZOID_API_URL: %v", err)
					}
					logger.Info("login: derived COREZOID_API_URL=%q from clients API", corezoidURL)
				}
			}
		}

		// Step 3: workspace selection.
		if workspaceID == "" {
			if clientSupportsElicitation {
				workspaces, fetchErr := fetchWorkspaceList()
				if fetchErr != nil {
					logger.Warn("login: fetchWorkspaceList failed: %v — falling back to text input", fetchErr)
				}

				var wsSchema map[string]interface{}
				wsIDByLabel := map[string]string{}

				if fetchErr == nil && len(workspaces) > 0 {
					enumVals := make([]string, len(workspaces))
					for i, ws := range workspaces {
						label := ws.companyID + " — " + ws.title
						if ws.role != "member" {
							label += " [" + ws.role + "]"
						}
						enumVals[i] = label
						wsIDByLabel[label] = ws.companyID
					}
					wsSchema = map[string]interface{}{
						"type":        "string",
						"title":       "Workspace",
						"description": "Select the workspace you want to work with",
						"enum":        enumVals,
					}
				} else {
					wsSchema = map[string]interface{}{
						"type":        "string",
						"title":       "Workspace ID",
						"description": "Your company/workspace identifier in Corezoid",
					}
				}

				content, action, err := elicitValues(
					"Select your Corezoid workspace:",
					map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{"workspace_id": wsSchema},
						"required":   []string{"workspace_id"},
					},
				)
				if err == nil && action == "accept" {
					if selected, _ := content["workspace_id"].(string); selected != "" {
						id := selected
						if raw, ok := wsIDByLabel[selected]; ok {
							id = raw
						}
						workspaceID = id
						os.Setenv("WORKSPACE_ID", id)
						if err := updateEnvFile(envPath, "WORKSPACE_ID", id); err != nil {
							logger.Warn("login: could not save WORKSPACE_ID: %v", err)
						}
					}
				}
			} else {
				// No elicitation — fetch workspace list and return it to the LLM.
				workspaces, fetchErr := fetchWorkspaceList()
				var sb strings.Builder
				sb.WriteString("Authenticated successfully.\n\nAvailable workspaces:\n")
				if fetchErr != nil {
					logger.Warn("login: fetchWorkspaceList failed: %v", fetchErr)
					sb.WriteString(fmt.Sprintf("(could not fetch workspace list: %v)\n", fetchErr))
				} else {
					for _, ws := range workspaces {
						label := ws.title
						if ws.role != "member" {
							label += " [" + ws.role + "]"
						}
						sb.WriteString(fmt.Sprintf("  %s — %s\n", ws.companyID, label))
					}
				}
				sb.WriteString("\nPlease ask the user which workspace they want to use, then call login(workspace_id=<selected_id>).")
				return sb.String(), false
			}
		}

		// Steps 4 & 5: pick project then stage.
		if stageID == 0 {
			if clientSupportsElicitation {
				var selectedProjectID int64

				// Step 4: fetch project list and elicit selection.
				projects, projErr := fetchProjectList(workspaceID)
				if projErr != nil {
					logger.Warn("login: fetchProjectList failed: %v", projErr)
				}

				if projErr == nil && len(projects) > 0 {
					enumVals := make([]string, len(projects))
					projIDByLabel := map[string]int64{}
					for i, p := range projects {
						label := fmt.Sprintf("%d — %s", p.projectID, p.title)
						if p.shortName != "" && p.shortName != p.title {
							label += " (" + p.shortName + ")"
						}
						enumVals[i] = label
						projIDByLabel[label] = p.projectID
					}
					content, action, err := elicitValues(
						"Select your Corezoid project:",
						map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"project": map[string]interface{}{
									"type":        "string",
									"title":       "Project",
									"description": "Select the project to work with",
									"enum":        enumVals,
								},
							},
							"required": []string{"project"},
						},
					)
					if err == nil && action == "accept" {
						if selected, _ := content["project"].(string); selected != "" {
							selectedProjectID = projIDByLabel[selected]
						}
					}
				}

				// Step 5: fetch stage list for selected project and elicit selection.
				if selectedProjectID != 0 {
					stages, stagesErr := fetchStageList(workspaceID, selectedProjectID)
					if stagesErr != nil {
						logger.Warn("login: fetchStageList failed: %v", stagesErr)
					}

					if stagesErr == nil && len(stages) > 0 {
						enumVals := make([]string, len(stages))
						stageIDByLabel := map[string]int64{}
						for i, s := range stages {
							label := fmt.Sprintf("%d — %s", s.stageID, s.title)
							if s.shortName != "" && s.shortName != s.title {
								label += " (" + s.shortName + ")"
							}
							if s.immutable {
								label += " [immutable]"
							}
							enumVals[i] = label
							stageIDByLabel[label] = s.stageID
						}
						content, action, err := elicitValues(
							"Select your Corezoid stage (root folder for this project):",
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"stage": map[string]interface{}{
										"type":        "string",
										"title":       "Stage",
										"description": "Select the stage to use as the root folder",
										"enum":        enumVals,
									},
								},
								"required": []string{"stage"},
							},
						)
						if err == nil && action == "accept" {
							if selected, _ := content["stage"].(string); selected != "" {
								if id, ok := stageIDByLabel[selected]; ok && id != 0 {
									stageID = int(id)
									v := strconv.FormatInt(id, 10)
									os.Setenv("COREZOID_STAGE_ID", v)
									if err := updateEnvFile(envPath, "COREZOID_STAGE_ID", v); err != nil {
										logger.Warn("login: could not save COREZOID_STAGE_ID: %v", err)
									}
								}
							}
						}
					}
				}

				// Fallback: if stage still not set, ask for stage ID directly.
				if stageID == 0 {
					content, action, err := elicitValues(
						"Enter your Stage ID (root folder ID for this project):",
						map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"stage_id": map[string]interface{}{
									"type":        "string",
									"title":       "Stage ID",
									"description": "Root folder ID for this project (numeric)",
								},
							},
							"required": []string{"stage_id"},
						},
					)
					if err == nil && action == "accept" {
						if v, _ := content["stage_id"].(string); v != "" {
							if id, err := strconv.Atoi(v); err == nil && id != 0 {
								stageID = id
								os.Setenv("COREZOID_STAGE_ID", v)
								if err := updateEnvFile(envPath, "COREZOID_STAGE_ID", v); err != nil {
									logger.Warn("login: could not save COREZOID_STAGE_ID: %v", err)
								}
							}
						}
					}
				}
			} else {
				// No elicitation — list projects so LLM can collect stage from user.
				projects, projErr := fetchProjectList(workspaceID)
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Workspace %s selected.\n\n", workspaceID))
				if projErr != nil || len(projects) == 0 {
					if projErr != nil {
						sb.WriteString(fmt.Sprintf("Could not fetch projects: %v\n", projErr))
					} else {
						sb.WriteString("No projects found.\n")
					}
					sb.WriteString(fmt.Sprintf("Please ask the user for their COREZOID_STAGE_ID (root folder ID), then call login(workspace_id=%s, stage_id=<stage_id>).", workspaceID))
				} else {
					sb.WriteString("Available projects:\n")
					for _, p := range projects {
						line := fmt.Sprintf("  %d — %s", p.projectID, p.title)
						if p.shortName != "" && p.shortName != p.title {
							line += fmt.Sprintf(" (%s)", p.shortName)
						}
						sb.WriteString(line + "\n")
					}
					sb.WriteString(fmt.Sprintf("\nPlease ask the user which project to use. Call list-stages(project_id=<id>, company_id=%s) to see available stages, then ask the user to pick one and call login(workspace_id=%s, stage_id=<stage_id>).", workspaceID, workspaceID))
				}
				return sb.String(), false
			}
		}

		// Auto pull-folder if stageID was set during this login call.
		if stageID != 0 && stageIDAtStart == 0 {
			pv := NewValidator(0)
			if pullErr := downloadStageRecursively(pv, stageID, "."); pullErr != nil {
				logger.Warn("login: auto pull-folder failed: %v", pullErr)
			}
		}

		msg := fmt.Sprintf("Setup complete! Configuration saved to %s.", envPath)
		if !tokenExpiry.IsZero() {
			msg += fmt.Sprintf(" Token expires: %s.", tokenExpiry.Format("2006-01-02 15:04"))
		}
		return msg, false

	case "logout":
		if err := deleteCredentials(); err != nil {
			return fmt.Sprintf("Failed to remove credentials: %v", err), true
		}
		// Clear all session-scoped state, not just the token. Without this,
		// `accountURL`, `workspaceID`, `stageID` and `apiURL` remain pinned to
		// the values captured at server startup — so a follow-up `login` call
		// with different args (or a refreshed `.env`) cannot actually switch
		// environments, and tools keep hitting the previous host. The .env
		// file keeps its non-credential keys intact: `login` re-reads them on
		// the next call.
		apiToken = ""
		accountURL = ""
		workspaceID = ""
		stageID = 0
		apiURL = ""
		return fmt.Sprintf("Logged out. ACCESS_TOKEN removed from %s.", envFilePath()), false

	case "pull-process":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		v := NewValidator(processID)
		procInfo1, err := v.ExportProcess()
		if err != nil {
			return fmt.Sprintf("Error fetching process: %v", err), true
		}
		var procInfo interface{}
		if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
			procInfo = arr[0]
		} else {
			procInfo = procInfo1
		}
		data, err := json.MarshalIndent(procInfo, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error marshaling process: %v", err), true
		}

		// Derive filename from process title if available
		fileName := fmt.Sprintf("%d.conv.json", processID)
		if m, ok := procInfo.(map[string]interface{}); ok {
			if title, _ := m["title"].(string); title != "" {
				safeName := strings.ReplaceAll(title, " ", "_")
				fileName = fmt.Sprintf("%d_%s.conv.json", processID, safeName)
			}
		}

		// Resolve save directory from parent_id so the file lands in the correct folder tree.
		var dir string
		if m, ok := procInfo.(map[string]interface{}); ok {
			parentID := 0
			if pid, ok := m["parent_id"].(float64); ok {
				parentID = int(pid)
			}
			if parentID != 0 && stageID != 0 {
				resolved, resolveErr := v.resolveFolderPathFromAPI(parentID)
				if resolveErr != nil {
					logger.Warn("pull-process: could not resolve folder path for parent_id %d: %v", parentID, resolveErr)
				} else {
					dir = resolved
				}
			}
		}

		if dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Sprintf("Error creating directory: %v", err), true
			}
		}

		filePath := filepath.Join(dir, fileName)
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Sprintf("Error writing file: %v", err), true
		}
		return fmt.Sprintf("Process %d saved to %s", processID, filePath), false

	case "pull-folder":
		folderID, err := intArg(args, "folder_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(0)
		if err := downloadStageRecursively(v, folderID, "."); err != nil {
			return fmt.Sprintf("Error fetching folder: %v", err), true
		}
		return fmt.Sprintf("Folder %d saved to current directory", folderID), false

	case "create-variable":
		rootFolderID, err := strArg(args, "stage_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		name, err := strArg(args, "name")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		description, err := strArg(args, "description")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		value, err := strArg(args, "value")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(0)
		if err := v.CreateVariable(rootFolderID, name, description, value); err != nil {
			return fmt.Sprintf("Error creating variable: %v", err), true
		}
		return fmt.Sprintf("Environment variable '%s' created successfully", name), false

	case "push-process":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		baseName := filepath.Base(filePath)
		reFileID := regexp.MustCompile(`^(\d+)_`)
		matches := reFileID.FindStringSubmatch(baseName)
		if matches == nil {
			return fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.json'", baseName), true
		}
		procID, _ := strconv.Atoi(matches[1])

		v := NewValidator(procID)

		jsonContent, err := LoadBinFromFile(filePath)
		if err != nil {
			return fmt.Sprintf("Error loading JSON file: %v", err), true
		}

		jsonContent1, messages := fixStruct(jsonContent, procID)
		if len(messages) > 0 {
			for _, msg := range messages {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
		if jsonContent1 != jsonContent {
			jsonContent = jsonContent1
			if err := os.WriteFile(filePath, []byte(jsonContent), 0644); err != nil {
				return fmt.Sprintf("Error writing fixed JSON: %v", err), true
			}
		}

		if err := ValidateJSONSchema(filePath, debug); err != nil {
			return fmt.Sprintf("JSON schema validation failed: %v", err), true
		}

		if err := v.BeforeValidation(jsonContent, nil); err != nil {
			return fmt.Sprintf("Validation failed: %v", err), true
		}

		if _, err := v.ProcessJSON(filePath, jsonContent); err != nil {
			return fmt.Sprintf("Error deploying process: %v", err), true
		}

		return fmt.Sprintf("Process deployed successfully, ProcessID: %d", procID), false

	case "lint-process":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		result, err := lintProcess(filePath)
		if err != nil {
			return fmt.Sprintf("Error: lint failed: %v", err), true
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Lint results for: %s\n", result.ProcessTitle))
		sb.WriteString(fmt.Sprintf("Total nodes: %d\n", result.TotalNodes))

		hasIssues := false

		if !result.SchemaValid {
			hasIssues = true
			sb.WriteString("\n=== JSON SCHEMA VALIDATION FAILED ===\n")
			sb.WriteString(fmt.Sprintf("  %s\n", result.SchemaError))
		}

		if len(result.NoopConditions) > 0 {
			hasIssues = true
			sb.WriteString(fmt.Sprintf("\n=== NOOP CONDITIONS (%d) ===\n", len(result.NoopConditions)))
			for _, nc := range result.NoopConditions {
				sb.WriteString(fmt.Sprintf("  [%s] %s\n", nc.ID, nc.Title))
				sb.WriteString(fmt.Sprintf("  Issue: %s\n", nc.Issue))
			}
		}

		if len(result.UnusedSetParams) > 0 {
			hasIssues = true
			sb.WriteString(fmt.Sprintf("\n=== UNUSED SET_PARAM (%d) ===\n", len(result.UnusedSetParams)))
			for _, up := range result.UnusedSetParams {
				sb.WriteString(fmt.Sprintf("  [%s] %s\n", up.ID, up.Title))
				sb.WriteString(fmt.Sprintf("  Issue: %s\n", up.Issue))
			}
		}

		if len(result.OrphanedNodes) > 0 {
			hasIssues = true
			sb.WriteString(fmt.Sprintf("\n=== ORPHANED NODES (%d / %d reachable from Start) ===\n",
				len(result.OrphanedNodes), result.ReachableCount))
			for _, on := range result.OrphanedNodes {
				sb.WriteString(fmt.Sprintf("  [%s] %s  (type: %s)\n", on.ID, on.Title, on.ObjType))
			}
		}

		if !hasIssues {
			sb.WriteString("\nNo issues found.")
		} else {
			schemaIssues := 0
			if !result.SchemaValid {
				schemaIssues = 1
			}
			total := len(result.NoopConditions) + len(result.UnusedSetParams) + len(result.OrphanedNodes) + schemaIssues
			sb.WriteString(fmt.Sprintf("\nTotal issues: %d\n", total))
		}

		return sb.String(), false

	case "run-task":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dataStr, err := strArg(args, "data")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		baseName := filepath.Base(filePath)
		reFileID := regexp.MustCompile(`^(\d+)_`)
		matches := reFileID.FindStringSubmatch(baseName)
		if matches == nil {
			return fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.json'", baseName), true
		}
		procID, _ := strconv.Atoi(matches[1])

		v := NewValidator(procID)

		jsonContent, err := LoadBinFromFile(filePath)
		if err != nil {
			return fmt.Sprintf("Error reading process file: %v", err), true
		}

		if _, err := v.ProcessJSON(filePath, jsonContent); err != nil {
			return fmt.Sprintf("Error deploying process: %v", err), true
		}

		taskData := make(map[string]interface{})
		if err := json.Unmarshal([]byte(dataStr), &taskData); err != nil {
			return fmt.Sprintf("Error parsing task data: %v", err), true
		}

		ref := fmt.Sprintf("%d_%d", time.Now().Unix(), rand.Intn(1000000))
		if err := v.createTask(ref, taskData); err != nil {
			return fmt.Sprintf("Error creating task: %v", err), true
		}

		time.Sleep(time.Second * 5)

		rspTask, err := v.showTask(ref)
		if err != nil {
			return fmt.Sprintf("Error getting task result: %v", err), true
		}
		logger.Info("Task response: %+v", rspTask)
		rspTaskData, _ := rspTask["data"].(map[string]interface{})
		rspTaskDataBin, _ := json.Marshal(rspTaskData)
		serverNodeID, _ := rspTask["node_id"].(string)

		if v.Debug {
			for k, ni := range v.NodeIDMap {
				logger.Debug("NodeIDMap entry: key=%s type=%d serverID=%s name=%s", k, ni.Type, ni.ServerID, ni.Name)
			}
		}

		nodeInfo, found := v.NodeIDMap[serverNodeID]
		if !found {
			for _, ni := range v.NodeIDMap {
				if serverNodeID == ni.ServerID {
					nodeInfo = ni
					found = true
					break
				}
			}
		}
		logger.Info("Node info (found=%v): %+v", found, nodeInfo)
		nodeType := "logic (not final)"
		msg := "Task failed: it stopped at the non-final node"
		isErr := true
		if nodeInfo.Type == 1 {
			nodeType = "start"
		} else if nodeInfo.Type == 2 {
			isErr = false
			nodeType = "end (Success)"
			msg = "Task completed"
			if nodeInfo.Icon == "error" {
				isErr = true
				nodeType = "error node"
				msg = "Task failed: stopped at error node"
			}
		}

		summary := fmt.Sprintf("%s\nNodeID: %s\nNodeName: %s\nNodeType: %s\nProcessID: %d\nData: %s",
			msg, serverNodeID, nodeInfo.Name, nodeType, v.ProcessID, string(rspTaskDataBin))
		return summary, isErr

	case "create-process":
		folderPath := resolveDirPath(args, "folder_path")
		processName, err := strArg(args, "process_name")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		folderID, err := resolveFolderIDFromDir(folderPath)
		if err != nil {
			return fmt.Sprintf("Error resolving folder ID: %v", err), true
		}

		v := NewValidator(0)
		processID := v.CreateEmptyProcess(folderID, processName, "")
		if processID == 0 {
			return fmt.Sprintf("Error: failed to create process '%s'", processName), true
		}

		procInfo1, err := v.ExportProcess()
		if err != nil {
			return fmt.Sprintf("Error exporting process: %v", err), true
		}
		var procInfo interface{}
		if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
			procInfo = arr[0]
		} else {
			procInfo = procInfo1
		}
		data, err := json.MarshalIndent(procInfo, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error marshaling process: %v", err), true
		}

		safeName := strings.ReplaceAll(processName, " ", "_")
		fileName := fmt.Sprintf("%d_%s.json", processID, safeName)
		filePath := filepath.Join(folderPath, fileName)
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Sprintf("Error writing file: %v", err), true
		}

		return fmt.Sprintf("Process '%s' created and saved to %s", processName, filePath), false

	case "create-folder":
		parentPath := resolveDirPath(args, "parent_path")
		folderName, err := strArg(args, "folder_name")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		parentFolderID, err := resolveFolderIDFromDir(parentPath)
		if err != nil {
			return fmt.Sprintf("Error resolving parent folder ID: %v", err), true
		}

		v := NewValidator(0)
		newFolderID, err := v.CreateFolder(parentFolderID, folderName, "")
		if err != nil {
			return fmt.Sprintf("Error creating folder '%s': %v", folderName, err), true
		}

		safeName := strings.ReplaceAll(folderName, " ", "_")
		dirName := fmt.Sprintf("%d_%s", newFolderID, safeName)
		dirPath := filepath.Join(parentPath, dirName)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Sprintf("Error creating directory '%s': %v", dirPath, err), true
		}

		type folderFileContent struct {
			Description string `json:"description"`
			ObjID       int    `json:"obj_id"`
			ObjType     int    `json:"obj_type"`
			ParentID    int    `json:"parent_id"`
			Title       string `json:"title"`
		}
		fileContent := folderFileContent{
			Description: "",
			ObjID:       newFolderID,
			ObjType:     0,
			ParentID:    parentFolderID,
			Title:       folderName,
		}
		fileData, err := json.MarshalIndent(fileContent, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error marshaling folder file: %v", err), true
		}
		fileName := fmt.Sprintf("%d_%s.folder.json", newFolderID, safeName)
		filePath := filepath.Join(dirPath, fileName)
		if err := os.WriteFile(filePath, fileData, 0644); err != nil {
			return fmt.Sprintf("Error writing folder file: %v", err), true
		}

		return fmt.Sprintf("Folder '%s' created and saved to %s", folderName, filePath), false

	case "create-alias":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		shortName, err := strArg(args, "short_name")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		baseName := filepath.Base(filePath)
		reFileID := regexp.MustCompile(`^(\d+)_`)
		matches := reFileID.FindStringSubmatch(baseName)
		if matches == nil {
			return fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.json'", baseName), true
		}
		procID, _ := strconv.Atoi(matches[1])

		if stageID == 0 {
			return "Error: COREZOID_STAGE_ID environment variable is not set or invalid", true
		}

		v := NewValidator(0)
		aliasID, err := v.CreateAlias(shortName, procID, stageID)
		if err != nil {
			return fmt.Sprintf("Error creating alias: %v", err), true
		}

		return fmt.Sprintf("Alias '%s' created successfully, AliasID: %d", shortName, aliasID), false

	case "list-workspaces":
		v := NewValidator(0)
		ops := []map[string]any{
			{
				"type": "list",
				"obj":  "company",
			},
		}
		resp, err := v.req("list_workspaces", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}

		// Extract the workspace list from ops[0].list
		opsArr, _ := resp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "No workspaces found", false
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		list, _ := opMap["list"].([]interface{})
		if len(list) == 0 {
			return "No workspaces found", false
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Workspaces (%d total):\n\n", len(list)))
		for _, item := range list {
			ws, _ := item.(map[string]interface{})
			companyID, _ := ws["company_id"].(string)
			title, _ := ws["title"].(string)
			isOwner, _ := ws["is_owner"].(bool)
			isAdmin, _ := ws["is_admin"].(bool)

			role := "member"
			if isOwner {
				role = "owner"
			} else if isAdmin {
				role = "admin"
			}

			sb.WriteString(fmt.Sprintf("  %-45s  %s  [%s]\n", companyID, title, role))
		}
		return sb.String(), false

	case "list-projects":
		companyID, err := strArg(args, "company_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(0)
		ops := []map[string]any{
			{
				"type":       "list",
				"obj":        "projects",
				"obj_id":     0,
				"id":         companyID,
				"company_id": companyID,
				"sort":       "title",
			},
		}
		resp, err := v.req("list_projects", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}

		opsArr, _ := resp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "No projects found", false
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		if proc, _ := opMap["proc"].(string); proc != "ok" {
			desc, _ := opMap["description"].(string)
			return fmt.Sprintf("Error: %s", desc), true
		}
		list, _ := opMap["list"].([]interface{})
		if len(list) == 0 {
			return "No projects found", false
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Projects in workspace %s (%d total):\n\n", companyID, len(list)))
		sb.WriteString(fmt.Sprintf("  %-10s  %-35s  %-30s  %s\n", "ID", "Title", "Short name", "Owner"))
		sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("-", 95)))
		for _, item := range list {
			p, _ := item.(map[string]interface{})
			projectID := int64(0)
			if f, ok := p["project_id"].(float64); ok {
				projectID = int64(f)
			}
			title, _ := p["title"].(string)
			shortName, _ := p["short_name"].(string)
			ownerLogin, _ := p["owner_login"].(string)
			undeployed := int(0)
			if f, ok := p["undeployed"].(float64); ok {
				undeployed = int(f)
			}
			undeployedStr := ""
			if undeployed > 0 {
				undeployedStr = fmt.Sprintf(" [%d undeployed]", undeployed)
			}
			sb.WriteString(fmt.Sprintf("  %-10d  %-35s  %-30s  %s%s\n",
				projectID, title, shortName, ownerLogin, undeployedStr))
		}
		return sb.String(), false

	case "list-stages":
		projectID, err := intArg(args, "project_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		companyID, err := strArg(args, "company_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(0)
		ops := []map[string]any{
			{
				"type":       "list",
				"obj":        "project",
				"obj_id":     projectID,
				"id":         companyID,
				"company_id": companyID,
				"sort":       "date",
				"order":      "asc",
			},
		}
		resp, err := v.req("list_stages", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}

		opsArr, _ := resp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "No stages found", false
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		if proc, _ := opMap["proc"].(string); proc != "ok" {
			desc, _ := opMap["description"].(string)
			return fmt.Sprintf("Error: %s", desc), true
		}
		list, _ := opMap["list"].([]interface{})
		if len(list) == 0 {
			return "No stages found", false
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Stages in project %d (%d total):\n\n", projectID, len(list)))
		sb.WriteString(fmt.Sprintf("  %-10s  %-20s  %-20s  %s\n", "ID", "Title", "Short name", "Immutable"))
		sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("-", 70)))
		for _, item := range list {
			s, _ := item.(map[string]interface{})
			stageID := int64(0)
			if f, ok := s["obj_id"].(float64); ok {
				stageID = int64(f)
			}
			title, _ := s["title"].(string)
			shortName, _ := s["short_name"].(string)
			immutable, _ := s["immutable"].(bool)
			immutableStr := "no"
			if immutable {
				immutableStr = "yes"
			}
			sb.WriteString(fmt.Sprintf("  %-10d  %-20s  %-20s  %s\n", stageID, title, shortName, immutableStr))
		}
		return sb.String(), false

	case "list-task-history":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		taskID, err := strArg(args, "task_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(processID)
		ops := []map[string]any{
			{
				"type":    "list",
				"obj":     "task_history",
				"conv_id": processID,
				"obj_id":  taskID,
			},
		}
		resp, err := v.req("list_task_history", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "list-node-tasks":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		nodeID, err := strArg(args, "node_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		limit := 50
		if v, ok := args["limit"]; ok {
			if n, err := intArg(args, "limit"); err == nil {
				_ = v
				limit = n
			}
		}
		offset := 0
		if v, ok := args["offset"]; ok {
			if n, err := intArg(args, "offset"); err == nil {
				_ = v
				offset = n
			}
		}

		validator := NewValidator(processID)
		ops := []map[string]any{
			{
				"type":       "list",
				"obj":        "node",
				"company_id": workspaceID,
				"conv_id":    processID,
				"obj_id":     nodeID,
				"limit":      limit,
				"offset":     offset,
			},
		}
		resp, err := validator.req("list_node_tasks", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "modify-task":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dataStr, err := strArg(args, "data")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		taskID, _ := args["task_id"].(string)
		ref, _ := args["ref"].(string)
		if taskID == "" && ref == "" {
			return "Error: at least one of task_id or ref must be provided", true
		}

		var taskData map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &taskData); err != nil {
			return fmt.Sprintf("Error parsing data JSON: %v", err), true
		}

		op := map[string]any{
			"type":    "modify",
			"obj":     "task",
			"conv_id": processID,
			"data":    taskData,
		}
		if taskID != "" {
			op["obj_id"] = taskID
		}
		if ref != "" {
			op["ref"] = ref
		}

		v := NewValidator(processID)
		resp, err := v.req("modify_task", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "delete-task":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		taskID, _ := args["task_id"].(string)
		ref, _ := args["ref"].(string)
		if taskID == "" && ref == "" {
			return "Error: at least one of task_id or ref must be provided", true
		}

		v := NewValidator(processID)

		// Resolve task_id and node_id via show first
		showOp := map[string]any{
			"type":    "show",
			"obj":     "task",
			"conv_id": processID,
		}
		if taskID != "" {
			showOp["obj_id"] = taskID
		} else {
			showOp["ref"] = ref
		}
		showResp, err := v.req("show_task", []map[string]any{showOp})
		if err != nil {
			return fmt.Sprintf("Error resolving task: %v", err), true
		}
		opsArr, _ := showResp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "Error: task not found", true
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		if opMap["proc"] != "ok" {
			desc, _ := opMap["description"].(string)
			return fmt.Sprintf("Error: %s", desc), true
		}
		resolvedTaskID, _ := opMap["obj_id"].(string)
		nodeID, _ := opMap["node_id"].(string)

		deleteOp := map[string]any{
			"type":    "delete",
			"obj":     "task",
			"conv_id": processID,
			"obj_id":  resolvedTaskID,
			"node_id": nodeID,
		}
		resp, err := v.req("delete_task", []map[string]any{deleteOp})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "create-dashboard":
		title, err := strArg(args, "title")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		description, _ := args["description"].(string)
		tzOffset := 0
		if tz, ok := args["timezone_offset"]; ok {
			if tzFloat, ok := tz.(float64); ok {
				tzOffset = int(tzFloat)
			} else if tzStr, ok := tz.(string); ok {
				tzOffset, _ = strconv.Atoi(tzStr)
			}
		}
		folderID := stageID
		if fid, ok := args["folder_id"]; ok {
			if fidFloat, ok := fid.(float64); ok {
				folderID = int(fidFloat)
			} else if fidStr, ok := fid.(string); ok {
				folderID, _ = strconv.Atoi(fidStr)
			}
		}

		v := NewValidator(0)
		projectID := v.GetProjectIDByStageID(folderID)
		now := int(time.Now().Unix())

		op := map[string]any{
			"obj":         "dashboard",
			"type":        "create",
			"obj_type":    0,
			"title":       title,
			"description": description,
			"folder_id":   folderID,
			"stage_id":    folderID,
			"project_id":  projectID,
			"company_id":  workspaceID,
			"status":      "active",
			"time_range": map[string]any{
				"select":          "online",
				"start":           now,
				"stop":            now,
				"timezone_offset": tzOffset,
			},
		}

		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "get-dashboard":
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		op := map[string]any{
			"obj":        "dashboard",
			"type":       "show",
			"obj_id":     dashboardID,
			"company_id": workspaceID,
		}

		v := NewValidator(0)
		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "add-chart":
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		name, err := strArg(args, "name")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		chartType, err := strArg(args, "chart_type")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		seriesStr, err := strArg(args, "series")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		var series []interface{}
		if err := json.Unmarshal([]byte(seriesStr), &series); err != nil {
			return fmt.Sprintf("Error parsing series JSON: %v", err), true
		}

		op := map[string]any{
			"obj":          "chart",
			"type":         "create",
			"obj_type":     chartType,
			"dashboard_id": dashboardID,
			"name":         name,
			"description":  "",
			"series":       series,
			"company_id":   workspaceID,
		}

		v := NewValidator(0)
		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "modify-chart":
		chartID, err := strArg(args, "chart_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		title, err := strArg(args, "name")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		chartType, err := strArg(args, "chart_type")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		seriesStr, err := strArg(args, "series")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		var series []interface{}
		if err := json.Unmarshal([]byte(seriesStr), &series); err != nil {
			return fmt.Sprintf("Error parsing series JSON: %v", err), true
		}

		op := map[string]any{
			"obj":          "chart",
			"type":         "modify",
			"obj_type":     chartType,
			"obj_id":       chartID,
			"dashboard_id": dashboardID,
			"name":         title,
			"description":  "",
			"series":       series,
			"company_id":   workspaceID,
		}

		v := NewValidator(0)
		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "get-chart":
		chartID, err := strArg(args, "chart_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		op := map[string]any{
			"obj":          "chart",
			"type":         "get",
			"obj_id":       chartID,
			"dashboard_id": dashboardID,
			"company_id":   workspaceID,
		}

		v := NewValidator(0)
		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "set-dashboard-layout":
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		gridStr, err := strArg(args, "grid")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		tzOffset := 0
		if tz, ok := args["timezone_offset"]; ok {
			if tzFloat, ok := tz.(float64); ok {
				tzOffset = int(tzFloat)
			} else if tzStr, ok := tz.(string); ok {
				tzOffset, _ = strconv.Atoi(tzStr)
			}
		}

		// Parse grid input: [{chart_id, x, y, width, height}]
		var gridInput []map[string]interface{}
		if err := json.Unmarshal([]byte(gridStr), &gridInput); err != nil {
			return fmt.Sprintf("Error parsing grid JSON: %v", err), true
		}

		// Convert chart_id -> obj_id for the API payload
		apiGrid := make([]map[string]interface{}, 0, len(gridInput))
		for _, entry := range gridInput {
			chartHexID, _ := entry["chart_id"].(string)
			if chartHexID == "" {
				return "Error: each grid entry must have a non-empty chart_id", true
			}
			gridEntry := map[string]interface{}{
				"obj_id": chartHexID,
			}
			for _, field := range []string{"x", "y", "width", "height"} {
				if v, ok := entry[field]; ok {
					gridEntry[field] = v
				}
			}
			apiGrid = append(apiGrid, gridEntry)
		}

		op := map[string]any{
			"obj":        "dashboard",
			"type":       "modify",
			"obj_id":     dashboardID,
			"company_id": workspaceID,
			"time_range": map[string]any{
				"select":          "online",
				"timezone_offset": tzOffset,
			},
			"grid": apiGrid,
		}

		v := NewValidator(0)
		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	default:
		return fmt.Sprintf("Unknown tool: %s", name), true
	}
}

// resolveFolderIDFromDir looks for a file matching <id>_<name>.folder.json or
// <id>_<name>.stage.json in the given directory and returns the numeric id.
func resolveFolderIDFromDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("failed to read directory '%s': %v", dir, err)
	}
	reFolderFile := regexp.MustCompile(`^(\d+)_.*\.(folder|stage)\.json$`)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := reFolderFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		id, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		return id, nil
	}
	return 0, fmt.Errorf("no <id>_<name>.folder.json file found in '%s'; cannot determine folder ID", dir)
}

// intArg extracts an integer argument from args map.
func intArg(args map[string]interface{}, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing required argument: %s", key)
	}
	switch val := v.(type) {
	case float64:
		return int(val), nil
	case int:
		return val, nil
	case string:
		n, err := strconv.Atoi(val)
		if err != nil {
			return 0, fmt.Errorf("argument %s must be an integer, got: %s", key, val)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("argument %s has unexpected type %T", key, v)
	}
}

// strArg extracts a string argument from args map.
func strArg(args map[string]interface{}, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string, got %T", key, v)
	}
	return s, nil
}

// optStrArg returns the string value of an optional argument, or "" if absent.
func optStrArg(args map[string]interface{}, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// resolveProcessPath resolves an optional process_path argument.
// If the argument is empty, it searches the current directory for a single
// .conv.json file and returns its path. Relative paths are accepted as-is.
func resolveProcessPath(args map[string]interface{}, key string) (string, error) {
	p := optStrArg(args, key)
	if p != "" {
		return p, nil
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		return "", fmt.Errorf("cannot read current directory: %v", err)
	}
	var matches []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".conv.json") {
			matches = append(matches, e.Name())
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple .conv.json files found in current directory — pass process_path explicitly: %v", matches)
	}
	return "", fmt.Errorf("no .conv.json file found in current directory and process_path was not provided")
}

// resolveDirPath returns the path argument or "." if absent.
func resolveDirPath(args map[string]interface{}, key string) string {
	p := optStrArg(args, key)
	if p == "" {
		return "."
	}
	return p
}
