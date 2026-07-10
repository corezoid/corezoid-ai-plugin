package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// mcpServerVersion is the version reported in MCP initialize responses.
// Keep this in sync with .claude-plugin/plugin.json.
const mcpServerVersion = "2.3.5"

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

// clientStateMu guards clientSupportsElicitation, clientName, and
// clientVersion. They're written once per connection during the initialize
// handshake but read afterwards from concurrent tool-call goroutines in HTTP
// mode (net/http dispatches each request on its own goroutine, same reason
// authStateMu exists for the auth-state globals above). Read paths take
// RLock; the write path (parseInitializeParams) takes Lock.
var clientStateMu sync.RWMutex

// clientSupportsElicitation is set during initialize based on the client's
// declared capabilities. Read it via clientElicitationSupported(), never
// directly — see clientStateMu.
var clientSupportsElicitation bool

// clientName and clientVersion capture the connecting MCP client's declared
// identity (e.g. "Claude Code", "1.2.3") from the initialize handshake, for
// attribution in analytics events. Empty if the client omitted clientInfo.
// Read them via clientIdentitySnapshot(), never directly — see clientStateMu.
var clientName string
var clientVersion string

// clientElicitationSupported reports whether the connected client declared
// elicitation support during initialize.
func clientElicitationSupported() bool {
	clientStateMu.RLock()
	defer clientStateMu.RUnlock()
	return clientSupportsElicitation
}

// clientIdentitySnapshot returns the connected client's declared name and
// version, taken under the read lock. In HTTP mode this reflects whichever
// session last called initialize — callers that have a per-request ctx
// should use clientIdentityFor instead, which is session-aware.
func clientIdentitySnapshot() (name, version string) {
	clientStateMu.RLock()
	defer clientStateMu.RUnlock()
	return clientName, clientVersion
}

// contextKey namespaces values stored on context.Context so they can't
// collide with keys another package might use.
type contextKey string

// clientIdentity is a transport-neutral client name/version pair. HTTP mode
// converts its session-store entry (mcp_http.go's httpClientIdentity, which
// also carries bookkeeping like LastSeen that has no meaning here) into this
// shape before attaching it to a request context.
type clientIdentity struct {
	Name    string
	Version string
}

// clientIdentityContextKey holds a resolved per-session clientIdentity,
// attached to the context for a tools/call request in HTTP mode once the
// caller's session has been resolved (see httpDispatch's tools/call case).
const clientIdentityContextKey contextKey = "mcpClientIdentity"

// clientIdentityFor resolves the calling client's identity for the given
// request context. HTTP mode can serve multiple concurrent MCP clients
// against one server process — net/http dispatches each request on its own
// goroutine — so a single process-global clientName/clientVersion (as used
// by clientIdentitySnapshot) would let whichever client initialized most
// recently silently overwrite every other connected client's attribution in
// analytics. If ctx carries a resolved per-session identity, that's used;
// otherwise this falls back to the process-global snapshot, which is always
// correct for stdio (one process is definitionally one client) and is the
// best available answer for an HTTP request that arrived without a
// recognized session.
func clientIdentityFor(ctx context.Context) (name, version string) {
	if ci, ok := ctx.Value(clientIdentityContextKey).(clientIdentity); ok {
		return ci.Name, ci.Version
	}
	return clientIdentitySnapshot()
}

// parseInitializeParams extracts elicitation support and client identity from
// an initialize request's params and stores them under clientStateMu. Shared
// by the stdio and HTTP transports. Fields the client omitted decode to ""
// (clientInfo is optional in the MCP spec); if raw itself fails to parse, the
// stored values are left unchanged. Returns the values it stored so callers
// can log them without a second locked read.
func parseInitializeParams(raw json.RawMessage) (supportsElicitation bool, name, version string) {
	var params struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
		ClientInfo   struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}

	clientStateMu.Lock()
	defer clientStateMu.Unlock()
	if err := json.Unmarshal(raw, &params); err == nil {
		_, clientSupportsElicitation = params.Capabilities["elicitation"]
		clientName = params.ClientInfo.Name
		clientVersion = params.ClientInfo.Version
	}
	return clientSupportsElicitation, clientName, clientVersion
}

// activeCancels maps in-progress tools/call request IDs to their cancel functions.
var activeCancels sync.Map

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

// runMCPServer starts an MCP server over stdin/stdout using newline-delimited JSON-RPC 2.0.
func runMCPServer() {
	oauthClientID = oauthDefaultClientID
	if v := os.Getenv("COREZOID_OAUTH_CLIENT_ID"); v != "" {
		oauthClientID = v
	}
	serverWriter = bufio.NewWriter(os.Stdout)

	// Auto-load saved credentials if no token is configured via env.
	// loadCredentials reads from env vars already populated by findAndLoadDotEnv().
	// Startup is single-goroutine, but we still take the lock so the race
	// detector sees a consistent ordering with later concurrent reads.
	_, snapToken, _, _, _ := authSnapshot()
	if snapToken == "" {
		if creds, err := loadCredentials(); err == nil && creds != nil && !isCredentialsExpired(creds) {
			withAuthLock(func() { apiToken = creds.AccessToken })
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
			// Read client capabilities and identity (elicitation support, name/version).
			supportsElicitation, name, version := parseInitializeParams(req.Params)
			logger.Info("initialize: clientSupportsElicitation=%v clientName=%q clientVersion=%q", supportsElicitation, name, version)

			serverSend(mcpResponse{
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
			})

		case "notifications/initialized":
			// no response needed for notifications

		case "tools/list":
			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"tools": toolRegistry,

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
			// The ctx is now actually consumed: handleToolCall threads it down
			// into Executor.req → http.NewRequestWithContext, so a client-side
			// notifications/cancelled aborts the in-flight HTTP call instead
			// of just orphaning the goroutine.
			ctx, cancel := context.WithCancel(context.Background())
			activeCancels.Store(req.ID, cancel)
			go func(reqID interface{}, name string, args map[string]interface{}, ctx context.Context) {
				defer activeCancels.Delete(reqID)
				defer cancel()
				result, isErr := handleToolCall(ctx, name, args)
				serverSend(mcpResponse{
					JSONRPC: "2.0",
					ID:      reqID,
					Result: mcpToolResult{
						Content: []mcpContent{{Type: "text", Text: result}},
						IsError: isErr,
					},
				})
			}(req.ID, params.Name, params.Arguments, ctx)

		case "notifications/cancelled":
			var cancelParams struct {
				RequestID interface{} `json:"requestId"`
			}
			if err := json.Unmarshal(req.Params, &cancelParams); err == nil && cancelParams.RequestID != nil {
				if cancel, ok := activeCancels.LoadAndDelete(cancelParams.RequestID); ok {
					cancel.(context.CancelFunc)()
				}
			}
			// notifications require no response

		case "resources/list":
			resources, err := listResources()
			if err != nil {
				sendError(req.ID, -32603, err.Error())
				continue
			}
			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{"resources": resources},
			})

		case "resources/read":
			var rParams struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(req.Params, &rParams); err != nil {
				sendError(req.ID, -32602, "invalid params: "+err.Error())
				continue
			}
			content, err := readResource(rParams.URI)
			if err != nil {
				sendError(req.ID, -32603, err.Error())
				continue
			}
			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{"contents": []interface{}{content}},
			})

		case "prompts/list":
			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{"prompts": builtinPrompts},
			})

		case "prompts/get":
			var pParams struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &pParams); err != nil {
				sendError(req.ID, -32602, "invalid params: "+err.Error())
				continue
			}
			prompt, err := getPrompt(pParams.Name, pParams.Arguments)
			if err != nil {
				sendError(req.ID, -32603, err.Error())
				continue
			}
			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  prompt,
			})

		default:
			sendError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}
