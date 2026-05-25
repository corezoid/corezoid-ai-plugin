package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

//go:embed json-schema
var schemaFS embed.FS

// Global logger instance
var logger = &Logger{}
var apiURL string
var accountURL string
var apiToken string
var workspaceID string
var debug bool
var apigwURL string
var stageID int
var insecureTLS bool

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
	result, isError := handleToolCall(toolName, args)
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

	// CLI mode: first argument is a tool name (e.g. "pull-folder folder_id=123").
	if len(os.Args) >= 2 && !strings.HasPrefix(os.Args[1], "-") {
		// In CLI mode log to stderr directly so stdout stays clean for the result.
		logger.writer = os.Stderr
		logger.IsDebug = os.Getenv("COREZOID_DEBUG") != ""
		loadConfig()
		runCLI(os.Args[1], os.Args[2:])
		return
	}

	// MCP server mode — configure debug log file so all output avoids stdout.
	err := os.Setenv("COREZOID_DEBUG_LOG", "/tmp/corezoid.log")
	if err != nil {
		log.Fatal(err)
	}
	if logPath := os.Getenv("COREZOID_DEBUG_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			logger.writer = f
			logger.IsDebug = true
		} else {
			fmt.Fprintf(os.Stderr, "[corezoid-mcp] WARNING: cannot open debug log %s: %v\n", logPath, err)
		}
	}

	cwd, _ := os.Getwd()
	logger.Debug("Starting corezoid-mcp server, cwd=%s", cwd)

	loadConfig()
	logger.Debug("Loaded configuration: apiURL=%s workspaceID=%s apigwURL=%s hasToken=%v", apiURL, workspaceID, apigwURL, apiToken != "")

	if apiToken == "" {
		fmt.Fprintln(os.Stderr, "[corezoid-mcp] NOTICE: No credentials found in .env.")
		fmt.Fprintln(os.Stderr, "[corezoid-mcp] Run the 'login' MCP tool to authenticate via OAuth2.")
		fmt.Fprintf(os.Stderr, "[corezoid-mcp] Credentials will be saved to: %s\n", filepath.Join(cwd, ".env"))
	}

	runMCPServer()
}

// ValidateJSONSchema validates a JSON file against the combined schema
// Returns nil if validation passes, error otherwise
func ValidateJSONSchema(filePath string, debug bool) error {
	// Create a temporary directory for schema files
	tmpDir, err := os.MkdirTemp("", "json-schema-validation")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tmpDir) // Clean up when done

	if debug {
		slog.Debug("Created temporary directory for schema validation", "dir", tmpDir)
	}

	// Extract embedded schema files to tmpDir
	logicsDir := filepath.Join(tmpDir, "logics")
	if err := os.MkdirAll(logicsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logics directory: %v", err)
	}

	copyEmbedded := func(src, dst string) error {
		data, err := schemaFS.ReadFile(src)
		if err != nil {
			return fmt.Errorf("failed to read embedded %s: %v", src, err)
		}
		return os.WriteFile(dst, data, 0644)
	}

	// Top-level schema files
	for _, name := range []string{"process.json", "node.json", "logics.json"} {
		if err := copyEmbedded("json-schema/"+name, filepath.Join(tmpDir, name)); err != nil {
			return err
		}
	}

	// logics/ schema files
	logicsFiles := []string{
		"condition.json", "semaphors.json", "go.json", "go_if_const.json",
		"set_param.json", "api.json", "api_callback.json", "api_sum.json",
		"api_code.json", "api_copy.json", "api_rpc.json", "api_rpc_reply.json",
		"api_queue.json", "api_get_task.json", "api_form.json", "api_git.json",
		"db_call.json", "semaphore_time.json", "semaphore_count.json",
	}
	for _, name := range logicsFiles {
		if err := copyEmbedded("json-schema/logics/"+name, filepath.Join(logicsDir, name)); err != nil {
			return err
		}
	}

	// Create the combined schema file
	combinedSchemaPath := filepath.Join(tmpDir, "combined_schema.json")
	combinedSchemaContent := `{
	"$schema": "http://json-schema.org/draft-07/schema#",
	"title": "Combined Schema",
	"description": "Combined schema for validation",
	"definitions": {
		"process": %s,
		"node": %s,
		"condition": %s,
		"logics": %s,
		"semaphors": %s,
		"go": %s,
		"go_if_const": %s,
		"set_param": %s,
		"api": %s,
		"api_callback": %s,
		"api_sum": %s,
		"api_code": %s,
		"api_copy": %s,
		"api_rpc": %s,
		"api_rpc_reply": %s,
		"api_queue": %s,
		"api_get_task": %s,
		"api_form": %s,
		"api_git": %s,
		"db_call": %s,
		"semaphore_time": %s,
		"semaphore_count": %s
	},
	"$ref": "#/definitions/process"
}`

	// Read all the schema files
	processSchema, err := os.ReadFile(filepath.Join(tmpDir, "process.json"))
	if err != nil {
		return fmt.Errorf("failed to read process.json: %v", err)
	}
	nodeSchema, err := os.ReadFile(filepath.Join(tmpDir, "node.json"))
	if err != nil {
		return fmt.Errorf("failed to read node.json: %v", err)
	}
	conditionSchema, err := os.ReadFile(filepath.Join(logicsDir, "condition.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/condition.json: %v", err)
	}
	logicsSchema, err := os.ReadFile(filepath.Join(tmpDir, "logics.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics.json: %v", err)
	}
	semaphorsSchema, err := os.ReadFile(filepath.Join(logicsDir, "semaphors.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/semaphors.json: %v", err)
	}
	goSchema, err := os.ReadFile(filepath.Join(logicsDir, "go.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/go.json: %v", err)
	}
	goIfConstSchema, err := os.ReadFile(filepath.Join(logicsDir, "go_if_const.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/go_if_const.json: %v", err)
	}
	setParamSchema, err := os.ReadFile(filepath.Join(logicsDir, "set_param.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/set_param.json: %v", err)
	}
	apiSchema, err := os.ReadFile(filepath.Join(logicsDir, "api.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api.json: %v", err)
	}
	apiCallbackSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_callback.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_callback.json: %v", err)
	}
	apiSumSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_sum.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_sum.json: %v", err)
	}
	apiCodeSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_code.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_code.json: %v", err)
	}
	apiCopySchema, err := os.ReadFile(filepath.Join(logicsDir, "api_copy.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_copy.json: %v", err)
	}
	apiRpcSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_rpc.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_rpc.json: %v", err)
	}
	apiRpcReplySchema, err := os.ReadFile(filepath.Join(logicsDir, "api_rpc_reply.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_rpc_reply.json: %v", err)
	}
	apiQueueSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_queue.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_queue.json: %v", err)
	}
	apiGetTaskSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_get_task.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_get_task.json: %v", err)
	}
	apiFormSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_form.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_form.json: %v", err)
	}
	apiGitSchema, err := os.ReadFile(filepath.Join(logicsDir, "api_git.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/api_git.json: %v", err)
	}
	dbCallSchema, err := os.ReadFile(filepath.Join(logicsDir, "db_call.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/db_call.json: %v", err)
	}
	semaphoreTimeSchema, err := os.ReadFile(filepath.Join(logicsDir, "semaphore_time.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/semaphore_time.json: %v", err)
	}
	semaphoreCountSchema, err := os.ReadFile(filepath.Join(logicsDir, "semaphore_count.json"))
	if err != nil {
		return fmt.Errorf("failed to read logics/semaphore_count.json: %v", err)
	}

	// Format the combined schema
	combinedSchema := fmt.Sprintf(
		combinedSchemaContent,
		string(processSchema),
		string(nodeSchema),
		string(conditionSchema),
		string(logicsSchema),
		string(semaphorsSchema),
		string(goSchema),
		string(goIfConstSchema),
		string(setParamSchema),
		string(apiSchema),
		string(apiCallbackSchema),
		string(apiSumSchema),
		string(apiCodeSchema),
		string(apiCopySchema),
		string(apiRpcSchema),
		string(apiRpcReplySchema),
		string(apiQueueSchema),
		string(apiGetTaskSchema),
		string(apiFormSchema),
		string(apiGitSchema),
		string(dbCallSchema),
		string(semaphoreTimeSchema),
		string(semaphoreCountSchema),
	)

	// Write the combined schema to a file
	err = os.WriteFile(combinedSchemaPath, []byte(combinedSchema), 0644)
	if err != nil {
		return fmt.Errorf("failed to write combined schema: %v", err)
	}

	if debug {
		logger.Debug("Created combined schema file, path=%s", combinedSchemaPath)
	}

	// Run the validation command
	// Copy the input file to the temporary directory
	inputFileName := filepath.Base(filePath)
	tmpFilePath := filepath.Join(tmpDir, inputFileName)
	inputContent, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read input file: %v", err)
	}
	err = os.WriteFile(tmpFilePath, inputContent, 0644)
	if err != nil {
		return fmt.Errorf("failed to copy input file to temporary directory: %v", err)
	}

	// Check that ajv CLI is available before running validation.
	// ajv is an optional Node.js tool; if absent, schema validation is skipped with an install hint.
	if _, err := exec.LookPath("ajv"); err != nil {
		return fmt.Errorf("schema validation skipped: 'ajv' CLI not found (install with: npm install -g ajv-cli)")
	}

	cmd := exec.Command("ajv", "validate", "-s", "combined_schema.json", "-d", inputFileName, "--allow-union-types")
	cmd.Dir = tmpDir
	if debug {
		logger.Debug("Running validation command, command=%s, dir=%s", cmd.String(), tmpDir)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("JSON schema validation failed:\n%s", string(output))
	}

	if debug {
		logger.Debug("JSON schema validation passed, output=%s", string(output))
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
	if nodes, ok := data["scheme"].(map[string]interface{})["nodes"].([]interface{}); ok {
		for _, node := range nodes {
			if nodeMap, ok := node.(map[string]interface{}); ok {
				if objType := int(nodeMap["obj_type"].(float64)); objType == 0 {
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
