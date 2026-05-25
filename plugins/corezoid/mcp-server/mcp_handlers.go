package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// handleToolCall dispatches an MCP tool invocation. ctx must be non-nil — it
// flows down through the Executor into every HTTP request, so callers can
// cancel a long-running tool (e.g. pull-folder on a large workspace) via the
// MCP notifications/cancelled message or an HTTP server timeout.
func handleToolCall(ctx context.Context, name string, args map[string]interface{}) (result string, isError bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	// lint works on local files only; login/logout manage auth — skip auth check for these.
	// Discovery tools only need a token, not a fully configured workspace/stage.
	switch name {
	case "lint-process", "login", "logout":
		// no auth required
	case "list-workspaces", "list-projects", "list-stages":
		if err := ensureTokenAuth(); err != nil {
			return err.Error(), true
		}
	default:
		if err := ensureAuth(); err != nil {
			return err.Error(), true
		}
	}

	switch name {
	case "login":
		envPath := envFilePath()

		// Re-read .env so that ACCESS_TOKEN (and other vars) added after server
		// startup are honoured — prevents triggering OAuth when the token is
		// already present in .env. The env-reload + arg-application block is a
		// composite check-then-set on shared state, so do it under the auth
		// write lock to keep concurrent readers consistent.
		findAndLoadDotEnv()
		var stageIDAtStart int
		withAuthLock(func() {
			if apiToken == "" {
				apiToken = os.Getenv("ACCESS_TOKEN")
			}
			if accountURL == "" {
				accountURL = os.Getenv("ACCOUNT_URL")
			}
			if workspaceID == "" {
				workspaceID = os.Getenv("WORKSPACE_ID")
			}
			if stageID == 0 {
				stageID, _ = strconv.Atoi(os.Getenv("COREZOID_STAGE_ID"))
			}
			if apiURL == "" {
				apiURL = os.Getenv("COREZOID_API_URL")
			}

			// Record initial stageID to detect if it gets set during this call.
			stageIDAtStart = stageID

			// Apply any values passed directly as arguments (bypasses elicitation).
			if v := optStrArg(args, "account_url"); v != "" && accountURL == "" {
				accountURL = v
				os.Setenv("ACCOUNT_URL", v)
				if err := updateEnvFile(envPath, "ACCOUNT_URL", v); err != nil {
					logger.Warn("login: could not save ACCOUNT_URL from arg: %v", err)
				}
			}
			if v := optStrArg(args, "workspace_id"); v != "" && workspaceID == "" {
				workspaceID = v
				os.Setenv("WORKSPACE_ID", v)
				if err := updateEnvFile(envPath, "WORKSPACE_ID", v); err != nil {
					logger.Warn("login: could not save WORKSPACE_ID from arg: %v", err)
				}
			}
			if v := optStrArg(args, "stage_id"); v != "" && stageID == 0 {
				if id, err := strconv.Atoi(v); err == nil && id != 0 {
					stageID = id
					os.Setenv("COREZOID_STAGE_ID", v)
					if err := updateEnvFile(envPath, "COREZOID_STAGE_ID", v); err != nil {
						logger.Warn("login: could not save COREZOID_STAGE_ID from arg: %v", err)
					}
				}
			}
		})

		// Snapshot the post-reload state for use in this handler — long-running
		// OAuth / elicitation must not hold the auth lock, so we drive most
		// logic from these locals and only re-acquire the lock for writes.
		_, snapToken, snapWorkspaceID, snapAccountURL, snapStageID := authSnapshot()
		logger.Info("login: accountURL=%q workspaceID=%q stageID=%d", snapAccountURL, snapWorkspaceID, snapStageID)

		// Step 1: ensure Account API URL.
		if snapAccountURL == "" {
			var resolved string
			if clientSupportsElicitation {
				content, action, err := elicitValues(
					"Enter your Account API URL to get started:",
					map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"account_url": map[string]interface{}{
								"type":        "string",
								"title":       "Account API URL",
								"description": "e.g. https://account.corezoid.com",
								"default":     "https://account.corezoid.com",
							},
						},
						"required": []string{"account_url"},
					},
				)
				if err != nil {
					logger.Warn("login: elicitation error for ACCOUNT_URL: %v — using default", err)
					resolved = "https://account.corezoid.com"
				} else if action != "accept" {
					logger.Info("login: user cancelled ACCOUNT_URL elicitation (action=%q)", action)
					return "Please ask the user for their Corezoid Account URL (e.g. https://account.corezoid.com), then call the login tool again with account_url=<value>.", false
				} else {
					if v, _ := content["account_url"].(string); v != "" {
						resolved = v
					} else {
						resolved = "https://account.corezoid.com"
					}
				}
			} else {
				return "Please ask the user for their Corezoid Account URL (e.g. https://account.corezoid.com), then call the login tool again with account_url=<value>.", false
			}
			snapAccountURL = resolved
			withAuthLock(func() { accountURL = resolved })
			os.Setenv("ACCOUNT_URL", resolved)
			if err := updateEnvFile(envPath, "ACCOUNT_URL", resolved); err != nil {
				logger.Warn("login: could not save ACCOUNT_URL: %v", err)
			}
		}

		// Step 2: OAuth2 PKCE browser flow (skipped if already authenticated).
		var tokenExpiry time.Time
		if snapToken == "" {
			res, err := oauthPKCEFlow(snapAccountURL, oauthClientID)
			if err != nil {
				return fmt.Sprintf("Authentication failed: %v", err), true
			}
			creds := &Credentials{
				AccessToken: res.AccessToken,
				ExpiresAt:   res.ExpiresAt,
				TokenType:   "Simulator",
			}
			if saveErr := saveCredentials(creds); saveErr != nil {
				logger.Warn("login: failed to save credentials: %v", saveErr)
			}
			snapToken = res.AccessToken
			tokenExpiry = res.ExpiresAt
			withAuthLock(func() { apiToken = res.AccessToken })

			// Step 2.5: derive COREZOID_API_URL from the account clients endpoint.
			authStateMu.RLock()
			apiURLEmpty := apiURL == ""
			authStateMu.RUnlock()
			if apiURLEmpty {
				corezoidURL, fetchErr := fetchCorezoidAPIURL(snapAccountURL, res.AccessToken)
				if fetchErr != nil {
					logger.Warn("login: fetchCorezoidAPIURL failed: %v", fetchErr)
				} else {
					withAuthLock(func() { apiURL = corezoidURL })
					os.Setenv("COREZOID_API_URL", corezoidURL)
					if err := updateEnvFile(envPath, "COREZOID_API_URL", corezoidURL); err != nil {
						logger.Warn("login: could not save COREZOID_API_URL: %v", err)
					}
					logger.Info("login: derived COREZOID_API_URL=%q from clients API", corezoidURL)
				}
			}
		}

		// Step 3: workspace selection.
		if snapWorkspaceID == "" {
			if clientSupportsElicitation {
				workspaces, fetchErr := fetchWorkspaceList(ctx)
				if fetchErr != nil {
					logger.Warn("login: fetchWorkspaceList failed: %v — falling back to text input", fetchErr)
				}

				var wsSchema map[string]interface{}
				wsIDByLabel := map[string]string{}

				if fetchErr == nil && len(workspaces) > 0 {
					enumVals := make([]string, len(workspaces))
					for i, ws := range workspaces {
						label := ws.companyID + " — " + ws.title
						if ws.role != "member" {
							label += " [" + ws.role + "]"
						}
						enumVals[i] = label
						wsIDByLabel[label] = ws.companyID
					}
					wsSchema = map[string]interface{}{
						"type":        "string",
						"title":       "Workspace",
						"description": "Select the workspace you want to work with",
						"enum":        enumVals,
					}
				} else {
					wsSchema = map[string]interface{}{
						"type":        "string",
						"title":       "Workspace ID",
						"description": "Your company/workspace identifier in Corezoid",
					}
				}

				content, action, err := elicitValues(
					"Select your Corezoid workspace:",
					map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{"workspace_id": wsSchema},
						"required":   []string{"workspace_id"},
					},
				)
				if err == nil && action == "accept" {
					if selected, _ := content["workspace_id"].(string); selected != "" {
						id := selected
						if raw, ok := wsIDByLabel[selected]; ok {
							id = raw
						}
						snapWorkspaceID = id
						withAuthLock(func() { workspaceID = id })
						os.Setenv("WORKSPACE_ID", id)
						if err := updateEnvFile(envPath, "WORKSPACE_ID", id); err != nil {
							logger.Warn("login: could not save WORKSPACE_ID: %v", err)
						}
					}
				}
			} else {
				// No elicitation — fetch workspace list and return it to the LLM.
				workspaces, fetchErr := fetchWorkspaceList(ctx)
				var sb strings.Builder
				sb.WriteString("Authenticated successfully.\n\nAvailable workspaces:\n")
				if fetchErr != nil {
					logger.Warn("login: fetchWorkspaceList failed: %v", fetchErr)
					sb.WriteString(fmt.Sprintf("(could not fetch workspace list: %v)\n", fetchErr))
				} else {
					for _, ws := range workspaces {
						label := ws.title
						if ws.role != "member" {
							label += " [" + ws.role + "]"
						}
						sb.WriteString(fmt.Sprintf("  %s — %s\n", ws.companyID, label))
					}
				}
				sb.WriteString("\nPlease ask the user which workspace they want to use, then call login(workspace_id=<selected_id>).")
				return sb.String(), false
			}
		}

		// Steps 4 & 5: pick project then stage.
		if snapStageID == 0 {
			if clientSupportsElicitation {
				var selectedProjectID int64

				// Step 4: fetch project list and elicit selection.
				projects, projErr := fetchProjectList(ctx, snapWorkspaceID)
				if projErr != nil {
					logger.Warn("login: fetchProjectList failed: %v", projErr)
				}

				if projErr == nil && len(projects) > 0 {
					enumVals := make([]string, len(projects))
					projIDByLabel := map[string]int64{}
					for i, p := range projects {
						label := fmt.Sprintf("%d — %s", p.projectID, p.title)
						if p.shortName != "" && p.shortName != p.title {
							label += " (" + p.shortName + ")"
						}
						enumVals[i] = label
						projIDByLabel[label] = p.projectID
					}
					content, action, err := elicitValues(
						"Select your Corezoid project:",
						map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"project": map[string]interface{}{
									"type":        "string",
									"title":       "Project",
									"description": "Select the project to work with",
									"enum":        enumVals,
								},
							},
							"required": []string{"project"},
						},
					)
					if err == nil && action == "accept" {
						if selected, _ := content["project"].(string); selected != "" {
							selectedProjectID = projIDByLabel[selected]
						}
					}
				}

				// Step 5: fetch stage list for selected project and elicit selection.
				if selectedProjectID != 0 {
					stages, stagesErr := fetchStageList(ctx, snapWorkspaceID, selectedProjectID)
					if stagesErr != nil {
						logger.Warn("login: fetchStageList failed: %v", stagesErr)
					}

					if stagesErr == nil && len(stages) > 0 {
						enumVals := make([]string, len(stages))
						stageIDByLabel := map[string]int64{}
						for i, s := range stages {
							label := fmt.Sprintf("%d — %s", s.stageID, s.title)
							if s.shortName != "" && s.shortName != s.title {
								label += " (" + s.shortName + ")"
							}
							if s.immutable {
								label += " [immutable]"
							}
							enumVals[i] = label
							stageIDByLabel[label] = s.stageID
						}
						content, action, err := elicitValues(
							"Select your Corezoid stage (root folder for this project):",
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"stage": map[string]interface{}{
										"type":        "string",
										"title":       "Stage",
										"description": "Select the stage to use as the root folder",
										"enum":        enumVals,
									},
								},
								"required": []string{"stage"},
							},
						)
						if err == nil && action == "accept" {
							if selected, _ := content["stage"].(string); selected != "" {
								if id, ok := stageIDByLabel[selected]; ok && id != 0 {
									snapStageID = int(id)
									withAuthLock(func() { stageID = int(id) })
									vstr := strconv.FormatInt(id, 10)
									os.Setenv("COREZOID_STAGE_ID", vstr)
									if err := updateEnvFile(envPath, "COREZOID_STAGE_ID", vstr); err != nil {
										logger.Warn("login: could not save COREZOID_STAGE_ID: %v", err)
									}
								}
							}
						}
					}
				}

				// Fallback: if stage still not set, ask for stage ID directly.
				if snapStageID == 0 {
					content, action, err := elicitValues(
						"Enter your Stage ID (root folder ID for this project):",
						map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"stage_id": map[string]interface{}{
									"type":        "string",
									"title":       "Stage ID",
									"description": "Root folder ID for this project (numeric)",
								},
							},
							"required": []string{"stage_id"},
						},
					)
					if err == nil && action == "accept" {
						if v, _ := content["stage_id"].(string); v != "" {
							if id, err := strconv.Atoi(v); err == nil && id != 0 {
								snapStageID = id
								withAuthLock(func() { stageID = id })
								os.Setenv("COREZOID_STAGE_ID", v)
								if err := updateEnvFile(envPath, "COREZOID_STAGE_ID", v); err != nil {
									logger.Warn("login: could not save COREZOID_STAGE_ID: %v", err)
								}
							}
						}
					}
				}
			} else {
				// No elicitation — list projects so LLM can collect stage from user.
				projects, projErr := fetchProjectList(ctx, snapWorkspaceID)
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Workspace %s selected.\n\n", snapWorkspaceID))
				if projErr != nil || len(projects) == 0 {
					if projErr != nil {
						sb.WriteString(fmt.Sprintf("Could not fetch projects: %v\n", projErr))
					} else {
						sb.WriteString("No projects found.\n")
					}
					sb.WriteString(fmt.Sprintf("Please ask the user for their COREZOID_STAGE_ID (root folder ID), then call login(workspace_id=%s, stage_id=<stage_id>).", snapWorkspaceID))
				} else {
					sb.WriteString("Available projects:\n")
					for _, p := range projects {
						line := fmt.Sprintf("  %d — %s", p.projectID, p.title)
						if p.shortName != "" && p.shortName != p.title {
							line += fmt.Sprintf(" (%s)", p.shortName)
						}
						sb.WriteString(line + "\n")
					}
					sb.WriteString(fmt.Sprintf("\nPlease ask the user which project to use. Call list-stages(project_id=<id>, company_id=%s) to see available stages, then ask the user to pick one and call login(workspace_id=%s, stage_id=<stage_id>).", snapWorkspaceID, snapWorkspaceID))
				}
				return sb.String(), false
			}
		}

		// Auto pull-folder if stageID was set during this login call.
		if snapStageID != 0 && stageIDAtStart == 0 {
			pv := NewValidator(ctx, 0)
			if pullErr := downloadStageRecursively(pv, snapStageID, "."); pullErr != nil {
				logger.Warn("login: auto pull-folder failed: %v", pullErr)
			}
		}

		msg := fmt.Sprintf("Setup complete! Configuration saved to %s.", envPath)
		if !tokenExpiry.IsZero() {
			msg += fmt.Sprintf(" Token expires: %s.", tokenExpiry.Format("2006-01-02 15:04"))
		}
		return msg, false

	case "logout":
		if err := deleteCredentials(); err != nil {
			return fmt.Sprintf("Failed to remove credentials: %v", err), true
		}
		withAuthLock(func() { apiToken = "" })
		return fmt.Sprintf("Logged out. ACCESS_TOKEN removed from %s.", envFilePath()), false

	case "pull-process":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		v := NewValidator(ctx, processID)
		procInfo1, err := v.ExportProcess()
		if err != nil {
			return fmt.Sprintf("Error fetching process: %v", err), true
		}
		var procInfo interface{}
		if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
			procInfo = arr[0]
		} else {
			procInfo = procInfo1
		}
		data, err := json.MarshalIndent(procInfo, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error marshaling process: %v", err), true
		}

		// Derive filename from process title if available
		fileName := fmt.Sprintf("%d.conv.json", processID)
		if m, ok := procInfo.(map[string]interface{}); ok {
			if title, _ := m["title"].(string); title != "" {
				safeName := strings.ReplaceAll(title, " ", "_")
				fileName = fmt.Sprintf("%d_%s.conv.json", processID, safeName)
			}
		}

		// Resolve save directory from parent_id so the file lands in the correct folder tree.
		var dir string
		if m, ok := procInfo.(map[string]interface{}); ok {
			parentID := 0
			if pid, ok := m["parent_id"].(float64); ok {
				parentID = int(pid)
			}
			if parentID != 0 && v.StageID != 0 {
				resolved, resolveErr := v.resolveFolderPathFromAPI(parentID)
				if resolveErr != nil {
					logger.Warn("pull-process: could not resolve folder path for parent_id %d: %v", parentID, resolveErr)
				} else {
					dir = resolved
				}
			}
		}

		if dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Sprintf("Error creating directory: %v", err), true
			}
		}

		filePath := filepath.Join(dir, fileName)
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Sprintf("Error writing file: %v", err), true
		}
		return fmt.Sprintf("Process %d saved to %s", processID, filePath), false

	case "pull-folder":
		folderID, err := intArg(args, "folder_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(ctx, 0)
		if err := downloadStageRecursively(v, folderID, "."); err != nil {
			return fmt.Sprintf("Error fetching folder: %v", err), true
		}
		return fmt.Sprintf("Folder %d saved to current directory", folderID), false

	case "create-variable":
		rootFolderID, err := strArg(args, "stage_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		name, err := strArg(args, "name")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		description, err := strArg(args, "description")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		value, err := strArg(args, "value")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(ctx, 0)
		if err := v.CreateVariable(rootFolderID, name, description, value); err != nil {
			return fmt.Sprintf("Error creating variable: %v", err), true
		}
		return fmt.Sprintf("Environment variable '%s' created successfully", name), false

	case "push-process":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		baseName := filepath.Base(filePath)
		reFileID := regexp.MustCompile(`^(\d+)_`)
		matches := reFileID.FindStringSubmatch(baseName)
		if matches == nil {
			return fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.json'", baseName), true
		}
		procID, _ := strconv.Atoi(matches[1])

		v := NewValidator(ctx, procID)

		jsonContent, err := LoadBinFromFile(filePath)
		if err != nil {
			return fmt.Sprintf("Error loading JSON file: %v", err), true
		}

		jsonContent1, messages := fixStruct(jsonContent, procID)
		if len(messages) > 0 {
			for _, msg := range messages {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
		if jsonContent1 != jsonContent {
			jsonContent = jsonContent1
			if err := os.WriteFile(filePath, []byte(jsonContent), 0644); err != nil {
				return fmt.Sprintf("Error writing fixed JSON: %v", err), true
			}
		}

		if err := ValidateJSONSchema(filePath, debug); err != nil {
			return fmt.Sprintf("JSON schema validation failed: %v", err), true
		}

		if err := v.BeforeValidation(jsonContent, nil); err != nil {
			return fmt.Sprintf("Validation failed: %v", err), true
		}

		if _, err := v.ProcessJSON(filePath, jsonContent); err != nil {
			return fmt.Sprintf("Error deploying process: %v", err), true
		}

		return fmt.Sprintf("Process deployed successfully, ProcessID: %d", procID), false

	case "lint-process":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		result, err := lintProcess(filePath)
		if err != nil {
			return fmt.Sprintf("Error: lint failed: %v", err), true
		}
		return FormatLintResult(result), false

	case "run-task":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dataStr, err := strArg(args, "data")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		baseName := filepath.Base(filePath)
		reFileID := regexp.MustCompile(`^(\d+)_`)
		matches := reFileID.FindStringSubmatch(baseName)
		if matches == nil {
			return fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.json'", baseName), true
		}
		procID, _ := strconv.Atoi(matches[1])

		v := NewValidator(ctx, procID)

		jsonContent, err := LoadBinFromFile(filePath)
		if err != nil {
			return fmt.Sprintf("Error reading process file: %v", err), true
		}

		if _, err := v.ProcessJSON(filePath, jsonContent); err != nil {
			return fmt.Sprintf("Error deploying process: %v", err), true
		}

		taskData := make(map[string]interface{})
		if err := json.Unmarshal([]byte(dataStr), &taskData); err != nil {
			return fmt.Sprintf("Error parsing task data: %v", err), true
		}

		ref := fmt.Sprintf("%d_%d", time.Now().Unix(), rand.Intn(1000000))
		if err := v.createTask(ref, taskData); err != nil {
			return fmt.Sprintf("Error creating task: %v", err), true
		}

		time.Sleep(time.Second * 5)

		rspTask, err := v.showTask(ref)
		if err != nil {
			return fmt.Sprintf("Error getting task result: %v", err), true
		}
		logger.Info("Task response: %+v", rspTask)
		rspTaskData, _ := rspTask["data"].(map[string]interface{})
		rspTaskDataBin, _ := json.Marshal(rspTaskData)
		serverNodeID, _ := rspTask["node_id"].(string)

		if v.Debug {
			for k, ni := range v.NodeIDMap {
				logger.Debug("NodeIDMap entry: key=%s type=%d serverID=%s name=%s", k, ni.Type, ni.ServerID, ni.Name)
			}
		}

		nodeInfo, found := v.NodeIDMap[serverNodeID]
		if !found {
			for _, ni := range v.NodeIDMap {
				if serverNodeID == ni.ServerID {
					nodeInfo = ni
					found = true
					break
				}
			}
		}
		logger.Info("Node info (found=%v): %+v", found, nodeInfo)
		nodeType := "logic (not final)"
		msg := "Task failed: it stopped at the non-final node"
		isErr := true
		if nodeInfo.Type == 1 {
			nodeType = "start"
		} else if nodeInfo.Type == 2 {
			isErr = false
			nodeType = "end (Success)"
			msg = "Task completed"
			if nodeInfo.Icon == "error" {
				isErr = true
				nodeType = "error node"
				msg = "Task failed: stopped at error node"
			}
		}

		summary := fmt.Sprintf("%s\nNodeID: %s\nNodeName: %s\nNodeType: %s\nProcessID: %d\nData: %s",
			msg, serverNodeID, nodeInfo.Name, nodeType, v.ProcessID, string(rspTaskDataBin))
		return summary, isErr

	case "create-process":
		folderPath := resolveDirPath(args, "folder_path")
		processName, err := strArg(args, "process_name")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		folderID, err := resolveFolderIDFromDir(folderPath)
		if err != nil {
			return fmt.Sprintf("Error resolving folder ID: %v", err), true
		}

		v := NewValidator(ctx, 0)
		processID := v.CreateEmptyProcess(folderID, processName, "")
		if processID == 0 {
			return fmt.Sprintf("Error: failed to create process '%s'", processName), true
		}

		procInfo1, err := v.ExportProcess()
		if err != nil {
			return fmt.Sprintf("Error exporting process: %v", err), true
		}
		var procInfo interface{}
		if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
			procInfo = arr[0]
		} else {
			procInfo = procInfo1
		}
		data, err := json.MarshalIndent(procInfo, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error marshaling process: %v", err), true
		}

		safeName := strings.ReplaceAll(processName, " ", "_")
		fileName := fmt.Sprintf("%d_%s.json", processID, safeName)
		filePath := filepath.Join(folderPath, fileName)
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Sprintf("Error writing file: %v", err), true
		}

		return fmt.Sprintf("Process '%s' created and saved to %s", processName, filePath), false

	case "create-folder":
		parentPath := resolveDirPath(args, "parent_path")
		folderName, err := strArg(args, "folder_name")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		parentFolderID, err := resolveFolderIDFromDir(parentPath)
		if err != nil {
			return fmt.Sprintf("Error resolving parent folder ID: %v", err), true
		}

		v := NewValidator(ctx, 0)
		newFolderID, err := v.CreateFolder(parentFolderID, folderName, "")
		if err != nil {
			return fmt.Sprintf("Error creating folder '%s': %v", folderName, err), true
		}

		safeName := strings.ReplaceAll(folderName, " ", "_")
		dirName := fmt.Sprintf("%d_%s", newFolderID, safeName)
		dirPath := filepath.Join(parentPath, dirName)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Sprintf("Error creating directory '%s': %v", dirPath, err), true
		}

		type folderFileContent struct {
			Description string `json:"description"`
			ObjID       int    `json:"obj_id"`
			ObjType     int    `json:"obj_type"`
			ParentID    int    `json:"parent_id"`
			Title       string `json:"title"`
		}
		fileContent := folderFileContent{
			Description: "",
			ObjID:       newFolderID,
			ObjType:     0,
			ParentID:    parentFolderID,
			Title:       folderName,
		}
		fileData, err := json.MarshalIndent(fileContent, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error marshaling folder file: %v", err), true
		}
		fileName := fmt.Sprintf("%d_%s.folder.json", newFolderID, safeName)
		filePath := filepath.Join(dirPath, fileName)
		if err := os.WriteFile(filePath, fileData, 0644); err != nil {
			return fmt.Sprintf("Error writing folder file: %v", err), true
		}

		return fmt.Sprintf("Folder '%s' created and saved to %s", folderName, filePath), false

	case "create-alias":
		filePath, err := resolveProcessPath(args, "process_path")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		shortName, err := strArg(args, "short_name")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		baseName := filepath.Base(filePath)
		reFileID := regexp.MustCompile(`^(\d+)_`)
		matches := reFileID.FindStringSubmatch(baseName)
		if matches == nil {
			return fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.json'", baseName), true
		}
		procID, _ := strconv.Atoi(matches[1])

		v := NewValidator(ctx, 0)
		if v.StageID == 0 {
			return "Error: COREZOID_STAGE_ID environment variable is not set or invalid", true
		}
		aliasID, err := v.CreateAlias(shortName, procID, v.StageID)
		if err != nil {
			return fmt.Sprintf("Error creating alias: %v", err), true
		}

		return fmt.Sprintf("Alias '%s' created successfully, AliasID: %d", shortName, aliasID), false

	case "list-workspaces":
		v := NewValidator(ctx, 0)
		ops := []map[string]any{
			{
				"type": "list",
				"obj":  "company",
			},
		}
		resp, err := v.req("list_workspaces", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}

		// Extract the workspace list from ops[0].list
		opsArr, _ := resp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "No workspaces found", false
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		list, _ := opMap["list"].([]interface{})
		if len(list) == 0 {
			return "No workspaces found", false
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Workspaces (%d total):\n\n", len(list)))
		for _, item := range list {
			ws, _ := item.(map[string]interface{})
			companyID, _ := ws["company_id"].(string)
			title, _ := ws["title"].(string)
			isOwner, _ := ws["is_owner"].(bool)
			isAdmin, _ := ws["is_admin"].(bool)

			role := "member"
			if isOwner {
				role = "owner"
			} else if isAdmin {
				role = "admin"
			}

			sb.WriteString(fmt.Sprintf("  %-45s  %s  [%s]\n", companyID, title, role))
		}
		return sb.String(), false

	case "list-projects":
		companyID, err := strArg(args, "company_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(ctx, 0)
		ops := []map[string]any{
			{
				"type":       "list",
				"obj":        "projects",
				"obj_id":     0,
				"id":         companyID,
				"company_id": companyID,
				"sort":       "title",
			},
		}
		resp, err := v.req("list_projects", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}

		opsArr, _ := resp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "No projects found", false
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		if proc, _ := opMap["proc"].(string); proc != "ok" {
			desc, _ := opMap["description"].(string)
			return fmt.Sprintf("Error: %s", desc), true
		}
		list, _ := opMap["list"].([]interface{})
		if len(list) == 0 {
			return "No projects found", false
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Projects in workspace %s (%d total):\n\n", companyID, len(list)))
		sb.WriteString(fmt.Sprintf("  %-10s  %-35s  %-30s  %s\n", "ID", "Title", "Short name", "Owner"))
		sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("-", 95)))
		for _, item := range list {
			p, _ := item.(map[string]interface{})
			projectID := int64(0)
			if f, ok := p["project_id"].(float64); ok {
				projectID = int64(f)
			}
			title, _ := p["title"].(string)
			shortName, _ := p["short_name"].(string)
			ownerLogin, _ := p["owner_login"].(string)
			undeployed := int(0)
			if f, ok := p["undeployed"].(float64); ok {
				undeployed = int(f)
			}
			undeployedStr := ""
			if undeployed > 0 {
				undeployedStr = fmt.Sprintf(" [%d undeployed]", undeployed)
			}
			sb.WriteString(fmt.Sprintf("  %-10d  %-35s  %-30s  %s%s\n",
				projectID, title, shortName, ownerLogin, undeployedStr))
		}
		return sb.String(), false

	case "list-stages":
		projectID, err := intArg(args, "project_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		companyID, err := strArg(args, "company_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(ctx, 0)
		ops := []map[string]any{
			{
				"type":       "list",
				"obj":        "project",
				"obj_id":     projectID,
				"id":         companyID,
				"company_id": companyID,
				"sort":       "date",
				"order":      "asc",
			},
		}
		resp, err := v.req("list_stages", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}

		opsArr, _ := resp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "No stages found", false
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		if proc, _ := opMap["proc"].(string); proc != "ok" {
			desc, _ := opMap["description"].(string)
			return fmt.Sprintf("Error: %s", desc), true
		}
		list, _ := opMap["list"].([]interface{})
		if len(list) == 0 {
			return "No stages found", false
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Stages in project %d (%d total):\n\n", projectID, len(list)))
		sb.WriteString(fmt.Sprintf("  %-10s  %-20s  %-20s  %s\n", "ID", "Title", "Short name", "Immutable"))
		sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("-", 70)))
		for _, item := range list {
			s, _ := item.(map[string]interface{})
			stageID := int64(0)
			if f, ok := s["obj_id"].(float64); ok {
				stageID = int64(f)
			}
			title, _ := s["title"].(string)
			shortName, _ := s["short_name"].(string)
			immutable, _ := s["immutable"].(bool)
			immutableStr := "no"
			if immutable {
				immutableStr = "yes"
			}
			sb.WriteString(fmt.Sprintf("  %-10d  %-20s  %-20s  %s\n", stageID, title, shortName, immutableStr))
		}
		return sb.String(), false

	case "list-task-history":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		taskID, err := strArg(args, "task_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(ctx, processID)
		ops := []map[string]any{
			{
				"type":    "list",
				"obj":     "task_history",
				"conv_id": processID,
				"obj_id":  taskID,
			},
		}
		resp, err := v.req("list_task_history", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "list-node-tasks":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		nodeID, err := strArg(args, "node_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		limit := 50
		if v, ok := args["limit"]; ok {
			if n, err := intArg(args, "limit"); err == nil {
				_ = v
				limit = n
			}
		}
		offset := 0
		if v, ok := args["offset"]; ok {
			if n, err := intArg(args, "offset"); err == nil {
				_ = v
				offset = n
			}
		}

		validator := NewValidator(ctx, processID)
		ops := []map[string]any{
			{
				"type":       "list",
				"obj":        "node",
				"company_id": validator.WorkspaceID,
				"conv_id":    processID,
				"obj_id":     nodeID,
				"limit":      limit,
				"offset":     offset,
			},
		}
		resp, err := validator.req("list_node_tasks", ops)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "modify-task":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dataStr, err := strArg(args, "data")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		taskID, _ := args["task_id"].(string)
		ref, _ := args["ref"].(string)
		if taskID == "" && ref == "" {
			return "Error: at least one of task_id or ref must be provided", true
		}

		var taskData map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &taskData); err != nil {
			return fmt.Sprintf("Error parsing data JSON: %v", err), true
		}

		op := map[string]any{
			"type":    "modify",
			"obj":     "task",
			"conv_id": processID,
			"data":    taskData,
		}
		if taskID != "" {
			op["obj_id"] = taskID
		}
		if ref != "" {
			op["ref"] = ref
		}

		v := NewValidator(ctx, processID)
		resp, err := v.req("modify_task", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "delete-task":
		processID, err := intArg(args, "process_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		taskID, _ := args["task_id"].(string)
		ref, _ := args["ref"].(string)
		if taskID == "" && ref == "" {
			return "Error: at least one of task_id or ref must be provided", true
		}

		v := NewValidator(ctx, processID)

		// Resolve task_id and node_id via show first
		showOp := map[string]any{
			"type":    "show",
			"obj":     "task",
			"conv_id": processID,
		}
		if taskID != "" {
			showOp["obj_id"] = taskID
		} else {
			showOp["ref"] = ref
		}
		showResp, err := v.req("show_task", []map[string]any{showOp})
		if err != nil {
			return fmt.Sprintf("Error resolving task: %v", err), true
		}
		opsArr, _ := showResp["ops"].([]interface{})
		if len(opsArr) == 0 {
			return "Error: task not found", true
		}
		opMap, _ := opsArr[0].(map[string]interface{})
		if opMap["proc"] != "ok" {
			desc, _ := opMap["description"].(string)
			return fmt.Sprintf("Error: %s", desc), true
		}
		resolvedTaskID, _ := opMap["obj_id"].(string)
		nodeID, _ := opMap["node_id"].(string)

		deleteOp := map[string]any{
			"type":    "delete",
			"obj":     "task",
			"conv_id": processID,
			"obj_id":  resolvedTaskID,
			"node_id": nodeID,
		}
		resp, err := v.req("delete_task", []map[string]any{deleteOp})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "create-dashboard":
		title, err := strArg(args, "title")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		description, _ := args["description"].(string)
		tzOffset := 0
		if tz, ok := args["timezone_offset"]; ok {
			if tzFloat, ok := tz.(float64); ok {
				tzOffset = int(tzFloat)
			} else if tzStr, ok := tz.(string); ok {
				tzOffset, _ = strconv.Atoi(tzStr)
			}
		}
		v := NewValidator(ctx, 0)
		folderID := v.StageID
		if fid, ok := args["folder_id"]; ok {
			if fidFloat, ok := fid.(float64); ok {
				folderID = int(fidFloat)
			} else if fidStr, ok := fid.(string); ok {
				folderID, _ = strconv.Atoi(fidStr)
			}
		}

		projectID := v.GetProjectIDByStageID(folderID)
		now := int(time.Now().Unix())

		op := map[string]any{
			"obj":         "dashboard",
			"type":        "create",
			"obj_type":    0,
			"title":       title,
			"description": description,
			"folder_id":   folderID,
			"stage_id":    folderID,
			"project_id":  projectID,
			"company_id":  v.WorkspaceID,
			"status":      "active",
			"time_range": map[string]any{
				"select":          "online",
				"start":           now,
				"stop":            now,
				"timezone_offset": tzOffset,
			},
		}

		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "get-dashboard":
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(ctx, 0)
		op := map[string]any{
			"obj":        "dashboard",
			"type":       "show",
			"obj_id":     dashboardID,
			"company_id": v.WorkspaceID,
		}

		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "add-chart":
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		name, err := strArg(args, "name")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		chartType, err := strArg(args, "chart_type")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		seriesStr, err := strArg(args, "series")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		var series []interface{}
		if err := json.Unmarshal([]byte(seriesStr), &series); err != nil {
			return fmt.Sprintf("Error parsing series JSON: %v", err), true
		}

		v := NewValidator(ctx, 0)
		op := map[string]any{
			"obj":          "chart",
			"type":         "create",
			"obj_type":     chartType,
			"dashboard_id": dashboardID,
			"name":         name,
			"description":  "",
			"series":       series,
			"company_id":   v.WorkspaceID,
		}

		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "modify-chart":
		chartID, err := strArg(args, "chart_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		title, err := strArg(args, "name")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		chartType, err := strArg(args, "chart_type")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		seriesStr, err := strArg(args, "series")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		var series []interface{}
		if err := json.Unmarshal([]byte(seriesStr), &series); err != nil {
			return fmt.Sprintf("Error parsing series JSON: %v", err), true
		}

		v := NewValidator(ctx, 0)
		op := map[string]any{
			"obj":          "chart",
			"type":         "modify",
			"obj_type":     chartType,
			"obj_id":       chartID,
			"dashboard_id": dashboardID,
			"name":         title,
			"description":  "",
			"series":       series,
			"company_id":   v.WorkspaceID,
		}

		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "get-chart":
		chartID, err := strArg(args, "chart_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}

		v := NewValidator(ctx, 0)
		op := map[string]any{
			"obj":          "chart",
			"type":         "get",
			"obj_id":       chartID,
			"dashboard_id": dashboardID,
			"company_id":   v.WorkspaceID,
		}

		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	case "set-dashboard-layout":
		dashboardID, err := intArg(args, "dashboard_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		gridStr, err := strArg(args, "grid")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		tzOffset := 0
		if tz, ok := args["timezone_offset"]; ok {
			if tzFloat, ok := tz.(float64); ok {
				tzOffset = int(tzFloat)
			} else if tzStr, ok := tz.(string); ok {
				tzOffset, _ = strconv.Atoi(tzStr)
			}
		}

		// Parse grid input: [{chart_id, x, y, width, height}]
		var gridInput []map[string]interface{}
		if err := json.Unmarshal([]byte(gridStr), &gridInput); err != nil {
			return fmt.Sprintf("Error parsing grid JSON: %v", err), true
		}

		// Convert chart_id -> obj_id for the API payload
		apiGrid := make([]map[string]interface{}, 0, len(gridInput))
		for _, entry := range gridInput {
			chartHexID, _ := entry["chart_id"].(string)
			if chartHexID == "" {
				return "Error: each grid entry must have a non-empty chart_id", true
			}
			gridEntry := map[string]interface{}{
				"obj_id": chartHexID,
			}
			for _, field := range []string{"x", "y", "width", "height"} {
				if v, ok := entry[field]; ok {
					gridEntry[field] = v
				}
			}
			apiGrid = append(apiGrid, gridEntry)
		}

		v := NewValidator(ctx, 0)
		op := map[string]any{
			"obj":        "dashboard",
			"type":       "modify",
			"obj_id":     dashboardID,
			"company_id": v.WorkspaceID,
			"time_range": map[string]any{
				"select":          "online",
				"timezone_offset": tzOffset,
			},
			"grid": apiGrid,
		}

		resp, err := v.req("json", []map[string]any{op})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), false

	default:
		return fmt.Sprintf("Unknown tool: %s", name), true
	}
}
