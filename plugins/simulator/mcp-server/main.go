package main

import (
	"flag"
	"log"
	"net/url"
	"os"
	"strings"

	mcpserver "git.corezoid.com/mw161089sar/swagger-mcp/app/mcp-server"
	"git.corezoid.com/mw161089sar/swagger-mcp/app/models"
	"git.corezoid.com/mw161089sar/swagger-mcp/app/swagger"
)

func getSseUrlAddr(sseUrl, sseAddr string) (string, string) {
	// Only complement if one is empty; if both are set, use as-is
	if sseAddr == "" && sseUrl == "" {
		return "http://localhost:8080", "localhost:8080"
	}
	if sseAddr != "" {
		// ":Port" or "IP:Port"
		if strings.HasPrefix(sseAddr, ":") {
			// sseUrl = http://localhost:Port
			return "http://localhost" + sseAddr, sseAddr
		}
		if !strings.Contains(sseAddr, ":") {
			log.Fatal("sseAddr must be in :Port or IP:Port format")
		}
		return "http://" + sseAddr, sseAddr
	} else if sseUrl != "" {
		u, err := url.Parse(sseUrl)
		if err != nil {
			log.Fatalf("Invalid sseUrl: %v", err)
		}
		host := u.Host
		port := ""
		if strings.Contains(host, ":") {
			parts := strings.Split(host, ":")
			host = parts[0]
			port = parts[1]
		}
		// 没有端口时根据 scheme 补全
		if port == "" {
			switch u.Scheme {
			case "http":
				port = "80"
			case "https":
				port = "443"
			default:
				log.Fatalf("Unknown scheme for sseUrl: %s", u.Scheme)
			}
		}
		return sseUrl, host + ":" + port
	} else {
		log.Fatal("Either sseAddr or sseUrl must be provided")
	}
	return "", ""
}

// loadDotEnv reads key=value pairs from path and sets them as env vars (does not overwrite existing).
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func main() {
	if workDir := os.Getenv("SIMULATOR_WORK_DIR"); workDir != "" {
		_ = os.Chdir(workDir)
	}

	// Load .env from CWD so ACCESS_TOKEN, ACCOUNT_URL, etc. are available.
	if cwd, err := os.Getwd(); err == nil {
		loadDotEnv(cwd + "/.env")
	}

	var finalSseUrl, finalSseAddr string
	specUrl := flag.String("specUrl", "", "URL of the Swagger JSON specification")
	spec := flag.String("spec", "", "Built-in spec name (e.g. 'simulator')")
	sseMode := flag.Bool("sse", false, "Run in SSE mode instead of stdio mode")
	sseAddr := flag.String("sseAddr", "", "SSE server listen address in :Port or IP:Port format")
	sseUrl := flag.String("sseUrl", "", "Base URL for the SSE server")
	baseUrl := flag.String("baseUrl", "", "Base URL for API requests")
	apiUrl := flag.String("url", "", "API URL to use instead of extracting from Swagger spec")
	includePaths := flag.String("includePaths", "", "Comma-separated list of paths or regex to include")
	excludePaths := flag.String("excludePaths", "", "Comma-separated list of paths or regex to exclude")
	includeMethods := flag.String("includeMethods", "", "Comma-separated list of HTTP methods to include")
	excludeMethods := flag.String("excludeMethods", "", "Comma-separated list of HTTP methods to exclude")
	security := flag.String("security", "", "API security type: basic, apiKey, or bearer")
	basicAuth := flag.String("basicAuth", "", "Basic auth credentials in user:password format, used in Authorization header")
	authorization := flag.String("authorization", "", "Authorization token for Authorization header")
	apiKeyAuth := flag.String("apiKeyAuth", "", "API key auth, format: 'passAs:name=value', passAs=header/query/cookie, multiple by comma")
	headers := flag.String("headers", "", "Additional headers to include in requests (format: name1=value1,name2=value2)")
	sseHeaders := flag.String("sseHeaders", "", "Read headers from sse request, and pass to API request (format: name1,name2)")
	oauthClientID := flag.String("oauthClientID", "", "OAuth2 client ID for PKCE browser login flow (also read from SIMULATOR_OAUTH_CLIENT_ID env var)")

	flag.Parse()

	// Validate that either specUrl or spec is provided
	if *specUrl == "" && *spec == "" {
		log.Fatal("Please provide --specUrl or --spec flag")
	}

	// Validate specUrl if provided
	if *specUrl != "" {
		if strings.HasPrefix(*specUrl, "http://") || strings.HasPrefix(*specUrl, "https://") {
			_, err := url.ParseRequestURI(*specUrl)
			if err != nil {
				log.Fatalf("Invalid spec URL: %v", err)
			}
		} else if strings.HasPrefix(*specUrl, "file://") {
			filePath := strings.TrimPrefix(*specUrl, "file://")
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				log.Fatalf("Spec file does not exist: %v", err)
			}
		} else {
			log.Fatal("Invalid specUrl format. Must be a valid HTTP URL or file:// path")
		}
	}

	// Validate baseUrl
	if *baseUrl != "" {
		if !strings.HasPrefix(*baseUrl, "http://") && !strings.HasPrefix(*baseUrl, "https://") {
			log.Fatal("baseUrl must start with http:// or https://")
		}
	}

	// Validate url parameter
	if *apiUrl != "" {
		if !strings.HasPrefix(*apiUrl, "http://") && !strings.HasPrefix(*apiUrl, "https://") {
			log.Fatal("url must start with http:// or https://")
		}
	}

	if *sseMode { // get final sseAddr and sseUrl
		finalSseUrl, finalSseAddr = getSseUrlAddr(*sseUrl, *sseAddr)
	}
	// Resolve built-in spec name to a swagger spec
	if *spec != "" {
		data, err := getBuiltinSpec(*spec)
		if err != nil {
			log.Fatal(err)
		}
		swaggerSpec, err := swagger.LoadSwaggerFromBytes(data)
		if err != nil {
			log.Fatalf("Failed to load built-in spec %q: %v", *spec, err)
		}
		config := models.Config{
			SpecUrl: *spec,
			SseCfg: models.SseConfig{
				SseMode: *sseMode,
				SseAddr: finalSseAddr,
				SseUrl:  finalSseUrl,
			},
			ApiCfg: models.ApiConfig{
				BaseUrl:        *baseUrl,
				Url:            *apiUrl,
				IncludePaths:   *includePaths,
				ExcludePaths:   *excludePaths,
				IncludeMethods: *includeMethods,
				ExcludeMethods: *excludeMethods,
				Security:       *security,
				BasicAuth:      *basicAuth,
				ApiKeyAuth:     *apiKeyAuth,
				Authorization:  *authorization,
				Headers:        *headers,
				SseHeaders:     *sseHeaders,
				OAuthClientID:  *oauthClientID,
			},
		}
		//log.Printf("Starting server with built-in spec: %s\n", *spec)
		mcpserver.CreateServer(swaggerSpec, config)
		return
	}

	// Check if we should use multi-service mode or single service mode
	// For backward compatibility, if specUrl is provided, use single service mode
	if *specUrl != "" {
		// Single service mode
		swaggerSpec, err := swagger.LoadSwagger(*specUrl)
		if err != nil {
			log.Fatalf("Failed to load Swagger spec: %v", err)
		}
		// Only extract swagger info if not in stdio mode (commented out for MCP)
		// swagger.ExtractSwagger(swaggerSpec)

		config := models.Config{
			SpecUrl: *specUrl,
			SseCfg: models.SseConfig{
				SseMode: *sseMode,
				SseAddr: finalSseAddr,
				SseUrl:  finalSseUrl,
			},
			ApiCfg: models.ApiConfig{
				BaseUrl:        *baseUrl,
				Url:            *apiUrl,
				IncludePaths:   *includePaths,
				ExcludePaths:   *excludePaths,
				IncludeMethods: *includeMethods,
				ExcludeMethods: *excludeMethods,
				Security:       *security,
				BasicAuth:      *basicAuth,
				ApiKeyAuth:     *apiKeyAuth,
				Authorization:  *authorization,
				Headers:        *headers,
				SseHeaders:     *sseHeaders,
				OAuthClientID:  *oauthClientID,
			},
		}

		log.Printf("Starting single service server with specUrl: %s, SSE mode: %v, SSE URL: %s, SSE Addr: %s, URL: %s, Base URL: %s, Include Paths: %s, Exclude Paths: %s, Include Methods: %s, Exclude Methods: %s, Security: %s, BasicAuth: %s, ApiKeyAuth: %s, Authorization: %s, Headers: %s, SSE Headers: %s\n",
			config.SpecUrl, config.SseCfg.SseMode, config.SseCfg.SseUrl, config.SseCfg.SseAddr, config.ApiCfg.Url, config.ApiCfg.BaseUrl, config.ApiCfg.IncludePaths, config.ApiCfg.ExcludePaths, config.ApiCfg.IncludeMethods, config.ApiCfg.ExcludeMethods, config.ApiCfg.Security, config.ApiCfg.BasicAuth, config.ApiCfg.ApiKeyAuth, config.ApiCfg.Authorization, config.ApiCfg.Headers, config.ApiCfg.SseHeaders)
		mcpserver.CreateServer(swaggerSpec, config)
	}
}
