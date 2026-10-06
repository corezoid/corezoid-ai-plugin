package main

// Hosted mode: a remote, multi-tenant MCP endpoint (e.g. https://mcp.corezoid.com/mcp)
// for clients that only speak MCP over a URL — the OpenAI plugin directory,
// claude.ai connectors. Enabled by COREZOID_HOSTED_ADDR; without it nothing in
// this file runs and stdio / the local HTTP transport behave exactly as before.
//
// What differs from the local modes, and why:
//
//   - Credentials come from every request, never from ~/.corezoid or globals.
//     The caller's OAuth token (account.corezoid.com, the same issuer the
//     login tool uses) arrives as "Authorization: Bearer <token>"; company and
//     stage arrive in a "scope" argument on the tool call. NewValidator builds
//     the Executor from that alone.
//   - No local state: no config reads or writes, no project-id cache, no
//     analytics, no MCP sessions — any replica serves any request.
//   - Only tools that talk purely to the Corezoid API are offered
//     (hostedAllowedTools). Tools that read or write the working directory,
//     run git, or manage local credentials are neither listed nor callable.
//   - Requests without a token are refused with 401 and an RFC 9728 pointer
//     to the authorization server, so MCP clients can run OAuth themselves.
//   - The Corezoid API URL is fixed by the operator (COREZOID_HOSTED_API_URL);
//     nothing in a request can redirect the caller's token elsewhere.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// hostedMode is set once at startup, before any request is served, and only
// read afterwards.
var hostedMode bool

// hostedAPIURL is the Corezoid API base every hosted request uses.
var hostedAPIURL string

const (
	hostedDefaultAPIURL        = "https://admin.corezoid.com"
	hostedDefaultAuthServerURL = "https://account.corezoid.com"
	hostedEndpointPath         = "/mcp"
	hostedHealthzPath          = "/healthz"
	hostedScopeArg             = "scope"
)

// hostedAllowedTools are the tools offered in hosted mode: each reaches the
// Corezoid API only, with no filesystem, git, process or local-credential
// access on the server. Router actions are filtered against the same set.
// Adding a tool here is a security decision — check its handler first.
var hostedAllowedTools = map[string]struct{}{
	"add-chart": {}, "add-to-group": {}, "create-communications-orchestrator": {},
	"create-dashboard": {}, "create-group": {}, "create-project": {},
	"delete-api-key": {}, "delete-folder": {}, "delete-group": {},
	"delete-process": {}, "delete-project": {}, "delete-task": {},
	"deploy-stage": {}, "find-principal": {}, "get-chart": {},
	"get-dashboard": {}, "get-node-stat": {}, "invite-user": {},
	"list-aliases": {}, "list-api-keys": {}, "list-folders": {},
	"list-group-objects": {}, "list-groups": {}, "list-node-tasks": {},
	"list-projects": {}, "list-shares": {}, "list-stages": {},
	"list-task-history": {}, "list-variables": {}, "list-workspaces": {},
	"modify-api-key": {}, "modify-chart": {}, "modify-folder": {},
	"modify-group": {}, "modify-project": {}, "modify-task": {},
	"move-folder": {}, "move-process": {}, "pause-process": {},
	"remove-from-group": {}, "resume-process": {}, "set-dashboard-layout": {},
	"set-stage-immutable": {}, "share-object": {}, "show-folder": {},
	"show-process": {}, "show-project": {}, "show-task": {},
	// Hosted variants that move content through the request instead of the
	// working directory (hosted_process.go).
	"pull-process": {}, "lint-process": {}, "create-process": {}, "run-task": {},
}

// hostedStageOptional tools address their object directly (a process id, a
// folder id, or JSON in the request), so they need no scope.stage_id.
var hostedStageOptional = map[string]struct{}{
	"pull-process": {}, "lint-process": {}, "create-process": {}, "run-task": {},
}

// hostedScope is the per-request identity a hosted tool call runs with.
type hostedScope struct {
	Token     string // raw OAuth access token, without scheme
	CompanyID string // Corezoid company id; "" for a personal workspace
	StageID   int
}

const hostedScopeContextKey contextKey = "hostedScope"

func withHostedScope(ctx context.Context, s hostedScope) context.Context {
	return context.WithValue(ctx, hostedScopeContextKey, s)
}

func hostedScopeFrom(ctx context.Context) hostedScope {
	if ctx == nil {
		return hostedScope{}
	}
	s, _ := ctx.Value(hostedScopeContextKey).(hostedScope)
	return s
}

// bearerToken extracts the token from "Bearer <t>", "Simulator <t>" or a bare
// token. A scheme with no token is no token: HTTP stacks trim the trailing
// space, so "Bearer " arrives as the single word "Bearer".
func bearerToken(header string) string {
	fields := strings.Fields(header)
	switch {
	case len(fields) == 0:
		return ""
	case len(fields) == 1:
		if isAuthScheme(fields[0]) {
			return ""
		}
		return fields[0]
	case isAuthScheme(fields[0]) && len(fields) == 2:
		return fields[1]
	default:
		return "" // a token never contains whitespace
	}
}

func isAuthScheme(s string) bool {
	return strings.EqualFold(s, "Bearer") || strings.EqualFold(s, "Simulator")
}

// takeHostedScope removes the "scope" argument from args and returns the
// company and stage it names. The key is removed before router resolution and
// argument validation, which would otherwise reject it as unknown.
func takeHostedScope(args map[string]interface{}) (companyID string, stageID int, err error) {
	raw, ok := args[hostedScopeArg]
	if !ok {
		return "", 0, nil
	}
	delete(args, hostedScopeArg)
	m, ok := raw.(map[string]interface{})
	if !ok {
		return "", 0, errors.New("scope must be an object: {\"company_id\": \"…\", \"stage_id\": 123}")
	}
	for k := range m {
		if k != "company_id" && k != "stage_id" {
			return "", 0, fmt.Errorf("scope has unknown key %q (allowed: company_id, stage_id)", k)
		}
	}
	if v, ok := m["company_id"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return "", 0, errors.New("scope.company_id must be a string")
		}
		companyID = strings.TrimSpace(s)
	}
	if v, ok := m["stage_id"]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			if n <= 0 || n != math.Trunc(n) || n > math.MaxInt32 {
				return "", 0, errors.New("scope.stage_id must be a positive integer")
			}
			stageID = int(n)
		case string:
			id, convErr := strconv.Atoi(strings.TrimSpace(n))
			if convErr != nil || id <= 0 {
				return "", 0, errors.New("scope.stage_id must be a positive integer")
			}
			stageID = id
		default:
			return "", 0, errors.New("scope.stage_id must be a positive integer")
		}
	}
	return companyID, stageID, nil
}

// hostedGate is handleToolCall's auth gate in hosted mode: it replaces the
// local checks (pruneAbandonedFolder, ensureAuth), which read ~/.corezoid and
// the process globals.
func hostedGate(ctx context.Context, tool string) error {
	if _, ok := hostedAllowedTools[tool]; !ok {
		return fmt.Errorf("[Error] %s is not available on the hosted Corezoid server: it works with local files or credentials. Use the Corezoid plugin locally for it", tool)
	}
	s := hostedScopeFrom(ctx)
	if s.Token == "" {
		return errors.New("[Error] Not authenticated: the request carried no access token")
	}
	_, tokenOnly := tokenOnlyTools[tool]
	_, stageOptional := hostedStageOptional[tool]
	if !tokenOnly && !stageOptional && s.StageID == 0 {
		return fmt.Errorf("[Error] %s needs scope.stage_id (and scope.company_id for a company workspace). Find them with list-workspaces, list-projects and list-stages, then pass scope: {\"company_id\": \"…\", \"stage_id\": …}", tool)
	}
	return nil
}

// hostedToolRegistry is tools/list in hosted mode: allowed core tools as they
// are, routers rebuilt with only their allowed actions (a router left with no
// action is dropped), and every tool given the optional "scope" argument.
func hostedToolRegistry() []mcpTool {
	var defs []mcpTool
	for _, d := range coreToolDefs {
		if _, ok := hostedAllowedTools[d.Name]; ok {
			if override, ok := hostedToolDefs[d.Name]; ok {
				d = override
			}
			defs = append(defs, withScopeArg(d))
		}
	}
	for _, r := range toolRouters {
		kept := r
		kept.Actions = nil
		for _, a := range r.Actions {
			if _, ok := hostedAllowedTools[a.Action]; ok {
				kept.Actions = append(kept.Actions, a)
			}
		}
		if len(kept.Actions) > 0 {
			defs = append(defs, withScopeArg(kept.toolDef()))
		}
	}
	return defs
}

// withScopeArg returns a copy of d whose input schema also declares "scope".
// The shared registry is never mutated.
func withScopeArg(d mcpTool) mcpTool {
	schema, ok := d.InputSchema.(map[string]interface{})
	if !ok {
		return d
	}
	copied := make(map[string]interface{}, len(schema)+1)
	for k, v := range schema {
		copied[k] = v
	}
	props := map[string]interface{}{}
	if p, ok := schema["properties"].(map[string]interface{}); ok {
		for k, v := range p {
			props[k] = v
		}
	}
	props[hostedScopeArg] = map[string]interface{}{
		"type":        "object",
		"description": "Which Corezoid workspace and stage to act in. Required except for workspace, project and stage discovery (list-workspaces, list-projects, list-stages, …). Find the values with those tools.",
		"properties": map[string]interface{}{
			"company_id": map[string]interface{}{"type": "string", "description": "Company id from list-workspaces; omit for a personal workspace."},
			"stage_id":   map[string]interface{}{"type": "integer", "description": "Stage id from list-stages."},
		},
		"additionalProperties": false,
	}
	copied["properties"] = props
	d.InputSchema = copied
	return d
}

// hostedConfig is read from the environment once at startup.
type hostedConfig struct {
	Addr          string
	APIURL        string
	ResourceURL   string // public URL of this server; enables RFC 9728 discovery
	AuthServerURL string
}

func loadHostedConfig(addr string) (hostedConfig, error) {
	cfg := hostedConfig{
		Addr:          addr,
		APIURL:        envOrDefault("COREZOID_HOSTED_API_URL", hostedDefaultAPIURL),
		ResourceURL:   strings.TrimSpace(os.Getenv("COREZOID_HOSTED_RESOURCE_URL")),
		AuthServerURL: envOrDefault("COREZOID_HOSTED_AUTH_SERVER_URL", hostedDefaultAuthServerURL),
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	u, err := url.Parse(cfg.APIURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return cfg, fmt.Errorf("COREZOID_HOSTED_API_URL must be an https URL, got %q", cfg.APIURL)
	}
	return cfg, nil
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// runHostedMode serves the hosted endpoint until SIGINT/SIGTERM.
func runHostedMode(addr string) {
	logger.writer = os.Stderr
	logger.IsDebug = os.Getenv("COREZOID_DEBUG") != ""

	cfg, err := loadHostedConfig(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[corezoid-mcp] hosted: %v\n", err)
		os.Exit(1)
	}
	hostedMode = true
	hostedAPIURL = cfg.APIURL
	analyticsEnabled.Store(false)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newHostedHandler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "[corezoid-mcp] hosted mode %s on %s%s api=%s oauth-resource=%q tools=%d\n",
		buildIdentity(), cfg.Addr, hostedEndpointPath, cfg.APIURL, cfg.ResourceURL, len(hostedToolRegistry()))

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "[corezoid-mcp] hosted: %v\n", err)
			os.Exit(1)
		}
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// newHostedHandler builds the hosted HTTP surface. Split from runHostedMode
// so tests can drive it with httptest.
func newHostedHandler(cfg hostedConfig) http.Handler {
	metaPath, metaURL, metaDoc := "", "", []byte(nil)
	if cfg.ResourceURL != "" && cfg.AuthServerURL != "" {
		if u, err := url.Parse(cfg.ResourceURL); err == nil && u.Host != "" {
			metaPath = "/.well-known/oauth-protected-resource" + strings.TrimRight(u.EscapedPath(), "/")
			metaURL = u.Scheme + "://" + u.Host + metaPath
			metaDoc, _ = json.Marshal(map[string]interface{}{
				"resource":              cfg.ResourceURL,
				"authorization_servers": []string{cfg.AuthServerURL},
			})
		}
	}

	mux := http.NewServeMux()
	if metaPath != "" {
		mux.HandleFunc(metaPath, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(metaDoc)
		})
	}
	mux.HandleFunc(hostedHealthzPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc(hostedEndpointPath, func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			if metaURL != "" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+metaURL+`"`)
			}
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			// Stateless: no server-initiated stream and no session to delete.
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		hostedHandlePost(w, r, token)
	})
	return mux
}

func hostedHandlePost(w http.ResponseWriter, r *http.Request, token string) {
	r.Body = http.MaxBytesReader(w, r.Body, httpMaxBodyBytes)
	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeHTTPJSONRPC(w, httpJSONRPCError(nil, -32600, fmt.Sprintf("request body too large (limit %d bytes)", httpMaxBodyBytes)))
			return
		}
		writeHTTPJSONRPC(w, httpJSONRPCError(nil, -32700, "parse error"))
		return
	}
	resp := hostedDispatch(r.Context(), req, token)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeHTTPJSONRPC(w, resp)
}

// hostedDispatch answers one JSON-RPC message. No session is minted or
// required: every message stands on its own.
func hostedDispatch(ctx context.Context, req mcpRequest, token string) interface{} {
	switch req.Method {
	case "initialize":
		return mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: hostedInitializeResult()}
	case "ping":
		return mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	case "tools/list":
		return mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"tools": hostedToolRegistry()}}
	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return httpJSONRPCError(req.ID, -32602, "invalid params")
		}
		if params.Arguments == nil {
			params.Arguments = map[string]interface{}{}
		}
		companyID, stageID, err := takeHostedScope(params.Arguments)
		if err != nil {
			return hostedToolResult(req.ID, "[Error] "+err.Error(), true)
		}
		callCtx, cancel := context.WithTimeout(withHostedScope(ctx, hostedScope{Token: token, CompanyID: companyID, StageID: stageID}), httpToolCallTimeout)
		defer cancel()
		result, isErr := handleToolCall(callCtx, params.Name, params.Arguments)
		return hostedToolResult(req.ID, result, isErr)
	default:
		if req.ID == nil || strings.HasPrefix(req.Method, "notifications/") {
			return nil
		}
		return httpJSONRPCError(req.ID, -32601, "method not found: "+req.Method)
	}
}

func hostedToolResult(id interface{}, text string, isErr bool) mcpResponse {
	return mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}, IsError: isErr},
	}
}

// hostedInitializeResult advertises tools only: resources and prompts point
// at local workspace files and workflows that do not exist on the server.
func hostedInitializeResult() map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		"serverInfo":      map[string]interface{}{"name": "convctl-mcp", "version": serverVersion()},
	}
}
