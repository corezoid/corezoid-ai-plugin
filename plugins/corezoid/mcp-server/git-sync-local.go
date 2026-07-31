package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	mirrorBeginMarker = "<!-- BEGIN corezoid-mirror (auto-generated, do not edit inside) -->"
	mirrorEndMarker   = "<!-- END corezoid-mirror -->"
)

// isLocalOnlyContext returns true when .git-context/.git exists but has no
// remote configured — i.e. the plugin is running in offline / local mode.
func isLocalOnlyContext(ctx context.Context, contextDir string) bool {
	out, err := runGit(ctx, contextDir, "remote", "-v")
	return err == nil && strings.TrimSpace(out) == ""
}

// detectStageName scans workDir for a directory whose name starts with
// "<stageID>_" and ends with ".stage", ".folder", or ".project" and returns
// the part between them (e.g. "1511196_develop.stage" → "develop").
// Falls back to the numeric stage ID as a string if nothing is found.
func detectStageName(workDir string, stageID int) string {
	prefix := fmt.Sprintf("%d_", stageID)
	entries, _ := os.ReadDir(workDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		for _, sfx := range []string{".stage", ".folder", ".project"} {
			if strings.HasSuffix(name, sfx) {
				// "<stageID>_<name>.stage" → "<name>"
				return name[len(prefix) : len(name)-len(sfx)]
			}
		}
	}
	return fmt.Sprintf("%d", stageID)
}

// initLocalGitContext creates (or ensures) the .git-context/ structure for
// local / offline mode:
//   - stages/<stageID>_<name>/_ext/docs/
//   - git init (if not already present)
//   - an initial empty commit so git-stash works later
//
// Returns the relative stage path (e.g. "stages/1511196_develop").
func initLocalGitContext(ctx context.Context) (string, error) {
	sid := func() int {
		authStateMu.RLock()
		defer authStateMu.RUnlock()
		return stageID
	}()
	if sid == 0 {
		return "", fmt.Errorf("COREZOID_STAGE_ID not set")
	}

	workDir, _ := os.Getwd()
	stageName := detectStageName(workDir, sid)
	stagePath := fmt.Sprintf("stages/%d_%s", sid, stageName)
	contextDir := gitContextDir()

	// Create directory structure.
	extDocsDir := filepath.Join(contextDir, filepath.FromSlash(stagePath), "_ext", "docs")
	if err := os.MkdirAll(extDocsDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create local context dirs: %w", err)
	}

	gitDir := filepath.Join(contextDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		// Fresh init.
		if out, err := runGit(ctx, contextDir, "init"); err != nil {
			return "", fmt.Errorf("git init failed: %s", out)
		}
		// Configure a local identity so commits work on unconfigured machines.
		runGit(ctx, contextDir, "config", "user.email", "corezoid-plugin@local") //nolint:errcheck
		runGit(ctx, contextDir, "config", "user.name", "Corezoid Plugin")        //nolint:errcheck
		// Empty initial commit so there's always a HEAD to diff/branch/reset against.
		if out, err := runGit(ctx, contextDir, "commit", "--allow-empty", "-m", "init: local git context"); err != nil {
			return "", fmt.Errorf("initial commit failed: %s", out)
		}
		if err := ensureGitignoreEntry(workDir, ".git-context/"); err != nil {
			logger.Warn("git-pull-context: could not update project .gitignore: %v", err)
		}
	}

	logger.Info("git-pull-context: local context ready at %s", stagePath)
	return stagePath, nil
}

// processEntry holds minimal metadata extracted from a .conv.json file.
type processEntry struct {
	Title string
	ObjID int
	Path  string // relative to workDir, forward slashes
}

// scanProcessFiles walks workDir and collects all .conv.json process entries.
func scanProcessFiles(workDir string) []processEntry {
	var entries []processEntry
	filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error { //nolint:errcheck
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".conv.json") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var meta struct {
			Title string `json:"title"`
			ObjID int    `json:"obj_id"`
		}
		if jsonErr := json.Unmarshal(data, &meta); jsonErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(workDir, path)
		entries = append(entries, processEntry{
			Title: meta.Title,
			ObjID: meta.ObjID,
			Path:  filepath.ToSlash(rel),
		})
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

// docFileOrder defines the canonical order and section titles for _ext/docs/ files,
// matching the format the git mirror bot uses for Developer Notes.
var docFileOrder = []struct{ file, title string }{
	{"context.md", "Context"},
	{"invariants.md", "Invariants"},
	{"decisions.md", "Decisions"},
	{"dependencies.md", "Dependencies"},
	{"issues.md", "Issues"},
}

// buildMirrorBlock generates the corezoid-mirror CLAUDE.md block.
// It assembles Developer Notes from _ext/docs/*.md (matching mirror-bot format),
// followed by the process index. Both sections live inside the mirror markers so
// the real mirror bot will overwrite cleanly when Gitea becomes available.
func buildMirrorBlock(stageID int, stageName, extDocsDir string, processes []processEntry) string {
	var sb strings.Builder
	sb.WriteString(mirrorBeginMarker + "\n")

	// --- Developer Notes from _ext/docs/ ---
	var notesSB strings.Builder
	for _, df := range docFileOrder {
		data, err := os.ReadFile(filepath.Join(extDocsDir, df.file))
		if err != nil || strings.TrimSpace(string(data)) == "" {
			continue // skip missing or empty files — no empty section headers
		}
		notesSB.WriteString(fmt.Sprintf("### %s\n\n", df.title))
		notesSB.WriteString(strings.TrimRight(string(data), "\n") + "\n\n")
	}
	if notesSB.Len() > 0 {
		sb.WriteString("## Developer Notes\n\n")
		sb.WriteString(notesSB.String())
	}

	// --- Process index ---
	sb.WriteString(fmt.Sprintf("## Stage %d_%s process index\n\n", stageID, stageName))
	for _, p := range processes {
		title := p.Title
		if title == "" {
			title = fmt.Sprintf("Process %d", p.ObjID)
		}
		sb.WriteString(fmt.Sprintf("- %s  `%s`  (id %d)\n", title, p.Path, p.ObjID))
	}
	sb.WriteString(mirrorEndMarker + "\n")
	return sb.String()
}

// injectMirrorBlock replaces the corezoid-mirror block inside existing content,
// or prepends a new block if none is present.
// Content outside the markers (e.g. Developer Notes added by the skill) is preserved.
func injectMirrorBlock(existing, newBlock string) string {
	start := strings.Index(existing, mirrorBeginMarker)
	end := strings.Index(existing, mirrorEndMarker)
	if start != -1 && end != -1 && end > start {
		before := existing[:start]
		after := strings.TrimPrefix(existing[end+len(mirrorEndMarker):], "\n")
		return before + newBlock + "\n" + after
	}
	return newBlock + "\n" + existing
}

// generateLocalCLAUDEMD scans local .conv.json files and writes a CLAUDE.md
// into .git-context/<stagePath>/ and into the project root.
// Commits the result to the local repo.
func generateLocalCLAUDEMD(ctx context.Context, stagePath string) error {
	sid := func() int {
		authStateMu.RLock()
		defer authStateMu.RUnlock()
		return stageID
	}()

	workDir, _ := os.Getwd()
	contextDir := gitContextDir()
	stageName := detectStageName(workDir, sid)

	extDocsDir := filepath.Join(contextDir, filepath.FromSlash(stagePath), "_ext", "docs")
	processes := scanProcessFiles(workDir)
	newBlock := buildMirrorBlock(sid, stageName, extDocsDir, processes)

	stageClaudeFile := filepath.Join(contextDir, filepath.FromSlash(stagePath), "CLAUDE.md")
	rootClaude := filepath.Clean(filepath.Join(contextDir, "..", "CLAUDE.md"))

	// Merge against the actual project-root CLAUDE.md (not the stage-tracked
	// copy) — that's the file we're about to overwrite, so it's the one whose
	// outside-marker content must be preserved. injectMirrorBlock replaces
	// only the mirror block, keeping anything a developer added outside it.
	var existing string
	if data, err := os.ReadFile(rootClaude); err == nil {
		existing = string(data)
	}
	merged := injectMirrorBlock(existing, newBlock)

	if err := os.WriteFile(stageClaudeFile, []byte(merged), 0644); err != nil {
		return fmt.Errorf("cannot write stage CLAUDE.md: %w", err)
	}

	// Copy to project root so Claude Code picks it up as context.
	if err := os.WriteFile(rootClaude, []byte(merged), 0644); err != nil {
		logger.Warn("git-pull-context: could not write CLAUDE.md to project root: %v", err)
	}

	// Commit only if something changed.
	runGit(ctx, contextDir, "add", filepath.Join(filepath.FromSlash(stagePath), "CLAUDE.md")) //nolint:errcheck
	if _, noChanges := runGit(ctx, contextDir, "diff", "--cached", "--quiet"); noChanges != nil {
		runGit(ctx, contextDir, "commit", "-m", //nolint:errcheck
			fmt.Sprintf("docs: regenerate CLAUDE.md for stage %d", sid))
	}

	logger.Info("git-pull-context: local CLAUDE.md written (%d processes, docs=%s)",
		len(processes), extDocsDir)
	return nil
}

// getDefaultBranch queries the remote for its HEAD branch name (master/main/…).
func getDefaultBranch(ctx context.Context, dir string) string {
	out, err := runGit(ctx, dir, "remote", "show", "origin")
	if err != nil {
		return "master"
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "HEAD branch:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "master"
}

// reconnectToGitea transitions .git-context/ from local-only mode to a real
// Gitea remote:
//  1. Add remote + fetch (validates connectivity)
//  2. Back up the full offline history (branch) and snapshot _ext/ on disk —
//     `git stash` alone is not enough here because gitPushContext commits
//     _ext/ edits straight to HEAD in local mode, so by the time we reconnect
//     there is usually nothing left uncommitted for stash to catch.
//  3. Reset hard to remote state
//  4. Merge the on-disk _ext/ snapshot into the freshly-reset tree (not stash
//     pop, and not a blind overwrite either — see mergeExtSnapshot): files
//     that only existed locally are restored, files identical on both sides
//     are left alone, and files that genuinely conflict keep the remote
//     version in place while the local version is saved alongside instead of
//     being silently discarded.
//  5. Push merged _ext/ to remote
//
// The backup branch is kept (not deleted) so a user can recover anything the
// _ext/-only snapshot didn't cover (e.g. they'd committed outside _ext/ by hand).
// Returns a human-readable summary (reconnect result + any conflicts found) so
// callers can surface it all the way to the MCP tool response, not just the
// server log.
func reconnectToGitea(ctx context.Context, repoURL, gLogin, gSecret string) (string, error) {
	targetDir := gitContextDir()

	// Add remote (plain URL — no credentials embedded, see runGitAuthed).
	if out, err := runGit(ctx, targetDir, "remote", "add", "origin", repoURL); err != nil {
		return "", fmt.Errorf("remote add failed: %s", out)
	}

	// Fetch — proves Gitea is reachable.
	if out, err := runGitAuthed(ctx, targetDir, gLogin, gSecret, "fetch", "origin"); err != nil {
		runGit(ctx, targetDir, "remote", "remove", "origin") //nolint:errcheck (rollback)
		return "", fmt.Errorf("fetch failed: %s", out)
	}

	branch := getDefaultBranch(ctx, targetDir)

	// Preserve the entire offline history under a recovery branch before
	// discarding it from the working branch — reset --hard below would
	// otherwise drop every commit made while offline, not just uncommitted
	// edits.
	backupBranch := fmt.Sprintf("corezoid-local-backup-%s", strings.TrimSpace(headShortSHA(ctx, targetDir)))
	if out, err := runGit(ctx, targetDir, "branch", backupBranch, "HEAD"); err != nil {
		logger.Warn("git-pull-context: could not create backup branch %s before reconnect: %s", backupBranch, out)
	}

	// Snapshot the current _ext/ tree from disk. This captures both committed
	// and uncommitted offline edits, unlike `git stash` which only sees
	// uncommitted changes.
	extDir := filepath.Join(targetDir, "_ext")
	snapshotDir, snapErr := os.MkdirTemp("", "corezoid-ext-snapshot-*")
	hasSnapshot := false
	if snapErr != nil {
		logger.Warn("git-pull-context: could not create temp dir for _ext/ snapshot: %v", snapErr)
	} else {
		defer os.RemoveAll(snapshotDir)
		if _, statErr := os.Stat(extDir); statErr == nil {
			if err := copyDirRecursive(extDir, snapshotDir); err != nil {
				logger.Warn("git-pull-context: could not snapshot _ext/ before reconnect: %v", err)
			} else {
				hasSnapshot = true
			}
		}
	}

	// Reset to remote — take authoritative mirror state.
	if out, err := runGit(ctx, targetDir, "reset", "--hard", "origin/"+branch); err != nil {
		return "", fmt.Errorf("reset to remote failed: %s", out)
	}

	// Merge the offline _ext/ snapshot into the freshly-reset (remote) tree.
	var conflicts []string
	if hasSnapshot {
		var mergeErr error
		conflicts, mergeErr = mergeExtSnapshot(snapshotDir, extDir)
		if mergeErr != nil {
			logger.Warn("git-pull-context: could not merge _ext/ snapshot after reconnect (offline history is still available on branch %s): %v", backupBranch, mergeErr)
		}
	}

	// Push merged _ext/ if anything changed.
	runGit(ctx, targetDir, "add", "_ext/") //nolint:errcheck
	if _, noChanges := runGit(ctx, targetDir, "diff", "--cached", "--quiet"); noChanges != nil {
		runGit(ctx, targetDir, "commit", "-m", "docs: merge local offline context on reconnect") //nolint:errcheck
	}
	if gitHasUnpushedCommits(ctx, targetDir) {
		if out, err := runGitAuthed(ctx, targetDir, gLogin, gSecret, "push", "origin", branch); err != nil {
			logger.Warn("git-pull-context: reconnect push failed: %s", out)
		}
	}

	msg := fmt.Sprintf("reconnected to Gitea (branch=%s, offline history backed up to %s)", branch, backupBranch)
	if len(conflicts) > 0 {
		msg += fmt.Sprintf("\n%d file(s) in _ext/ changed on the remote while offline and conflict with your local edits — remote version kept, local version saved alongside as '<file>.local-conflict' (full offline history is on branch %s):\n  - %s",
			len(conflicts), backupBranch, strings.Join(conflicts, "\n  - "))
	}
	logger.Info("git-pull-context: %s", msg)
	return msg, nil
}

// mergeExtSnapshot restores snapshotDir (the pre-reconnect local _ext/ state)
// into extDir (the just-reset, remote _ext/ state), file by file:
//   - a file that only exists locally is restored as-is (pure local addition)
//   - a file that exists in both and is byte-identical is left alone
//   - a file that exists in both and differs is a genuine conflict: the remote
//     version stays in place (consistent with "remote wins" everywhere else in
//     the mirror), the local version is written alongside as
//     "<file>.local-conflict" instead of being silently discarded, and its
//     path is returned so the caller can surface it instead of only logging it.
func mergeExtSnapshot(snapshotDir, extDir string) ([]string, error) {
	var conflicts []string
	err := filepath.WalkDir(snapshotDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(snapshotDir, path)
		if relErr != nil {
			return relErr
		}
		localData, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		remotePath := filepath.Join(extDir, rel)
		remoteData, remoteErr := os.ReadFile(remotePath)
		switch {
		case os.IsNotExist(remoteErr):
			if err := os.MkdirAll(filepath.Dir(remotePath), 0755); err != nil {
				return err
			}
			return os.WriteFile(remotePath, localData, 0644)
		case remoteErr != nil:
			return remoteErr
		case bytes.Equal(localData, remoteData):
			return nil
		default:
			conflicts = append(conflicts, filepath.ToSlash(rel))
			return os.WriteFile(remotePath+".local-conflict", localData, 0644)
		}
	})
	return conflicts, err
}

// headShortSHA returns the short SHA of HEAD, used to make backup branch
// names unique across repeated reconnect attempts. Falls back to "unknown".
func headShortSHA(ctx context.Context, dir string) string {
	out, err := runGit(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

// copyDirRecursive copies the contents of src into dst, creating dst if
// needed. Used to snapshot/restore _ext/ around a `git reset --hard` where
// git's own stash/checkout machinery isn't reliable for already-committed
// content (see reconnectToGitea).
func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}
