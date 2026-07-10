package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed json-schema
var schemaFS embed.FS

// Version is injected at build time via -ldflags "-X main.Version=v1.2.3".
// Falls back to "dev" for local builds.
var Version = "dev"

// Global logger instance
var logger = &Logger{}

// authStateMu guards the mutable auth-state globals below. Reads happen on
// many goroutines (one per MCP request in HTTP mode, plus tool-call goroutines
// in stdio mode); writes happen during login/logout, credential loading at
// startup, and the env-default block at the top of each handler. Without the
// mutex the race detector flags every concurrent access — and HTTP mode
// genuinely races because net/http dispatches handlers concurrently.
//
// Read paths take RLock (see authSnapshot). Write paths take Lock. Long-running
// operations (OAuth, elicitation, API calls) must NOT be performed while
// holding the lock; snapshot or release first.
var authStateMu sync.RWMutex
var apiURL string
var accountURL string
var apiToken string
var workspaceID string
var debug bool
var apigwURL string
var stageID int
var insecureTLS bool
// cachedProjectID is written once (protected by authStateMu) and then read-only.
// Reset to 0 on every loadConfig so a workspace switch gets a fresh value.
var cachedProjectID int

// authSnapshot returns a coherent snapshot of the auth-state globals taken
// under the read lock. Callers that subsequently need to mutate state must
// acquire authStateMu.Lock() (not upgrade — Go's RWMutex doesn't support that).
func authSnapshot() (apiURLv, tokenv, workspaceIDv, accountURLv string, stageIDv int) {
	authStateMu.RLock()
	defer authStateMu.RUnlock()
	return apiURL, apiToken, workspaceID, accountURL, stageID
}

// withAuthLock runs fn while holding the auth-state write lock. Use for
// composite read-then-write operations (e.g. "set X only if empty") that must
// be atomic with respect to other readers and writers. fn must not perform
// long-running I/O — that would block every concurrent request.
func withAuthLock(fn func()) {
	authStateMu.Lock()
	defer authStateMu.Unlock()
	fn()
}

func loadDotEnv(filename string) {
	data, err := os.ReadFile(filename)
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
		value := strings.TrimSpace(parts[1])
		// strip optional surrounding quotes
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		// skip unexpanded shell variable references (e.g. ${VAR_NAME})
		if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
			continue
		}
		os.Setenv(key, value)
	}
}

// isProjectRoot returns true if the given directory contains a *stage.json file,
// which marks the root of a convctl project.
func isProjectRoot(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	reStage := regexp.MustCompile(`^\d+_.*\.stage\.json$`)
	for _, e := range entries {
		if !e.IsDir() && reStage.MatchString(e.Name()) {
			return true
		}
	}
	return false
}

// findAndLoadDotEnv searches for a .env file starting from the current directory
// and walking up the tree. It stops at the project root (directory containing a
// *stage.json file) and will not traverse above it.
func findAndLoadDotEnv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	dir := cwd
	for {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			loadDotEnv(envPath)
			return
		}
		// Stop here — cannot go above the project root.
		if isProjectRoot(dir) {
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding anything.
			return
		}
		dir = parent
	}
}

func loadConfig() {
	// User-level credentials (~/.corezoid/credentials) are loaded first so that
	// a project .env can still override ACCESS_TOKEN for power users who need it.
	if credPath, err := credentialsFilePath(); err == nil {
		if _, err := os.Stat(credPath); err == nil {
			loadDotEnv(credPath)
		}
	}
	findAndLoadDotEnv()
	apiURL = os.Getenv("COREZOID_API_URL")
	accountURL = os.Getenv("ACCOUNT_URL")
	workspaceID = os.Getenv("WORKSPACE_ID")
	apiToken = os.Getenv("ACCESS_TOKEN")
	apigwURL = os.Getenv("COREZOID_APIGW_URL")
	if apigwURL == "" {
		apigwURL = "https://api-apigw.corezoid.com"
	}
	stageID, _ = strconv.Atoi(os.Getenv("COREZOID_STAGE_ID"))
	insecureTLS = os.Getenv("COREZOID_INSECURE_TLS") != ""
	cachedProjectID = 0              // reset on workspace switch so it is re-resolved
	os.Unsetenv("COREZOID_PROJECT_ID") // prevent stale process env from short-circuiting resolution
}

// runCLI executes a single MCP tool from the command line and exits.
// Usage: <binary> <tool-name> [key=value ...]
// For pull-folder: folder_id defaults to COREZOID_STAGE_ID from .env; path defaults to cwd.
// example export COREZOID_WORK_DIR="$PWD"; cd "/Users/mac/work/sources/corezoid-ai-doc/plugins/corezoid/mcp-server" && go run . pull-process process_id=1832359 && cd $COREZOID_WORK_DIR
func runCLI(toolName string, rawArgs []string) {
	args := make(map[string]interface{}, len(rawArgs))
	for _, a := range rawArgs {
		k, v, _ := strings.Cut(a, "=")
		args[k] = v
	}
	// Apply env-based defaults so the tool works with zero arguments.
	if _, ok := args["folder_id"]; !ok && stageID != 0 {
		args["folder_id"] = stageID
	}
	// CLI mode runs to completion or until the user kills the process; we
	// don't have a richer cancellation source than the process itself, so
	// context.Background() is fine here.
	result, isError := handleToolCall(context.Background(), toolName, args)
	if isError {
		fmt.Fprintln(os.Stderr, result)
		os.Exit(1)
	}
	fmt.Println(result)
	os.Exit(0)
}

func main() {
	if workDir := os.Getenv("COREZOID_WORK_DIR"); workDir != "" {
		_ = os.Chdir(workDir)
	}

	if len(os.Args) >= 2 && (os.Args[1] == "--version" || os.Args[1] == "-version" || os.Args[1] == "version") {
		fmt.Println(Version)
		os.Exit(0)
	}

	// CLI mode: first argument is a tool name (e.g. "pull-folder folder_id=123").
	if len(os.Args) >= 2 && !strings.HasPrefix(os.Args[1], "-") {
		// In CLI mode log to stderr directly so stdout stays clean for the result.
		logger.writer = os.Stderr
		logger.IsDebug = os.Getenv("COREZOID_DEBUG") != ""
		loadConfig()
		runCLI(os.Args[1], os.Args[2:])
		return
	}

	// MCP server mode — route all log output to a file so it never leaks onto
	// MCP stdout (which carries JSON-RPC messages).
	// Debug level is opt-in: set COREZOID_DEBUG=1 to enable.
	logPath := os.Getenv("COREZOID_DEBUG_LOG")
	if logPath == "" {
		home, _ := os.UserHomeDir()
		logPath = filepath.Join(home, ".corezoid", "mcp.log")
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
		logger.writer = f
	} else {
		fmt.Fprintf(os.Stderr, "[corezoid-mcp] WARNING: cannot open log file %s: %v\n", logPath, err)
	}
	logger.IsDebug = os.Getenv("COREZOID_DEBUG") != ""

	cwd, _ := os.Getwd()
	logger.Debug("Starting corezoid-mcp server, cwd=%s", cwd)

	loadConfig()
	logger.Debug("Loaded configuration: apiURL=%s workspaceID=%s apigwURL=%s hasToken=%v", apiURL, workspaceID, apigwURL, apiToken != "")

	if apiToken == "" {
		fmt.Fprintln(os.Stderr, "[corezoid-mcp] NOTICE: No credentials found.")
		fmt.Fprintln(os.Stderr, "[corezoid-mcp] Run the 'login' MCP tool to authenticate via OAuth2.")
		if credPath, err := credentialsFilePath(); err == nil {
			fmt.Fprintf(os.Stderr, "[corezoid-mcp] Token will be saved to: %s\n", credPath)
		}
	}

	if port := os.Getenv("COREZOID_HTTP_PORT"); port != "" {
		analyticsTransport = "http"
		initAnalytics()
		addr := "127.0.0.1:" + port
		if err := runHTTPServer(addr); err != nil {
			fmt.Fprintf(os.Stderr, "[corezoid-mcp] HTTP server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	analyticsTransport = "stdio"
	initAnalytics()
	runMCPServer()
}

var (
	compiledSchemaOnce sync.Once
	compiledSchema     *jsonschema.Schema
	compiledSchemaErr  error
)

// schemaDefinitions maps the keys used in the combined schema's "definitions"
// block to their embedded source paths under schemaFS.
var schemaDefinitions = []struct{ name, path string }{
	{"process", "json-schema/process.json"},
	{"node", "json-schema/node.json"},
	{"condition", "json-schema/logics/condition.json"},
	{"logics", "json-schema/logics.json"},
	{"semaphors", "json-schema/logics/semaphors.json"},
	{"go", "json-schema/logics/go.json"},
	{"go_if_const", "json-schema/logics/go_if_const.json"},
	{"set_param", "json-schema/logics/set_param.json"},
	{"api", "json-schema/logics/api.json"},
	{"api_callback", "json-schema/logics/api_callback.json"},
	{"api_sum", "json-schema/logics/api_sum.json"},
	{"api_code", "json-schema/logics/api_code.json"},
	{"api_copy", "json-schema/logics/api_copy.json"},
	{"api_rpc", "json-schema/logics/api_rpc.json"},
	{"api_rpc_reply", "json-schema/logics/api_rpc_reply.json"},
	{"api_queue", "json-schema/logics/api_queue.json"},
	{"api_get_task", "json-schema/logics/api_get_task.json"},
	{"api_form", "json-schema/logics/api_form.json"},
	{"api_git", "json-schema/logics/api_git.json"},
	{"db_call", "json-schema/logics/db_call.json"},
	{"semaphore_time", "json-schema/logics/semaphore_time.json"},
	{"semaphore_count", "json-schema/logics/semaphore_count.json"},
}

// loadCompiledSchema parses the embedded schema files and compiles the
// combined draft-07 schema. The result is cached for the lifetime of the
// process — schemas are static, so we pay the parsing cost exactly once.
func loadCompiledSchema() (*jsonschema.Schema, error) {
	compiledSchemaOnce.Do(func() {
		defs := make(map[string]any, len(schemaDefinitions))
		for _, d := range schemaDefinitions {
			data, err := schemaFS.ReadFile(d.path)
			if err != nil {
				compiledSchemaErr = fmt.Errorf("failed to read embedded %s: %v", d.path, err)
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				compiledSchemaErr = fmt.Errorf("failed to parse %s: %v", d.path, err)
				return
			}
			defs[d.name] = doc
		}
		combined := map[string]any{
			"$schema":     "http://json-schema.org/draft-07/schema#",
			"title":       "Combined Schema",
			"description": "Combined schema for validation",
			"definitions": defs,
			"$ref":        "#/definitions/process",
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("mem:///combined.json", combined); err != nil {
			compiledSchemaErr = fmt.Errorf("failed to register combined schema: %v", err)
			return
		}
		sch, err := c.Compile("mem:///combined.json")
		if err != nil {
			compiledSchemaErr = fmt.Errorf("failed to compile combined schema: %v", err)
			return
		}
		compiledSchema = sch
	})
	return compiledSchema, compiledSchemaErr
}

// ValidateJSONSchema validates a JSON file against the combined schema.
// Returns nil if validation passes, an error otherwise.
func ValidateJSONSchema(filePath string, debug bool) error {
	sch, err := loadCompiledSchema()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read input file: %v", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to parse input JSON: %v", err)
	}
	if err := sch.Validate(instance); err != nil {
		return fmt.Errorf("JSON schema validation failed:\n%v", err)
	}
	if debug {
		logger.Debug("JSON schema validation passed, file=%s", filePath)
	}
	return nil
}

func getNodes(data map[string]interface{}) ([]interface{}, error) {
	// Extract nodes from the process data
	scheme, ok := data["scheme"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("scheme not found in process data")
	}

	nodes, ok := scheme["nodes"].([]any)
	if !ok {
		return nil, fmt.Errorf("nodes not found in scheme")
	}
	return nodes, nil
}

// Logger writes structured log lines to an io.Writer.
// All output goes to the writer (default: stderr) — never to stdout,
// which is reserved for MCP JSON-RPC messages.
// Enable debug output by setting COREZOID_DEBUG_LOG to a file path.
type Logger struct {
	IsDebug bool
	writer  io.Writer
}

func (l *Logger) w() io.Writer {
	if l.writer != nil {
		return l.writer
	}
	return os.Stderr
}

func (l *Logger) Log(level, msg string, args ...interface{}) {
	formattedMsg := msg
	if len(args) > 0 {
		formattedMsg = fmt.Sprintf(msg, args...)
	}
	fmt.Fprintf(l.w(), "%s:%s\n", strings.ToUpper(level), formattedMsg)
}

func (l *Logger) Debug(msg string, args ...interface{}) {
	if l.IsDebug {
		l.Log("DEBUG", msg, args...)
	}
}

func (l *Logger) Info(msg string, args ...interface{}) {
	l.Log("INFO", msg, args...)
}

func (l *Logger) Warn(msg string, args ...interface{}) {
	l.Log("WARN", msg, args...)
}

func (l *Logger) Error(msg string, args ...interface{}) {
	l.Log("ERROR", msg, args...)
}

type NodeInfo struct {
	Type     int    `json:"type"`
	ObjType  int    `json:"obj_type"`
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	Icon     string `json:"icon"`
}

// LoadBinFromFile loads a JSON file and returns its content as a string
func LoadBinFromFile(filePath string) (string, error) {
	// Read the file
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("error reading file: %v", err)
	}

	return string(fileContent), nil
}

func fixStruct(dataBin string, inProcessID int) (string, []string) {
	messages := make([]string, 0)
	var data map[string]interface{}
	err := json.Unmarshal([]byte(dataBin), &data)
	if err != nil {
		return dataBin, messages
	}
	// если obj_id не задан, то задаем его
	processID := inProcessID

	if data["obj_id"] == nil {
		data["obj_id"] = processID
	}
	// loop by nodes, найти поле options, если это поле объект превратить в строку
	schemeMap, _ := data["scheme"].(map[string]interface{})
	if nodes, ok := schemeMap["nodes"].([]interface{}); ok {
		for _, node := range nodes {
			if nodeMap, ok := node.(map[string]interface{}); ok {
				objTypeF, _ := nodeMap["obj_type"].(float64)
				if int(objTypeF) == 0 {
					//	for by logics
					condition, ok := nodeMap["condition"].(map[string]interface{})
					if !ok {
						nodeBin, _ := json.Marshal(nodeMap)
						messages = append(messages, fmt.Sprintf("WARNING: condition block not found in node %s, skipping", string(nodeBin)))
						continue
					}
					if logics, ok := condition["logics"].([]interface{}); ok {
						for _, logic := range logics {
							if logicMap, ok := logic.(map[string]interface{}); ok {
								if convIDBin, ok := logicMap["conv_id"].(string); ok {
									convID, err := strconv.Atoi(convIDBin)
									if err == nil {
										logicMap["conv_id"] = convID
									}
								}
								if logicMap["type"] == "go_if_const" {
									funAliases := map[string]string{
										"gte": "more_or_eq",
										"lte": "less_or_eq",
										"gt":  "more",
										"lt":  "less",
										"ne":  "not_eq",
										"neq": "not_eq",
									}
									if conditions, ok := logicMap["conditions"].([]interface{}); ok {
										for _, cond := range conditions {
											if condMap, ok := cond.(map[string]interface{}); ok {
												if fun, ok := condMap["fun"].(string); ok {
													if replacement, found := funAliases[fun]; found {
														condMap["fun"] = replacement
														messages = append(messages,
															fmt.Sprintf("go_if_const condition in node %s: \"fun\":\"%s\" replaced with \"fun\":\"%s\"", nodeMap["id"], fun, replacement))
													}
												}
											}
										}
									}
								}
								if logicMap["type"] == "git_call" || logicMap["type"] == "api_git" {
									// git_call deploys via a separate container build (see BuildGitCallNodes).
									// Mark the source valid so Commit accepts the node; the build runs
									// between compile and commit.
									if _, set := logicMap["code_error"]; !set {
										logicMap["code_error"] = false
									}
								}
								if logicMap["type"] == "api" {
									if extra, ok := logicMap["extra"].(map[string]interface{}); ok {
										if body, ok := extra["body"].(string); ok && len(extra) == 1 {
											//	then body to raw_body field and extra["body"] to delete
											logicMap["raw_body"] = body
											logicMap["format"] = "raw"
											logicMap["extra"] = make(map[string]interface{})
											logicMap["extra_type"] = make(map[string]interface{})
											messages = append(messages,
												fmt.Sprintf("Logic \"api\" in the node %s was fixed. If you need to pass a variable %s as the entire body part, Instead of \"extra\" and \"extra_type\" you need to use then fields: \"raw_body\":\"%s\", \"format\":\"raw\". I am already fixed it. You don't need to change anything anymore", nodeMap["id"], body, body))
										}
									} else {
										logicMap["extra"] = make(map[string]interface{})
										logicMap["extra_type"] = make(map[string]interface{})
									}
								}
							}
						}
					}
				}
				if options, ok := nodeMap["options"].(map[string]interface{}); ok {
					optionsStr, err := json.Marshal(options)
					if err != nil {
						continue
					}
					nodeMap["options"] = string(optionsStr)
				}

			}
		}
	}

	dataRspBin, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return dataBin, messages
	}
	return string(dataRspBin), messages
}

// resolveAndCacheProjectID returns the project ID for the current stage and an
// optional user-visible notice when COREZOID_PROJECT_ID is written to .env for
// the first time. The notice is non-empty only on the first API resolution.
// Priority: in-memory cache → COREZOID_PROJECT_ID env var → API (ShowFolder).
// Thread-safe: reads under RLock, writes under Lock.
func resolveAndCacheProjectID(v *Executor) (int, string) {
	// Fast path: cache hit (read lock only).
	authStateMu.RLock()
	id := cachedProjectID
	authStateMu.RUnlock()
	if id != 0 {
		return id, ""
	}

	// Try env var (no API call needed).
	if s := os.Getenv("COREZOID_PROJECT_ID"); s != "" {
		if parsed, err := strconv.Atoi(s); err == nil && parsed != 0 {
			withAuthLock(func() { cachedProjectID = parsed })
			return parsed, ""
		}
	}

	// Resolve via API — must not hold the lock during I/O.
	if v.StageID == 0 {
		return 0, ""
	}
	resolved := v.GetProjectIDByStageID(v.StageID)
	if resolved == 0 {
		return 0, ""
	}
	withAuthLock(func() { cachedProjectID = resolved })
	os.Setenv("COREZOID_PROJECT_ID", strconv.Itoa(resolved))
	if written := appendToDotEnv("COREZOID_PROJECT_ID", strconv.Itoa(resolved)); written {
		notice := fmt.Sprintf("(COREZOID_PROJECT_ID=%d saved to .env for future use)", resolved)
		return resolved, notice
	}
	return resolved, ""
}

// appendToDotEnv appends key=value to the nearest .env file if the key is absent.
// Returns true when the value was actually written (first time only).
// The read-check-write is serialised under authStateMu to prevent duplicate entries
// from concurrent resolveAndCacheProjectID calls.
func appendToDotEnv(key, value string) bool {
	authStateMu.Lock()
	defer authStateMu.Unlock()

	envPath := findDotEnvPath()
	if envPath == "" {
		return false
	}
	data, _ := os.ReadFile(envPath)
	// Skip if key already present.
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
			return false
		}
	}
	f, err := os.OpenFile(envPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn("[snapshot] could not update .env: %v", err)
		return false
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "\n%s=%s\n", key, value)
	logger.Info("[snapshot] wrote %s=%s to %s", key, value, envPath)
	return true
}

// findDotEnvPath returns the path of the nearest .env file by walking up from cwd.
func findDotEnvPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if isProjectRoot(dir) {
			// Create .env at project root if none exists yet.
			return filepath.Join(dir, ".env")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join(cwd, ".env")
		}
		dir = parent
	}
}

// extractObjIDFromJSON returns the obj_id from a process JSON string.
// Returns 0 if obj_id is null or missing (new / unsaved process).
func extractObjIDFromJSON(jsonContent string) int {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(jsonContent), &m); err != nil {
		return 0
	}
	switch v := m["obj_id"].(type) {
	case float64:
		return int(v)
	default:
		return 0
	}
}
