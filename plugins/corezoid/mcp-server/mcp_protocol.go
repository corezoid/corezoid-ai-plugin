package main

import (
	"context"
	"encoding/json"
)

// MCP 2026-07-28 ("modern") protocol core, shared by the stdio, local HTTP and
// hosted transports.
//
// The modern revision is stateless: there is no initialize handshake and no
// session. Every request carries its protocol version, client capabilities and
// (optionally) client identity in params._meta, and every result carries a
// resultType and the server's identity in result._meta. This server is
// dual-era: a request with that _meta is served per 2026-07-28; a request
// without it is a legacy (2025-03-26) request and is served as before, so
// clients that still open with initialize keep working. initialize is never
// required — nothing below reads state a handshake would have set.

// mcpModernProtocolVersion is the stateless MCP revision served to requests
// that carry per-request _meta.
const mcpModernProtocolVersion = "2026-07-28"

// Reserved _meta keys (MCP 2026-07-28, basic/index#meta).
const (
	metaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaKeyClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaKeyLogLevel           = "io.modelcontextprotocol/logLevel"
	metaKeyServerInfo         = "io.modelcontextprotocol/serverInfo"
	metaKeySubscriptionID     = "io.modelcontextprotocol/subscriptionId"
)

// JSON-RPC error codes the MCP specification reserves (-32020 … -32099).
const (
	errCodeHeaderMismatch             = -32020
	errCodeUnsupportedProtocolVersion = -32022
)

// resultType values. Every result carries one; "input_required" is only ever
// returned from tools/call (see mcp_mrtr.go).
const (
	resultTypeComplete      = "complete"
	resultTypeInputRequired = "input_required"
)

// Cache hints for CacheableResult (tools/list, prompts/list, resources/list,
// resources/read, server/discover). The tool and prompt lists are compiled
// into the binary and identical for every caller, so they are public and
// long-lived; resources are the caller's own local process files.
const (
	staticListTTLMs = 60 * 60 * 1000
	resourceTTLMs   = 5 * 1000
	cacheScopePub   = "public"
	cacheScopePriv  = "private"
)

// mcpServerName is the implementation name reported in serverInfo.
const mcpServerName = "convctl-mcp"

// requestMeta is what a request's params._meta says about the caller. Modern
// is false for a legacy request (no protocolVersion in _meta); the other
// fields are then empty and callers fall back to the legacy, process-global
// client state set by initialize.
type requestMeta struct {
	Modern          bool
	ProtocolVersion string
	ClientName      string
	ClientVersion   string
	Capabilities    map[string]json.RawMessage
	// LogLevel is recorded but nothing is emitted for it: this server sends no
	// notifications/message at all, which satisfies "MUST NOT emit for requests
	// without the field" trivially and keeps logs on stderr only.
	LogLevel string
}

// supportsElicitation reports whether the request declared the elicitation
// client capability. A server must not put elicitation/create into
// inputRequests unless it did.
func (m requestMeta) supportsElicitation() bool {
	_, ok := m.Capabilities["elicitation"]
	return ok
}

// parseRequestMeta reads the per-request protocol fields from params._meta.
// A request without _meta.protocolVersion is legacy and returns Modern=false
// and no error. A modern request with an unsupported version gets
// UnsupportedProtocolVersionError; one missing the required
// clientCapabilities is malformed (-32602).
func parseRequestMeta(params json.RawMessage) (requestMeta, *mcpError) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil {
		return requestMeta{}, nil
	}
	rawVersion, ok := p.Meta[metaKeyProtocolVersion]
	if !ok {
		return requestMeta{}, nil
	}
	var meta requestMeta
	if err := json.Unmarshal(rawVersion, &meta.ProtocolVersion); err != nil || meta.ProtocolVersion == "" {
		return requestMeta{}, &mcpError{Code: -32602, Message: "invalid params: _meta." + metaKeyProtocolVersion + " must be a non-empty string"}
	}
	if !isModernProtocolVersion(meta.ProtocolVersion) {
		return requestMeta{}, unsupportedProtocolVersionError(meta.ProtocolVersion)
	}
	meta.Modern = true

	rawCaps, ok := p.Meta[metaKeyClientCapabilities]
	if !ok {
		return requestMeta{}, &mcpError{Code: -32602, Message: "invalid params: missing required _meta." + metaKeyClientCapabilities}
	}
	if err := json.Unmarshal(rawCaps, &meta.Capabilities); err != nil {
		return requestMeta{}, &mcpError{Code: -32602, Message: "invalid params: _meta." + metaKeyClientCapabilities + " must be an object"}
	}
	if rawInfo, ok := p.Meta[metaKeyClientInfo]; ok {
		var info struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(rawInfo, &info) == nil {
			meta.ClientName, meta.ClientVersion = info.Name, info.Version
		}
	}
	if rawLevel, ok := p.Meta[metaKeyLogLevel]; ok {
		_ = json.Unmarshal(rawLevel, &meta.LogLevel)
	}
	return meta, nil
}

func isModernProtocolVersion(v string) bool {
	return v == mcpModernProtocolVersion
}

// unsupportedProtocolVersionError is UnsupportedProtocolVersionError, carrying
// the same version list server/discover reports.
func unsupportedProtocolVersionError(requested string) *mcpError {
	return &mcpError{
		Code:    errCodeUnsupportedProtocolVersion,
		Message: "Unsupported protocol version",
		Data: map[string]interface{}{
			"supported": mcpSupportedProtocolVersions(),
			"requested": requested,
		},
	}
}

// serverInfoMeta is the result _meta every response carries so the server
// identifies itself without relying on a handshake.
func serverInfoMeta() map[string]interface{} {
	return map[string]interface{}{
		metaKeyServerInfo: map[string]interface{}{"name": mcpServerName, "version": serverVersion()},
	}
}

// completeResult stamps resultType "complete" and the serverInfo _meta onto a
// map-shaped result.
func completeResult(fields map[string]interface{}) map[string]interface{} {
	fields["resultType"] = resultTypeComplete
	fields["_meta"] = serverInfoMeta()
	return fields
}

// cacheableResult is completeResult plus the CacheableResult freshness hints.
func cacheableResult(fields map[string]interface{}, ttlMs int, scope string) map[string]interface{} {
	fields["ttlMs"] = ttlMs
	fields["cacheScope"] = scope
	return completeResult(fields)
}

// newToolResult builds a complete tools/call result with one text block.
func newToolResult(text string, isErr bool) mcpToolResult {
	return mcpToolResult{
		ResultType: resultTypeComplete,
		Content:    []mcpContent{{Type: "text", Text: text}},
		IsError:    isErr,
		Meta:       serverInfoMeta(),
	}
}

// modernServerCapabilities is what server/discover advertises for the local
// (stdio / local HTTP) server. resources.subscribe and listChanged are served
// through subscriptions/listen (mcp_subscriptions.go).
func modernServerCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools":     map[string]interface{}{},
		"resources": map[string]interface{}{"subscribe": true, "listChanged": true},
		"prompts":   map[string]interface{}{},
	}
}

// buildDiscoverResult returns the server/discover result: the supported
// protocol versions, the capabilities and the server identity.
func buildDiscoverResult() map[string]interface{} {
	return discoverResult(modernServerCapabilities())
}

func discoverResult(capabilities map[string]interface{}) map[string]interface{} {
	return cacheableResult(map[string]interface{}{
		"supportedVersions": mcpSupportedProtocolVersions(),
		"capabilities":      capabilities,
	}, staticListTTLMs, cacheScopePub)
}

// toolCallParams is tools/call's params, including the Multi Round-Trip
// Request retry fields.
type toolCallParams struct {
	Name           string                     `json:"name"`
	Arguments      map[string]interface{}     `json:"arguments"`
	InputResponses map[string]json.RawMessage `json:"inputResponses,omitempty"`
	RequestState   string                     `json:"requestState,omitempty"`
}

// runToolCall executes one tools/call and returns either a complete
// mcpToolResult or, when a handler needs user input from a modern client, an
// InputRequiredResult. legacy is the elicitor for a legacy client (server-
// initiated elicitation/create over stdio), or nil. allowMRTR is false where
// a retry could land on another replica (hosted mode), since the MRTR state
// handle lives in this process's memory.
func runToolCall(ctx context.Context, p toolCallParams, meta requestMeta, legacy elicitor, allowMRTR bool) (interface{}, *mcpError) {
	if meta.Modern && (meta.ClientName != "" || meta.ClientVersion != "") {
		ctx = context.WithValue(ctx, clientIdentityContextKey, clientIdentity{Name: meta.ClientName, Version: meta.ClientVersion})
	}

	if meta.Modern && allowMRTR && meta.supportsElicitation() {
		var ex *mrtrExchange
		var res interface{}
		var err error
		if p.RequestState != "" {
			var answered bool
			ex, answered, err = resumeMRTRExchange(p.Name, p.RequestState, p.InputResponses)
			if err != nil {
				return nil, &mcpError{Code: -32602, Message: "invalid params: " + err.Error()}
			}
			if answered {
				res, err = ex.next(ctx)
			} else {
				res, err = ex.repeat(ctx)
			}
		} else {
			ex = startMRTRExchange(ctx, p.Name, p.Arguments, mrtrToolTimeout)
			res, err = ex.next(ctx)
		}
		if err != nil {
			return nil, &mcpError{Code: -32603, Message: "tool call aborted: " + err.Error()}
		}
		return res, nil
	}
	if p.RequestState != "" {
		return nil, &mcpError{Code: -32602, Message: "invalid params: requestState was not issued by this server for this request"}
	}
	if !meta.Modern && legacy != nil {
		ctx = withElicitor(ctx, legacy)
	}

	text, isErr := handleToolCall(ctx, p.Name, p.Arguments)
	return newToolResult(text, isErr), nil
}

// serveCommonMethod answers the methods every local transport serves the same
// way. ok=false means the method is not one of them.
func serveCommonMethod(req mcpRequest) (result interface{}, rpcErr *mcpError, ok bool) {
	switch req.Method {
	case "server/discover":
		return buildDiscoverResult(), nil, true

	case "tools/list":
		return cacheableResult(map[string]interface{}{"tools": toolRegistry}, staticListTTLMs, cacheScopePub), nil, true

	case "resources/list":
		resources, err := listResources()
		if err != nil {
			return nil, &mcpError{Code: -32603, Message: err.Error()}, true
		}
		if resources == nil {
			resources = []mcpResource{}
		}
		return cacheableResult(map[string]interface{}{"resources": resources}, resourceTTLMs, cacheScopePriv), nil, true

	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid params: " + err.Error()}, true
		}
		content, err := readResource(params.URI)
		if err != nil {
			// Resource not found is -32602 (Invalid Params) since 2026-07-28.
			return nil, &mcpError{Code: -32602, Message: err.Error()}, true
		}
		return cacheableResult(map[string]interface{}{"contents": []interface{}{content}}, resourceTTLMs, cacheScopePriv), nil, true

	case "prompts/list":
		return cacheableResult(map[string]interface{}{"prompts": builtinPrompts}, staticListTTLMs, cacheScopePub), nil, true

	case "prompts/get":
		var params struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid params: " + err.Error()}, true
		}
		prompt, err := getPrompt(params.Name, params.Arguments)
		if err != nil {
			return nil, &mcpError{Code: -32603, Message: err.Error()}, true
		}
		prompt.ResultType = resultTypeComplete
		prompt.Meta = serverInfoMeta()
		return prompt, nil, true
	}
	return nil, nil, false
}

// methodNotFound is the error for a method this server does not implement —
// including ping and logging/setLevel, which 2026-07-28 removed.
func methodNotFound(method string) *mcpError {
	return &mcpError{Code: -32601, Message: "method not found: " + method}
}

// rpcResult wraps a result or error into a JSON-RPC response.
func rpcResult(id interface{}, result interface{}, rpcErr *mcpError) mcpResponse {
	if rpcErr != nil {
		return mcpResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
	}
	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
}
