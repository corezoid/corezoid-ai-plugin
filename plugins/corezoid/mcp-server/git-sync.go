package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitSyncConfig holds all parameters needed for git mirror operations.
// All fields except StagePath come from env vars (see loadGitSyncConfig).
// StagePath is set at call time from the resolved project/stage names.
type GitSyncConfig struct {
	LoginID   string // COREZOID_LOGIN — API key login_id (numeric string)
	Secret    string // COREZOID_SECRET — API key secret
	CompanyID string // WORKSPACE_ID   — company UUID or iXXXXX
	BaseURL   string // COREZOID_API_URL
	WorkDir   string // base dir; .git-context/ is created here
	StagePath string // "projects/<id>_<Name>/stages/<id>_<Name>" — set per-request
}

// IsConfigured returns true when all required fields are present.
// Missing credentials → all git operations are silently skipped.
func (c *GitSyncConfig) IsConfigured() bool {
	return c.LoginID != "" && c.Secret != "" && c.CompanyID != "" && c.BaseURL != ""
}

// RepoURL builds the authenticated HTTPS clone URL.
// url.UserPassword is used so that special characters in the secret
// (@ : / #) are correctly percent-encoded in the userinfo component.
func (c *GitSyncConfig) RepoURL() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	repoURL := &url.URL{
		Scheme: "https",
		User:   url.UserPassword(c.LoginID, c.Secret),
		Host:   u.Host,
		Path:   "/corezoid-dev/c-" + c.CompanyID + ".git",
	}
	return repoURL.String()
}

// gitContextDir returns the local path where the sparse clone is kept.
func (c *GitSyncConfig) gitContextDir() string {
	base := c.WorkDir
	if base == "" {
		base, _ = os.Getwd()
	}
	return filepath.Join(base, ".git-context")
}

// gitRun executes a git command in dir with a timeout.
// Returns combined stdout+stderr and any error.
// Credential-bearing URLs in args are redacted in error messages.
func gitRun(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", strings.Join(redactGitArgs(args), " "), err, out)
	}
	return string(out), nil
}

// redactGitArgs returns a copy of args with any https://user:pass@host URL
// replaced by its redacted form (password → "xxxxx").
func redactGitArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if u, err := url.Parse(a); err == nil && u.User != nil {
			out[i] = u.Redacted()
		} else {
			out[i] = a
		}
	}
	return out
}

// buildSparsePaths returns the cone-mode directory list for sparse checkout.
// Cone mode automatically includes top-level files of all ancestor directories,
// so CLAUDE.md at workspace/project/stage level is included for free.
func buildSparsePaths(cfg GitSyncConfig) []string {
	// Root _ext/ is always included.
	paths := []string{"_ext"}

	if cfg.StagePath == "" {
		return paths
	}
	// StagePath = "projects/<id>_<Name>/stages/<id>_<Name>"
	parts := strings.SplitN(cfg.StagePath, "/stages/", 2)
	if len(parts) == 2 {
		projectPath := parts[0] // "projects/<id>_<Name>"
		paths = append(paths,
			projectPath+"/_ext",
			cfg.StagePath+"/_ext",
		)
	}
	return paths
}

// GitPull performs a sparse checkout of CLAUDE.md + _ext/ for the current stage.
// First call: clones. Subsequent calls: pulls with --rebase.
// Never returns a fatal error — caller logs and continues without git context.
func GitPull(cfg GitSyncConfig) error {
	if !cfg.IsConfigured() {
		logger.Debug("[git-sync] not configured, skipping pull")
		return nil
	}

	contextDir := cfg.gitContextDir()
	gitDir := filepath.Join(contextDir, ".git")

	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return gitInitialClone(cfg, contextDir)
	}
	return gitPullRebase(cfg, contextDir)
}

// gitInitialClone creates a shallow sparse clone of the git mirror.
func gitInitialClone(cfg GitSyncConfig, contextDir string) error {
	if err := os.MkdirAll(contextDir, 0755); err != nil {
		return fmt.Errorf("[git-sync] mkdir %s: %w", contextDir, err)
	}

	repoURL := cfg.RepoURL()
	if repoURL == "" {
		return fmt.Errorf("[git-sync] could not build repo URL from base URL %q", cfg.BaseURL)
	}

	sparseSetArgs := append([]string{"sparse-checkout", "set"}, buildSparsePaths(cfg)...)

	steps := []struct {
		timeout time.Duration
		args    []string
	}{
		{5 * time.Second, []string{"init"}},
		{5 * time.Second, []string{"remote", "add", "origin", repoURL}},
		{5 * time.Second, []string{"sparse-checkout", "init", "--cone"}},
		{5 * time.Second, sparseSetArgs},
		{30 * time.Second, []string{"fetch", "--depth=1", "origin"}},
		// checkout -b creates a local branch so pull --rebase and push work correctly.
		{10 * time.Second, []string{"checkout", "-b", "main", "FETCH_HEAD"}},
		{5 * time.Second, []string{"branch", "--set-upstream-to=origin/main", "main"}},
	}

	for _, s := range steps {
		if _, err := gitRun(contextDir, s.timeout, s.args...); err != nil {
			return fmt.Errorf("[git-sync] clone step %v: %w", s.args[0], err)
		}
	}

	logger.Info("[git-sync] cloned context for stage %q", cfg.StagePath)
	return nil
}

// gitPullRebase refreshes the sparse clone and rebases any local _ext/ commits.
func gitPullRebase(cfg GitSyncConfig, contextDir string) error {
	// Keep credentials current in case they changed.
	if repoURL := cfg.RepoURL(); repoURL != "" {
		_, _ = gitRun(contextDir, 5*time.Second, "remote", "set-url", "origin", repoURL)
	}

	// Update sparse paths in case StagePath changed.
	paths := buildSparsePaths(cfg)
	sparsePaths := append([]string{"sparse-checkout", "set"}, paths...)
	if _, err := gitRun(contextDir, 5*time.Second, sparsePaths...); err != nil {
		logger.Warn("[git-sync] sparse-checkout update failed: %v", err)
	}

	if _, err := gitRun(contextDir, 15*time.Second, "pull", "--rebase", "origin", "main"); err != nil {
		return fmt.Errorf("[git-sync] pull --rebase: %w", err)
	}

	logger.Info("[git-sync] pulled context for stage %q", cfg.StagePath)
	return nil
}

// GitPush commits local _ext/ changes and pushes to the mirror.
// Returns a descriptive error if push is rejected (403 = not yet enabled).
func GitPush(cfg GitSyncConfig, message string) error {
	if !cfg.IsConfigured() {
		logger.Debug("[git-sync] not configured, skipping push")
		return nil
	}

	contextDir := cfg.gitContextDir()
	if _, err := os.Stat(contextDir); os.IsNotExist(err) {
		return fmt.Errorf("[git-sync] context dir not found — run git-pull-context first")
	}

	// Stage _ext/ only.
	extPath := gitExtPath(cfg, contextDir)
	if _, err := gitRun(contextDir, 5*time.Second, "add", extPath); err != nil {
		return fmt.Errorf("[git-sync] git add: %w", err)
	}

	// Nothing to commit?
	status, err := gitRun(contextDir, 5*time.Second, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("[git-sync] git status: %w", err)
	}
	if strings.TrimSpace(status) == "" {
		logger.Debug("[git-sync] nothing to push")
		return nil
	}

	if message == "" {
		message = fmt.Sprintf("docs: update _ext/docs/ after task session %s", time.Now().UTC().Format(time.RFC3339))
	}
	if _, err := gitRun(contextDir, 5*time.Second, "commit", "-m", message); err != nil {
		return fmt.Errorf("[git-sync] git commit: %w", err)
	}

	// Pull --rebase before push to minimise conflicts.
	if _, err := gitRun(contextDir, 15*time.Second, "pull", "--rebase", "origin", "main"); err != nil {
		logger.Warn("[git-sync] pull before push failed (continuing): %v", err)
	}

	if _, err := gitRun(contextDir, 15*time.Second, "push", "origin", "main"); err != nil {
		output := err.Error()
		if strings.Contains(output, "403") {
			return fmt.Errorf("[git-sync] push not yet available (server-side push access pending). Changes committed locally in .git-context/")
		}
		return fmt.Errorf("[git-sync] push: %w", err)
	}

	logger.Info("[git-sync] pushed _ext/ changes")
	return nil
}

// ReadContextFile reads a file from _ext/ relative to the current stage path.
// path should be like "_ext/docs/context.md".
// Returns (content, true) on success or ("", false) if not found.
func ReadContextFile(cfg GitSyncConfig, path string) (string, bool) {
	fullPath := gitStagePath(cfg, path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// UpdateContextFile writes content to a file in _ext/docs/ for the current stage.
// mode "append" appends; mode "replace" overwrites.
func UpdateContextFile(cfg GitSyncConfig, path string, content string, mode string) error {
	fullPath := gitStagePath(cfg, path)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("[git-sync] mkdir for %s: %w", path, err)
	}

	if mode == "append" {
		f, err := os.OpenFile(fullPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("[git-sync] open %s: %w", path, err)
		}
		defer f.Close()
		_, err = f.WriteString(content)
		return err
	}

	return os.WriteFile(fullPath, []byte(content), 0644)
}

// gitStagePath builds an absolute path inside .git-context for the given relative path.
// If StagePath is set, the path is relative to the stage directory.
func gitStagePath(cfg GitSyncConfig, relPath string) string {
	contextDir := cfg.gitContextDir()
	if cfg.StagePath != "" {
		return filepath.Join(contextDir, cfg.StagePath, relPath)
	}
	return filepath.Join(contextDir, relPath)
}

// gitExtPath returns the absolute path to the _ext/ directory of the current stage.
func gitExtPath(cfg GitSyncConfig, contextDir string) string {
	if cfg.StagePath != "" {
		return filepath.Join(contextDir, cfg.StagePath, "_ext")
	}
	return filepath.Join(contextDir, "_ext")
}
