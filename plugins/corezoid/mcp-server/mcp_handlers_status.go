package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleStatus reports the server's identity, uptime and configuration so a
// user (or the AI) can see at a glance what process they are talking to and
// whether auth is in order. It runs with NO auth — its whole point is to
// diagnose auth problems — and makes no network calls unless probe=true.
func handleStatus(ctx context.Context, args map[string]interface{}) (string, bool) {
	apiURLv, tok, ws, acc, stg := authSnapshot()

	version := Version
	if version == "dev" {
		version = "dev (unstamped local build)"
	}
	uptime := "-"
	started := "-"
	if !serverStartedAt.IsZero() {
		started = serverStartedAt.UTC().Format("2006-01-02 15:04:05Z")
		uptime = time.Since(serverStartedAt).Round(time.Second).String()
	}
	cwd, _ := os.Getwd()

	// Paths are computed, not created: status must not have side effects, so
	// it does not go through credentialsFilePath (which MkdirAlls ~/.corezoid).
	credPath := "(unknown — cannot determine home directory)"
	logPath := os.Getenv("COREZOID_DEBUG_LOG")
	if home, err := os.UserHomeDir(); err == nil {
		credPath = filepath.Join(home, ".corezoid", "credentials")
		if logPath == "" {
			logPath = filepath.Join(home, ".corezoid", "mcp.log")
		}
	} else if logPath == "" {
		logPath = "(unknown — cannot determine home directory)"
	}

	tokenLine := "absent — run login"
	if tok != "" {
		expiry := "no expiry recorded"
		if creds, err := loadCredentials(); err == nil && creds != nil &&
			creds.AccessToken == tok && !creds.ExpiresAt.IsZero() {
			if isCredentialsExpired(creds) {
				expiry = fmt.Sprintf("EXPIRED %s — re-run login (force=true)", creds.ExpiresAt.UTC().Format("2006-01-02 15:04"))
			} else {
				expiry = "valid until " + creds.ExpiresAt.UTC().Format("2006-01-02 15:04")
			}
		}
		tokenLine = fmt.Sprintf("present (%s)", expiry)
	}

	accLine := acc
	if acc == "" {
		accLine = "(not set — login will ask)"
	} else if strings.Contains(acc, "://admin.") {
		accLine = acc + "  ⚠ this looks like the admin UI host — OAuth lives on the ACCOUNT host (cloud: https://account.corezoid.com); fix ACCOUNT_URL in .env"
	}
	apiLine := apiURLv
	if apiURLv == "" {
		apiLine = "(not set — every API call will fail; run login or set COREZOID_API_URL)"
	}
	stageLine := "-"
	if stg != 0 {
		stageLine = fmt.Sprintf("%d", stg)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("corezoid-mcp %s\n", version))
	sb.WriteString(fmt.Sprintf("  pid:        %d   started: %s   uptime: %s\n", os.Getpid(), started, uptime))
	sb.WriteString(fmt.Sprintf("  workdir:    %s\n", cwd))
	sb.WriteString(fmt.Sprintf("  ACCOUNT_URL: %s\n", accLine))
	sb.WriteString(fmt.Sprintf("  API URL:    %s\n", apiLine))
	sb.WriteString(fmt.Sprintf("  workspace:  %s   stage: %s\n", orDash(ws), stageLine))
	sb.WriteString(fmt.Sprintf("  token:      %s\n", tokenLine))
	sb.WriteString(fmt.Sprintf("  credentials: %s\n", credPath))
	sb.WriteString(fmt.Sprintf("  log:        %s\n", logPath))

	probe := false
	if b, ok := args["probe"].(bool); ok {
		probe = b
	} else if ps, ok := args["probe"].(string); ok {
		probe = strings.EqualFold(ps, "true") || ps == "1"
	}
	probeFailed := false
	if probe {
		if tok == "" || apiURLv == "" {
			sb.WriteString("\nprobe: SKIPPED — no token or API URL to probe with.\n")
		} else if _, err := fetchWorkspaceList(ctx); err != nil {
			probeFailed = true
			sb.WriteString(fmt.Sprintf("\nprobe: FAILED — %v%s\n", err, authErrorHint(err)))
		} else {
			sb.WriteString("\nprobe: OK — the token works against the API.\n")
		}
	}
	sb.WriteString("\nIf OTHER sessions report \"No such tool available\" for corezoid tools, this server was restarted after they connected — those sessions must be RESTARTED (a plain reconnect may not be enough).")
	return sb.String(), probeFailed
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
