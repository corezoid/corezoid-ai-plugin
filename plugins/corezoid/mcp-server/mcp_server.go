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

// mcpError is a JSON-RPC 2.0 error object. Data carries the optional free-form
// payload some MCP errors define (currently only UnsupportedProtocolVersionError,
// which puts the supported-version list there); it is omitted when nil so every
// pre-existing error site stays byte-identical on the wire.
type mcpError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
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

// mcpProtocolVersion is the MCP specification revision this server implements,
// and the version it answers every initialize handshake with. This server is
// deliberately legacy-only: MCP 2026-07-28 calls the handshake-based revisions
// (2025-11-25 and earlier) "legacy" and specifies how a client falls back to
// them, which is the mode we serve. Do not bump this until the modern stateless
// (_meta-based) request path is actually implemented. Distinct from
// mcpServerVersion, which is the plugin's own version and moves on every release.
const mcpProtocolVersion = "2025-03-26"

// errCodeUnsupportedProtocolVersion is the JSON-RPC error code MCP 2026-07-28
// assigns to UnsupportedProtocolVersionError. It sits in the JSON-RPC range
// reserved for pre-defined server errors (-32000..-32099), claimed exclusively
// by the MCP spec, so it cannot collide with anything else this server returns.
const errCodeUnsupportedProtocolVersion = -32022

// msgUnsupportedProtocolVersion is the message paired with
// errCodeUnsupportedProtocolVersion, spelled as in the spec.
const msgUnsupportedProtocolVersion = "Unsupported protocol version"

// mcpSupportedProtocolVersions returns the protocol revision(s) this server
// actually implements — what a client should put on the wire to talk to us. It
// backs the data.supported list of UnsupportedProtocolVersionError and the
// version list in the server/discover diagnostic, so the two can never
// disagree. Returns a fresh slice per call — callers hand it straight to the
// JSON encoder and must not be able to mutate a shared backing array.
func mcpSupportedProtocolVersions() []string {
	return []string{mcpProtocolVersion}
}

// mcpNegotiableProtocolVersions returns the published MCP revisions whose
// initialize handshake this server will serve. Deliberately wider than
// mcpSupportedProtocolVersions, because in the handshake-based ("legacy") era a
// version mismatch is a negotiation, not an error. The 2025-03-26 lifecycle
// spec:
//
//	"If the server supports the requested protocol version, it MUST respond
//	 with the same version. Otherwise, the server MUST respond with another
//	 protocol version it supports."
//
// and clients "SHOULD" ask for the latest revision they know. So any client
// newer than 2025-03-26 asks for a version we do not implement and must still
// get a normal handshake answered with mcpProtocolVersion — exactly what
// happened before the version check existed. Rejecting those would take the
// server offline for every up-to-date host.
//
// Only a version that is not a handshake revision at all earns
// UnsupportedProtocolVersionError: the modern 2026-07-28 and later (whose
// stateless semantics we genuinely cannot serve), or a nonsense string like the
// "1.0.0" in the lifecycle spec's own version-mismatch example.
//
// Add to this list when the working group publishes another handshake-based
// revision. Fresh slice per call, for the same reason as above.
func mcpNegotiableProtocolVersions() []string {
	return []string{"2024-10-07", "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}
}

// mcpCapabilities returns the capability set advertised in the handshake. Split
// out of buildInitializeResult so the server/discover diagnostic can report the
// same set without restating it.
//
// Deliberately no extensions key: an empty extensions map signals nothing, and
// this server implements no MCP extensions yet.
func mcpCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools":     map[string]interface{}{},
		"resources": map[string]interface{}{},
		"prompts":   map[string]interface{}{},
	}
}

// mcpServerInfo returns the server identity block advertised in the handshake.
func mcpServerInfo() map[string]interface{} {
	return map[string]interface{}{
		"name":    "convctl-mcp",
		"version": mcpServerVersion,
	}
}

// buildInitializeResult returns the result body of an initialize response. Both
// transports call it (stdio's runMCPServer loop and httpDispatch) so the
// handshake they advertise cannot drift apart. A fresh map per call.
func buildInitializeResult() map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    mcpCapabilities(),
		"serverInfo":      mcpServerInfo(),
	}
}

// discoverLegacyErrorData is the JSON-RPC error `data` this server attaches to
// server/discover, MCP 2026-07-28's era-detection probe.
//
// A legacy-only server must NOT answer that probe with a DiscoverResult. The
// stdio backward-compatibility rules classify the server purely from the
// probe's outcome: a DiscoverResult means "the server is modern. Select a
// mutually supported version from supportedVersions and continue", while "any
// other error, or does not respond within a reasonable timeout: the server is
// legacy. Fall back to the initialize handshake." A DiscoverResult from us
// would therefore label us modern and send the client hunting for a mutually
// supported *modern* version that does not exist, turning the spec's
// "dual-era client + legacy server → works" row into a hard failure. There is
// no honest DiscoverResult a legacy-only server can return.
//
// The spec expects exactly what this server already did — "legacy servers
// respond to unknown pre-initialize requests with implementation-defined errors
// (commonly -32601 or -32602) or not at all", and clients "MUST NOT" key the
// fallback to one specific code. So the era signal stays the -32601 that
// server/discover has always produced; what this adds is the diagnostic that
// bare error lacked: which era we are, which handshake revisions we speak, what
// we can do, and what the client should send instead.
func discoverLegacyErrorData() map[string]interface{} {
	return map[string]interface{}{
		"era":               "legacy",
		"supportedVersions": mcpSupportedProtocolVersions(),
		"capabilities":      mcpCapabilities(),
		"serverInfo":        mcpServerInfo(),
		"hint":              "this server implements the initialize-handshake (legacy) MCP revisions only; open the connection with an initialize request declaring one of supportedVersions",
	}
}

// protocolVersionNegotiable reports whether an initialize request declaring
// this protocolVersion can be served. An empty string means the client omitted
// the field, which plenty of older clients do and the legacy spec tolerates —
// treat it as acceptable and answer with our own version, as with every other
// negotiable revision.
func protocolVersionNegotiable(version string) bool {
	if version == "" {
		return true
	}
	for _, negotiable := range mcpNegotiableProtocolVersions() {
		if version == negotiable {
			return true
		}
	}
	return false
}

// unsupportedProtocolVersionData builds the data payload of
// UnsupportedProtocolVersionError, shaped as the MCP 2026-07-28 spec specifies:
// the versions we do support plus the one the client asked for, so the client
// can surface an actionable message instead of failing deeper into the
// handshake.
func unsupportedProtocolVersionData(requested string) map[string]interface{} {
	return map[string]interface{}{
		"supported": mcpSupportedProtocolVersions(),
		"requested": requested,
	}
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

// parseRequestedProtocolVersion extracts the protocolVersion an initialize
// request declared, or "" if the field was absent or the params don't parse
// (both of which are the tolerant path — see protocolVersionNegotiable).
//
// A sibling of parseInitializeParams rather than an extra return value on it:
// the version check has to run and reject *before* any client state is stored,
// and the existing signature has several call sites. The cost is one extra
// unmarshal of a handful of bytes, once per connection.
func parseRequestedProtocolVersion(raw json.RawMessage) string {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return ""
	}
	return params.ProtocolVersion
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

	// sendErrorWithData is the general form; sendError is the (far more common)
	// no-data case, kept with its original signature so none of the existing
	// -32601/-32602/-32603 call sites below have to change.
	sendErrorWithData := func(id interface{}, code int, msg string, data interface{}) {
		serverSend(mcpResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &mcpError{Code: code, Message: msg, Data: data},
		})
	}

	sendError := func(id interface{}, code int, msg string) {
		sendErrorWithData(id, code, msg, nil)
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
			// Reject a version we can't negotiate before storing any client
			// state, so a mismatched client can't leave its identity behind on a
			// connection that never completed the handshake. A newer *handshake*
			// revision is not a mismatch — see mcpNegotiableProtocolVersions.
			if requested := parseRequestedProtocolVersion(req.Params); !protocolVersionNegotiable(requested) {
				logger.Info("initialize: rejecting unsupported protocolVersion %q (supported: %v)", requested, mcpSupportedProtocolVersions())
				sendErrorWithData(req.ID, errCodeUnsupportedProtocolVersion, msgUnsupportedProtocolVersion, unsupportedProtocolVersionData(requested))
				continue
			}

			// Read client capabilities and identity (elicitation support, name/version).
			supportsElicitation, name, version := parseInitializeParams(req.Params)
			logger.Info("initialize: clientSupportsElicitation=%v clientName=%q clientVersion=%q", supportsElicitation, name, version)

			serverSend(mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  buildInitializeResult(),
			})

		case "server/discover":
			// MCP 2026-07-28's era-detection probe. This server is legacy, so
			// the probe must fail — answering it with a DiscoverResult would
			// classify us as modern and break the fallback it exists to drive.
			// discoverLegacyErrorData explains the rules and carries the
			// diagnostic. Stateless either way: no client state is read or
			// stored, so it stays answerable before (and independently of) the
			// initialize handshake.
			sendErrorWithData(req.ID, -32601, "method not found: "+req.Method, discoverLegacyErrorData())

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
