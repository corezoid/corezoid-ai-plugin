package mcpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.corezoid.com/mw161089sar/swagger-mcp/app/auth"
	"git.corezoid.com/mw161089sar/swagger-mcp/app/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

const sseHeadersKey = "__sseHeadersKey"

func ExtractSchemaName(ref, schemaType string) string {
	if ref != "" {
		parts := strings.Split(ref, "/")
		return parts[len(parts)-1]
	}
	return schemaType
}

// getDefinition gets a definition from either components.schemas (OpenAPI 3.0+) or definitions (Swagger 2.0)
func getDefinition(swaggerSpec models.SwaggerSpec, schemaName string) (models.Definition, bool) {
	// Try OpenAPI 3.0+ components first
	if swaggerSpec.Components != nil {
		if definition, found := swaggerSpec.Components.Schemas[schemaName]; found {
			return definition, true
		}
	}
	// Fall back to Swagger 2.0 definitions
	if definition, found := swaggerSpec.Definitions[schemaName]; found {
		return definition, true
	}

	return models.Definition{}, false
}

// resolveParameterRef resolves a $ref parameter reference
func resolveParameterRef(swaggerSpec models.SwaggerSpec, ref string) (models.Parameter, bool) {
	// Handle internal refs like "#/paths/~1download~1zip~1%7BaccId%7D/post/parameters/1"
	if strings.HasPrefix(ref, "#/") {
		parts := strings.Split(ref, "/")
		if len(parts) >= 6 && parts[1] == "paths" {
			// Decode URL-encoded path
			pathKey := strings.ReplaceAll(parts[2], "~1", "/")
			pathKey = strings.ReplaceAll(pathKey, "%7B", "{")
			pathKey = strings.ReplaceAll(pathKey, "%7D", "}")

			method := parts[3]
			if parts[4] == "parameters" {
				// Get parameter by index
				if paramIdx, err := strconv.Atoi(parts[5]); err == nil {
					if pathMethods, exists := swaggerSpec.Paths[pathKey]; exists {
						if endpoint, exists := pathMethods[method]; exists {
							if paramIdx < len(endpoint.Parameters) {
								return endpoint.Parameters[paramIdx], true
							}
						}
					}
				}
			}
		}
	}

	return models.Parameter{}, false
}

func compileRegexes(paths string) []*regexp.Regexp {
	var regexes []*regexp.Regexp
	for _, path := range strings.Split(paths, ",") {
		if path = strings.TrimSpace(path); path != "" {
			regex, err := regexp.Compile(path)
			if err != nil {
				log.Printf("Invalid regex pattern: %s, error: %v", path, err)
				continue
			}
			regexes = append(regexes, regex)
		}
	}
	return regexes
}

func shouldIncludePath(path string, includeRegexes, excludeRegexes []*regexp.Regexp) bool {
	// If no include regexes are specified, include all paths by default
	include := len(includeRegexes) == 0

	for _, regex := range includeRegexes {
		if regex.MatchString(path) {
			include = true
			break
		}
	}

	if !include {
		return false
	}

	for _, regex := range excludeRegexes {
		if regex.MatchString(path) {
			return false
		}
	}

	return true
}

func shouldIncludeMethod(method string, includeMethods, excludeMethods []string) bool {
	// If no include methods are specified, include all methods by default
	include := len(includeMethods) == 0

	for _, m := range includeMethods {
		if strings.EqualFold(strings.TrimSpace(m), method) {
			include = true
			break
		}
	}

	if !include {
		return false
	}

	for _, m := range excludeMethods {
		if strings.EqualFold(strings.TrimSpace(m), method) {
			return false
		}
	}

	return true
}

func CreateServer(swaggerSpec models.SwaggerSpec, config models.Config) {
	mcpServer := server.NewMCPServer(
		"swagegr-mcp",
		"1.0.0",
	)

	LoadSwaggerServer(mcpServer, swaggerSpec, config.ApiCfg)

	if config.SseCfg.SseMode {
		// Create and start SSE server
		sseServer := server.NewSSEServer(mcpServer, server.WithBaseURL(config.SseCfg.SseUrl), server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if len(config.ApiCfg.SseHeaders) == 0 {
				return ctx
			}
			keys := strings.Split(config.ApiCfg.SseHeaders, ",")
			sseHeaders := map[string]string{}
			for _, key := range keys {
				sseHeaders[key] = r.Header.Get(key)
			}
			return context.WithValue(ctx, sseHeadersKey, sseHeaders)
		}))
		endpoint, err := sseServer.CompleteSseEndpoint()
		if err != nil {
			log.Fatalf("Error creating SSE endpoint: %v", err)
		}
		log.Printf("Starting SSE server on %s, endpoint: %s", config.SseCfg.SseAddr, endpoint)
		if err := sseServer.Start(config.SseCfg.SseAddr); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	} else {
		// Run as stdio server
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}
}

type Operation struct {
	ID          string      `json:"id"`
	OperationID string      `json:"operation_id,omitempty"`
	Description string      `json:"description"`
	Method      string      `json:"method"`
	Path        string      `json:"path"`
	Summary     string      `json:"summary"`
	URL         string      `json:"url"`
	Parameters  []Parameter `json:"parameters"`
	RequestBody interface{} `json:"request_body"`
	Responses   interface{} `json:"responses"`
	Invisible   bool        `json:"invisible,omitempty"`
}

type Parameter struct {
	Name        string      `json:"name"`
	In          string      `json:"in"`
	TIn         string      `json:"-"`
	Required    bool        `json:"required"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Schema      interface{} `json:"schema"`
}

var globalOperations []Operation
var globalSwaggerSpec models.SwaggerSpec
var globalApiConfig models.ApiConfig
var globalMCPServer *server.MCPServer
var globalOAuthClientID string

// operationScore is used for scoring search results
type operationScore struct {
	operation Operation
	score     float64
}

// operationToolName converts an Operation into a valid MCP tool name. If the
// swagger spec provides an operationId, we use it verbatim so endpoints keep
// their camelCase names like "createActor" instead of "post-actors-actor-formId".
// Falls back to <method>-<path-segments> when operationId is missing.
func operationToolName(op Operation) string {
	if op.OperationID != "" {
		return op.OperationID
	}
	method := strings.ToLower(op.Method)
	path := strings.TrimPrefix(op.Path, "/")
	path = strings.ReplaceAll(path, "/", "-")
	return method + "-" + path
}

// registerOperationTools adds one MCP tool per API operation to the server.
func registerOperationTools(mcpServer *server.MCPServer, operations []Operation) {
	for _, op := range operations {
		op := op // capture for closure
		toolName := operationToolName(op)

		var opts []mcp.ToolOption
		desc := op.Summary
		if desc == "" {
			desc = op.Description
		} else if op.Description != "" && op.Description != op.Summary {
			desc = op.Summary + "\n" + op.Description
		}
		opts = append(opts, mcp.WithDescription(desc))

		for _, param := range op.Parameters {
			if param.Name == "accId" {
				continue // auto-injected from WORKSPACE_ID env var
			}
			var propOpts []mcp.PropertyOption
			if param.Description != "" {
				propOpts = append(propOpts, mcp.Description(param.Description))
			}
			if param.Required {
				propOpts = append(propOpts, mcp.Required())
			}
			opts = append(opts, propertyForType(param.Name, param.Type, propOpts))
		}

		if op.RequestBody != nil {
			opts = append(opts, bodyToolOption(op.RequestBody))
		}

		// massLink: expose an optional layerId param so callers can request
		// automatic edge placement on a layer after the links are created.
		if toolName == "massLink" {
			opts = append(opts, mcp.WithString("layerId",
				mcp.Description("Optional layer actor ID. When provided, edges are automatically added to this layer after creation."),
			))
		}

		mcpServer.AddTool(
			mcp.NewTool(toolName, opts...),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if authErr := ensureAuth(ctx); authErr != nil {
					return authErr, nil
				}
				args := req.GetArguments()
				// Array-body tools expose items as a top-level param; move it
				// to body so executeOperation handles it uniformly.
				if raw, ok := args["items"]; ok && raw != nil {
					if b, err := json.Marshal(raw); err == nil {
						args["body"] = string(b)
					}
					delete(args, "items")
				}
				normalizeBodyArg(args)
				queryParams := map[string]interface{}{}
				headerParams := map[string]interface{}{}
				for _, p := range op.Parameters {
					if p.Name == "accId" {
						if accID := os.Getenv("WORKSPACE_ID"); accID != "" {
							queryParams["accId"] = accID
						}
						continue
					}
					if val, ok := args[p.Name]; ok {
						if p.TIn == "path" || p.In == "query" {
							queryParams[p.Name] = val
						} else if p.In == "header" {
							headerParams[p.Name] = val
						}
					}
				}
				var childFormID int
				if toolName == "createActor" {
					var injErr error
					childFormID, injErr = injectCreateActorData(ctx, args, queryParams)
					if injErr != nil {
						log.Printf("Warning: createActor data injection failed: %v", injErr)
					}
				}
				if toolName == "manageLayer" {
					if err := injectManageLayerData(ctx, args); err != nil {
						log.Printf("Warning: manageLayer data injection failed: %v", err)
					}
				}
				if toolName == "createLink" {
					if err := injectCreateLinkData(ctx, args); err != nil {
						log.Printf("Warning: createLink data injection failed: %v", err)
					}
				}
				// Extract layerId for massLink before the API call (not a real API param).
				var massLinkLayerID string
				var massLinkPairs [][2]string
				if toolName == "massLink" {
					massLinkLayerID, _ = args["layerId"].(string)
					delete(args, "layerId")
					if err := injectMassLinkData(ctx, args); err != nil {
						log.Printf("Warning: massLink data injection failed: %v", err)
					}
					// Capture source→target pairs from the request body before execution.
					// The API response does not reliably return edge IDs for batch creates.
					if bodyStr, ok := args["body"].(string); ok {
						massLinkPairs = parseMassLinkPairs(bodyStr)
					}
				}
				var bodyParams map[string]interface{}
				if bodyStr, ok := args["body"].(string); ok && bodyStr != "" {
					json.Unmarshal([]byte(bodyStr), &bodyParams)
				}
				result, err := executeOperation(ctx, op, queryParams, headerParams, bodyParams, req)
				if toolName == "createActor" && err == nil && result != nil && !result.IsError {
					for _, c := range result.Content {
						if tc, ok := c.(mcp.TextContent); ok {
							cacheActorFormIDFromResult(tc.Text)
							if childFormID != 0 {
								overrideActorFormID(tc.Text, childFormID)
							}
						}
					}
				}
				if toolName == "massLink" && massLinkLayerID != "" && len(massLinkPairs) > 0 && err == nil && result != nil && !result.IsError {
					log.Printf("massLink: placing %d edges on layer %s", len(massLinkPairs), massLinkLayerID)
					if placeErr := autoPlaceEdgesOnLayer(ctx, massLinkLayerID, massLinkPairs); placeErr != nil {
						log.Printf("Warning: autoPlaceEdgesOnLayer failed: %v", placeErr)
					}
				}
				return result, err
			},
		)
	}
}

// propertyForType returns a ToolOption that registers the parameter using the
// JSONSchema type from the Swagger spec instead of always defaulting to string.
func propertyForType(name, swaggerType string, propOpts []mcp.PropertyOption) mcp.ToolOption {
	switch strings.ToLower(swaggerType) {
	case "integer", "number":
		return mcp.WithNumber(name, propOpts...)
	case "boolean":
		return mcp.WithBoolean(name, propOpts...)
	default:
		return mcp.WithString(name, propOpts...)
	}
}

// bodyToolOption builds a ToolOption for the request body parameter, expanding
// the JSON schema into a structured object when possible so MCP clients can
// see and fill individual fields. Falls back to a free-form JSON string when
// the schema is missing or not an object.
func bodyToolOption(requestBody interface{}) mcp.ToolOption {
	rb, ok := requestBody.(*models.RequestBody)
	if !ok || rb == nil {
		return mcp.WithString("body", mcp.Description("Request body as JSON"))
	}

	schema := extractBodySchema(rb)
	if schema == nil {
		return mcp.WithString("body", mcp.Description("Request body as JSON"))
	}

	js := schemaToJSONSchema(schema, 0)
	sanitizeJSONSchema(js)
	propsRaw, hasProps := js["properties"].(map[string]interface{})

	// Array body: expose as top-level "items" parameter so MCP clients see the
	// item schema directly. The handler moves args["items"] → args["body"] before
	// executeOperation picks it up.
	if !hasProps && js["type"] == "array" {
		desc := "Request body items"
		if rb.Description != "" {
			desc = rb.Description
		}
		propOpts := []mcp.PropertyOption{mcp.Description(desc)}
		if itemSchema, ok := js["items"]; ok && itemSchema != nil {
			propOpts = append(propOpts, mcp.Items(itemSchema))
		}
		if rb.Required {
			propOpts = append(propOpts, mcp.Required())
		}
		return mcp.WithArray("items", propOpts...)
	}

	// Scalars or empty schemas: fall back to JSON string body.
	if !hasProps || len(propsRaw) == 0 {
		desc := "Request body as JSON"
		if d, ok := js["description"].(string); ok && d != "" {
			desc = d
		}
		return mcp.WithString("body", mcp.Description(desc))
	}

	propOpts := []mcp.PropertyOption{
		mcp.Description("Request body"),
		mcp.Properties(propsRaw),
	}
	if required, ok := js["required"].([]string); ok && len(required) > 0 {
		ifaces := make([]interface{}, len(required))
		for i, r := range required {
			ifaces[i] = r
		}
		propOpts = append(propOpts, func(s map[string]interface{}) {
			s["required"] = ifaces
		})
	}
	if rb.Required {
		propOpts = append(propOpts, mcp.Required())
	}
	return mcp.WithObject("body", propOpts...)
}

// normalizeBodyArg converts args["body"] from a structured object/array (as
// sent by clients that understand the expanded schema) into the JSON string
// form that the downstream executeOperation pipeline expects.
func normalizeBodyArg(args map[string]interface{}) {
	if args == nil {
		return
	}
	raw, ok := args["body"]
	if !ok || raw == nil {
		return
	}
	if _, isStr := raw.(string); isStr {
		return
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return
	}
	args["body"] = string(b)
}

// extractBodySchema returns the application/json schema (or the first
// available media type schema) from a request body.
func extractBodySchema(rb *models.RequestBody) *models.SchemaRef {
	if rb == nil {
		return nil
	}
	if mt, ok := rb.Content["application/json"]; ok && mt.Schema != nil {
		return mt.Schema
	}
	for _, mt := range rb.Content {
		if mt.Schema != nil {
			return mt.Schema
		}
	}
	return nil
}

// schemaToJSONSchema converts a SchemaRef into a JSON Schema map suitable for
// use with mcp.Properties(). $ref values are resolved against globalSwaggerSpec
// and recursion is depth-limited to avoid cycles in self-referential specs.
func schemaToJSONSchema(schema *models.SchemaRef, depth int) map[string]interface{} {
	if schema == nil || depth > 10 {
		return map[string]interface{}{}
	}
	if schema.Ref != "" {
		name := ExtractSchemaName(schema.Ref, "")
		if def, found := getDefinition(globalSwaggerSpec, name); found {
			return definitionToJSONSchema(def, depth+1)
		}
		return map[string]interface{}{}
	}

	out := map[string]interface{}{}
	if schema.Type != "" {
		out["type"] = schema.Type
	}
	if schema.Description != "" {
		out["description"] = schema.Description
	}
	if schema.Format != "" {
		out["format"] = schema.Format
	}
	if len(schema.Enum) > 0 {
		out["enum"] = schema.Enum
	}
	if schema.Example != nil {
		out["example"] = schema.Example
	}
	if schema.Default != nil {
		out["default"] = schema.Default
	}
	if len(schema.Properties) > 0 {
		if _, has := out["type"]; !has {
			out["type"] = "object"
		}
		props := map[string]interface{}{}
		for name, p := range schema.Properties {
			props[name] = schemaToJSONSchema(p, depth+1)
		}
		out["properties"] = props
	}
	if len(schema.Required) > 0 {
		out["required"] = schema.Required
	}
	if (schema.Type == "array" || schema.Items != nil) && schema.Items != nil {
		out["items"] = schemaToJSONSchema(schema.Items, depth+1)
		if _, has := out["type"]; !has {
			out["type"] = "array"
		}
	}
	return out
}

// validJSONSchemaTypes lists the seven primitive types allowed by JSON Schema
// draft 2020-12. Anything outside this set (e.g. the "Field value" typo in the
// Simulator swagger) causes Anthropic's tool API to reject the schema.
var validJSONSchemaTypes = map[string]bool{
	"string":  true,
	"number":  true,
	"integer": true,
	"boolean": true,
	"object":  true,
	"array":   true,
	"null":    true,
}

// sanitizeJSONSchema recursively rewrites a JSON Schema map so it conforms to
// JSON Schema draft 2020-12. The swagger spec we consume contains OpenAPI-isms
// and outright typos (invalid "type" values, enum entries with null when the
// declared type is a single string) that would otherwise be passed through
// verbatim and rejected by the tool API.
func sanitizeJSONSchema(s map[string]interface{}) {
	if s == nil {
		return
	}

	if t, ok := s["type"].(string); ok && !validJSONSchemaTypes[t] {
		delete(s, "type")
	}

	if enumRaw, ok := s["enum"].([]interface{}); ok {
		hasNull := false
		for _, v := range enumRaw {
			if v == nil {
				hasNull = true
				break
			}
		}
		if hasNull {
			if t, ok := s["type"].(string); ok && t != "null" {
				s["type"] = []interface{}{t, "null"}
			}
		}
	}

	if props, ok := s["properties"].(map[string]interface{}); ok {
		for _, v := range props {
			if sub, ok := v.(map[string]interface{}); ok {
				sanitizeJSONSchema(sub)
			}
		}
	}

	if items, ok := s["items"].(map[string]interface{}); ok {
		sanitizeJSONSchema(items)
	}
}

// definitionToJSONSchema converts a Swagger 2.0 / OpenAPI 3.0 Definition into
// a JSON Schema map.
func definitionToJSONSchema(def models.Definition, depth int) map[string]interface{} {
	out := map[string]interface{}{}
	if def.Type != "" {
		out["type"] = def.Type
	} else {
		out["type"] = "object"
	}
	if len(def.Properties) > 0 {
		props := map[string]interface{}{}
		for n, p := range def.Properties {
			sub := map[string]interface{}{}
			if p.Type != "" {
				sub["type"] = p.Type
			}
			if p.Example != nil {
				sub["example"] = p.Example
			}
			props[n] = sub
		}
		out["properties"] = props
	}
	if len(def.Required) > 0 {
		out["required"] = def.Required
	}
	return out
}

func LoadSwaggerServer(mcpServer *server.MCPServer, swaggerSpec models.SwaggerSpec, apiCfg models.ApiConfig) {
	globalOperations = buildOperations(swaggerSpec, apiCfg)
	globalSwaggerSpec = swaggerSpec
	globalApiConfig = apiCfg
	globalMCPServer = mcpServer

	// Build a set of tool names marked x-invisible in the swagger spec.
	// These tools are still registered (so they can be called internally) but
	// are filtered out of the tools/list response so they don't clutter the LLM's context.
	invisibleTools := map[string]struct{}{}
	for _, op := range globalOperations {
		if op.Invisible {
			invisibleTools[operationToolName(op)] = struct{}{}
		}
	}
	if len(invisibleTools) > 0 {
		server.WithToolFilter(func(_ context.Context, tools []mcp.Tool) []mcp.Tool {
			visible := tools[:0]
			for _, t := range tools {
				if _, hidden := invisibleTools[t.Name]; !hidden {
					visible = append(visible, t)
				}
			}
			return visible
		})(mcpServer)
	}

	// Resolve OAuth client ID: flag > env var > built-in default
	globalOAuthClientID = apiCfg.OAuthClientID
	if globalOAuthClientID == "" {
		globalOAuthClientID = os.Getenv("SIMULATOR_OAUTH_CLIENT_ID")
	}
	if globalOAuthClientID == "" {
		globalOAuthClientID = auth.DefaultClientID
	}

	// If no auth token is set, try to load from saved credentials
	if globalApiConfig.Authorization == "" {
		if creds, err := auth.Load(); err == nil && creds != nil && !auth.IsExpired(creds) {
			globalApiConfig.Authorization = creds.AuthorizationHeader()
			log.Printf("Loaded auth token from saved credentials (expires %s)", creds.ExpiresAt.Format("2006-01-02 15:04"))
		}
	}

	registerOperationTools(mcpServer, globalOperations)

	mcpServer.AddTool(
		mcp.NewTool("login",
			mcp.WithString("account_url",
				mcp.Description("Corezoid Account URL (default: https://account.corezoid.com). Saved to .env as ACCOUNT_URL."),
			),
			mcp.WithDescription("Authenticate with Simulator via OAuth2 browser flow. Saves the token to .env so it persists across sessions."),
		),
		handleLogin,
	)

	mcpServer.AddTool(
		mcp.NewTool("set-workspace",
			mcp.WithString("acc_id",
				mcp.Description("Workspace ID (accId) to use for all Simulator API calls"),
				mcp.Required(),
			),
			mcp.WithDescription("Save the Simulator workspace ID to .env as SIMULATOR_ACC_ID."),
		),
		handleSetWorkspace,
	)

	// Add MCP resources capability
	initializeResources(mcpServer)

	// Pre-warm hierarchy edge type cache in the background.
	// Polls until auth + WORKSPACE_ID are available (up to 60 s), then fetches once.
	go func() {
		for i := 0; i < 30; i++ {
			if globalApiConfig.Authorization != "" && os.Getenv("WORKSPACE_ID") != "" {
				if _, err := fetchHierarchyEdgeTypeID(context.Background()); err != nil {
					log.Printf("Warning: pre-warm hierarchyEdgeTypeID failed: %v", err)
				}
				return
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("Warning: auth/workspace not ready after 60 s, hierarchyEdgeTypeID will be fetched on first createLink call")
	}()
}

func buildOperations(swaggerSpec models.SwaggerSpec, apiCfg models.ApiConfig) []Operation {
	includeRegexes := compileRegexes(apiCfg.IncludePaths)
	excludeRegexes := compileRegexes(apiCfg.ExcludePaths)
	includedMethods := []string{}
	if len(strings.TrimSpace(apiCfg.IncludeMethods)) > 0 {
		includedMethods = strings.Split(apiCfg.IncludeMethods, ",")
	}
	excludedMethods := []string{}
	if len(strings.TrimSpace(apiCfg.ExcludeMethods)) > 0 {
		excludedMethods = strings.Split(apiCfg.ExcludeMethods, ",")
	}

	var operations []Operation
	operationID := 0

	for path, methods := range swaggerSpec.Paths {
		if !shouldIncludePath(path, includeRegexes, excludeRegexes) {
			continue
		}

		for method, details := range methods {
			if !shouldIncludeMethod(method, includedMethods, excludedMethods) {
				continue
			}

			var baseURL string
			if apiCfg.Url != "" {
				// Use the --url parameter with highest priority
				baseURL = apiCfg.Url
			} else if apiCfg.BaseUrl != "" {
				// Use the --baseUrl parameter with second priority
				baseURL = apiCfg.BaseUrl
			} else {
				// Fall back to extracting from Swagger spec
				if swaggerSpec.OpenAPI != "" {
					if len(swaggerSpec.Servers) > 0 {
						baseURL = strings.TrimSuffix(swaggerSpec.Servers[0].URL, "/")
					} else {
						baseURL = "/"
					}
				} else {
					baseURL = swaggerSpec.Host
					if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
						baseURL = "https://" + baseURL
					}
					if swaggerSpec.BasePath != "" {
						baseURL = strings.TrimSuffix(baseURL, "/") + "/" + strings.TrimPrefix(swaggerSpec.BasePath, "/")
					}
				}
			}

			reqURL := strings.TrimSuffix(baseURL, "/") + "/" + strings.TrimPrefix(path, "/")

			var parameters []Parameter
			for _, param := range details.Parameters {
				// Check if this is a $ref parameter
				if param.Ref != "" {
					// Resolve the $ref
					if resolvedParam, found := resolveParameterRef(swaggerSpec, param.Ref); found {
						// Skip Authorization header parameters
						if resolvedParam.In == "header" && strings.ToLower(resolvedParam.Name) == "authorization" {
							continue
						}

						in := resolvedParam.In
						if in == "path" {
							in = "query"
						}

						// Get type from schema if not directly specified
						paramType := resolvedParam.Type
						if paramType == "" && resolvedParam.Schema != nil {
							paramType = resolvedParam.Schema.Type
						}

						parameters = append(parameters, Parameter{
							Name:        resolvedParam.Name,
							In:          in,
							TIn:         resolvedParam.In,
							Required:    resolvedParam.Required,
							Type:        paramType,
							Description: resolvedParam.Description,
							Schema:      resolvedParam.Schema,
						})
					}
				} else {
					// Skip Authorization header parameters
					if param.In == "header" && strings.ToLower(param.Name) == "authorization" {
						continue
					}

					// Regular parameter
					in := param.In
					if in == "path" {
						in = "query"
					}

					// Get type from schema if not directly specified
					paramType := param.Type
					if paramType == "" && param.Schema != nil {
						paramType = param.Schema.Type
					}

					parameters = append(parameters, Parameter{
						Name:        param.Name,
						In:          in,
						TIn:         param.In,
						Required:    param.Required,
						Type:        paramType,
						Description: param.Description,
						Schema:      param.Schema,
					})
				}
			}

			var requestBody interface{}
			if details.RequestBody != nil {
				requestBody = details.RequestBody
			}

			operationID++
			path1 := strings.ReplaceAll(path, "{", "")
			path1 = strings.ReplaceAll(path1, "}", "")
			operations = append(operations, Operation{

				ID:          fmt.Sprintf("%s:%s", strings.ToUpper(method), path1),
				OperationID: details.OperationID,
				Description: details.Description,
				Method:      method,
				Path:        path1,
				Summary:     details.Summary,
				URL:         reqURL,
				Parameters:  parameters,
				RequestBody: requestBody,
				Responses:   details.Responses,
				Invisible:   details.Invisible,
			})
		}
	}

	return operations
}

// checkIfRequestBodyIsArray checks if the request body schema requires an array
func checkIfRequestBodyIsArray(op Operation) bool {
	log.Printf("DEBUG: checkIfRequestBodyIsArray called for operation %s", op.ID)

	if op.RequestBody == nil {
		log.Printf("DEBUG: RequestBody is nil")
		return false
	}

	// First try to cast to *models.RequestBody
	if requestBody, ok := op.RequestBody.(*models.RequestBody); ok {
		log.Printf("DEBUG: RequestBody is *models.RequestBody")

		// Check application/json content type
		if mediaType, exists := requestBody.Content["application/json"]; exists {
			log.Printf("DEBUG: found application/json content type")

			if mediaType.Schema != nil {
				log.Printf("DEBUG: schema found in media type")

				// Check if schema type is array
				if mediaType.Schema.Type == "array" {
					log.Printf("DEBUG: schema type is array - returning true")
					return true
				}

				// Check if schema has a reference to an array definition
				if mediaType.Schema.Ref != "" {
					log.Printf("DEBUG: schema has $ref: %s", mediaType.Schema.Ref)
					schemaName := ExtractSchemaName(mediaType.Schema.Ref, "")
					if definition, found := getDefinition(globalSwaggerSpec, schemaName); found {
						log.Printf("DEBUG: found definition %s with type: %s", schemaName, definition.Type)
						return definition.Type == "array"
					}
				}
			}
		}
	}

	// Fallback: try to cast to map[string]interface{}
	requestBodyMap, ok := op.RequestBody.(map[string]interface{})
	if !ok {
		log.Printf("DEBUG: RequestBody is neither *models.RequestBody nor map[string]interface{}")
		return false
	}

	// Check the content field
	content, exists := requestBodyMap["content"]
	if !exists {
		log.Printf("DEBUG: content field not found in RequestBody")
		return false
	}

	contentMap, ok := content.(map[string]interface{})
	if !ok {
		log.Printf("DEBUG: content is not a map[string]interface{}")
		return false
	}

	// Check application/json content type
	appJson, exists := contentMap["application/json"]
	if !exists {
		log.Printf("DEBUG: application/json not found in content")
		return false
	}

	appJsonMap, ok := appJson.(map[string]interface{})
	if !ok {
		log.Printf("DEBUG: application/json is not a map[string]interface{}")
		return false
	}

	// Check the schema
	schema, exists := appJsonMap["schema"]
	if !exists {
		log.Printf("DEBUG: schema not found in application/json")
		return false
	}

	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		log.Printf("DEBUG: schema is not a map[string]interface{}")
		return false
	}

	log.Printf("DEBUG: schema found: %+v", schemaMap)

	// Check if type is array
	if schemaType, exists := schemaMap["type"]; exists {
		log.Printf("DEBUG: schema type found: %v", schemaType)
		if typeStr, ok := schemaType.(string); ok && typeStr == "array" {
			log.Printf("DEBUG: schema type is array - returning true")
			return true
		}
	}

	// Check if schema has a reference to an array definition
	if ref, exists := schemaMap["$ref"]; exists {
		if refStr, ok := ref.(string); ok {
			log.Printf("DEBUG: schema has $ref: %s", refStr)
			schemaName := ExtractSchemaName(refStr, "")
			if definition, found := getDefinition(globalSwaggerSpec, schemaName); found {
				log.Printf("DEBUG: found definition %s with type: %s", schemaName, definition.Type)
				return definition.Type == "array"
			}
		}
	}

	log.Printf("DEBUG: no array indication found - returning false")
	return false
}

// requestBodyHasProperty returns true if the operation's application/json requestBody
// schema declares the given property name.
func requestBodyHasProperty(op Operation, property string) bool {
	if op.RequestBody == nil {
		return false
	}
	if requestBody, ok := op.RequestBody.(*models.RequestBody); ok {
		if mediaType, exists := requestBody.Content["application/json"]; exists && mediaType.Schema != nil {
			if mediaType.Schema.Properties != nil {
				_, found := mediaType.Schema.Properties[property]
				return found
			}
		}
	}
	return false
}

// SysFormItem is a single record written to sys-forms.json.
type SysFormItem struct {
	ID          int                      `json:"id"          yaml:"id"`
	Title       string                   `json:"title"                yaml:"title"`
	Description string                   `json:"description,omitempty" yaml:"description,omitempty"`
	Fields      []map[string]interface{} `json:"fields,omitempty" yaml:"fields,omitempty"`
	Childs      []SysFormItem            `json:"childs,omitempty" yaml:"childs,omitempty"`
}

// omitEmptyFields recursively removes keys with empty string, empty slice, empty map, or nil values.
func omitEmptyFields(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		cleaned := cleanFieldValue(v)
		if !isEmptyFieldValue(cleaned) {
			result[k] = cleaned
		}
	}
	return result
}

func cleanFieldValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		c := omitEmptyFields(val)
		if len(c) == 0 {
			return nil
		}
		return c
	case []interface{}:
		if len(val) == 0 {
			return nil
		}
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = cleanFieldValue(item)
		}
		return out
	default:
		return v
	}
}

func isEmptyFieldValue(v interface{}) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case []interface{}:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	}
	return false
}

// fetchFormFields calls GET /forms/{formId} and returns all content items across all sections.
func fetchFormFields(ctx context.Context, formOp *Operation, formID int) ([]map[string]interface{}, error) {
	queryParams := map[string]interface{}{"formId": strconv.Itoa(formID)}
	result, err := executeOperation(ctx, *formOp, queryParams, nil, nil, mcp.CallToolRequest{})
	if err != nil {
		return nil, err
	}
	var responseText string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			responseText = tc.Text
		}
	}
	if result.IsError {
		return nil, fmt.Errorf("API error: %s", responseText)
	}
	var apiResult struct {
		Data struct {
			Form struct {
				Sections []struct {
					Content []map[string]interface{} `json:"content"`
				} `json:"sections"`
			} `json:"form"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &apiResult); err != nil {
		return nil, err
	}
	var fields []map[string]interface{}
	for _, section := range apiResult.Data.Form.Sections {
		for _, field := range section.Content {
			fields = append(fields, omitEmptyFields(field))
		}
	}
	return fields, nil
}

// fetchAndSaveSystemForms calls the getSystemForms tool internally,
// builds a parent→children hierarchy, enriches each item with form fields,
// and writes sys-forms.json to the current directory.
func fetchAndSaveSystemForms(ctx context.Context, accID string) error {
	// Find the system forms list operation
	var listOp *Operation
	for i, op := range globalOperations {
		if strings.EqualFold(op.Method, "get") && strings.Contains(op.URL, "forms/templates/system") {
			listOp = &globalOperations[i]
			break
		}
	}
	if listOp == nil {
		return fmt.Errorf("getSystemForms operation not found")
	}

	// Find the form detail operation (GET /forms/{formId})
	var formOp *Operation
	for i, op := range globalOperations {
		if operationToolName(op) == "getForm" {
			formOp = &globalOperations[i]
			break
		}
	}
	if formOp == nil {
		return fmt.Errorf("getForm operation not found")
	}

	// Fetch system forms list
	result, err := executeOperation(ctx, *listOp, map[string]interface{}{"accId": accID}, nil, nil, mcp.CallToolRequest{})
	if err != nil {
		return fmt.Errorf("executeOperation failed: %w", err)
	}
	var responseText string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			responseText = tc.Text
		}
	}
	if result.IsError {
		return fmt.Errorf("API error: %s", responseText)
	}

	var apiResult struct {
		Data []struct {
			ID          int    `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			ParentID    *int   `json:"parentId"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &apiResult); err != nil {
		return fmt.Errorf("failed to parse forms response: %w", err)
	}

	// Build hierarchy
	childrenOf := map[int][]SysFormItem{}
	var roots []SysFormItem

	//allowedRootTitles := map[string]bool{
	//	"Graphs":         true,
	//	"Layers":         true,
	//	"FlowchartBlock": true,
	//	"Actor":          true,
	//	"SmartTags":      true,
	//	"Events":         true,
	//	"Reactions":      true,
	//	"Dashboards":     true,
	//	"Tags":           true,
	//	"Snippets":       true,
	//	"Default": true,
	//}

	for _, item := range apiResult.Data {
		form := SysFormItem{
			ID:          item.ID,
			Title:       item.Title,
			Description: item.Description,
		}
		if item.ParentID == nil {
			//if allowedRootTitles[item.Title] {
			roots = append(roots, form)
			//}
		} else {
			childrenOf[*item.ParentID] = append(childrenOf[*item.ParentID], form)
		}
	}

	// Enrich each item with form fields and attach children
	enrichItem := func(item *SysFormItem) {
		fields, fetchErr := fetchFormFields(ctx, formOp, item.ID)
		if fetchErr != nil {
			log.Printf("Warning: failed to fetch fields for form %d: %v", item.ID, fetchErr)
		} else {
			item.Fields = fields
		}
	}

	for i := range roots {
		enrichItem(&roots[i])
		if ch, ok := childrenOf[roots[i].ID]; ok {
			roots[i].Childs = ch
		}
	}

	data, err := yaml.Marshal(roots)
	if err != nil {
		return fmt.Errorf("failed to marshal forms: %w", err)
	}
	if err := os.WriteFile("sys-forms.yaml", data, 0644); err != nil {
		return fmt.Errorf("failed to write sys-forms.yaml: %w", err)
	}
	log.Printf("Saved %d root forms to sys-forms.yaml", len(roots))
	return nil
}

// sysFormsCache holds the parsed sys-forms.yaml from the working directory,
// loaded lazily on the first createActor invocation.
var (
	sysFormsCache     []SysFormItem
	sysFormsCacheErr  error
	sysFormsCacheOnce sync.Once
)

// formFieldsCache caches GET /forms/{formId} results for the lifetime of the server,
// avoiding repeated HTTP round-trips on every createActor call.
var (
	formFieldsCache   = map[int]map[string]interface{}{}
	formFieldsCacheMu sync.RWMutex
)

// actorFormIDCache maps actor UUID → formId, populated from createActor responses
// and on-demand via getActor so injectManageLayerData can resolve actor UUIDs.
var (
	actorFormIDCache   = map[string]int{}
	actorFormIDCacheMu sync.RWMutex
)

// hierarchyEdgeTypeID caches the ID of the "hierarchy" edge type fetched from getEdgeTypes.
var (
	hierarchyEdgeTypeID int
	hierarchyEdgeTypeMu sync.RWMutex
)

// loadSysForms reads sys-forms.yaml from the current working directory once
// per server lifetime. If the file does not exist, returns (nil, nil) so the
// caller can fall through to the unmodified flow.
func loadSysForms() ([]SysFormItem, error) {
	sysFormsCacheOnce.Do(func() {
		data, err := os.ReadFile("sys-forms.yaml")
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			sysFormsCacheErr = err
			return
		}
		var items []SysFormItem
		if err := yaml.Unmarshal(data, &items); err != nil {
			sysFormsCacheErr = fmt.Errorf("failed to parse sys-forms.yaml: %w", err)
			return
		}
		sysFormsCache = items
	})
	return sysFormsCache, sysFormsCacheErr
}

// findFormInTree searches `forms` for an item with the given id, looking at the
// root level and recursively inside `childs`. Returns the discovered item's
// parent id (0 if at root) and whether the item was nested under a parent.
func findFormInTree(forms []SysFormItem, formID, parentID int) (parent int, isChild, found bool) {
	for i := range forms {
		if forms[i].ID == formID {
			return parentID, parentID != 0, true
		}
		if len(forms[i].Childs) > 0 {
			if p, ch, ok := findFormInTree(forms[i].Childs, formID, forms[i].ID); ok {
				return p, ch, true
			}
		}
	}
	return 0, false, false
}

// fetchFormFieldValues calls GET /forms/{formId} via the getForm
// operation and returns a map of field-id -> value built from
// sections[].content[]. The `view` field is parsed from a JSON string to an
// object; all other values are passed through as-is.
// Results are cached in formFieldsCache for the lifetime of the server.
func fetchFormFieldValues(ctx context.Context, formID int) (map[string]interface{}, error) {
	formFieldsCacheMu.RLock()
	if cached, ok := formFieldsCache[formID]; ok {
		formFieldsCacheMu.RUnlock()
		log.Printf("fetchFormFieldValues: cache hit formId=%d", formID)
		return cached, nil
	}
	formFieldsCacheMu.RUnlock()

	log.Printf("fetchFormFieldValues: cache miss formId=%d, fetching from API", formID)

	var formOp *Operation
	for i, op := range globalOperations {
		if operationToolName(op) == "getForm" {
			formOp = &globalOperations[i]
			break
		}
	}
	if formOp == nil {
		return nil, fmt.Errorf("getForm operation not found")
	}

	queryParams := map[string]interface{}{"formId": strconv.Itoa(formID)}
	result, err := executeOperation(ctx, *formOp, queryParams, nil, nil, mcp.CallToolRequest{})
	if err != nil {
		return nil, err
	}
	var responseText string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			responseText = tc.Text
		}
	}
	if result.IsError {
		return nil, fmt.Errorf("getForm error: %s", responseText)
	}

	var apiResult struct {
		Data struct {
			Form struct {
				PictureBase64 string `json:"pictureBase64"`
				Sections      []struct {
					Content []struct {
						ID    string      `json:"id"`
						Value interface{} `json:"value"`
					} `json:"content"`
				} `json:"sections"`
			} `json:"form"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &apiResult); err != nil {
		return nil, fmt.Errorf("failed to parse /forms/%d response: %w", formID, err)
	}

	fields := map[string]interface{}{}
	for _, s := range apiResult.Data.Form.Sections {
		for _, f := range s.Content {
			value := f.Value
			if f.ID == "view" {
				if str, ok := value.(string); ok && str != "" {
					var parsed interface{}
					if jsonErr := json.Unmarshal([]byte(str), &parsed); jsonErr == nil {
						value = parsed
					}
				}
			}
			fields[f.ID] = value
		}
	}
	if apiResult.Data.Form.PictureBase64 != "" {
		fields["pictureBase64"] = apiResult.Data.Form.PictureBase64
	}

	formFieldsCacheMu.Lock()
	formFieldsCache[formID] = fields
	formFieldsCacheMu.Unlock()
	log.Printf("fetchFormFieldValues: cached formId=%d (%d fields)", formID, len(fields))

	return fields, nil
}

// injectCreateActorData looks up the createActor formId in sys-forms.yaml; if
// the form is nested under a parent, it fetches forms_graph/tree for that
// parent and replaces the `data` field in args["body"] with a __form__<id>:<k>
// map built from the parent's children. Other body fields are preserved.
// injectCreateActorData returns the original child formId (non-zero only when a
// parent swap occurred), so the caller can fix up actorFormIDCache after the
// API response is received.
func injectCreateActorData(ctx context.Context, args, queryParams map[string]interface{}) (int, error) {
	formID := toInt(queryParams["formId"])
	if formID == 0 {
		return 0, nil
	}

	// Always fetch (and cache) form fields so subsequent manageLayer calls can use them.
	fields, err := fetchFormFieldValues(ctx, formID)
	if err != nil {
		return 0, err
	}

	// Build __form__ data only when sys-forms.yaml says this is a child form.
	sysForms, sysErr := loadSysForms()
	if sysErr != nil || sysForms == nil {
		return 0, nil
	}

	parentID, isChild, found := findFormInTree(sysForms, formID, 0)
	if !found || !isChild {
		return 0, nil
	}

	autoData := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		autoData[fmt.Sprintf("__form__%d:%s", formID, k)] = v
	}

	body := map[string]interface{}{}
	if bodyStr, ok := args["body"].(string); ok && bodyStr != "" {
		if jsonErr := json.Unmarshal([]byte(bodyStr), &body); jsonErr != nil {
			body = map[string]interface{}{}
		}
	}
	body["data"] = autoData

	newBody, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	args["body"] = string(newBody)

	queryParams["formId"] = strconv.Itoa(parentID)
	return formID, nil // return original child formId so caller can correct the cache
}

// overrideActorFormID parses the actor UUID from responseText and forcibly sets
// actorFormIDCache[uuid] = childFormID, overriding whatever cacheActorFormIDFromResult
// stored. This is needed because the API stores the actor under the parent formId
// but picture/fields belong to the original child form.
func overrideActorFormID(responseText string, childFormID int) {
	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &resp); err != nil || resp.Data.ID == "" {
		return
	}
	actorFormIDCacheMu.Lock()
	actorFormIDCache[resp.Data.ID] = childFormID
	actorFormIDCacheMu.Unlock()
	log.Printf("actorFormIDCache: corrected actor %s → formId=%d (child)", resp.Data.ID, childFormID)
}

// cacheActorFormIDFromResult parses a createActor/getActor response and populates
// actorFormIDCache (UUID→formId).
//
// FormId resolution order:
//  1. Scan data.data keys for "__form__<N>:<field>" pattern — extract N as formId.
//  2. Fall back to data.formId if no __form__ keys are present.
//
// Note: formFieldsCache is intentionally NOT populated here. Actor data may lack
// pictureBase64 (stored in /forms/{id}, not in actor data.data). fetchFormFieldValues
// is responsible for populating formFieldsCache via the form API.
func cacheActorFormIDFromResult(responseText string) {
	var resp struct {
		Data struct {
			ID     string                 `json:"id"`
			FormID int                    `json:"formId"`
			Data   map[string]interface{} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &resp); err != nil {
		return
	}
	if resp.Data.ID == "" {
		return
	}

	formID := 0

	// Step 1: extract formId from __form__<N>:<fieldName> keys.
	for key := range resp.Data.Data {
		if !strings.HasPrefix(key, "__form__") {
			continue
		}
		withoutPrefix := strings.TrimPrefix(key, "__form__")
		colonIdx := strings.Index(withoutPrefix, ":")
		if colonIdx < 0 {
			continue
		}
		fid := toInt(withoutPrefix[:colonIdx])
		if fid != 0 {
			formID = fid
			break
		}
	}

	// Step 2: fall back to data.formId when no __form__ keys found.
	if formID == 0 {
		formID = resp.Data.FormID
	}
	if formID == 0 {
		return
	}

	actorFormIDCacheMu.Lock()
	actorFormIDCache[resp.Data.ID] = formID
	actorFormIDCacheMu.Unlock()
	log.Printf("actorFormIDCache: actor %s → formId=%d", resp.Data.ID, formID)
}

// resolveActorFormID returns the formId for an actor UUID: checks actorFormIDCache
// first, then falls back to calling the getActor API and caching the result.
func resolveActorFormID(ctx context.Context, actorID string) int {
	actorFormIDCacheMu.RLock()
	if fid, ok := actorFormIDCache[actorID]; ok {
		actorFormIDCacheMu.RUnlock()
		log.Printf("actorFormIDCache: hit actor %s → formId=%d", actorID, fid)
		return fid
	}
	actorFormIDCacheMu.RUnlock()

	var actorOp *Operation
	for i, op := range globalOperations {
		if operationToolName(op) == "getActor" {
			actorOp = &globalOperations[i]
			break
		}
	}
	if actorOp == nil {
		log.Printf("resolveActorFormID: getActor operation not found")
		return 0
	}

	log.Printf("resolveActorFormID: cache miss actor %s, fetching via getActor", actorID)
	result, err := executeOperation(ctx, *actorOp, map[string]interface{}{"actorId": actorID}, nil, nil, mcp.CallToolRequest{})
	if err != nil || result.IsError {
		log.Printf("resolveActorFormID: getActor failed for %s", actorID)
		return 0
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			cacheActorFormIDFromResult(tc.Text)
		}
	}
	actorFormIDCacheMu.RLock()
	fid := actorFormIDCache[actorID]
	actorFormIDCacheMu.RUnlock()
	return fid
}

// injectManageLayerData iterates over each item in the manageLayer request body
// array. For items whose data.id resolves to a known cached form that has a
// non-empty pictureBase64, the value is injected into data.areaPicture.img.
func injectManageLayerData(ctx context.Context, args map[string]interface{}) error {
	bodyStr, ok := args["body"].(string)
	if !ok || bodyStr == "" {
		return nil
	}
	var body []interface{}
	if err := json.Unmarshal([]byte(bodyStr), &body); err != nil {
		return nil // not an array, skip silently
	}
	if len(body) == 0 {
		return nil
	}

	modified := false
	for _, raw := range body {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		data, ok := item["data"].(map[string]interface{})
		if !ok {
			continue
		}
		formID := toInt(data["id"])
		if formID == 0 {
			// data.id is a UUID actor ID — resolve formId via cache or getActor API.
			if actorID, ok := data["id"].(string); ok && actorID != "" {
				formID = resolveActorFormID(ctx, actorID)
			}
		}
		if formID == 0 {
			continue
		}
		fields, err := fetchFormFieldValues(ctx, formID)
		if err != nil {
			log.Printf("Warning: manageLayer failed to fetch form %d: %v", formID, err)
			continue
		}
		pictureBase64, ok := fields["pictureBase64"].(string)
		if !ok || pictureBase64 == "" {
			continue
		}
		areaPicture, _ := data["areaPicture"].(map[string]interface{})
		if areaPicture == nil {
			areaPicture = map[string]interface{}{}
		}
		areaPicture["img"] = pictureBase64
		areaPicture["type"] = "flowchart"

		// Parse view field (already decoded to map by fetchFormFieldValues).
		var viewMap map[string]interface{}
		if viewRaw := fields["view"]; viewRaw != nil {
			viewMap, _ = viewRaw.(map[string]interface{})
		}

		// Fill areaPicture height/width from view.size if caller didn't provide them.
		if _, hasH := areaPicture["height"]; !hasH {
			if viewMap != nil {
				if sizeRaw, ok := viewMap["size"].(map[string]interface{}); ok {
					if h, ok := sizeRaw["h"]; ok {
						areaPicture["height"] = h
					}
					if w, ok := sizeRaw["w"]; ok {
						areaPicture["width"] = w
					}
				}
			}
		}
		data["areaPicture"] = areaPicture

		// Build layerSettings at the same level as areaPicture.
		layerSettings := map[string]interface{}{
			"height": areaPicture["height"],
			"width":  areaPicture["width"],
		}
		if blockID, ok := fields["blockId"].(string); ok && blockID != "" {
			layerSettings["blockId"] = blockID
		}
		if viewMap != nil {
			if shape, ok := viewMap["shape"]; ok {
				layerSettings["shape"] = shape
			}
			if textFrame, ok := viewMap["textFrame"]; ok {
				layerSettings["textFrame"] = textFrame
			}
		}
		data["layerSettings"] = layerSettings
		modified = true
	}

	if modified {
		newBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		args["body"] = string(newBody)
	}
	return nil
}

// fetchHierarchyEdgeTypeID calls getEdgeTypes, finds the "hierarchy" entry, and caches its ID.
func fetchHierarchyEdgeTypeID(ctx context.Context) (int, error) {
	hierarchyEdgeTypeMu.RLock()
	if hierarchyEdgeTypeID != 0 {
		cached := hierarchyEdgeTypeID
		hierarchyEdgeTypeMu.RUnlock()
		log.Printf("hierarchyEdgeTypeID: cache hit id=%d", cached)
		return cached, nil
	}
	hierarchyEdgeTypeMu.RUnlock()

	var edgeTypesOp *Operation
	for i, op := range globalOperations {
		if operationToolName(op) == "getEdgeTypes" {
			edgeTypesOp = &globalOperations[i]
			break
		}
	}
	if edgeTypesOp == nil {
		return 0, fmt.Errorf("getEdgeTypes operation not found")
	}

	qp := map[string]interface{}{}
	if accID := os.Getenv("WORKSPACE_ID"); accID != "" {
		qp["accId"] = accID
	}
	result, err := executeOperation(ctx, *edgeTypesOp, qp, nil, nil, mcp.CallToolRequest{})
	if err != nil {
		return 0, err
	}
	var responseText string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			responseText = tc.Text
		}
	}
	if result.IsError {
		return 0, fmt.Errorf("getEdgeTypes error: %s", responseText)
	}

	var apiResult struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &apiResult); err != nil {
		return 0, fmt.Errorf("failed to parse getEdgeTypes response: %w", err)
	}

	for _, et := range apiResult.Data {
		if et.Name == "hierarchy" {
			hierarchyEdgeTypeMu.Lock()
			hierarchyEdgeTypeID = et.ID
			hierarchyEdgeTypeMu.Unlock()
			log.Printf("hierarchyEdgeTypeID cached: %d", et.ID)
			return et.ID, nil
		}
	}
	return 0, fmt.Errorf("hierarchy edge type not found in getEdgeTypes response")
}

// injectCreateLinkData auto-populates edgeTypeId in the createLink request body
// by fetching the "hierarchy" type ID from getEdgeTypes (result is cached).
// The field is only injected when the caller hasn't provided it explicitly.
func injectCreateLinkData(ctx context.Context, args map[string]interface{}) error {
	typeID, err := fetchHierarchyEdgeTypeID(ctx)
	if err != nil {
		return err
	}

	body := map[string]interface{}{}
	if bodyStr, ok := args["body"].(string); ok && bodyStr != "" {
		_ = json.Unmarshal([]byte(bodyStr), &body)
	}

	if _, hasTypeID := body["edgeTypeId"]; !hasTypeID {
		body["edgeTypeId"] = typeID
	}

	newBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	args["body"] = string(newBody)
	return nil
}

// autoPlaceEdgesOnLayer calls getLayer to build an actorId→laId map, then
// resolves edge IDs via getActorLinks, and finally calls manageLayer to draw
// each of the supplied edges as a visual element.
// pairs is a slice of [sourceActorId, targetActorId] taken from the massLink request body.
func autoPlaceEdgesOnLayer(ctx context.Context, layerID string, pairs [][2]string) error {
	if len(pairs) == 0 {
		return nil
	}

	// 1. Find getLayer operation
	var getLayerOp *Operation
	for i, op := range globalOperations {
		if operationToolName(op) == "getLayer" {
			getLayerOp = &globalOperations[i]
			break
		}
	}
	if getLayerOp == nil {
		return fmt.Errorf("getLayer operation not found")
	}

	// 2. Call getLayer to get actorId → laId mapping
	glResult, err := executeOperation(ctx, *getLayerOp,
		map[string]interface{}{"layerId": layerID},
		nil, nil, mcp.CallToolRequest{})
	if err != nil {
		return fmt.Errorf("getLayer failed: %w", err)
	}
	var glText string
	for _, c := range glResult.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			glText = tc.Text
		}
	}
	if glResult.IsError {
		return fmt.Errorf("getLayer error: %s", glText)
	}

	var glResp struct {
		Data struct {
			Nodes []struct {
				ID   string `json:"id"`
				LaID int    `json:"laId"`
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(glText), &glResp); err != nil {
		return fmt.Errorf("failed to parse getLayer response: %w", err)
	}

	laIDMap := make(map[string]int, len(glResp.Data.Nodes))
	for _, n := range glResp.Data.Nodes {
		laIDMap[n.ID] = n.LaID
	}
	log.Printf("autoPlaceEdgesOnLayer: layer %s has %d nodes, resolving %d edges", layerID, len(laIDMap), len(pairs))

	// 3. Collect unique source actors and fetch their edge IDs via getActorLinks.
	type edgeKey struct{ src, tgt string }
	edgeIDMap := make(map[edgeKey]string)
	seen := make(map[string]bool)
	for _, p := range pairs {
		src := p[0]
		if seen[src] {
			continue
		}
		seen[src] = true
		links, fetchErr := fetchActorLinks(ctx, src)
		if fetchErr != nil {
			log.Printf("autoPlaceEdgesOnLayer: getActorLinks(%s) failed: %v", src, fetchErr)
			continue
		}
		for _, lnk := range links {
			edgeIDMap[edgeKey{lnk[0], lnk[1]}] = lnk[2]
		}
	}

	// 4. Build manageLayer body — skip edges whose actors aren't on the layer
	//    or whose IDs couldn't be resolved.
	type edgeItem struct {
		Action string `json:"action"`
		Data   struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			LaIDSrc int    `json:"laIdSource"`
			LaIDTgt int    `json:"laIdTarget"`
		} `json:"data"`
	}
	var items []edgeItem
	for _, p := range pairs {
		src, tgt := p[0], p[1]
		srcLaID, srcOK := laIDMap[src]
		tgtLaID, tgtOK := laIDMap[tgt]
		if !srcOK || !tgtOK {
			log.Printf("autoPlaceEdgesOnLayer: skipping %s→%s — source or target not on layer %s", src, tgt, layerID)
			continue
		}
		edgeID, edgeOK := edgeIDMap[edgeKey{src, tgt}]
		if !edgeOK {
			log.Printf("autoPlaceEdgesOnLayer: skipping %s→%s — edge not found via getActorLinks", src, tgt)
			continue
		}
		item := edgeItem{Action: "create"}
		item.Data.ID = edgeID
		item.Data.Type = "edge"
		item.Data.LaIDSrc = srcLaID
		item.Data.LaIDTgt = tgtLaID
		items = append(items, item)
	}
	if len(items) == 0 {
		log.Printf("autoPlaceEdgesOnLayer: no eligible edges for layer %s", layerID)
		return nil
	}

	// 5. Find manageLayer operation
	var manageLayerOp *Operation
	for i, op := range globalOperations {
		if operationToolName(op) == "manageLayer" {
			manageLayerOp = &globalOperations[i]
			break
		}
	}
	if manageLayerOp == nil {
		return fmt.Errorf("manageLayer operation not found")
	}

	bodyBytes, err := json.Marshal(items)
	if err != nil {
		return err
	}
	mlReq := mcp.CallToolRequest{}
	mlReq.Params.Arguments = map[string]interface{}{"body": string(bodyBytes)}
	mlResult, err := executeOperation(ctx, *manageLayerOp,
		map[string]interface{}{"layerId": layerID},
		nil, nil, mlReq)
	if err != nil {
		return fmt.Errorf("manageLayer failed: %w", err)
	}
	if mlResult.IsError {
		for _, c := range mlResult.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				return fmt.Errorf("manageLayer error: %s", tc.Text)
			}
		}
	}
	log.Printf("autoPlaceEdgesOnLayer: placed %d edges on layer %s", len(items), layerID)
	return nil
}

// fetchActorLinks calls getActorLinks for the given actor and returns a slice of
// [source, target, edgeId] triples.
func fetchActorLinks(ctx context.Context, actorID string) ([][3]string, error) {
	var op *Operation
	for i, o := range globalOperations {
		if operationToolName(o) == "getActorLinks" {
			op = &globalOperations[i]
			break
		}
	}
	if op == nil {
		return nil, fmt.Errorf("getActorLinks operation not found")
	}

	result, err := executeOperation(ctx, *op,
		map[string]interface{}{"actorId": actorID},
		nil, nil, mcp.CallToolRequest{})
	if err != nil {
		return nil, fmt.Errorf("getActorLinks HTTP failed: %w", err)
	}
	var text string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text = tc.Text
		}
	}

	var resp struct {
		Data []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse getActorLinks response: %w", err)
	}
	out := make([][3]string, 0, len(resp.Data))
	for _, d := range resp.Data {
		if d.ID != "" {
			out = append(out, [3]string{d.Source, d.Target, d.ID})
		}
	}
	return out, nil
}

// parseMassLinkPairs extracts [source, target] pairs from the massLink request body.
// These are used for auto-placement on a layer after massLink executes, since the API
// response does not reliably return the created edge IDs.
func parseMassLinkPairs(bodyStr string) [][2]string {
	var items []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal([]byte(bodyStr), &items); err != nil {
		return nil
	}
	out := make([][2]string, 0, len(items))
	for _, item := range items {
		if item.Source != "" && item.Target != "" {
			out = append(out, [2]string{item.Source, item.Target})
		}
	}
	return out
}

// injectMassLinkData ensures every element in the massLink array body has an
// edgeTypeId, defaulting to the cached hierarchy edge type when absent.
func injectMassLinkData(ctx context.Context, args map[string]interface{}) error {
	typeID, err := fetchHierarchyEdgeTypeID(ctx)
	if err != nil {
		return err
	}

	bodyStr, _ := args["body"].(string)
	var items []map[string]interface{}
	if bodyStr != "" {
		if err := json.Unmarshal([]byte(bodyStr), &items); err != nil {
			return fmt.Errorf("massLink body is not a JSON array: %w", err)
		}
	}

	changed := false
	for i, item := range items {
		if _, hasTypeID := item["edgeTypeId"]; !hasTypeID {
			items[i]["edgeTypeId"] = typeID
			changed = true
		}
	}

	if changed || bodyStr == "" {
		newBody, err := json.Marshal(items)
		if err != nil {
			return err
		}
		args["body"] = string(newBody)
	}
	return nil
}

// toInt converts loosely-typed JSON values (string, float64, int) to an int.
// Returns 0 when the input cannot be interpreted as an integer.
func toInt(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}

// simulatorWorkspace holds a single entry from the account workspaces API.
type simulatorWorkspace struct {
	ID    int    `json:"id"`
	ExtID string `json:"ext_id"`
	Name  string `json:"name"`
}

// fetchSimulatorWorkspaces calls account.corezoid.com to list workspaces available
// to the authenticated user. Uses the Simulator JWT token for authorization.
func fetchSimulatorWorkspaces(accountURL, authorization string) ([]simulatorWorkspace, error) {
	accountURL = strings.TrimRight(accountURL, "/")
	apiURL := accountURL + "/face/api/1/workspaces?limit=100&offset=0"

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authorization)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data []simulatorWorkspace `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse workspace list: %w", err)
	}
	return result.Data, nil
}

// handleLogin runs the OAuth2 PKCE flow, saves credentials, then uses MCP elicitation
// to let the user pick a workspace from the list fetched from the account API.
func handleLogin(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	accountURL, _ := args["account_url"].(string)

	// If account_url not provided and not in env, ask via elicitation.
	if accountURL == "" && os.Getenv("ACCOUNT_URL") == "" {
		mcpSrv := server.ServerFromContext(ctx)
		if mcpSrv != nil {
			elicitReq := mcp.ElicitationRequest{
				Request: mcp.Request{Method: string(mcp.MethodElicitationCreate)},
				Params: mcp.ElicitationParams{
					Message: "Enter your Corezoid Account URL (leave blank for the default: https://account.corezoid.com):",
					RequestedSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"account_url": map[string]interface{}{
								"type":    "string",
								"title":   "Account URL",
								"default": auth.DefaultAccountURL,
							},
						},
					},
				},
			}
			result, err := mcpSrv.RequestElicitation(ctx, elicitReq)
			if err == nil && result != nil && result.Action == mcp.ElicitationResponseActionAccept {
				content, _ := result.Content.(map[string]interface{})
				accountURL, _ = content["account_url"].(string)
			}
		}
	}

	if accountURL != "" {
		if err := auth.SaveAccountURL(accountURL); err != nil {
			log.Printf("Warning: failed to save ACCOUNT_URL: %v", err)
		}
	}

	// Skip OAuth if a valid (non-expired) token is already saved.
	existingCreds, _ := auth.Load()
	if existingCreds != nil && !auth.IsExpired(existingCreds) {
		globalApiConfig.Authorization = existingCreds.AuthorizationHeader()
	} else {
		creds, err := auth.PKCEFlow(accountURL, globalOAuthClientID, nil)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("[Error] OAuth2 login failed: %v", err)), nil
		}
		if err := auth.Save(creds); err != nil {
			log.Printf("Warning: failed to save credentials: %v", err)
		}
		globalApiConfig.Authorization = creds.AuthorizationHeader()
	}

	// Fetch workspaces and present a selection if WORKSPACE_ID is not yet set.
	if os.Getenv("WORKSPACE_ID") == "" {
		acctURL := os.Getenv("ACCOUNT_URL")
		if acctURL == "" {
			acctURL = auth.DefaultAccountURL
		}

		workspaces, fetchErr := fetchSimulatorWorkspaces(acctURL, globalApiConfig.Authorization)
		if fetchErr != nil {
			log.Printf("Warning: failed to fetch workspace list: %v", fetchErr)
		}

		mcpSrv := server.ServerFromContext(ctx)
		if mcpSrv != nil && fetchErr == nil && len(workspaces) > 0 {
			// Build enum labels and a reverse map to ext_id.
			enumVals := make([]string, len(workspaces))
			wsIDByLabel := make(map[string]string, len(workspaces))
			for i, ws := range workspaces {
				label := ws.Name + " (" + ws.ExtID + ")"
				enumVals[i] = label
				wsIDByLabel[label] = ws.ExtID
			}

			elicitReq := mcp.ElicitationRequest{
				Request: mcp.Request{Method: string(mcp.MethodElicitationCreate)},
				Params: mcp.ElicitationParams{
					Message: "Authentication successful! Select the Simulator workspace to use:",
					RequestedSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"workspace": map[string]interface{}{
								"type":  "string",
								"title": "Workspace",
								"enum":  enumVals,
							},
						},
						"required": []string{"workspace"},
					},
				},
			}

			result, err := mcpSrv.RequestElicitation(ctx, elicitReq)
			if err == nil && result != nil && result.Action == mcp.ElicitationResponseActionAccept {
				content, _ := result.Content.(map[string]interface{})
				if selected, _ := content["workspace"].(string); selected != "" {
					accID := wsIDByLabel[selected]
					if accID == "" {
						accID = selected
					}
					if saveErr := auth.SaveWorkspaceID(accID); saveErr != nil {
						log.Printf("Warning: failed to save workspace ID: %v", saveErr)
					}
					if fetchErr := fetchAndSaveSystemForms(ctx, accID); fetchErr != nil {
						log.Printf("Warning: failed to fetch/save system forms: %v", fetchErr)
					}
					return mcp.NewToolResultText(fmt.Sprintf("Setup complete! Workspace %q saved to .env. You can now use all Simulator tools.", accID)), nil
				}
			}
		}

		// Elicitation not supported or failed — return workspace list as text for Claude to handle.
		if fetchErr == nil && len(workspaces) > 0 {
			var sb strings.Builder
			sb.WriteString("Authentication successful! Token saved to .env.\n\nAvailable workspaces:\n")
			for _, ws := range workspaces {
				sb.WriteString(fmt.Sprintf("  %s — %s\n", ws.ExtID, ws.Name))
			}
			sb.WriteString("\nCall set-workspace(acc_id=<ext_id>) with the workspace you want to use.")
			return mcp.NewToolResultText(sb.String()), nil
		}
	}

	if accID := os.Getenv("WORKSPACE_ID"); accID != "" {
		if fetchErr := fetchAndSaveSystemForms(ctx, accID); fetchErr != nil {
			log.Printf("Warning: failed to fetch/save system forms: %v", fetchErr)
		}
	}
	return mcp.NewToolResultText("Authentication successful! Token saved to .env. You can now use Simulator tools."), nil
}

// handleSetWorkspace saves the workspace ID (accId) to .env as SIMULATOR_ACC_ID.
func handleSetWorkspace(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	accID, ok := args["acc_id"].(string)
	if !ok || accID == "" {
		return mcp.NewToolResultError("[Error] missing or invalid acc_id parameter"), nil
	}

	if err := auth.SaveWorkspaceID(accID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("[Error] failed to save workspace ID: %v", err)), nil
	}

	if globalApiConfig.Authorization != "" {
		if fetchErr := fetchAndSaveSystemForms(ctx, accID); fetchErr != nil {
			log.Printf("Warning: failed to fetch/save system forms: %v", fetchErr)
		}
	}

	return mcp.NewToolResultText(fmt.Sprintf("Workspace saved: WORKSPACE_ID=%s", accID)), nil
}

// ensureAuth checks that globalApiConfig.Authorization is set.
// If not, it tries to load/refresh from saved credentials.
// If still empty, uses MCP Elicitation to ask the user whether to start OAuth2.
// Returns an error result if authentication cannot be established.
func ensureAuth(ctx context.Context) *mcp.CallToolResult {
	// Already authenticated
	if globalApiConfig.Authorization != "" {
		return nil
	}

	// Try loading saved credentials
	creds, err := auth.Load()
	if err == nil && creds != nil {
		if !auth.IsExpired(creds) {
			globalApiConfig.Authorization = creds.AuthorizationHeader()
			return nil
		}
	}

	// No valid token — use elicitation to ask the user whether to open OAuth2 in browser
	mcpSrv := server.ServerFromContext(ctx)
	if mcpSrv != nil {
		elicitReq := mcp.ElicitationRequest{
			Request: mcp.Request{Method: string(mcp.MethodElicitationCreate)},
			Params: mcp.ElicitationParams{
				Message: "Simulator authentication required. Would you like to open a browser and sign in via OAuth2?",
				RequestedSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"confirm": map[string]interface{}{
							"type":        "boolean",
							"title":       "Open browser to sign in",
							"description": "Click OK to open your browser for OAuth2 login",
						},
					},
				},
			},
		}

		result, err := mcpSrv.RequestElicitation(ctx, elicitReq)
		if err == nil && result != nil && result.Action == mcp.ElicitationResponseActionAccept {
			if creds, err := auth.PKCEFlow("", globalOAuthClientID, nil); err == nil {
				_ = auth.Save(creds)
				globalApiConfig.Authorization = creds.AuthorizationHeader()
				return nil
			}
		}
	}

	return mcp.NewToolResultError("[Error] Not authenticated. Set ACCESS_TOKEN env var, or run the 'login' tool to authenticate via OAuth2.")
}

func executeOperation(ctx context.Context, op Operation, queryParams, headerParams, bodyParams map[string]interface{}, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	currentReqURL := op.URL
	log.Printf("DEBUG: pathParams called for operation %+v, URL: %s", queryParams, currentReqURL)

	// Handle path parameters
	for _, param := range op.Parameters {
		log.Printf("DEBUG: Found  parameter: %+v", param)
		if param.TIn == "path" {

			if queryParams != nil {
				if paramValue, exists := queryParams[param.Name]; exists {
					if paramStr, ok := paramValue.(string); ok {
						log.Printf("DEBUG: Replacing path parameter %s with value %s, in URL %s", param.Name, paramStr, currentReqURL)
						currentReqURL = strings.Replace(currentReqURL, fmt.Sprintf("{%s}", param.Name), paramStr, 1)
					} else {
						if paramBool, ok := paramValue.(bool); ok {
							currentReqURL = strings.Replace(currentReqURL, fmt.Sprintf("{%s}", param.Name), strconv.FormatBool(paramBool), 1)
						} else {
							if paramInt, ok := paramValue.(float64); ok {
								currentReqURL = strings.Replace(currentReqURL, fmt.Sprintf("{%s}", param.Name), strconv.Itoa(int(paramInt)), 1)
							} else {
								return mcp.NewToolResultError(fmt.Sprintf("[Error] invalid path parameter %s.", param.Name)), nil
							}

						}
					}
				} else if param.Required {
					return mcp.NewToolResultError(fmt.Sprintf("[Error] missing required path parameter: '%+v'", param)), nil
				}
			} else if param.Required {
				return mcp.NewToolResultError(fmt.Sprintf("[Error] missing required path parameter:'%s'", param.Name)), nil
			}
		}
	}

	// Handle query parameters
	u, err := url.Parse(currentReqURL)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("[Error] failed to parse URL: %v", err)), nil
	}
	q := u.Query()
	for _, param := range op.Parameters {
		// Skip path params here — they were already substituted into the URL above.
		if param.In == "query" && param.TIn != "path" {
			if queryParams != nil {
				if paramValue, exists := queryParams[param.Name]; exists {
					if paramStr, ok := paramValue.(string); ok {
						q.Set(param.Name, paramStr)
					} else {
						if paramBool, ok := paramValue.(bool); ok {
							q.Set(param.Name, strconv.FormatBool(paramBool))
						} else {
							if paramInt, ok := paramValue.(float64); ok {
								q.Set(param.Name, strconv.Itoa(int(paramInt)))
							} else {
								return mcp.NewToolResultError(fmt.Sprintf("[Error] invalid query parameter %s", param.Name)), nil
							}
						}

					}
				} else if param.Required {
					return mcp.NewToolResultError(fmt.Sprintf("[Error] missing required query parameter: %s", param.Name)), nil
				}
			} else if param.Required {
				return mcp.NewToolResultError(fmt.Sprintf("[Error] missing required query parameter: %s", param.Name)), nil
			}
		}
	}
	u.RawQuery = q.Encode()
	currentReqURL = u.String()

	// Handle request body
	var reqBodyBytes []byte
	hasBody := false

	// Check if we have a body to process
	if bodyStr, ok := request.GetArguments()["body"].(string); ok && bodyStr != "" {
		// Check if the schema expects an array and wrap the body parameter if needed
		isArray := checkIfRequestBodyIsArray(op)
		log.Printf("DEBUG: checkIfRequestBodyIsArray returned: %t for operation %s", isArray, op.ID)

		if isArray {
			// First, try to unmarshal the original body string to check if it's already an array
			var originalBody interface{}
			if err := json.Unmarshal([]byte(bodyStr), &originalBody); err == nil {
				// Check if the original body is already an array
				if _, isSlice := originalBody.([]interface{}); isSlice {
					log.Printf("DEBUG: Body is already an array, using as-is")
					reqBodyBytes, _ = json.Marshal(originalBody)
				} else {
					log.Printf("DEBUG: Body is not an array, wrapping in array")
					// Convert body to array
					arrayBody := []interface{}{originalBody}
					reqBodyBytes, _ = json.Marshal(arrayBody)
				}
			} else {
				log.Printf("DEBUG: Failed to unmarshal original body, error: %v", err)
				return mcp.NewToolResultError(fmt.Sprintf("[Error] invalid JSON in body parameter: %v", err)), nil
			}
			log.Printf("DEBUG: Final body: %s", string(reqBodyBytes))
		} else {
			// Schema doesn't expect an array, use the original body as-is
			reqBodyBytes = []byte(bodyStr)
			// If the schema declares a "data" property and the body omits it, default to {}
			if requestBodyHasProperty(op, "data") {
				var bodyMap map[string]interface{}
				if err := json.Unmarshal(reqBodyBytes, &bodyMap); err == nil {
					if _, hasData := bodyMap["data"]; !hasData {
						bodyMap["data"] = map[string]interface{}{}
						if patched, err := json.Marshal(bodyMap); err == nil {
							reqBodyBytes = patched
						}
					}
				}
			}
			log.Printf("DEBUG: Using body as-is: %s", string(reqBodyBytes))
		}
		hasBody = true
	} else if bodyParams != nil && len(bodyParams) > 0 {
		// Fallback: use bodyParams if available
		reqBodyBytes, _ = json.Marshal(bodyParams)
		log.Printf("DEBUG: Using bodyParams: %s", string(reqBodyBytes))
		hasBody = true
	}

	// Create HTTP request
	var reqBody io.Reader
	if hasBody {
		reqBody = bytes.NewBuffer(reqBodyBytes)
	}

	req, err := http.NewRequest(strings.ToUpper(op.Method), currentReqURL, reqBody)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("[Error] failed to create HTTP request: %v", err)), nil
	}

	// Handle headers
	for _, param := range op.Parameters {
		if param.In == "header" {
			if headerParams != nil {
				if paramValue, exists := headerParams[param.Name]; exists {
					if paramStr, ok := paramValue.(string); ok {
						req.Header.Set(param.Name, paramStr)
					} else {
						return mcp.NewToolResultError(fmt.Sprintf("[Error] invalid header parameter %s", param.Name)), nil
					}
				} else if param.Required {
					return mcp.NewToolResultError(fmt.Sprintf("[Error] missing required header parameter: %s", param.Name)), nil
				}
			} else if param.Required {
				return mcp.NewToolResultError(fmt.Sprintf("[Error] missing required header parameter: %s", param.Name)), nil
			}
		}
	}
	log.Printf("Request  : %s %s %s %s\n", strings.ToUpper(op.Method), string(reqBodyBytes), currentReqURL, req.Header)

	// Add Authorization header if configured
	if globalApiConfig.Authorization != "" {
		req.Header.Set("Authorization", globalApiConfig.Authorization)
	}

	// Only set Content-Type if there's actually a body
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}

	// Execute request
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("[Error] failed to make HTTP request: %v", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("[Error] failed to read HTTP response: %v", err)), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func setRequestSecurity(req *http.Request, security string, basicAuth string, apiKeyAuth string, bearerAuth string) {
	securityType := strings.TrimSpace(security)

	// basic auth
	if securityType == "basic" && basicAuth != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(basicAuth))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	// bearer auth
	if securityType == "bearer" && bearerAuth != "" {
		req.Header.Set("Authorization", "Bearer "+bearerAuth)
	}

	// apiKey auth
	// Example: header:token=abc,query:token=xyz,cookie:sid=ccc
	queryValues := make(map[string]string)
	cookieValues := []*http.Cookie{}
	if securityType == "apiKey" && apiKeyAuth != "" {
		for _, part := range strings.Split(apiKeyAuth, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			// format passAs:name=value
			colonIdx := strings.Index(part, ":")
			eqIdx := strings.Index(part, "=")
			if colonIdx == -1 || eqIdx == -1 || eqIdx < colonIdx+2 {
				continue
			}
			passAs := strings.ToLower(strings.TrimSpace(part[:colonIdx]))
			name := strings.TrimSpace(part[colonIdx+1 : eqIdx])
			value := strings.TrimSpace(part[eqIdx+1:])
			switch passAs {
			case "header":
				req.Header.Set(name, value)
			case "query":
				queryValues[name] = value
			case "cookie":
				cookieValues = append(cookieValues, &http.Cookie{Name: name, Value: value})
			}
		}
	}
	// add query
	if len(queryValues) > 0 {
		origUrl := req.URL.String()
		u, err := url.Parse(origUrl)
		if err == nil {
			q := u.Query()
			for k, v := range queryValues {
				q.Set(k, v)
			}
			u.RawQuery = q.Encode()
			req.URL = u
		}
	}
	// add cookie
	for _, c := range cookieValues {
		req.AddCookie(c)
	}
}

// initializeResources scans the data/resources directory and adds them as MCP resources
func initializeResources(mcpServer *server.MCPServer) {
	resourcesPath := "data/resources"

	if _, err := os.Stat(resourcesPath); os.IsNotExist(err) {
		return
	}

	// Walk through the resources directory and add each file as a resource
	err := filepath.Walk(resourcesPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process markdown files for now
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}

		// Create a resource URI from the relative path
		relPath, err := filepath.Rel(resourcesPath, path)
		if err != nil {
			return err
		}

		// Replace backslashes with forward slashes for URI
		resourceURI := "docs/" + strings.ReplaceAll(relPath, "\\", "/")

		// Create and add the resource
		resource := mcp.NewResource(
			resourceURI,
			filepath.Base(path),
			mcp.WithResourceDescription(fmt.Sprintf("Documentation file: %s", relPath)),
			mcp.WithMIMEType("text/markdown"),
		)

		mcpServer.AddResource(resource, handleResourceRead)

		// Log available resource
		log.Printf("Added MCP resource: %s", resourceURI)

		return nil
	})

	if err != nil {
		log.Printf("Error walking resources directory: %v", err)
	}
}

// RunCLI executes a single MCP tool directly without starting the MCP server.
// It initialises global state (auth, operations) the same way LoadSwaggerServer does,
// then dispatches to the appropriate handler and returns (resultText, isError).
func RunCLI(swaggerSpec models.SwaggerSpec, apiCfg models.ApiConfig, toolName string, args map[string]interface{}) (string, bool) {
	globalOperations = buildOperations(swaggerSpec, apiCfg)
	globalSwaggerSpec = swaggerSpec
	globalApiConfig = apiCfg

	globalOAuthClientID = apiCfg.OAuthClientID
	if globalOAuthClientID == "" {
		globalOAuthClientID = os.Getenv("SIMULATOR_OAUTH_CLIENT_ID")
	}
	if globalOAuthClientID == "" {
		globalOAuthClientID = auth.DefaultClientID
	}

	if globalApiConfig.Authorization == "" {
		if creds, err := auth.Load(); err == nil && creds != nil && !auth.IsExpired(creds) {
			globalApiConfig.Authorization = creds.AuthorizationHeader()
		}
	}

	ctx := context.Background()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args

	var result *mcp.CallToolResult
	var err error

	switch toolName {
	case "login":
		result, err = handleLogin(ctx, req)
	case "set-workspace":
		result, err = handleSetWorkspace(ctx, req)
	default:
		var found *Operation
		for i, op := range globalOperations {
			if operationToolName(op) == toolName {
				found = &globalOperations[i]
				break
			}
		}
		if found == nil {
			return fmt.Sprintf("[Error] unknown tool: %s", toolName), true
		}

		queryParams := map[string]interface{}{}
		headerParams := map[string]interface{}{}
		for _, p := range found.Parameters {
			if p.Name == "accId" {
				if accID := os.Getenv("WORKSPACE_ID"); accID != "" {
					queryParams["accId"] = accID
				}
				continue
			}
			if val, ok := args[p.Name]; ok {
				if p.In == "header" {
					headerParams[p.Name] = val
				} else {
					queryParams[p.Name] = val
				}
			}
		}
		var childFormID int
		if toolName == "createActor" {
			var injErr error
			childFormID, injErr = injectCreateActorData(ctx, args, queryParams)
			if injErr != nil {
				log.Printf("Warning: createActor data injection failed: %v", injErr)
			}
		}
		if toolName == "manageLayer" {
			if injErr := injectManageLayerData(ctx, args); injErr != nil {
				log.Printf("Warning: manageLayer data injection failed: %v", injErr)
			}
		}
		if toolName == "createLink" {
			if injErr := injectCreateLinkData(ctx, args); injErr != nil {
				log.Printf("Warning: createLink data injection failed: %v", injErr)
			}
		}
		var massLinkLayerID2 string
		var massLinkPairs2 [][2]string
		if toolName == "massLink" {
			massLinkLayerID2, _ = args["layerId"].(string)
			delete(args, "layerId")
			if injErr := injectMassLinkData(ctx, args); injErr != nil {
				log.Printf("Warning: massLink data injection failed: %v", injErr)
			}
			if bodyStr, ok := args["body"].(string); ok {
				massLinkPairs2 = parseMassLinkPairs(bodyStr)
			}
		}

		var bodyParams map[string]interface{}
		if bodyStr, ok := args["body"].(string); ok && bodyStr != "" {
			var raw json.RawMessage
			if jsonErr := json.Unmarshal([]byte(bodyStr), &raw); jsonErr != nil {
				return fmt.Sprintf("[Error] invalid JSON in body: %v", jsonErr), true
			}
			// Array bodies pass through to executeOperation via req.Params.Arguments["body"].
			_ = json.Unmarshal([]byte(bodyStr), &bodyParams)
		}

		result, err = executeOperation(ctx, *found, queryParams, headerParams, bodyParams, req)
		if toolName == "createActor" && err == nil && result != nil && !result.IsError {
			for _, c := range result.Content {
				if tc, ok := c.(mcp.TextContent); ok {
					cacheActorFormIDFromResult(tc.Text)
					if childFormID != 0 {
						overrideActorFormID(tc.Text, childFormID)
					}
				}
			}
		}
		if toolName == "massLink" && massLinkLayerID2 != "" && len(massLinkPairs2) > 0 && err == nil && result != nil && !result.IsError {
			log.Printf("massLink: placing %d edges on layer %s", len(massLinkPairs2), massLinkLayerID2)
			if placeErr := autoPlaceEdgesOnLayer(ctx, massLinkLayerID2, massLinkPairs2); placeErr != nil {
				log.Printf("Warning: autoPlaceEdgesOnLayer failed: %v", placeErr)
			}
		}
	}

	if err != nil {
		return err.Error(), true
	}
	if result == nil {
		return "", false
	}

	var sb strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), result.IsError
}

// handleResourceRead handles MCP resource read requests
func handleResourceRead(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	uri := request.Params.URI

	// Remove the "docs/" prefix to get the file path
	if !strings.HasPrefix(uri, "docs/") {
		return nil, fmt.Errorf("invalid resource URI: %s", uri)
	}

	relPath := strings.TrimPrefix(uri, "docs/")
	fullPath := filepath.Join("data/resources", relPath)

	// Check if file exists and is within the resources directory
	absResourcePath, err := filepath.Abs("data/resources")
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %v", err)
	}

	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %v", err)
	}

	if !strings.HasPrefix(absFullPath, absResourcePath) {
		return nil, fmt.Errorf("access denied: %s", uri)
	}

	// Read the file
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource %s: %v", uri, err)
	}

	// Return the content as TextResourceContents
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     string(content),
		},
	}, nil
}
