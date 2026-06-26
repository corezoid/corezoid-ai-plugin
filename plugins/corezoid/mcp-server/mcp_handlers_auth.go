package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// handleLogin runs the OAuth2 PKCE flow. ACCOUNT_URL, WORKSPACE_ID, and
// COREZOID_STAGE_ID are persisted to the project .env; ACCESS_TOKEN is saved
// to ~/.corezoid/credentials via saveCredentials(). The handler is
// long-running and interactive (elicitation + browser OAuth), so it must NOT
// hold the auth lock across user-facing waits; we snapshot the auth state,
// drive the flow from locals, and re-acquire the lock only for the writes
// that update globals after each step.
func handleLogin(ctx context.Context, args map[string]interface{}) (string, bool) {
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
		// Reload unconditionally so that values in a swapped .env override the
		// in-memory state captured at startup — enables mid-session env switching.
		accountURL = os.Getenv("ACCOUNT_URL")
		workspaceID = os.Getenv("WORKSPACE_ID")
		stageID, _ = strconv.Atoi(os.Getenv("COREZOID_STAGE_ID"))
		apiURL = os.Getenv("COREZOID_API_URL")

		// Record initial stageID to detect if it gets set during this call.
		stageIDAtStart = stageID

		// Apply any values passed directly as arguments (bypasses elicitation).
		// Arguments override .env so users can switch environments explicitly.
		if v := optStrArg(args, "account_url"); v != "" {
			if v != accountURL {
				// Account URL changed; the derived API URL is no longer valid for the new host.
				apiURL = ""
				os.Setenv("COREZOID_API_URL", "")
				if err := updateEnvFile(envPath, "COREZOID_API_URL", ""); err != nil {
					logger.Warn("login: could not clear COREZOID_API_URL on host switch: %v", err)
				}
			}
			accountURL = v
			os.Setenv("ACCOUNT_URL", v)
			if err := updateEnvFile(envPath, "ACCOUNT_URL", v); err != nil {
				logger.Warn("login: could not save ACCOUNT_URL from arg: %v", err)
			}
		}
		if v := optStrArg(args, "workspace_id"); v != "" {
			workspaceID = v
			os.Setenv("WORKSPACE_ID", v)
			if err := updateEnvFile(envPath, "WORKSPACE_ID", v); err != nil {
				logger.Warn("login: could not save WORKSPACE_ID from arg: %v", err)
			}
		}
		if v := optStrArg(args, "stage_id"); v != "" {
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
	} else {
		// If we already have a token but no derived API URL (e.g. token came from
		// .env directly without completing OAuth), fetch the URL now.
		authStateMu.RLock()
		apiURLEmpty := apiURL == ""
		authStateMu.RUnlock()
		if apiURLEmpty {
			corezoidURL, fetchErr := fetchCorezoidAPIURL(snapAccountURL, snapToken)
			if fetchErr != nil {
				logger.Warn("login: fetchCorezoidAPIURL failed: %v", fetchErr)
			} else {
				withAuthLock(func() { apiURL = corezoidURL })
				os.Setenv("COREZOID_API_URL", corezoidURL)
				if err := updateEnvFile(envPath, "COREZOID_API_URL", corezoidURL); err != nil {
					logger.Warn("login: could not save COREZOID_API_URL: %v", err)
				}
				logger.Info("login: derived COREZOID_API_URL=%q from clients API (pre-existing token)", corezoidURL)
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

	// One-time opt-in: ask for email to include in telemetry.
	// Only shown once per installation; skipping is always valid.
	if clientSupportsElicitation {
		prefs := loadUserPreferences()
		if !prefs.TelemetryEmailAsked {
			content, action, err := elicitValues(
				"Would you like to share your email with the Corezoid team? It helps them contact you if issues arise. This is optional — press Cancel to skip.",
				map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"email": map[string]interface{}{
							"type":        "string",
							"title":       "Email address",
							"description": "Optional — leave blank or press Cancel to skip",
						},
					},
				},
			)
			prefs.TelemetryEmailAsked = true
			if err == nil && action == "accept" {
				if email, _ := content["email"].(string); email != "" {
					prefs.TelemetryEmail = email
					telemetryEmail = email
				}
			}
			if saveErr := saveUserPreferences(prefs); saveErr != nil {
				logger.Warn("login: could not save preferences: %v", saveErr)
			}
		}
	}

	return msg, false
}

// handleLogout deletes the saved credentials from ~/.corezoid/credentials and
// clears the cached access token. The token write is under the auth lock so a
// concurrent in-flight request can't briefly use the cleared token-before-deletion state.
func handleLogout(_ context.Context, _ map[string]interface{}) (string, bool) {
	credPath, err := credentialsFilePath()
	if err != nil {
		credPath = "~/.corezoid/credentials"
	}
	if err := deleteCredentials(); err != nil {
		return fmt.Sprintf("Failed to remove credentials: %v", err), true
	}
	withAuthLock(func() {
		apiToken = ""
		accountURL = ""
		workspaceID = ""
		stageID = 0
		apiURL = ""
	})
	return fmt.Sprintf("Logged out. ACCESS_TOKEN removed from %s.", credPath), false
}
