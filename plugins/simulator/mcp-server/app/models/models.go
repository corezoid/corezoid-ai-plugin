package models

type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type SwaggerSpec struct {
	// Swagger 2.0 fields
	Host     string `json:"host,omitempty"`
	BasePath string `json:"basePath,omitempty"`
	Swagger  string `json:"swagger,omitempty"`

	// OpenAPI 3.0+ fields
	OpenAPI           string      `json:"openapi,omitempty"`
	Servers           []Server    `json:"servers,omitempty"`
	Components        *Components `json:"components,omitempty"`
	JsonSchemaDialect string      `json:"jsonSchemaDialect,omitempty"` // OpenAPI 3.1+
	Webhooks          interface{} `json:"webhooks,omitempty"`          // OpenAPI 3.1+

	// Common fields
	Paths       map[string]map[string]Endpoint `json:"paths"`
	Definitions map[string]Definition          `json:"definitions,omitempty"` // Swagger 2.0
}

type Components struct {
	Schemas map[string]Definition `json:"schemas,omitempty"` // OpenAPI 3.0
}

type Definition struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"` // Required fields
	Schema     string              `json:"$schema,omitempty"`  // OpenAPI 3.1+ JSON Schema dialect
	Examples   []interface{}       `json:"examples,omitempty"` // OpenAPI 3.1+ multiple examples
	Example    interface{}         `json:"example,omitempty"`  // Single example (3.0 style)
}

type Property struct {
	Type     string        `json:"type"`
	Schema   string        `json:"$schema,omitempty"`  // OpenAPI 3.1+ JSON Schema dialect
	Examples []interface{} `json:"examples,omitempty"` // OpenAPI 3.1+ multiple examples
	Example  interface{}   `json:"example,omitempty"`  // Single example (3.0 style)
}

type Endpoint struct {
	OperationID string              `json:"operationId,omitempty"`
	Summary     string              `json:"summary"`
	Description string              `json:"description"`
	Parameters  []Parameter         `json:"parameters"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"` // OpenAPI 3.0+ request body
	Responses   map[string]Response `json:"responses"`
	Consumes    []string            `json:"consumes"` // Swagger 2.0 only
	Produces    []string            `json:"produces"` // Swagger 2.0 only
	Invisible   bool                `json:"x-invisible,omitempty"` // When true, tool is hidden from tools/list but still callable
}

type RequestBody struct {
	Description string               `json:"description,omitempty"`
	Content     map[string]MediaType `json:"content"`
	Required    bool                 `json:"required,omitempty"`
}

type MediaType struct {
	Schema   *SchemaRef         `json:"schema,omitempty"`
	Example  interface{}        `json:"example,omitempty"`
	Examples map[string]Example `json:"examples,omitempty"` // OpenAPI 3.1+ named examples
}

type Example struct {
	Summary     string      `json:"summary,omitempty"`
	Description string      `json:"description,omitempty"`
	Value       interface{} `json:"value,omitempty"`
}

type Parameter struct {
	Ref         string             `json:"$ref,omitempty"`
	Name        string             `json:"name"`
	In          string             `json:"in"`
	Required    bool               `json:"required"`
	Type        string             `json:"type"`
	Schema      *SchemaRef         `json:"schema,omitempty"`
	Description string             `json:"description"`
	Example     interface{}        `json:"example,omitempty"`
	Examples    map[string]Example `json:"examples,omitempty"` // OpenAPI 3.1+ named examples
}

type Response struct {
	Description string               `json:"description"`
	Schema      *SchemaRef           `json:"schema,omitempty"`  // Swagger 2.0 style
	Content     map[string]MediaType `json:"content,omitempty"` // OpenAPI 3.0+ style
	Type        string               `json:"type,omitempty"`
	Headers     map[string]Header    `json:"headers,omitempty"`
}

type Header struct {
	Description string      `json:"description,omitempty"`
	Schema      *SchemaRef  `json:"schema,omitempty"`
	Example     interface{} `json:"example,omitempty"`
}

type SchemaRef struct {
	Ref         string                `json:"$ref,omitempty"`
	Type        string                `json:"type,omitempty"`
	Format      string                `json:"format,omitempty"`
	Items       *SchemaRef            `json:"items,omitempty"`
	Properties  map[string]*SchemaRef `json:"properties,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Schema      string                `json:"$schema,omitempty"`  // OpenAPI 3.1+ JSON Schema dialect
	Examples    []interface{}         `json:"examples,omitempty"` // OpenAPI 3.1+ multiple examples
	Example     interface{}           `json:"example,omitempty"`  // Single example
	Enum        []interface{}         `json:"enum,omitempty"`
	Default     interface{}           `json:"default,omitempty"`
	Title       string                `json:"title,omitempty"`
	Description string                `json:"description,omitempty"`
	MinItems    int                   `json:"minItems,omitempty"`
	MaxItems    int                   `json:"maxItems,omitempty"`
	MinLength   int                   `json:"minLength,omitempty"`
	MaxLength   int                   `json:"maxLength,omitempty"`
}

// SseConfig stores SSE (Server-Sent Events) related parameters
type SseConfig struct {
	SseMode bool   `json:"sseMode"` // Whether to run in SSE mode
	SseAddr string `json:"sseAddr"` // SSE server listen address
	SseUrl  string `json:"sseUrl"`  // Base URL for the SSE server
}

// ApiConfig stores API related parameters
type ApiConfig struct {
	BaseUrl        string `json:"baseUrl"`        // Base URL for API requests
	Url            string `json:"url"`            // API URL to use instead of extracting from Swagger spec
	IncludePaths   string `json:"includePaths"`   // List of paths or regex patterns to include
	ExcludePaths   string `json:"excludePaths"`   // List of paths or regex patterns to exclude
	IncludeMethods string `json:"includeMethods"` // List of HTTP methods to include
	ExcludeMethods string `json:"excludeMethods"` // List of HTTP methods to exclude
	Security       string `json:"security"`       // API security type
	BasicAuth      string `json:"basicAuth"`      // Basic auth credentials
	ApiKeyAuth     string `json:"apiKeyAuth"`     // API key authentication information
	Authorization  string `json:"authorization"`  // Authorization token
	SseHeaders     string `json:"sseHeaders"`     // Read headers from sse request, and pass to API request (format: name1,name2)
	Headers        string `json:"headers"`        // Additional headers to include in requests (format: name1=value1,name2=value2)
	OAuthClientID  string `json:"oauthClientId"`  // OAuth2 client ID for PKCE flow
}

// Config stores all command line parameters
type Config struct {
	SpecUrl string    `json:"specUrl,omitempty"` // Spec URL
	SseCfg  SseConfig `json:"sseCfg"`            // SSE related configuration
	ApiCfg  ApiConfig `json:"apiCfg,omitempty"`  // API configuration
}
