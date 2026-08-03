package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveAndSaveStagePath finds the stage directory inside .git-context/ that
// matches the current Folder's stage_id, saves the relative path to the
// global gitStagePath and persists it to the current Folder in
// ~/.corezoid/config.json. Returns the path (empty string if not found or
// stage ID not set).
func resolveAndSaveStagePath() string {
	sid := func() int {
		authStateMu.RLock()
		defer authStateMu.RUnlock()
		return stageID
	}()

	if sid == 0 {
		logger.Warn("git-pull-context: stage_id not set — cannot resolve stage path")
		return ""
	}

	stagePath, err := findGitStagePath(gitContextDir(), sid)
	if err != nil {
		logger.Warn("git-pull-context: stage path search failed: %v", err)
		return ""
	}
	if stagePath == "" {
		logger.Warn("git-pull-context: stage %d not found in git context (not yet mirrored?)", sid)
		return ""
	}

	if err := UpdateCurrent(func(f *Folder) { f.GitStagePath = stagePath }); err != nil {
		logger.Warn("git-pull-context: could not persist git_stage_path to config: %v", err)
	}
	syncGlobalsFromCurrent()
	logger.Info("git-pull-context: stage path resolved → %s", stagePath)

	copyStageCLAUDEMD(gitContextDir(), stagePath)
	return stagePath
}

// copyStageCLAUDEMD copies CLAUDE.md from the stage directory inside
// .git-context/ to the project root (COREZOID_WORK_DIR / cwd).
// The mirror content is merged into the root file via injectMirrorBlock
// (the mirror-generated file carries its own corezoid-mirror markers), so
// any content a developer added to the root CLAUDE.md outside those markers
// survives instead of being clobbered by a blind overwrite.
func copyStageCLAUDEMD(contextDir, stagePath string) {
	src := filepath.Join(contextDir, filepath.FromSlash(stagePath), "CLAUDE.md")
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("git-pull-context: no CLAUDE.md in stage yet (not mirrored or stage empty)")
		} else {
			logger.Warn("git-pull-context: could not read stage CLAUDE.md: %v", err)
		}
		return
	}

	dst := filepath.Join(gitContextDir(), "..", "CLAUDE.md") // = cwd/CLAUDE.md
	dst = filepath.Clean(dst)

	var existing string
	if existingData, readErr := os.ReadFile(dst); readErr == nil {
		existing = string(existingData)
	}
	merged := injectMirrorBlock(existing, string(data))

	if err := os.WriteFile(dst, []byte(merged), 0644); err != nil {
		logger.Warn("git-pull-context: could not write CLAUDE.md to project root: %v", err)
		return
	}
	logger.Info("git-pull-context: CLAUDE.md copied to project root (%d bytes)", len(merged))
}

// resolveGitURL returns the current Folder's git_url, deriving it from
// account_url if not already set. Persists the derived URL to
// ~/.corezoid/config.json so it survives across server restarts. Returns
// ("", false) on any failure — caller silently skips.
func resolveGitURL() (string, bool) {
	gURL, _, _, _, acctURL := gitConfigSnapshot()
	if gURL != "" {
		return gURL, true
	}

	derived, err := deriveGitURL(acctURL)
	if err != nil {
		logger.Warn("git-pull-context: cannot derive git_url from account_url %q: %v", acctURL, err)
		return "", false
	}

	if err := UpdateCurrent(func(f *Folder) { f.GitURL = derived }); err != nil {
		logger.Warn("git-pull-context: could not persist git_url to config: %v", err)
	}
	syncGlobalsFromCurrent()
	logger.Info("git-pull-context: derived git_url=%s", derived)
	return derived, true
}

// resolveProjectID returns the numeric project ID that owns the current
// stage, needed to build the project-level git mirror clone URL
// (c-<companyID>-p-<projectID>). Delegates to resolveAndCacheProjectID (see
// main.go) — the same in-memory cache used by push-process's
// auto-snapshot and pull-folder's pre-warm, so the git-context and process
// paths never resolve or persist project_id independently.
// Returns 0 if stage_id is unset on the current Folder or resolution fails —
// callers treat 0 as "cannot build the project-level mirror repo URL yet"
// and fall back to local mode.
func resolveProjectID(ctx context.Context) int {
	pid, _ := resolveAndCacheProjectID(NewValidator(ctx, 0))
	return pid
}

// syncGitContext derives git_url (best-effort), pulls/clones/reconnects
// .git-context/ via gitPullContext, and resolves+saves the current stage path.
// It's the single sequence shared by ensureGitContext (pull-folder's best-effort
// caller) and handleGitPullContext (the explicit MCP tool) — a fix to this
// sequence only has to be made once instead of kept in lockstep across both.
func syncGitContext(ctx context.Context) (msg, stagePath string, err error) {
	// Best-effort URL derivation — gitPullContext handles all credential combinations:
	//   GIT_URL + API_LOGIN + API_SECRET all present → try online, fallback to local on error
	//   any of the three missing              → go straight to local mode
	resolveGitURL()

	msg, err = gitPullContext(ctx)
	if err != nil {
		return "", "", err
	}

	stagePath = resolveAndSaveStagePath()
	return msg, stagePath, nil
}

// ensureGitContext tries to sync .git-context/ before pull-folder downloads
// processes. It falls back to local mode if Gitea is unreachable.
// Never returns an error — on total failure it logs a warning and returns false.
func ensureGitContext(ctx context.Context) bool {
	_, _, _, gCompany, _ := gitConfigSnapshot()

	if gCompany == "" {
		logger.Warn("git-pull-context: workspace_id not set on current Folder — skipping git context entirely")
		return false
	}

	msg, _, err := syncGitContext(ctx)
	if err != nil {
		logger.Warn("git-pull-context: %v — continuing without git context", err)
		return false
	}
	logger.Info("git-pull-context: %s", msg)
	return true
}

// regenerateLocalCLAUDEMDIfNeeded regenerates CLAUDE.md from local .conv.json
// files when running in offline / local mode. Called after pull-folder so the
// process list is fresh. No-op in online mode (mirror CLAUDE.md is authoritative).
func regenerateLocalCLAUDEMDIfNeeded(ctx context.Context) {
	contextDir := gitContextDir()
	if _, err := os.Stat(filepath.Join(contextDir, ".git")); err != nil {
		return // no .git-context at all — git was completely skipped
	}
	if !isLocalOnlyContext(ctx, contextDir) {
		return // online mode — copyStageCLAUDEMD already handled it
	}
	sp := gitStagePathSnapshot()
	if sp == "" {
		return
	}
	if err := generateLocalCLAUDEMD(ctx, sp); err != nil {
		logger.Warn("git-pull-context: local CLAUDE.md generation failed: %v", err)
	}
}

// handleGitPullContext is the MCP tool handler for "git-pull-context".
// Surfaces errors to the caller; derives git_url automatically.
func handleGitPullContext(ctx context.Context, args map[string]interface{}) (string, bool) {
	_, _, _, gCompany, _ := gitConfigSnapshot()

	if gCompany == "" {
		return "Error: workspace_id not set on the current Folder. Run the 'login' tool first to select a workspace.", true
	}

	msg, stagePath, err := syncGitContext(ctx)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if stagePath != "" {
		msg += fmt.Sprintf("\nStage path: %s", stagePath)
	}
	return msg, false
}

// handleGitPushContext is the MCP tool handler for "git-push-context".
func handleGitPushContext(ctx context.Context, args map[string]interface{}) (string, bool) {
	commitMsg, _ := args["commit_message"].(string)

	msg, err := gitPushContext(ctx, commitMsg)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	// In local mode: after committing _ext/docs/ changes, rebuild CLAUDE.md so
	// Developer Notes (from _ext/docs/*.md) are visible without a full pull-folder.
	regenerateLocalCLAUDEMDIfNeeded(ctx)

	return msg, false
}

// handleReadContextFile reads a file from .git-context/ and returns its content.
func handleReadContextFile(ctx context.Context, args map[string]interface{}) (string, bool) {
	relPath, err := strArg(args, "path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	targetDir := gitContextDir()
	fullPath := filepath.Join(targetDir, filepath.FromSlash(relPath))

	// Prevent path traversal outside .git-context/.
	if !strings.HasPrefix(fullPath, targetDir+string(os.PathSeparator)) && fullPath != targetDir {
		return "Error: path escapes .git-context/ directory", true
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", relPath, err), true
	}
	return string(data), false
}

// handleUpdateContextFile writes or appends content to a file inside _ext/.
func handleUpdateContextFile(ctx context.Context, args map[string]interface{}) (string, bool) {
	relPath, err := strArg(args, "path")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	content, err := strArg(args, "content")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	mode, _ := args["mode"].(string)
	if mode == "" {
		mode = "replace"
	}

	// Validate that the path resolves to an _ext/ directory.
	// Accepted forms:
	//   _ext/docs/context.md                                         (workspace-root _ext/)
	//   projects/123_Foo/stages/456_Bar/_ext/docs/context.md        (stage _ext/)
	clean := filepath.ToSlash(filepath.Clean(relPath))
	isExtPath := strings.HasPrefix(clean, "_ext/") || strings.Contains(clean, "/_ext/")
	if !isExtPath {
		return "Error: update-context-file can only write to _ext/ paths (e.g. _ext/docs/context.md or projects/.../stages/.../_ext/docs/context.md)", true
	}

	targetDir := gitContextDir()
	fullPath := filepath.Join(targetDir, filepath.FromSlash(relPath))

	// C1: containment guard — same as handleReadContextFile.
	// filepath.Join already cleans ".." but we must still verify the result stays
	// inside .git-context/ so a path like "../_ext/evil" cannot escape.
	if !strings.HasPrefix(fullPath, targetDir+string(os.PathSeparator)) {
		return "Error: path escapes .git-context/ directory", true
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Sprintf("Error creating directory for %s: %v", relPath, err), true
	}

	var flag int
	switch mode {
	case "append":
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	default:
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}

	f, err := os.OpenFile(fullPath, flag, 0644)
	if err != nil {
		return fmt.Sprintf("Error opening %s: %v", relPath, err), true
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Sprintf("Error writing %s: %v", relPath, err), true
	}

	return fmt.Sprintf("Written to %s (mode=%s)", relPath, mode), false
}
