package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// httpToolCallTimeout caps the duration of any single tool call served over
// the HTTP transport. Without a cap, a stuck Corezoid API call (e.g. a slow
// pull-folder against a very large workspace) would hold the request goroutine
// open until the client gave up. 10 minutes matches the existing elicitation
// timeout and is well above any reasonable interactive tool call.
const httpToolCallTimeout = 10 * time.Minute

// runHTTPServer starts the Streamable-HTTP MCP transport on addr.
// Activate by setting COREZOID_HTTP_PORT (e.g. "8080").
// In hosted environments credentials must be pre-configured via env vars;
// the login tool (browser OAuth) is not usable from a remote server.
func runHTTPServer(addr string) error {
	// Load saved OAuth credentials the same way stdio mode does.
	// Lock the check-then-set so the race detector sees a happens-before edge
	// against the HTTP handlers we're about to start.
	_, snapToken, _, _, _ := authSnapshot()
	if snapToken == "" {
		if creds, err := loadCredentials(); err == nil && creds != nil && !isCredentialsExpired(creds) {
			withAuthLock(func() { apiToken = creds.AccessToken })
			logger.Info("http: loaded saved credentials")
		}
	}
	if oauthClientID == "" {
		oauthClientID = oauthDefaultClientID
	}
	if v := os.Getenv("COREZOID_OAUTH_CLIENT_ID"); v != "" {
		oauthClientID = v
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", corsWrap(httpMCPEndpoint))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	logger.Info("MCP HTTP transport listening on %s", addr)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func corsWrap(next http.HandlerFunc) http.HandlerFunc {
	allowed := os.Getenv("COREZOID_HTTP_ALLOWED_ORIGINS")
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && allowed != "" {
			for _, o := range strings.Split(allowed, ",") {
				if strings.TrimSpace(o) == origin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					break
				}
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Accept, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func httpMCPEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		httpHandlePost(w, r)
	case http.MethodGet:
		httpHandleSSE(w, r)
	case http.MethodDelete:
		// Session termination — stateless server has nothing to tear down.
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func httpHandlePost(w http.ResponseWriter, r *http.Request) {
	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeHTTPJSONRPC(w, httpJSONRPCError(nil, -32700, "parse error: "+err.Error()))
		return
	}

	resp := httpDispatch(r.Context(), req)
	if resp == nil {
		// Notification — accepted, no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeHTTPJSONRPC(w, resp)
}

// httpHandleSSE opens a server-sent-event stream per the Streamable HTTP spec.
// In this implementation the stream only carries the required endpoint event;
// server-initiated messages (elicitation) are not supported over HTTP.
func httpHandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "event: endpoint\ndata: /mcp\n\n")
	flusher.Flush()
	<-r.Context().Done()
}

// httpDispatch routes an MCP JSON-RPC request and returns the response object.
// Returns nil for notifications that require no response.
//
// reqCtx is the HTTP request's context. For tools/call we derive a timeout-
// bounded child so a wedged Corezoid API call can't pin the goroutine open
// past httpToolCallTimeout. If the client disconnects the underlying r.Context
// fires first and propagates cancellation downstream the same way.
func httpDispatch(reqCtx context.Context, req mcpRequest) interface{} {
	switch req.Method {
	case "initialize":
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2025-03-26",
				"capabilities": map[string]interface{}{
					"tools":     map[string]interface{}{},
					"resources": map[string]interface{}{},
					"prompts":   map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "convctl-mcp",
					"version": mcpServerVersion,
				},
			},
		}

	case "notifications/initialized", "notifications/cancelled":
		return nil

	case "tools/list":
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"tools": toolRegistry},
		}

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return httpJSONRPCError(req.ID, -32602, "invalid params: "+err.Error())
		}
		callCtx, cancel := context.WithTimeout(reqCtx, httpToolCallTimeout)
		defer cancel()
		result, isErr := handleToolCall(callCtx, params.Name, params.Arguments)
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolResult{
				Content: []mcpContent{{Type: "text", Text: result}},
				IsError: isErr,
			},
		}

	case "resources/list":
		resources, err := listResources()
		if err != nil {
			return httpJSONRPCError(req.ID, -32603, err.Error())
		}
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"resources": resources},
		}

	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return httpJSONRPCError(req.ID, -32602, "invalid params: "+err.Error())
		}
		content, err := readResource(params.URI)
		if err != nil {
			return httpJSONRPCError(req.ID, -32603, err.Error())
		}
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"contents": []interface{}{content}},
		}

	case "prompts/list":
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"prompts": builtinPrompts},
		}

	case "prompts/get":
		var params struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return httpJSONRPCError(req.ID, -32602, "invalid params: "+err.Error())
		}
		result, err := getPrompt(params.Name, params.Arguments)
		if err != nil {
			return httpJSONRPCError(req.ID, -32603, err.Error())
		}
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	default:
		return httpJSONRPCError(req.ID, -32601, "method not found: "+req.Method)
	}
}

func httpJSONRPCError(id interface{}, code int, msg string) mcpResponse {
	return mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpError{Code: code, Message: msg},
	}
}

func writeHTTPJSONRPC(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
