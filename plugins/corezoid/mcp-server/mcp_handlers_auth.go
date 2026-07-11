package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	// Step 2: OAuth2 PKCE browser flow. Skipped only when an existing token
	// actually WORKS: a non-empty token is probed with a cheap authenticated
	// call first. Trusting any non-empty token made re-login impossible — a
	// stale/revoked token in .env or credentials produced "Setup complete"
	// while every subsequent call failed with "cookie or headers are not
	// valid", and the only way out was hand-deleting files.
	force := false
	if b, ok := args["force"].(bool); ok {
		force = b
	} else if fs, ok := args["force"].(string); ok {
		// CLI mode passes args as strings; a silently ignored force defeats
		// its whole purpose (recovering from a bad token).
		force = strings.EqualFold(fs, "true") || fs == "1"
	}
	staleNote := ""
	if snapToken != "" && force {
		staleNote = "\nRe-authentication forced (force=true) — previous token discarded."
		snapToken = ""
		withAuthLock(func() { apiToken = "" })
	}
	if snapToken != "" {
		rejected, perr := probeExistingToken(ctx, snapAccountURL, snapToken)
		switch {
		case perr == nil:
			// Token verified — proceed without OAuth.
		case rejected:
			logger.Warn("login: existing token rejected by the server: %v — starting OAuth", perr)
			staleNote = "\nThe existing token was rejected by the server (stale or revoked) — a fresh OAuth login was performed."
			snapToken = ""
			withAuthLock(func() { apiToken = "" })
		default:
			// Transport/config trouble — keep the session; destroying a working
			// token (and popping a browser) over a network blip is worse.
			logger.Warn("login: could not verify the existing token (%v) — keeping it", perr)
			staleNote = fmt.Sprintf("\n⚠ The existing token could not be verified (%v) — keeping it. If calls fail, re-run login with force=true.", perr)
		}
	}
	var tokenExpiry time.Time
	if snapToken == "" {
		if aerr := assertAccountService(snapAccountURL); aerr != nil {
			return fmt.Sprintf("Authentication not started: %v", aerr), true
		}
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
		// A project .env that carries its own ACCESS_TOKEN overrides the
		// user-level credentials on every load — left in place, it would
		// shadow the fresh token on the next restart and resurrect the stale
		// session this login just replaced. REMOVE it rather than update it:
		// credentials were deliberately moved out of the project tree so a
		// token can never be committed to git, and writing the new secret
		// back into a repo-local file would undo that decision (it would also
		// clobber ${VAR} placeholders users manage themselves).
		tokenEnvPath := dotEnvPathInUse()
		if envHasKey(tokenEnvPath, "ACCESS_TOKEN") {
			if err := removeEnvKey(tokenEnvPath, "ACCESS_TOKEN"); err != nil {
				logger.Warn("login: could not remove stale ACCESS_TOKEN from %s: %v", tokenEnvPath, err)
			} else {
				_ = removeEnvKey(tokenEnvPath, "ACCESS_TOKEN_EXPIRES_AT")
				staleNote += fmt.Sprintf("\nStale ACCESS_TOKEN removed from %s — ~/.corezoid/credentials is now the single token source.", tokenEnvPath)
			}
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
		if clientElicitationSupported() {
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

	msg := fmt.Sprintf("Setup complete! Configuration saved to %s.%s", envPath, staleNote)
	if !tokenExpiry.IsZero() {
		msg += fmt.Sprintf(" Token expires: %s.", tokenExpiry.Format("2006-01-02 15:04"))
	}

	// One-time opt-in: ask for email to include in telemetry.
	// Only shown once per installation; skipping is always valid.
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

// handleLogout deletes the saved credentials from ~/.corezoid/credentials and
// clears the cached access token. The token write is under the auth lock so a
// concurrent in-flight request can't briefly use the cleared token-before-deletion state.
func handleLogout(_ context.Context, _ map[string]interface{}) (string, bool) {
	credPath, err := credentialsFilePath()
	if err != nil {
		credPath = "~/.corezoid/credentials"
	}
	// Kill the live session BEFORE any file operation: whatever fails below,
	// the user must already be logged out in this process — reporting a
	// failed logout while every tool call keeps working would be a lie.
	withAuthLock(func() {
		apiToken = ""
		accountURL = ""
		workspaceID = ""
		stageID = 0
		apiURL = ""
	})
	if err := deleteCredentials(); err != nil {
		return fmt.Sprintf("Logged out of this session, but failed to remove the credentials file: %v — remove %s manually or the next start will silently reuse it.", err, credPath), true
	}
	// The .env that actually feeds tokens into this process is the one
	// findAndLoadDotEnv resolves (it walks UP from cwd) — not blindly
	// cwd/.env. Leaving a token there means the very next login — or a
	// server restart — silently resurrects the session the user just ended.
	envPath := dotEnvPathInUse()
	envNote := ""
	if envHasKey(envPath, "ACCESS_TOKEN") {
		if err := removeEnvKey(envPath, "ACCESS_TOKEN"); err != nil {
			return fmt.Sprintf("Logged out of this session, but failed to remove ACCESS_TOKEN from %s: %v — remove it manually or the next login will silently reuse it.", envPath, err), true
		}
		_ = removeEnvKey(envPath, "ACCESS_TOKEN_EXPIRES_AT")
		envNote = fmt.Sprintf(" ACCESS_TOKEN also removed from %s (it would have silently re-authenticated the next login).", envPath)
	}
	return fmt.Sprintf("Logged out. ACCESS_TOKEN removed from %s.%s", credPath, envNote), false
}

// probeExistingToken answers "does this token still work?" with one cheap
// AUTHENTICATED call before login trusts it. Without the probe, any non-empty
// token — including one revoked server-side — made login report success while
// every later tool call failed with the opaque "cookie or headers are not
// valid", and re-login was impossible short of hand-deleting files.
//
// The probe is the Corezoid workspace list. The account clients endpoint is
// NOT usable as a probe: it answers 200 even without a token (verified live),
// so it is only used to derive COREZOID_API_URL when missing — a derivation
// failure says nothing about the token and is reported as transport trouble.
//
// rejected=true means the server explicitly refused the token; err != nil
// with rejected=false is transport/config trouble — the caller must KEEP the
// token in that case: discarding a working session over a network blip (and
// popping a surprise OAuth browser window) is worse than the disease.
func probeExistingToken(ctx context.Context, accountURL, token string) (rejected bool, err error) {
	authStateMu.RLock()
	haveAPIURL := apiURL != ""
	authStateMu.RUnlock()
	if !haveAPIURL {
		corezoidURL, derr := fetchCorezoidAPIURL(accountURL, token)
		if derr != nil {
			// An HTML answer means the host is a UI, not the account service —
			// the classic misconfiguration is ACCOUNT_URL pointing at
			// admin.<domain>. Left undiagnosed, the subsequent OAuth flow opens
			// the admin UI instead of the consent page and login dead-ends.
			if strings.Contains(derr.Error(), "invalid character '<'") {
				suggestion := "https://account.corezoid.com"
				if u := strings.Replace(accountURL, "admin.", "account.", 1); u != accountURL {
					suggestion = u
				}
				return false, fmt.Errorf("ACCOUNT_URL %q does not behave like the account service (it returned HTML, not JSON) — it is probably the admin UI host. Fix ACCOUNT_URL in your .env (likely %s) and retry login", accountURL, suggestion)
			}
			return false, fmt.Errorf("could not derive COREZOID_API_URL: %w", derr)
		}
		withAuthLock(func() { apiURL = corezoidURL })
		os.Setenv("COREZOID_API_URL", corezoidURL)
		if uerr := updateEnvFile(envFilePath(), "COREZOID_API_URL", corezoidURL); uerr != nil {
			logger.Warn("login: could not save COREZOID_API_URL: %v", uerr)
		}
	}
	// Probe with the token we were HANDED, not whatever the global happens to
	// hold — callers keep them in sync today, but that is a trap to rely on.
	v := NewValidator(ctx, 0)
	v.Token = token
	_, perr := v.req("login_probe", []map[string]any{{"type": "list", "obj": "company"}})
	if perr == nil {
		return false, nil
	}
	if isAuthRejection(perr) {
		return true, perr
	}
	return false, perr
}

// isAuthRejection classifies an API error as an explicit token refusal.
// Conservative on purpose: anything unrecognized is treated as transport
// trouble so a valid session is never destroyed by a blip or a 5xx.
func isAuthRejection(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"cookie or headers are not valid",
		"unauthorized",
		"access denied",
		"token is not valid",
		"invalid token",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// dotEnvPathInUse returns the .env file findAndLoadDotEnv would load: the
// walk-up from cwd stops at the first .env or the project root. Token cleanup
// must target THIS file — blindly using cwd/.env misses a shadowing token in
// a parent directory and reports a clean logout that isn't.
func dotEnvPathInUse() string {
	cwd, err := os.Getwd()
	if err != nil {
		return envFilePath()
	}
	dir := cwd
	for {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if isProjectRoot(dir) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return envFilePath()
}

// envHasKey reports whether the .env file at path contains the key.
func envHasKey(path, key string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}


// assertAccountService refuses to start an OAuth flow against a host that is
// not the account service. The classic misconfiguration — ACCOUNT_URL set to
// admin.<domain> — makes the browser open the admin UI instead of the consent
// page, a dead end the user cannot diagnose from the browser alone. The
// unauthenticated clients endpoint answers JSON on a real account host and
// HTML on the admin UI, which is exactly the tell we need.
func assertAccountService(accountURL string) error {
	if _, err := fetchCorezoidAPIURL(accountURL, ""); err != nil && strings.Contains(err.Error(), "invalid character '<'") {
		suggestion := "https://account.corezoid.com"
		if u := strings.Replace(accountURL, "admin.", "account.", 1); u != accountURL {
			suggestion = u
		}
		return fmt.Errorf("ACCOUNT_URL %q is not the account service (it returned HTML — probably the admin UI host), so the OAuth consent page cannot open there. Fix ACCOUNT_URL in your .env (likely %s) and retry login", accountURL, suggestion)
	}
	return nil
}
