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
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type mcpTool struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description"`
	InputSchema interface{}      `json:"inputSchema"`
	Annotations *toolAnnotations `json:"annotations,omitempty"`
}

// toolAnnotations carries the MCP tool annotations (spec 2025-03-26 and
// later) that let clients and models reason about a tool's safety before
// invoking it. The hints are *bool on purpose: an omitted hint means
// "unknown", while false is a positive claim ("this tool is not
// destructive"). Plain bools would serialise every unset hint as false and
// turn every silence into a claim.
type toolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// boolPtr returns a pointer to b, for the *bool annotation hints.
func boolPtr(b bool) *bool { return &b }

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// mcpToolResult is a complete tools/call result. Build it with newToolResult
// so resultType and the serverInfo _meta are never missing.
type mcpToolResult struct {
	ResultType string                 `json:"resultType"`
	Content    []mcpContent           `json:"content"`
	IsError    bool                   `json:"isError,omitempty"`
	Meta       map[string]interface{} `json:"_meta,omitempty"`
}

// serverVersion is the version reported in MCP initialize responses and in
// analytics/feedback events. It derives from main.Version, which the release
// workflow injects via -ldflags "-X main.Version=${GITHUB_REF_NAME}" — so it
// arrives tag-shaped ("v2.11.0") and is normalized here to the manifest shape
// ("2.11.0"). Local and test builds report "dev".
//
// Never reintroduce a hand-maintained version constant here: it silently
// drifts from the manifests (see version_guard_test.go, which enforces this).
func serverVersion() string {
	return strings.TrimPrefix(Version, "v")
}

// oauthClientID is the OAuth2 client ID used for PKCE flow.
// Resolved from COREZOID_OAUTH_CLIENT_ID env var, falling back to the built-in default.
var oauthClientID string

// serverWriter is the MCP stdout writer, shared across all goroutines.
var serverWriter *bufio.Writer
var serverWriteMu sync.Mutex

// pendingReqs maps elicitation request IDs to response channels. Only legacy
// stdio clients ever get a server-initiated request (see stdioElicitor).
var pendingReqs sync.Map

// reqCounter generates unique IDs for server-initiated requests.
var reqCounter int64

func nextReqCounter() int64 { return atomic.AddInt64(&reqCounter, 1) }

// clientStateMu guards clientSupportsElicitation, clientName, and
// clientVersion — the legacy (2025-03-26) client state. A modern request
// carries the same facts in its own _meta and never reads these. They're
// written once per connection during the initialize
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

// clientIdentity is a transport-neutral client name/version pair.
type clientIdentity struct {
	Name    string
	Version string
}

// clientIdentityContextKey holds the per-request clientIdentity taken from
// _meta.io.modelcontextprotocol/clientInfo (see runToolCall).
const clientIdentityContextKey contextKey = "mcpClientIdentity"

// clientIdentityFor resolves the calling client's identity for the given
// request context: the request's own _meta clientInfo when it carried one
// (runToolCall attaches it). HTTP mode can serve multiple concurrent MCP clients
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

// mcpProtocolVersion is the MCP protocol revision this server negotiates in the
// legacy connection-scoped `initialize` handshake.
const mcpProtocolVersion = "2025-03-26"

// mcpSupportedProtocolVersions lists every protocol revision this server can
// serve, newest first. It is reported by `server/discover` and carried in
// UnsupportedProtocolVersionError's data.supported.
//
// Returns a fresh slice per call so a caller mutating the result can't corrupt
// what the next caller sees.
func mcpSupportedProtocolVersions() []string {
	return []string{mcpModernProtocolVersion, mcpProtocolVersion}
}

// buildInitializeResult returns the legacy `initialize` result payload. Shared
// by the stdio (runMCPServer) and HTTP (httpDispatch) dispatchers so the
// advertised capabilities and serverInfo can never drift between transports.
// initialize is optional: nothing a modern request needs depends on it.
func buildInitializeResult() map[string]interface{} {
	return completeResult(map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{},
			"resources": map[string]interface{}{},
			"prompts":   map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    mcpServerName,
			"version": serverVersion(),
		},
	})
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

// runMCPServer starts an MCP server over stdin/stdout using newline-delimited JSON-RPC 2.0.
func runMCPServer() {
	oauthClientID = oauthDefaultClientID
	if v := os.Getenv("COREZOID_OAUTH_CLIENT_ID"); v != "" {
		oauthClientID = v
	}
	serverWriter = bufio.NewWriter(os.Stdout)

	// Refresh in-memory auth state from ~/.corezoid/config.json in case another
	// process wrote credentials since startup's loadConfig. Startup is
	// single-goroutine, but we still take the lock so the race detector sees
	// a consistent ordering with later concurrent reads.
	_, snapToken, _, _, _ := authSnapshot()
	if snapToken == "" {
		syncGlobalsFromCurrent()
		_, snapToken, _, _, _ = authSnapshot()
		if snapToken != "" {
			f := Current()
			expiry := ""
			if f != nil && !f.ExpiresAt.IsZero() {
				expiry = ", expires " + f.ExpiresAt.Format("2006-01-02 15:04")
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

		if strings.HasPrefix(req.Method, "notifications/") {
			// notifications/initialized and notifications/roots/list_changed
			// are accepted and ignored; only cancellation means anything here.
			if req.Method == "notifications/cancelled" {
				var cancelParams struct {
					RequestID interface{} `json:"requestId"`
				}
				if err := json.Unmarshal(req.Params, &cancelParams); err == nil && cancelParams.RequestID != nil {
					if cancel, ok := activeCancels.LoadAndDelete(cancelParams.RequestID); ok {
						cancel.(context.CancelFunc)()
					}
				}
			}
			continue
		}

		if req.Method == "initialize" {
			// Legacy handshake: read client capabilities and identity.
			supportsElicitation, name, version := parseInitializeParams(req.Params)
			logger.Info("initialize: clientSupportsElicitation=%v clientName=%q clientVersion=%q", supportsElicitation, name, version)
			serverSend(rpcResult(req.ID, buildInitializeResult(), nil))
			continue
		}

		meta, metaErr := parseRequestMeta(req.Params)
		if metaErr != nil {
			serverSend(rpcResult(req.ID, nil, metaErr))
			continue
		}

		if result, rpcErr, ok := serveCommonMethod(req); ok {
			serverSend(rpcResult(req.ID, result, rpcErr))
			continue
		}

		switch req.Method {
		case "tools/call":
			var params toolCallParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				sendError(req.ID, -32602, "invalid params: "+err.Error())
				continue
			}
			// Server-initiated elicitation is only for a legacy client that
			// declared it in initialize; a modern client gets MRTR instead.
			var legacy elicitor
			if !meta.Modern && clientElicitationSupported() {
				legacy = stdioElicitor{}
			}

			// Run in a goroutine so the scanner loop can continue reading —
			// this is required to route legacy elicitation responses back to
			// the handler, and lets notifications/cancelled abort the call
			// (handleToolCall threads ctx down into the HTTP requests).
			ctx, cancel := context.WithCancel(context.Background())
			activeCancels.Store(req.ID, cancel)
			go func(reqID interface{}, ctx context.Context) {
				defer activeCancels.Delete(reqID)
				defer cancel()
				result, rpcErr := runToolCall(ctx, params, meta, legacy, true)
				if ctx.Err() != nil {
					return // cancelled: no response is sent
				}
				serverSend(rpcResult(reqID, result, rpcErr))
			}(req.ID, ctx)

		case "subscriptions/listen":
			if !meta.Modern {
				sendError(req.ID, -32601, "method not found: "+req.Method)
				continue
			}
			filter, ferr := parseSubscriptionFilter(req.Params)
			if ferr != nil {
				serverSend(rpcResult(req.ID, nil, ferr))
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			activeCancels.Store(req.ID, cancel)
			go func(subID interface{}) {
				defer activeCancels.Delete(subID)
				defer cancel()
				_ = runSubscription(ctx, subID, filter, func(msg interface{}) error {
					serverSend(msg)
					return nil
				})
			}(req.ID)

		default:
			// Includes ping and logging/setLevel, which MCP 2026-07-28 removed.
			serverSend(rpcResult(req.ID, nil, methodNotFound(req.Method)))
		}
	}
}
