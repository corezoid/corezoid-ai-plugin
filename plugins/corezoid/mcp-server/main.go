package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

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

// debug gates the Executor's detailed API request/response tracing (full
// payload dumps via the `if v.Debug` guards). It shares the COREZOID_DEBUG
// switch with logger.IsDebug — before this assignment the var was never set,
// so the executor trace was unreachable dead code and API-level issues could
// not be diagnosed at all.
var debug = debugFromEnv()

// debugFromEnv reports whether COREZOID_DEBUG requests the detailed trace.
// Split out so tests can exercise the wiring with t.Setenv.
func debugFromEnv() bool { return os.Getenv("COREZOID_DEBUG") != "" }

var apigwURL string
var stageID int
var insecureTLS bool

// cachedProjectID holds the project ID resolved by resolveProjectID (see
// mcp_handlers_git.go), persisted to the current Folder in
// ~/.corezoid/config.json so it survives server restarts without a repeat API
// round-trip. It is keyed by stage — resolveProjectID resolves it from the
// Folder's StageID — so it is cleared (in-memory and in the Folder) whenever
// workspace or stage changes (handleLogin) or on logout.
var cachedProjectID int

// apiLogin and apiSecret are the Corezoid API key credentials. They provide
// an alternative to OAuth2 PKCE for environments where browser-based
// authentication is not available. When both are set, requests are signed
// using the Corezoid double-salted SHA1 pattern instead of using a Simulator
// bearer token. Persisted per-folder in ~/.corezoid/config.json. The same
// credentials are reused for git mirror Basic auth (login_id:secret).
var apiLogin string
var apiSecret string

// gitURL is the Corezoid git mirror base URL including org path
// (e.g. https://git-dev.dev.corezoid.com/corezoid-dev). Persisted per-folder
// in ~/.corezoid/config.json. apiLogin/apiSecret are reused for git Basic
// auth — no separate git credential needed.
var gitURL string

// gitStagePath is the relative path inside .git-context/ to the current stage
// directory (e.g. "projects/123_Foo/stages/456_Bar"). Resolved once after
// git-pull-context and persisted per-folder in ~/.corezoid/config.json so
// the agent can reference it directly when reading CLAUDE.md or _ext/docs/*.
var gitStagePath string

// authSnapshot returns a coherent snapshot of the auth-state globals taken
// under the read lock. Callers that subsequently need to mutate state must
// acquire authStateMu.Lock() (not upgrade — Go's RWMutex doesn't support that).
func authSnapshot() (apiURLv, tokenv, workspaceIDv, accountURLv string, stageIDv int) {
	authStateMu.RLock()
	defer authStateMu.RUnlock()
	return apiURL, apiToken, workspaceID, accountURL, stageID
}

// apiKeySnapshot returns a coherent snapshot of the API key credentials taken
// under the read lock. Same pattern as authSnapshot.
func apiKeySnapshot() (login, secret string) {
	authStateMu.RLock()
	defer authStateMu.RUnlock()
	return apiLogin, apiSecret
}

// gitConfigSnapshot returns a coherent snapshot of the git mirror config.
// gitLogin/gitSecret reuse apiLogin/apiSecret (same Corezoid API key for both).
// accountURLv is included so callers can derive git_url when not set.
// The project ID itself is not part of this snapshot — resolveProjectID
// resolves/caches it via the shared resolveAndCacheProjectID (see main.go).
func gitConfigSnapshot() (gitURLv, loginv, secretv, companyIDv, accountURLv string) {
	authStateMu.RLock()
	defer authStateMu.RUnlock()
	return gitURL, apiLogin, apiSecret, workspaceID, accountURL
}

// gitStagePathSnapshot returns the resolved stage path inside .git-context/.
func gitStagePathSnapshot() string {
	authStateMu.RLock()
	defer authStateMu.RUnlock()
	return gitStagePath
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

// loadConfig populates the in-memory auth-state globals from the current
// Folder in ~/.corezoid/config.json. Called at startup and after any write
// that could have changed persisted state. Dev flags (COREZOID_DEBUG,
// COREZOID_INSECURE_TLS) are still read from env — they are process-scope
// switches, not user-visible config.
func loadConfig() {
	syncGlobalsFromCurrent()
	debug = debugFromEnv()
	insecureTLS = os.Getenv("COREZOID_INSECURE_TLS") != ""
}

// runCLI executes a single MCP tool from the command line and exits.
// Usage: <binary> <tool-name> [key=value ...]
// For pull-folder: folder_id defaults to the current Folder's stage_id in
// ~/.corezoid/config.json; path defaults to cwd.
// example export COREZOID_WORK_DIR="$PWD"; cd "/Users/mac/work/sources/corezoid-ai-doc/plugins/corezoid/mcp-server" && go run . pull-process process_id=1832359 && cd $COREZOID_WORK_DIR
func runCLI(toolName string, rawArgs []string) {
	args := make(map[string]interface{}, len(rawArgs))
	for _, a := range rawArgs {
		k, v, _ := strings.Cut(a, "=")
		args[k] = v
	}
	if cerr := coerceCLIArgs(toolName, args); cerr != nil {
		fmt.Println("Error:", cerr)
		os.Exit(1)
	}
	// Apply env-based defaults so folder tools work with zero arguments —
	// but only where the schema REQUIRES folder_id (pull-folder & friends).
	// Injecting it blindly into every call passed a junk argument to tools
	// like login, and would silently redirect create-process away from its
	// documented directory-based target resolution.
	if _, ok := args["folder_id"]; !ok && stageID != 0 && toolRequiresArg(toolName, "folder_id") {
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

// installShutdownFlush flushes buffered analytics events before the process
// exits on SIGINT/SIGTERM (e.g. the MCP client terminating the server).
// Go's default signal handling terminates immediately without running
// deferred calls, so this is the only chance to send events queued right
// before shutdown.
func installShutdownFlush() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		stopAnalytics()
		os.Exit(0)
	}()
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

	// Flush any buffered analytics events before exit. Covers both a normal
	// return (stdio EOF, HTTP server closed cleanly) via defer, and the
	// client killing the process with SIGINT/SIGTERM, which by default
	// terminates immediately without running deferred calls.
	defer stopAnalytics()
	installShutdownFlush()

	cwd, _ := os.Getwd()
	logger.Debug("Starting corezoid-mcp server, cwd=%s", cwd)

	loadConfig()
	logger.Debug("Loaded configuration: apiURL=%s workspaceID=%s apigwURL=%s hasToken=%v", apiURL, workspaceID, apigwURL, apiToken != "")

	if apiToken == "" && (apiLogin == "" || apiSecret == "") {
		fmt.Fprintln(os.Stderr, "[corezoid-mcp] NOTICE: No credentials found for this working directory.")
		fmt.Fprintln(os.Stderr, "[corezoid-mcp] Authenticate via one of:")
		fmt.Fprintln(os.Stderr, "[corezoid-mcp]   1. OAuth2 browser flow — run the 'login' MCP tool.")
		fmt.Fprintln(os.Stderr, "[corezoid-mcp]   2. API key — call login(api_login=<L>, api_secret=<S>) to select workspace/stage.")
		if cfgPath, err := configFilePath(); err == nil {
			fmt.Fprintf(os.Stderr, "[corezoid-mcp] Credentials will be saved to: %s\n", cfgPath)
		}
	}

	if port := os.Getenv("COREZOID_HTTP_PORT"); port != "" {
		analyticsTransport = "http"
		initAnalytics()
		addr := "127.0.0.1:" + port
		if err := runHTTPServer(addr); err != nil {
			fmt.Fprintf(os.Stderr, "[corezoid-mcp] HTTP server error: %v\n", err)
			stopAnalytics()
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
	{"stub", "json-schema/logics/stub.json"},
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

func isProcessLogicObjType(objType int) bool {
	return objType == 0 || objType == 4
}

func normalizeGoIfConstLogic(nodeID interface{}, logicMap map[string]interface{}, messages *[]string) {
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
						*messages = append(*messages,
							fmt.Sprintf("go_if_const condition in node %v: \"fun\":\"%s\" replaced with \"fun\":\"%s\"", nodeID, fun, replacement))
					}
				}
			}
		}
	}
}

func normalizeLogicForDeploy(nodeID interface{}, logicMap map[string]interface{}, messages *[]string) {
	if convIDBin, ok := logicMap["conv_id"].(string); ok {
		convID, err := strconv.Atoi(convIDBin)
		if err == nil {
			logicMap["conv_id"] = convID
		}
	}

	logicType, _ := logicMap["type"].(string)
	switch logicType {
	case "go_if_const":
		normalizeGoIfConstLogic(nodeID, logicMap, messages)
	case "git_call", "api_git":
		// git_call deploys via a separate container build (see BuildGitCallNodes).
		// Mark the source valid so Commit accepts the node; the build runs
		// between compile and commit.
		if _, set := logicMap["code_error"]; !set {
			logicMap["code_error"] = false
		}
	case "api":
		if extra, ok := logicMap["extra"].(map[string]interface{}); ok {
			if body, ok := extra["body"].(string); ok && len(extra) == 1 {
				// then body to raw_body field and extra["body"] to delete
				logicMap["raw_body"] = body
				logicMap["format"] = "raw"
				logicMap["extra"] = make(map[string]interface{})
				logicMap["extra_type"] = make(map[string]interface{})
				*messages = append(*messages,
					fmt.Sprintf("Logic \"api\" in the node %v was fixed. If you need to pass a variable %s as the entire body part, Instead of \"extra\" and \"extra_type\" you need to use then fields: \"raw_body\":\"%s\", \"format\":\"raw\". I am already fixed it. You don't need to change anything anymore", nodeID, body, body))
			}
		} else {
			logicMap["extra"] = make(map[string]interface{})
			logicMap["extra_type"] = make(map[string]interface{})
		}
	}
}

func normalizeStubForDeploy(nodeID interface{}, condition map[string]interface{}, messages *[]string) {
	stub, ok := condition["stub"].(map[string]interface{})
	if !ok {
		return
	}
	branches, ok := stub["logics"].([]interface{})
	if !ok {
		return
	}
	for _, branch := range branches {
		branchItems, ok := branch.([]interface{})
		if !ok {
			continue
		}
		for _, item := range branchItems {
			if logicMap, ok := item.(map[string]interface{}); ok {
				normalizeLogicForDeploy(nodeID, logicMap, messages)
			}
		}
	}
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
				if isProcessLogicObjType(int(objTypeF)) {
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
								normalizeLogicForDeploy(nodeMap["id"], logicMap, &messages)
							}
						}
					}
					normalizeStubForDeploy(nodeMap["id"], condition, &messages)
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

	// Auto-place NEW nodes (x==0 && y==0) before serialising. schemeMap aliases
	// data["scheme"], so mutating it persists into the marshaled output.
	if schemeMap != nil {
		convType, _ := data["conv_type"].(string)
		messages = append(messages, applyLayout(schemeMap, convType)...)
	}

	dataRspBin, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return dataBin, messages
	}
	return string(dataRspBin), messages
}

// resolveAndCacheProjectID returns the project ID for the current stage.
// Priority: in-memory cache (populated by syncGlobalsFromCurrent from the
// marker's parent_id, or the Folder's ProjectID as fallback) → API
// (GetProjectIDByStageID). On API discovery the value is persisted to
// Folder.ProjectID so subsequent server restarts don't repeat the round-trip
// even when the marker doesn't carry parent_id.
//
// The second return value is a user-visible notice emitted when a fresh ID
// is persisted; empty otherwise.
func resolveAndCacheProjectID(v *Executor) (int, string) {
	authStateMu.RLock()
	id := cachedProjectID
	authStateMu.RUnlock()
	if id != 0 {
		return id, ""
	}
	if v.StageID == 0 {
		return 0, ""
	}
	resolved := v.GetProjectIDByStageID(v.StageID)
	if resolved == 0 {
		return 0, ""
	}
	if err := UpdateCurrent(func(f *Folder) {
		f.ProjectID = resolved
	}); err != nil {
		logger.Warn("resolveProjectID: could not persist project ID to config: %v", err)
	}
	syncGlobalsFromCurrent()
	notice := fmt.Sprintf("(project_id=%d cached in ~/.corezoid/config.json for future use)", resolved)
	return resolved, notice
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
