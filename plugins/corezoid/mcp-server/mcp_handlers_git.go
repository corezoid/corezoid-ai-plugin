package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// handleGitPullContext performs a sparse checkout / git pull of CLAUDE.md + _ext/
// for the current stage. Uses credentials from env (COREZOID_LOGIN / COREZOID_SECRET).
func handleGitPullContext(ctx context.Context, args map[string]interface{}) (string, bool) {
	cfg := loadGitSyncConfig()
	if !cfg.IsConfigured() {
		return "Git context skipped: COREZOID_LOGIN or COREZOID_SECRET not set.", false
	}

	if err := GitPull(cfg); err != nil {
		return fmt.Sprintf("⚠ Git context unavailable, continuing without it.\nDetails: %v", err), false
	}
	return "Git context pulled successfully. CLAUDE.md and _ext/ are up to date.", false
}

// handleGitPushContext commits local _ext/ changes and pushes to the git mirror.
func handleGitPushContext(ctx context.Context, args map[string]interface{}) (string, bool) {
	cfg := loadGitSyncConfig()
	if !cfg.IsConfigured() {
		return "Git push skipped: COREZOID_LOGIN or COREZOID_SECRET not set.", false
	}

	message, _ := args["message"].(string)

	if err := GitPush(cfg, message); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "push not yet available") {
			return "⚠ Git push not yet available (server-side push access pending). Changes committed locally in .git-context/", false
		}
		return fmt.Sprintf("⚠ Git push failed. Changes saved locally.\nDetails: %v", err), false
	}
	return "Git context pushed successfully.", false
}

// handleReadContextFile reads a file from _ext/ for the current stage.
func handleReadContextFile(ctx context.Context, args map[string]interface{}) (string, bool) {
	path, err := strArg(args, "path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	cfg := loadGitSyncConfig()
	content, found := ReadContextFile(cfg, path)
	if !found {
		return fmt.Sprintf(`{"content":"","found":false,"path":%q}`, path), false
	}
	// json.Marshal handles all special chars: \, ", \n, \r, \t, control chars.
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf("Error encoding file content: %v", err), true
	}
	return fmt.Sprintf(`{"content":%s,"found":true,"path":%q}`, contentJSON, path), false
}

// handleUpdateContextFile writes or appends content to a file in _ext/.
func handleUpdateContextFile(ctx context.Context, args map[string]interface{}) (string, bool) {
	path, err := strArg(args, "path")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	content, err := strArg(args, "content")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	mode := "append"
	if m, ok := args["mode"].(string); ok && (m == "append" || m == "replace") {
		mode = m
	}

	cfg := loadGitSyncConfig()
	if err := UpdateContextFile(cfg, path, content, mode); err != nil {
		return fmt.Sprintf("Error updating context file: %v", err), true
	}
	return fmt.Sprintf(`{"status":"ok","path":%q,"mode":%q}`, path, mode), false
}
