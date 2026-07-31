package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gitContextDir returns the path to .git-context/ inside the current working dir.
func gitContextDir() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".git-context")
}

// ensureGitignoreEntry appends entry to <workDir>/.gitignore if not already
// present, creating the file if needed. .git-context/ nests its own .git
// directory with a Basic-auth header configured per-invocation (see
// runGitAuthed) — nothing secret is written to it, but it's still project
// working state that shouldn't be committed into the user's own repo, so we
// gitignore it defensively the first time it's created.
func ensureGitignoreEntry(workDir, entry string) error {
	path := filepath.Join(workDir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == entry {
			return nil // already present
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += entry + "\n"
	return os.WriteFile(path, []byte(content), 0644)
}

// deriveGitURL computes COREZOID_GIT_URL from the Corezoid account host.
//
// Formula (see Gitea-URL-Formula.md):
//
//  1. apex  = last 2 domain labels of host  (e.g. "corezoid.com")
//  2. prefix = labels before apex           (e.g. ["admin","dev"])
//  3. Inspect first label of prefix:
//     - "admin" or "corezoid" (exact)     → drop it entirely
//     - starts with "admin-"              → strip "admin-", keep remainder
//     - starts with "corezoid-"           → strip "corezoid-", keep remainder
//     - anything else                     → return error (caller silently skips)
//  4. env = join(remaining labels, ".")   or "prod" if nothing remains
//  5. COREZOID_GIT_URL = https://git-{env}.{env}.{apex}/corezoid-{env}
//
// Examples:
//
//	admin.dev.corezoid.com     → https://git-dev.dev.corezoid.com/corezoid-dev
//	admin-pre.corezoid.com     → https://git-pre.pre.corezoid.com/corezoid-pre
//	admin.corezoid.com         → https://git-prod.prod.corezoid.com/corezoid-prod
//	corezoid.leobank.az        → https://git-prod.prod.leobank.az/corezoid-prod
//	corezoid-lq.leobank.az     → https://git-lq.lq.leobank.az/corezoid-lq
//	corezoid.staging.liobank.vn → https://git-staging.staging.liobank.vn/corezoid-staging
func deriveGitURL(rawAccountURL string) (string, error) {
	if rawAccountURL == "" {
		return "", fmt.Errorf("ACCOUNT_URL is empty")
	}
	if !strings.Contains(rawAccountURL, "://") {
		rawAccountURL = "https://" + rawAccountURL
	}
	u, err := url.Parse(rawAccountURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid ACCOUNT_URL %q", rawAccountURL)
	}
	host := u.Hostname() // strip port if any

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("cannot derive git URL from host %q: need at least 2 labels", host)
	}

	// apex = last 2 labels
	apex := strings.Join(labels[len(labels)-2:], ".")
	// prefix = everything before apex
	prefixLabels := labels[:len(labels)-2]

	var env string
	if len(prefixLabels) == 0 {
		// bare apex (e.g. "corezoid.com") — treat as prod
		env = "prod"
	} else {
		first := prefixLabels[0]
		var rest []string

		switch {
		case first == "admin" || first == "corezoid":
			rest = prefixLabels[1:]
		case strings.HasPrefix(first, "admin-"):
			rest = append([]string{first[len("admin-"):]}, prefixLabels[1:]...)
		case strings.HasPrefix(first, "corezoid-"):
			rest = append([]string{first[len("corezoid-"):]}, prefixLabels[1:]...)
		default:
			return "", fmt.Errorf("cannot derive git URL from host %q: first label %q does not match admin/corezoid pattern", host, first)
		}

		if len(rest) == 0 {
			env = "prod"
		} else {
			env = strings.Join(rest, ".")
		}
	}

	gitHost := fmt.Sprintf("git-%s.%s.%s", env, env, apex)
	org := fmt.Sprintf("corezoid-%s", env)
	return fmt.Sprintf("https://%s/%s", gitHost, org), nil
}

// buildGitRepoURL constructs the (credential-free) HTTPS clone URL for the
// project-level mirror repo (layout=project).
// Format: https://<host>/<org>/c-<companyID>-p-<projectID>.git
//
// Normalisation rules:
//   - If base has no scheme (e.g. "git-pre.pre.corezoid.com/corezoid-pre"), prepend "https://".
//   - companyID/projectID may arrive with their "c-"/"p-" prefix already attached,
//     or bare; either way the repo name is always "c-<id>-p-<id>" (strip any
//     existing prefix before re-adding it, to avoid double "c-c-"/"p-p-...").
//
// Credentials are deliberately NOT embedded in this URL — a credentialed URL
// passed to `git clone`/`git remote add` gets written verbatim into
// .git-context/.git/config and stays there in plaintext indefinitely. Callers
// authenticate per-invocation via runGitAuthed instead (login/secret are the
// client's own Corezoid API key — the same credentials used for the Corezoid
// API, not a bot token). The mirror's nginx auth-gw validates them against
// capi and enforces read-only access except under _ext/** (ext_push=true).
func buildGitRepoURL(base, companyID, projectID string) (string, error) {
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid COREZOID_GIT_URL %q: %w", base, err)
	}
	rawCompanyID := strings.TrimPrefix(companyID, "c-")
	rawProjectID := strings.TrimPrefix(projectID, "p-")
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/c-%s-p-%s.git", rawCompanyID, rawProjectID)
	return u.String(), nil
}

// maskSecret replaces the secret in any string (e.g. git output) to prevent leaks.
func maskSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// runGit runs a git command in dir and returns combined output.
// GIT_TERMINAL_PROMPT=0 prevents interactive credential prompts.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runGitAuthed runs a git command with per-invocation HTTP Basic auth for the
// Corezoid git mirror, for network operations (clone/fetch/pull/push) against
// a credential-free URL (see buildGitRepoURL).
//
// The credential is passed via GIT_CONFIG_COUNT/KEY/VALUE environment
// variables rather than a `-c http.extraHeader=...` CLI flag or a URL-embedded
// user:pass — both of those get written to .git-context/.git/config by
// `clone`/`remote add` and persist on disk indefinitely. An env var is
// process-scoped: it is never written to any git config file and does not
// appear in the command's argv (so it doesn't show up in `ps`/cmdline output).
func runGitAuthed(ctx context.Context, dir, login, secret string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if login != "" || secret != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(login + ":" + secret))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+encoded,
		)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitPullContext syncs .git-context/ with the Corezoid git mirror.
//
// Mode selection (in order):
//  1. .git exists + has remote  → git pull --rebase --autostash  (online, normal)
//  2. .git exists + no remote   → try reconnectToGitea(); if Gitea still down → local mode
//  3. .git missing + URL known  → git clone; if clone fails → local mode fallback
//  4. .git missing + no URL     → local mode (offline from the start)
//
// Local mode creates a minimal local git repo so _ext/docs/ and CLAUDE.md are
// maintained without Gitea. CLAUDE.md is (re)generated after pull-folder writes
// the .conv.json files via generateLocalCLAUDEMD().
func gitPullContext(ctx context.Context) (string, error) {
	gURL, gLogin, gSecret, gCompany, _ := gitConfigSnapshot()

	targetDir := gitContextDir()
	gitDir := filepath.Join(targetDir, ".git")

	if _, err := os.Stat(gitDir); err == nil {
		// .git exists.
		if isLocalOnlyContext(ctx, targetDir) {
			// Was in local mode — try to reconnect if we now have a URL.
			if gURL != "" && gLogin != "" && gSecret != "" && gCompany != "" {
				if projectID := resolveProjectID(ctx); projectID != 0 {
					repoURL, buildErr := buildGitRepoURL(gURL, gCompany, strconv.Itoa(projectID))
					if buildErr == nil {
						reconnMsg, reconnErr := reconnectToGitea(ctx, repoURL, gLogin, gSecret)
						if reconnErr != nil {
							logger.Warn("git-pull-context: reconnect attempt failed: %v — staying in local mode", reconnErr)
							return "local mode (Gitea still unavailable)", nil
						}
						return reconnMsg, nil
					}
				}
			}
			return "local mode (no Gitea URL available)", nil
		}

		// Normal online pull.
		out, err := runGitAuthed(ctx, targetDir, gLogin, gSecret, "pull", "--rebase", "--autostash", "origin")
		if err != nil {
			return "", fmt.Errorf("git pull failed: %s", maskSecret(out, gSecret))
		}
		return fmt.Sprintf("Git context updated (%s)", maskSecret(strings.TrimSpace(out), gSecret)), nil
	}

	// No .git yet — try to clone from Gitea if we have a URL.
	if gURL != "" && gLogin != "" && gSecret != "" && gCompany != "" {
		if projectID := resolveProjectID(ctx); projectID != 0 {
			repoURL, buildErr := buildGitRepoURL(gURL, gCompany, strconv.Itoa(projectID))
			if buildErr == nil {
				// m4: clean up incomplete .git-context/ before cloning.
				if _, dirErr := os.Stat(targetDir); dirErr == nil {
					os.RemoveAll(targetDir) //nolint:errcheck
				}
				if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
					return "", fmt.Errorf("cannot create parent directory: %w", err)
				}
				out, cloneErr := runGitAuthed(ctx, "", gLogin, gSecret, "clone", "-q", repoURL, targetDir)
				if cloneErr == nil {
					if err := ensureGitignoreEntry(filepath.Dir(targetDir), ".git-context/"); err != nil {
						logger.Warn("git-pull-context: could not update project .gitignore: %v", err)
					}
					return fmt.Sprintf("Git context cloned to %s", targetDir), nil
				}
				// Clone failed — Gitea unreachable, fall through to local mode.
				logger.Warn("git-pull-context: Gitea clone failed (%s) — switching to local mode",
					maskSecret(strings.TrimSpace(out), gSecret))
			}
		} else {
			logger.Warn("git-pull-context: cannot resolve project ID for stage — switching to local mode")
		}
	}

	// Local mode fallback.
	localPath, initErr := initLocalGitContext(ctx)
	if initErr != nil {
		return "", fmt.Errorf("cannot initialize local git context: %w", initErr)
	}
	return fmt.Sprintf("local mode initialized (%s)", localPath), nil
}

// findGitStagePath walks .git-context/ and returns the path of the stage
// directory whose numeric prefix matches stageID.
//
// The mirror repo is now cloned at the project level
// (c-<companyID>-p-<projectID>), so the standard tree layout is:
//
//	stages/<stageID>_<Name>/
//
// which is the same root-level layout used by local/offline mode. The older
// company-root layout (projects/<projectID>_<Name>/stages/<stageID>_<Name>/)
// is still checked as a fallback for repos mirrored before the project-level
// clone change.
//
// The returned path is relative to contextDir (e.g. "stages/456_Bar").
// If no match is found an empty string is returned without error — the stage
// may not yet be mirrored (new stage, no committed processes).
func findGitStagePath(contextDir string, stageID int) (string, error) {
	if stageID == 0 {
		return "", nil
	}
	prefix := fmt.Sprintf("%d_", stageID)

	// 1. Check stages/ at root level — standard project-level clone layout,
	//    also used by local / offline mode.
	stagesDir := filepath.Join(contextDir, "stages")
	if entries, err := os.ReadDir(stagesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				return filepath.ToSlash("stages/" + e.Name()), nil
			}
		}
	}

	// 2. Walk projects/*/stages/ — legacy fallback for company-root mirror clones.
	projectsRoot := filepath.Join(contextDir, "projects")
	var found string
	err := filepath.WalkDir(projectsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "stages" && strings.HasPrefix(d.Name(), prefix) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("error scanning git context for stage %d: %w", stageID, err)
	}
	if found == "" {
		return "", nil
	}
	rel, err := filepath.Rel(contextDir, found)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// gitPushContext stages _ext/ changes and commits them.
// Handles four cases:
//  1. Local-only mode (no remote configured) → stage + commit only, never push
//  2. New local edits in _ext/ (online) → stage + commit + push
//  3. Already-committed but unpushed commits (online) → push directly
//  4. Nothing to commit/push → return informational message (not an error)
//
// Safety: only _ext/ files are ever staged; any accidentally staged non-_ext/
// files are silently unstaged before the commit so the pre-receive hook cannot
// reject the push.
func gitPushContext(ctx context.Context, commitMsg string) (string, error) {
	targetDir := gitContextDir()

	if _, err := os.Stat(filepath.Join(targetDir, ".git")); err != nil {
		return "", fmt.Errorf("git context not initialised — run git-pull-context first")
	}

	// M1: get login/secret for per-invocation auth and to mask the secret in
	// all git output returned to the caller.
	_, gLogin, gSecret, _, _ := gitConfigSnapshot()

	if commitMsg == "" {
		commitMsg = fmt.Sprintf("docs: update _ext/ after task session %s",
			time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	}

	// Local-only mode has no "origin" to sync with or push to — attempting
	// `pull ... origin` here would fail immediately (unknown remote) and
	// abort before ever committing, silently dropping local _ext/ edits and
	// preventing regenerateLocalCLAUDEMDIfNeeded (called by the handler only
	// on success) from ever running. So skip remote sync/push entirely and
	// just commit locally.
	localOnly := isLocalOnlyContext(ctx, targetDir)

	if !localOnly {
		// Sync with remote first (--autostash handles local modifications).
		if out, err := runGitAuthed(ctx, targetDir, gLogin, gSecret, "pull", "--rebase", "--autostash", "origin"); err != nil {
			return "", fmt.Errorf("git pull before push failed: %s", maskSecret(out, gSecret))
		}
	}

	// Stage _ext/ — the only zone we are allowed to write.
	runGit(ctx, targetDir, "add", "_ext/") //nolint:errcheck (_ext/ may not exist yet)

	// Safety: unstage any non-_ext/ files that may have been added accidentally.
	// The server-side pre-receive hook would reject the entire push otherwise.
	if staged, err := runGit(ctx, targetDir, "diff", "--cached", "--name-only"); err == nil {
		for _, f := range strings.Split(strings.TrimSpace(staged), "\n") {
			if f == "" {
				continue
			}
			if !strings.HasPrefix(filepath.ToSlash(f), "_ext/") {
				runGit(ctx, targetDir, "reset", "HEAD", "--", f) //nolint:errcheck
				logger.Warn("git-push-context: unstaged non-_ext/ file %q — only _ext/** is writable", f)
			}
		}
	}

	// Check for staged changes.
	_, noStaged := runGit(ctx, targetDir, "diff", "--cached", "--quiet")
	hasStaged := noStaged != nil

	// Check for already-committed but unpushed commits (fixes: push after manual commit).
	hasUnpushed := gitHasUnpushedCommits(ctx, targetDir)

	if !hasStaged && !hasUnpushed {
		if localOnly {
			return "No changes in _ext/ to commit (local mode — no remote configured, nothing to push)", nil
		}
		return "No changes in _ext/ to push (nothing staged, nothing unpushed)", nil
	}

	if hasStaged {
		if out, err := runGit(ctx, targetDir, "commit", "-m", commitMsg); err != nil {
			return "", fmt.Errorf("git commit failed: %s", out)
		}
	}

	if localOnly {
		return fmt.Sprintf("Committed locally (offline mode — no remote configured, nothing pushed): %s", commitMsg), nil
	}

	out, err := runGitAuthed(ctx, targetDir, gLogin, gSecret, "push", "origin")
	if err != nil {
		return "", fmt.Errorf("git push failed: %s", maskSecret(out, gSecret))
	}
	return fmt.Sprintf("Git context pushed: %s", maskSecret(strings.TrimSpace(out), gSecret)), nil
}

// gitHasUnpushedCommits returns true if there are commits in HEAD not yet
// pushed to the upstream tracking branch.
func gitHasUnpushedCommits(ctx context.Context, dir string) bool {
	// @{u} resolves to the upstream tracking branch (works for master/main/any branch).
	out, err := runGit(ctx, dir, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		// m2: log so silent skips are observable (e.g. detached HEAD, no upstream).
		logger.Warn("git-push-context: could not check unpushed commits (%v) — assuming none", err)
		return false
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n > 0
}
