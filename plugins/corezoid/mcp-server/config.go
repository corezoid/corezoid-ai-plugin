package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// currentConfigVersion is the schema version stored in ~/.corezoid/config.json.
// Bumps must be paired with an in-place migration in LoadConfig.
const currentConfigVersion = 1

// defaultAPIGwURL is the fallback used when Folder.APIGwURL is empty.
const defaultAPIGwURL = "https://api-apigw.corezoid.com"

// Folder is one workspace binding in ~/.corezoid/config.json. The MCP server
// resolves the current working directory (or $COREZOID_WORK_DIR when the host
// process — Codex, Kiro — cannot preserve cwd across MCP subprocess spawn) to
// the longest-prefix RootPath in Config.Folders, and uses that Folder as the
// sole source of auth state for the lifetime of the process.
//
// StageID and ProjectID are persisted here — one Corezoid stage per workspace.
// pull-folder writes the stage contents directly into RootPath (no
// <id>_<name>.stage/ wrapper subdirectory), so RootPath IS the stage root on
// disk. Both IDs are (re)written by handleLogin whenever the user (re)selects
// a stage, and cleared on workspace change.
type Folder struct {
	RootPath     string    `json:"root_path"`
	AccountURL   string    `json:"account_url"`
	CorezoidURL  string    `json:"corezoid_url"`
	APIGwURL     string    `json:"apigw_url,omitempty"`
	WorkspaceID  string    `json:"workspace_id"`
	ProjectID    int       `json:"project_id,omitempty"`
	StageID      int       `json:"stage_id,omitempty"`
	GitURL       string    `json:"git_url,omitempty"`
	GitStagePath string    `json:"git_stage_path,omitempty"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	APILogin     string    `json:"api_login"`
	APISecret    string    `json:"api_secret"`
}

// Config is the whole ~/.corezoid/config.json.
type Config struct {
	Version int      `json:"version"`
	Folders []Folder `json:"folders"`
}

// configFilePath returns ~/.corezoid/config.json, creating the parent
// directory with mode 0700 on first use. It is the single source of auth +
// workspace state for this MCP server — no other files are read or written.
func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	return filepath.Join(dir, "config.json"), nil
}

// resolveWorkDir returns the absolute path used to match a Folder.
// $COREZOID_WORK_DIR takes precedence over os.Getwd() because Codex and Kiro
// spawn the MCP subprocess without inheriting the user's cwd.
func resolveWorkDir() string {
	candidate := os.Getenv("COREZOID_WORK_DIR")
	if candidate == "" {
		if cwd, err := os.Getwd(); err == nil {
			candidate = cwd
		}
	}
	if candidate == "" {
		return ""
	}
	if abs, err := filepath.Abs(candidate); err == nil {
		return abs
	}
	return candidate
}

// pathIsAncestor reports whether root equals cwd or is a proper ancestor
// directory of cwd, tested on path-separator boundaries so /a/b does not
// match /a/bc.
func pathIsAncestor(root, cwd string) bool {
	if root == "" || cwd == "" {
		return false
	}
	sep := string(filepath.Separator)
	rTrim := strings.TrimRight(root, sep)
	cTrim := strings.TrimRight(cwd, sep)
	if rTrim == cTrim {
		return true
	}
	return strings.HasPrefix(cTrim, rTrim+sep)
}

// matchFolder returns the index of the Folder in folders whose RootPath is
// the longest prefix of cwd, or -1 if none match.
func matchFolder(folders []Folder, cwd string) int {
	best := -1
	bestLen := 0
	for i, f := range folders {
		if !pathIsAncestor(f.RootPath, cwd) {
			continue
		}
		if best == -1 || len(f.RootPath) > bestLen {
			best = i
			bestLen = len(f.RootPath)
		}
	}
	return best
}

// LoadConfig reads ~/.corezoid/config.json. A missing file returns an empty
// Config — the fresh-install state, not an error. A malformed file returns
// an error so callers do not silently overwrite user data.
func LoadConfig() (*Config, error) {
	path, err := configFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Version: currentConfigVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return &Config{Version: currentConfigVersion}, nil
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Version == 0 {
		c.Version = currentConfigVersion
	}
	if c.Folders == nil {
		c.Folders = []Folder{}
	}
	return &c, nil
}

// writeConfigAtomically writes c to path via temp file + fsync + rename.
// Caller must hold the cross-process flock and the in-process mutex.
func writeConfigAtomically(path string, c *Config) error {
	if c.Version == 0 {
		c.Version = currentConfigVersion
	}
	if c.Folders == nil {
		c.Folders = []Folder{}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.json.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we fail before rename.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := os.Chmod(tmpName, 0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	renamed = true
	return nil
}

// configWriteMu serialises concurrent writers inside a single process. The
// flock on <path>.lock serialises writers across processes. Both are needed
// — flock alone will not serialise goroutines inside the same process, and
// the in-process mutex alone cannot see writers in other processes (a second
// MCP server started from another IDE window).
var configWriteMu sync.Mutex

// withConfigLock acquires the in-process mutex and the cross-process flock,
// then runs fn with the resolved config path. Blocks until both locks are
// available.
func withConfigLock(fn func(path string) error) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("flock %s.lock: %w", path, err)
	}
	defer func() { _ = lock.Unlock() }()
	return fn(path)
}

// UpdateCurrent applies mutator to the Folder matching the current working
// directory, creating a new Folder rooted at cwd if none matches. The whole
// read-modify-write cycle is atomic + serialised across processes.
func UpdateCurrent(mutator func(*Folder)) error {
	if mutator == nil {
		return errors.New("UpdateCurrent: nil mutator")
	}
	cwd := resolveWorkDir()
	if cwd == "" {
		return errors.New("UpdateCurrent: cannot resolve working directory")
	}
	return withConfigLock(func(path string) error {
		c, err := LoadConfig()
		if err != nil {
			return err
		}
		idx := matchFolder(c.Folders, cwd)
		if idx < 0 {
			c.Folders = append(c.Folders, Folder{RootPath: cwd})
			idx = len(c.Folders) - 1
		}
		mutator(&c.Folders[idx])
		return writeConfigAtomically(path, c)
	})
}

// RemoveCurrent removes the Folder that matches the current cwd from the
// config file. No-op if no Folder matches.
func RemoveCurrent() error {
	cwd := resolveWorkDir()
	if cwd == "" {
		return errors.New("RemoveCurrent: cannot resolve working directory")
	}
	return withConfigLock(func(path string) error {
		c, err := LoadConfig()
		if err != nil {
			return err
		}
		idx := matchFolder(c.Folders, cwd)
		if idx < 0 {
			return nil
		}
		c.Folders = append(c.Folders[:idx], c.Folders[idx+1:]...)
		return writeConfigAtomically(path, c)
	})
}

// Current returns a copy of the Folder matching the current cwd, or nil if
// none matches. Reads are lock-free; concurrent writers may race, but hot
// paths consume the in-memory globals populated by syncGlobalsFromCurrent —
// this helper is only used during startup and by tools that need the full
// Folder snapshot.
func Current() *Folder {
	cwd := resolveWorkDir()
	if cwd == "" {
		return nil
	}
	c, err := LoadConfig()
	if err != nil {
		return nil
	}
	idx := matchFolder(c.Folders, cwd)
	if idx < 0 {
		return nil
	}
	f := c.Folders[idx]
	return &f
}

// workspaceProvisionedMarker is the sentinel directory name written into
// Folder.RootPath after a successful login-time pull when the pulled stage
// (or workspace root) contained no files. Its presence tells
// pruneAbandonedFolder that the workspace is genuinely provisioned as
// opposed to a bare empty directory the user has just wiped or recreated.
const workspaceProvisionedMarker = ".corezoid"

// writeWorkspaceProvisionedMarkerIfEmpty creates the sentinel inside
// rootPath when the pull produced no other entries. If the pull
// materialised any content, that content is itself proof of provisioning
// and no marker is needed.
func writeWorkspaceProvisionedMarkerIfEmpty(rootPath string) error {
	if rootPath == "" {
		return nil
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	return os.MkdirAll(filepath.Join(rootPath, workspaceProvisionedMarker), 0700)
}

// isRootPathAbandoned reports whether Folder.RootPath signals that the
// user has wiped the workspace since it was last provisioned. Two triggers:
//  1. RootPath no longer exists on disk (deleted, never recreated).
//  2. RootPath exists but has zero entries — matches "user rm -rf'd and
//     recreated the same path" and "user emptied everything, including the
//     .corezoid marker".
//
// A directory that contains any entry (files, subdirs, dotfiles) is
// treated as provisioned; if there is content we do not second-guess the
// user's intent.
func isRootPathAbandoned(rootPath string) bool {
	if rootPath == "" {
		return false
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		return false
	}
	if !info.IsDir() {
		return true
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

// pruneAbandonedFolder removes the current Folder from
// ~/.corezoid/config.json when its RootPath looks abandoned (see
// isRootPathAbandoned) and refreshes the in-memory auth globals so
// downstream ensureAuth() sees a fresh, un-authenticated state. Returns
// true when a Folder was pruned. No-op when no Folder matches the current
// cwd or the workspace is provisioned.
func pruneAbandonedFolder() bool {
	f := Current()
	if f == nil {
		return false
	}
	if !isRootPathAbandoned(f.RootPath) {
		return false
	}
	if err := RemoveCurrent(); err != nil {
		logger.Warn("pruneAbandonedFolder: RemoveCurrent failed: %v", err)
		return false
	}
	syncGlobalsFromCurrent()
	logger.Info("pruneAbandonedFolder: removed stale Folder rooted at %q (workspace wiped or recreated)", f.RootPath)
	return true
}

// syncGlobalsFromCurrent reads the current Folder from disk and mirrors its
// values into the package-level auth-state globals under authStateMu. Call
// after every UpdateCurrent / RemoveCurrent and at startup.
//
// StageID and ProjectID come straight from the Folder — one stage per
// workspace, both persisted on login.
func syncGlobalsFromCurrent() {
	syncGlobalsFromFolder(Current())
}

// syncGlobalsFromFolder mirrors f into the auth-state globals under
// authStateMu. Split out from syncGlobalsFromCurrent so callers holding an
// in-memory Folder (e.g. handleLogin's staged buffer) can update the globals
// without an intermediate disk write. Passing nil resets every global to its
// zero value, matching the fresh-install state — this prevents stale
// in-memory state from a previous cwd leaking into new operations.
//
// Fields the caller's Folder leaves unset (and every field when f is nil) are
// filled in from COREZOID_* environment variables by applyEnvFallback — the
// config file always wins, the environment only fills gaps. This is the single
// choke point for that fallback: every hot path reads the globals it writes.
func syncGlobalsFromFolder(f *Folder) {
	f = applyEnvFallback(f)
	authStateMu.Lock()
	defer authStateMu.Unlock()
	if f == nil {
		apiToken = ""
		accountURL = ""
		apiURL = ""
		apigwURL = defaultAPIGwURL
		workspaceID = ""
		apiLogin = ""
		apiSecret = ""
		gitURL = ""
		gitStagePath = ""
		stageID = 0
		cachedProjectID = 0
		return
	}
	apiToken = f.AccessToken
	// Clear the in-memory token if it has a known expiry in the past — the
	// old loadCredentials/isCredentialsExpired pair had the same policy, so
	// preserving it prevents callers from unknowingly using a stale token.
	if apiToken != "" && !f.ExpiresAt.IsZero() && time.Now().After(f.ExpiresAt) {
		apiToken = ""
	}
	accountURL = f.AccountURL
	apiURL = f.CorezoidURL
	apigwURL = f.APIGwURL
	if apigwURL == "" {
		apigwURL = defaultAPIGwURL
	}
	workspaceID = f.WorkspaceID
	apiLogin = f.APILogin
	apiSecret = f.APISecret
	gitURL = f.GitURL
	gitStagePath = f.GitStagePath
	stageID = f.StageID
	cachedProjectID = f.ProjectID
}
