package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// httpToolCallTimeout caps the duration of any single tool call served over
// the HTTP transport. Without a cap, a stuck Corezoid API call (e.g. a slow
// pull-folder against a very large workspace) would hold the request goroutine
// open until the client gave up. 10 minutes matches the existing elicitation
// timeout and is well above any reasonable interactive tool call.
const httpToolCallTimeout = 10 * time.Minute

// httpSessionIDContextKey holds the Mcp-Session-Id string for the current
// request, attached in httpHandlePost so httpDispatch can resolve it into a
// per-session client identity without needing the *http.Request itself.
const httpSessionIDContextKey contextKey = "mcpHTTPSessionID"

// httpClientIdentity is one HTTP session's declared client name/version,
// plus when it was last confirmed active (for idle eviction).
type httpClientIdentity struct {
	Name     string
	Version  string
	LastSeen time.Time
}

const httpSessionIdleTimeout = 1 * time.Hour
const httpSessionSweepInterval = 10 * time.Minute

// httpSessionsMu guards httpSessions. The Streamable HTTP transport can serve
// multiple concurrent MCP clients against one server process — unlike stdio,
// where one process is definitionally one client — so client identity has to
// be tracked per session (keyed by the Mcp-Session-Id minted at initialize)
// rather than as a process-global, or whichever client's handshake ran most
// recently would silently overwrite every other connected client's identity.
var httpSessionsMu sync.Mutex
var httpSessions = map[string]httpClientIdentity{}

// registerHTTPSession stores/refreshes the client identity for a session,
// called once from httpDispatch's initialize case.
func registerHTTPSession(sessionID, name, version string) {
	if sessionID == "" {
		return
	}
	httpSessionsMu.Lock()
	defer httpSessionsMu.Unlock()
	httpSessions[sessionID] = httpClientIdentity{Name: name, Version: version, LastSeen: time.Now()}
}

// httpSessionIdentity returns the stored identity for a session and refreshes
// its LastSeen timestamp, so an actively-used session is never evicted by
// sweepIdleHTTPSessions regardless of how long ago it last called initialize.
func httpSessionIdentity(sessionID string) (identity httpClientIdentity, ok bool) {
	if sessionID == "" {
		return httpClientIdentity{}, false
	}
	httpSessionsMu.Lock()
	defer httpSessionsMu.Unlock()
	id, found := httpSessions[sessionID]
	if !found {
		return httpClientIdentity{}, false
	}
	id.LastSeen = time.Now()
	httpSessions[sessionID] = id
	return id, true
}

// endHTTPSession removes a session's tracked identity. Called when the
// client sends DELETE /mcp per the Streamable HTTP session-termination flow.
func endHTTPSession(sessionID string) {
	if sessionID == "" {
		return
	}
	httpSessionsMu.Lock()
	defer httpSessionsMu.Unlock()
	delete(httpSessions, sessionID)
}

// sweepIdleHTTPSessions periodically evicts sessions that haven't been seen
// in httpSessionIdleTimeout, so a client that vanishes without sending DELETE
// (crash, dropped connection, a client that doesn't implement session
// termination) doesn't leak memory in httpSessions forever. Started once
// from runHTTPServer.
func sweepIdleHTTPSessions() {
	ticker := time.NewTicker(httpSessionSweepInterval)
	defer ticker.Stop()
	for range ticker.C {
		pruneIdleHTTPSessions(time.Now().Add(-httpSessionIdleTimeout))
	}
}

// pruneIdleHTTPSessions removes every session last seen before cutoff. Split
// out from sweepIdleHTTPSessions so the eviction logic is directly testable
// without waiting on the real ticker interval.
func pruneIdleHTTPSessions(cutoff time.Time) {
	httpSessionsMu.Lock()
	defer httpSessionsMu.Unlock()
	for id, ci := range httpSessions {
		if ci.LastSeen.Before(cutoff) {
			delete(httpSessions, id)
		}
	}
}

// runHTTPServer starts the Streamable-HTTP MCP transport on addr.
// Activate by setting COREZOID_HTTP_PORT (e.g. "8080").
// In hosted environments credentials must be pre-configured via env vars;
// the login tool (browser OAuth) is not usable from a remote server.
func runHTTPServer(addr string) error {
	go sweepIdleHTTPSessions()

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
	initOAuthClientID()

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
		endHTTPSession(r.Header.Get("Mcp-Session-Id"))
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

	// A new logical session begins at every initialize; the server mints the
	// ID and the client is expected to echo it back on every later request
	// for this session (per the Streamable HTTP transport spec). Otherwise
	// use whatever the client already sent — an empty string here just means
	// httpDispatch's session lookups below will all miss, falling back to
	// the process-global client identity.
	sessionID := r.Header.Get("Mcp-Session-Id")
	if req.Method == "initialize" {
		sessionID = generateUUIDv4()
		w.Header().Set("Mcp-Session-Id", sessionID)
	} else if sessionID != "" && !strings.HasPrefix(req.Method, "notifications/") {
		// The client is asserting an existing session (evicted by idle
		// eviction, orphaned by a server restart, or simply invented) that we
		// don't recognize. Per the Streamable HTTP spec this must be a 404,
		// not a silent degrade — a compliant client treats 404 as a signal to
		// discard the stale ID and call initialize again on its own, instead
		// of quietly getting worse analytics attribution forever. A request
		// with NO session header at all is a different case (a client that
		// never implemented the session protocol) and is intentionally left
		// alone here — it still falls back to the global client identity via
		// clientIdentityFor, same as before this check existed. Notifications
		// are exempt: they get no JSON-RPC response to signal recovery on,
		// and 202-with-empty-body is already the correct reply either way.
		if _, found := httpSessionIdentity(sessionID); !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}

	ctx := r.Context()
	if sessionID != "" {
		ctx = context.WithValue(ctx, httpSessionIDContextKey, sessionID)
	}

	resp := httpDispatch(ctx, req)
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
		// Read client identity for analytics attribution. Elicitation support
		// is intentionally ignored here — the comment on httpHandleSSE explains
		// why server-initiated elicitation isn't wired up over HTTP.
		//
		// parseInitializeParams also updates the process-global clientName/
		// clientVersion (shared with stdio). In HTTP mode that global is no
		// longer authoritative — registerHTTPSession below is — but it's kept
		// as clientIdentityFor's fallback for a request that arrives without
		// a recognized session, and for the initialize log line right below.
		_, name, version := parseInitializeParams(req.Params)
		if sid, ok := reqCtx.Value(httpSessionIDContextKey).(string); ok {
			registerHTTPSession(sid, name, version)
		}
		logger.Info("initialize: clientName=%q clientVersion=%q", name, version)
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
		sid, hasSID := reqCtx.Value(httpSessionIDContextKey).(string)
		identity, foundSession := httpSessionIdentity(sid)
		if foundSession {
			callCtx = context.WithValue(callCtx, clientIdentityContextKey, clientIdentity{Name: identity.Name, Version: identity.Version})
		} else {
			// No (or unrecognized) Mcp-Session-Id — falls back to the
			// process-global client identity in clientIdentityFor, same as
			// before this fix. Logged so the degrade path is diagnosable.
			logger.Debug("tools/call: no session identity for sessionID=%q (hasSID=%v) — falling back to global client identity", sid, hasSID)
		}
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
