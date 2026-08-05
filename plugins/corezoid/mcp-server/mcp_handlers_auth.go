package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// updateAndSync mutates the current Folder in ~/.corezoid/config.json and
// then refreshes the in-memory auth-state globals. Convenience wrapper used
// by handleLogin's many small state updates; warns on persistence failure so
// the user-driven login flow does not abort mid-step.
func updateAndSync(mutator func(*Folder), logCtx string) {
	if err := UpdateCurrent(mutator); err != nil {
		logger.Warn("%s: could not persist config: %v", logCtx, err)
	}
	syncGlobalsFromCurrent()
}

// resolveAndSaveAPIURL discovers the Corezoid API URL via fetchCorezoidAPIURL
// and persists it to the current Folder as CorezoidURL. Falls back to
// account_url only when discovery genuinely returns an empty result with no
// error — a real fetch failure (network, transient 5xx, permission) leaves
// the URL unset so the next login retries, instead of silently pointing
// requests at a host that may not serve /api/2/json.
func resolveAndSaveAPIURL(accountURL, token, logSuffix string) {
	corezoidURL, fetchErr := fetchCorezoidAPIURL(accountURL, token)
	if fetchErr != nil {
		logger.Warn("login: fetchCorezoidAPIURL failed: %v — corezoid_url left unresolved, retry on next login", fetchErr)
		return
	}
	if corezoidURL == "" {
		corezoidURL = strings.TrimRight(accountURL, "/")
		logger.Info("login: corezoid_url discovery returned empty — using account_url %q", corezoidURL)
	}
	updateAndSync(func(f *Folder) { f.CorezoidURL = corezoidURL }, "login")
	logger.Info("login: corezoid_url=%q%s", corezoidURL, logSuffix)
}

// handleLogin runs the OAuth2 PKCE flow (or accepts pre-provided API-key
// credentials). All persisted state lives in the current Folder in
// ~/.corezoid/config.json. The handler is long-running and interactive
// (elicitation + browser OAuth); we never hold authStateMu across user-facing
// waits, and every mutation is serialised via UpdateCurrent's flock.
func handleLogin(ctx context.Context, args map[string]interface{}) (string, bool) {
	// Refresh in case another process wrote to config since startup.
	syncGlobalsFromCurrent()

	// Capture state BEFORE args are applied — snapStageIDBefore is used at the
	// end to decide whether to auto-run pull-folder (only if stage was
	// unconfigured before this login call).
	_, _, _, _, snapStageIDBefore := authSnapshot()

	// Apply argument-provided values. Arguments always override current state
	// so users can switch environments explicitly.
	if v := optStrArg(args, "account_url"); v != "" {
		_, _, _, current, _ := authSnapshot()
		if v != current {
			// Account URL changed — derived API URL and git mirror URL are no
			// longer valid for the new host.
			updateAndSync(func(f *Folder) {
				f.AccountURL = v
				f.CorezoidURL = ""
				f.GitURL = ""
				f.GitStagePath = ""
			}, "login")
		} else {
			updateAndSync(func(f *Folder) { f.AccountURL = v }, "login")
		}
	}
	if v := optStrArg(args, "workspace_id"); v != "" {
		_, _, current, _, _ := authSnapshot()
		if v != current {
			// Workspace changed — invalidate cached stage_id, project_id, and
			// git_stage_path (all keyed by workspace). The on-disk contents of
			// RootPath from the previous workspace's stage stay in place;
			// pull-folder will overwrite them on stage re-selection.
			updateAndSync(func(f *Folder) {
				f.WorkspaceID = v
				f.ProjectID = 0
				f.StageID = 0
				f.GitStagePath = ""
			}, "login")
		} else {
			updateAndSync(func(f *Folder) { f.WorkspaceID = v }, "login")
		}
	}
	// stage_id arg: persist immediately so a follow-up call without stage_id
	// still knows which stage is active. When the value changes, cached
	// project_id and git_stage_path become stale.
	var argStageID int
	if v := optStrArg(args, "stage_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil && id != 0 {
			argStageID = id
			_, _, _, _, current := authSnapshot()
			if id != current {
				updateAndSync(func(f *Folder) {
					f.StageID = id
					f.ProjectID = 0
					f.GitStagePath = ""
				}, "login")
			} else {
				updateAndSync(func(f *Folder) { f.StageID = id }, "login")
			}
		}
	}
	if v := optStrArg(args, "api_login"); v != "" {
		updateAndSync(func(f *Folder) { f.APILogin = v }, "login")
	}
	if v := optStrArg(args, "api_secret"); v != "" {
		updateAndSync(func(f *Folder) { f.APISecret = v }, "login")
	}

	// Snapshot post-arg-application state for the rest of this handler.
	// stage_id is NOT read from the global — the arg only affects the auto-pull
	// below, which materializes the marker; only after that will the global
	// mirror the on-disk state.
	_, snapToken, snapWorkspaceID, snapAccountURL, snapStageID := authSnapshot()
	if argStageID != 0 {
		snapStageID = argStageID
	}
	snapAPILogin, snapAPISecret := apiKeySnapshot()
	logger.Info("login: accountURL=%q workspaceID=%q stageID=%d hasAPIKey=%v", snapAccountURL, snapWorkspaceID, snapStageID, snapAPILogin != "" && snapAPISecret != "")

	// Step 1: ensure Account API URL.
	if snapAccountURL == "" {
		var resolved string
		if clientElicitationSupported() {
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
				logger.Warn("login: elicitation error for account_url: %v — using default", err)
				resolved = "https://account.corezoid.com"
			} else if action != "accept" {
				logger.Info("login: user cancelled account_url elicitation (action=%q)", action)
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
		updateAndSync(func(f *Folder) { f.AccountURL = resolved }, "login")
	}

	// Step 2: OAuth2 PKCE browser flow (skipped if already authenticated or if
	// API key credentials are present — API key auth does not require a
	// browser flow).
	var tokenExpiry time.Time
	if snapToken == "" && !(snapAPILogin != "" && snapAPISecret != "") {
		res, err := oauthPKCEFlow(snapAccountURL, oauthClientID)
		if err != nil {
			return fmt.Sprintf("Authentication failed: %v", err), true
		}
		updateAndSync(func(f *Folder) {
			f.AccessToken = res.AccessToken
			f.ExpiresAt = res.ExpiresAt
		}, "login")
		snapToken = res.AccessToken
		tokenExpiry = res.ExpiresAt

		// Step 2.5: derive the working API URL from the account clients endpoint.
		authStateMu.RLock()
		apiURLEmpty := apiURL == ""
		authStateMu.RUnlock()
		if apiURLEmpty {
			resolveAndSaveAPIURL(snapAccountURL, res.AccessToken, "")
		}
	} else if snapToken != "" {
		// Token pre-existing but no derived API URL — fetch now.
		authStateMu.RLock()
		apiURLEmpty := apiURL == ""
		authStateMu.RUnlock()
		if apiURLEmpty {
			resolveAndSaveAPIURL(snapAccountURL, snapToken, " (pre-existing token)")
		}
	} else {
		// API key flow: /face/api/1/clients requires a bearer token — fall back
		// to account_url. In most deployments the API is served from the same
		// host as the admin UI. Users needing a different API host can edit
		// corezoid_url in ~/.corezoid/config.json directly.
		authStateMu.RLock()
		apiURLEmpty := apiURL == ""
		authStateMu.RUnlock()
		if apiURLEmpty && snapAccountURL != "" {
			fallback := strings.TrimRight(snapAccountURL, "/")
			updateAndSync(func(f *Folder) { f.CorezoidURL = fallback }, "login")
			logger.Info("login: corezoid_url not set — defaulting to account_url %q", fallback)
		}
	}

	// Step 3: workspace selection.
	if snapWorkspaceID == "" {
		if clientElicitationSupported() {
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
					// Workspace being set from empty — drop cached
					// project_id so subsequent stage selection starts clean.
					updateAndSync(func(f *Folder) {
						f.WorkspaceID = id
						f.ProjectID = 0
					}, "login")
					snapWorkspaceID = id
				}
			}
		} else {
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
	// noProjectSelected is set when the user explicitly chose "No Project" at
	// step 4 — the workspace has no stage root selected, and pull-folder will
	// later see folder_id=0 and download workspace-root items directly.
	var noProjectSelected bool
	const noProjectLabel = "No Project"
	if snapStageID == 0 {
		if clientElicitationSupported() {
			var selectedProjectID int64

			// Step 4: fetch project list and elicit selection.
			projects, projErr := fetchProjectList(ctx, snapWorkspaceID)
			if projErr != nil {
				logger.Warn("login: fetchProjectList failed: %v", projErr)
			}

			if projErr == nil {
				enumVals := make([]string, 0, len(projects)+1)
				projIDByLabel := map[string]int64{}
				for _, p := range projects {
					label := fmt.Sprintf("%d — %s", p.projectID, p.title)
					if p.shortName != "" && p.shortName != p.title {
						label += " (" + p.shortName + ")"
					}
					enumVals = append(enumVals, label)
					projIDByLabel[label] = p.projectID
				}
				// Always offer "No Project" as the last option so the user can
				// work at workspace root when a Corezoid project isn't required.
				enumVals = append(enumVals, noProjectLabel)
				content, action, err := elicitValues(
					"Select your Corezoid project:",
					map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"project": map[string]interface{}{
								"type":        "string",
								"title":       "Project",
								"description": "Select the project to work with, or pick \"No Project\" to work at workspace root",
								"enum":        enumVals,
							},
						},
						"required": []string{"project"},
					},
				)
				if err == nil && action == "accept" {
					if selected, _ := content["project"].(string); selected != "" {
						if selected == noProjectLabel {
							noProjectSelected = true
						} else {
							selectedProjectID = projIDByLabel[selected]
						}
					}
				}
			}

			// Step 5: fetch stage list for selected project and elicit selection.
			if !noProjectSelected && selectedProjectID != 0 {
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
							}
						}
					}
				}
			}

			// Fallback: if stage still not set, ask for stage ID directly.
			// Skipped when the user chose "No Project" — they have opted out of
			// a stage entirely and pull-folder will operate at workspace root.
			if !noProjectSelected && snapStageID == 0 {
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
				sb.WriteString(fmt.Sprintf("Please ask the user for their stage ID (root folder ID), then call login(workspace_id=%s, stage_id=<stage_id>).", snapWorkspaceID))
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

	// Auto pull-folder whenever a stage is (re)selected. Persist StageID
	// first so subsequent tool calls in this session — and any concurrent
	// goroutines — see the same stage the pull is about to materialise.
	// Covers both first-time setup (snapStageIDBefore == 0) and stage
	// switching (snapStageIDBefore != 0 && snapStageID differs).
	// When the user chose "No Project", project_id/stage_id are pinned to 0
	// and a workspace-root pull runs instead.
	var autoPullErr error
	if noProjectSelected {
		updateAndSync(func(f *Folder) {
			f.ProjectID = 0
			f.StageID = 0
			f.GitStagePath = ""
		}, "login")
		pv := NewValidator(ctx, 0)
		if pullErr := downloadWorkspaceRootRecursively(pv, "."); pullErr != nil {
			logger.Warn("login: auto workspace-root pull failed: %v", pullErr)
			autoPullErr = pullErr
		}
	} else if snapStageID != 0 && snapStageID != snapStageIDBefore {
		selectedStageID := snapStageID
		updateAndSync(func(f *Folder) { f.StageID = selectedStageID }, "login")
		pv := NewValidator(ctx, 0)
		if pullErr := downloadStageRecursively(pv, snapStageID, "."); pullErr != nil {
			logger.Warn("login: auto pull-folder failed: %v", pullErr)
			autoPullErr = pullErr
		}
	}

	// Re-snapshot so the response reflects the persisted stage_id.
	_, _, _, _, finalStageID := authSnapshot()

	cfgPath, _ := configFilePath()
	if cfgPath == "" {
		cfgPath = "~/.corezoid/config.json"
	}

	// When stage selection did not land (elicitation cancelled, unsupported, or
	// user typed a blank stage_id), return a clearly-actionable message instead
	// of the generic "Setup complete". The init skill and the LLM both key off
	// this string to decide whether to proceed to pull-folder or drive stage
	// selection themselves. "No Project" is not a failure — skip this branch.
	if !noProjectSelected && finalStageID == 0 {
		msg := "Setup incomplete: stage not selected. "
		msg += fmt.Sprintf("Call list-stages(project_id=<id>, company_id=%s) to see available stages, ", snapWorkspaceID)
		msg += fmt.Sprintf("then call login(workspace_id=%s, stage_id=<stage_id>).", snapWorkspaceID)
		if !tokenExpiry.IsZero() {
			msg += fmt.Sprintf(" (Token saved to %s, expires %s.)", cfgPath, tokenExpiry.Format("2006-01-02 15:04"))
		}
		return msg, false
	}

	var msg string
	if noProjectSelected {
		msg = fmt.Sprintf("Setup complete! Configuration saved to %s. \"No Project\" selected — working at workspace root", cfgPath)
	} else {
		msg = fmt.Sprintf("Setup complete! Configuration saved to %s. Stage %d selected", cfgPath, finalStageID)
	}
	if autoPullErr != nil {
		// Pull failed but stage_id is set (e.g. previous marker still on disk).
		// Callers should retry pull-folder — the marker is authoritative.
		msg += fmt.Sprintf(" (auto-pull failed: %v; call pull-folder to retry)", autoPullErr)
	}
	msg += "."
	if !tokenExpiry.IsZero() {
		msg += fmt.Sprintf(" Token expires: %s.", tokenExpiry.Format("2006-01-02 15:04"))
	}

	// One-time opt-in: ask for email to include in telemetry.
	if clientElicitationSupported() {
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

// handleLogout removes the current Folder from ~/.corezoid/config.json and
// resets the in-memory auth-state globals. The removal is atomic + serialised
// across processes via RemoveCurrent's flock.
func handleLogout(_ context.Context, _ map[string]interface{}) (string, bool) {
	_, _, _, snapAccountURL, _ := authSnapshot()

	if err := RemoveCurrent(); err != nil {
		return fmt.Sprintf("Failed to remove folder entry from config: %v", err), true
	}
	syncGlobalsFromCurrent()

	cfgPath, _ := configFilePath()
	if cfgPath == "" {
		cfgPath = "~/.corezoid/config.json"
	}

	browserHost := strings.TrimRight(snapAccountURL, "/")
	if browserHost == "" {
		browserHost = "https://account.corezoid.com"
	}

	return fmt.Sprintf(
		"Logged out. Folder entry removed from %s.\n\n"+
			"Important: your browser may still have an active SSO session at %s. "+
			"If a subsequent login produces an already-expired token (\"exp\" in the past), "+
			"you must also log out of that site in your browser before calling login again.",
		cfgPath, browserHost), false
}
