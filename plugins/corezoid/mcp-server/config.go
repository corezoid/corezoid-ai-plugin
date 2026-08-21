package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

	// sourcePath is the config file this Folder was read from. Unexported, so
	// encoding/json never persists it. LoadConfig sets it on every Folder it
	// returns; UpdateCurrent and RemoveCurrent use it to write a Folder back
	// to the file that defined it instead of always to the primary
	// config.json.
	sourcePath string
}

// Config is the whole ~/.corezoid/config.json.
type Config struct {
	Version int      `json:"version"`
	Folders []Folder `json:"folders"`
}

// configDirPath returns ~/.corezoid, creating it with mode 0700 on first use.
func configDirPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	return dir, nil
}

// configFilePath returns ~/.corezoid/config.json — the primary config file.
// It is the only file this server creates, and the one that wins whenever a
// workspace is described by more than one file (see LoadConfig).
func configFilePath() (string, error) {
	dir, err := configDirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// auxConfigFilePaths returns the secondary config files —
// ~/.corezoid/config-<anything>.json, sorted by name. They are read and
// merged into the effective config (LoadConfig) and written back to when they
// own the matched workspace, but never created by this server: they exist so
// an operator or another tool can drop in extra workspace bindings without
// touching config.json.
func auxConfigFilePaths() ([]string, error) {
	dir, err := configDirPath()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(dir, "config-*.json"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", dir, err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

// configFilePaths returns every config file that participates in the merge,
// primary first, then the aux files in name order. The order is also the
// precedence order used by LoadConfig.
func configFilePaths() ([]string, error) {
	primary, err := configFilePath()
	if err != nil {
		return nil, err
	}
	aux, err := auxConfigFilePaths()
	if err != nil {
		return nil, err
	}
	return append([]string{primary}, aux...), nil
}

// normalizeRootPath canonicalises a Folder.RootPath for identity comparison
// across config files: two entries with the same normalized root describe the
// same workspace, so only the highest-precedence one is used.
func normalizeRootPath(root string) string {
	if root == "" {
		return ""
	}
	return strings.TrimRight(filepath.Clean(root), string(filepath.Separator))
}

// currentConfigFilePath returns the config file that owns the workspace
// matching the current cwd — the aux config-<name>.json that declared it, or
// the primary config.json otherwise (including when nothing matches yet, since
// that is where a new Folder would be created). For user-facing messages that
// name the file being written.
func currentConfigFilePath() string {
	if f := Current(); f != nil && f.sourcePath != "" {
		return f.sourcePath
	}
	path, err := configFilePath()
	if err != nil {
		return ""
	}
	return path
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

// loadConfigFile reads one config file. A missing or empty file returns an
// empty Config — the fresh-install state, not an error. A malformed file
// returns an error so callers do not silently overwrite user data. Every
// returned Folder carries sourcePath == path.
func loadConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Version: currentConfigVersion, Folders: []Folder{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return &Config{Version: currentConfigVersion, Folders: []Folder{}}, nil
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
	for i := range c.Folders {
		c.Folders[i].sourcePath = path
	}
	return &c, nil
}

// LoadConfig reads ~/.corezoid/config.json and merges every
// ~/.corezoid/config-*.json on top of it, producing the effective config.
//
// Precedence: config.json first, then the aux files in name order. A workspace
// (Folder.RootPath, normalized) that appears in more than one file is taken
// from the first file that declared it — so config.json always wins, and an
// earlier aux file wins over a later one.
//
// A malformed primary config.json is an error: it is the file this server
// writes, and silently treating it as empty would overwrite the user's
// credentials on the next write. A malformed aux file is logged and skipped
// instead — a stray file dropped into ~/.corezoid must not take the server
// down.
func LoadConfig() (*Config, error) {
	paths, err := configFilePaths()
	if err != nil {
		return nil, err
	}
	return mergeConfigFiles(paths)
}

// mergeConfigFiles merges the given config files in order — paths[0] is the
// primary (strict: a parse error is returned), the rest are aux files
// (lenient: a parse error is logged and the file skipped). Split out from
// LoadConfig so writers can merge exactly the set of files they hold locks
// on, instead of re-globbing the directory inside the critical section.
func mergeConfigFiles(paths []string) (*Config, error) {
	if len(paths) == 0 {
		return &Config{Version: currentConfigVersion, Folders: []Folder{}}, nil
	}
	merged, err := loadConfigFile(paths[0])
	if err != nil {
		return nil, err
	}
	aux := paths[1:]
	if len(aux) == 0 {
		return merged, nil
	}

	seen := make(map[string]struct{}, len(merged.Folders))
	for _, f := range merged.Folders {
		if key := normalizeRootPath(f.RootPath); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, path := range aux {
		c, err := loadConfigFile(path)
		if err != nil {
			logger.Warn("mergeConfigFiles: skipping %s: %v", path, err)
			continue
		}
		for _, f := range c.Folders {
			key := normalizeRootPath(f.RootPath)
			if key != "" {
				if _, dup := seen[key]; dup {
					logger.Debug("mergeConfigFiles: %s: folder %q already defined with higher precedence, ignoring", path, f.RootPath)
					continue
				}
				seen[key] = struct{}{}
			}
			merged.Folders = append(merged.Folders, f)
		}
	}
	return merged, nil
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

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
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

// withConfigLock acquires the in-process mutex and the cross-process flock on
// every config file that takes part in the merge (primary + aux), then runs
// fn. All files are locked, not just the one about to be written, because the
// write target is only known after the merged config has been read — and the
// read must not race a writer in another process. Locks are taken in a stable
// (sorted) order so two processes cannot deadlock against each other.
//
// fn receives the exact list of locked files, primary first — it must not
// re-scan the directory, or it could end up writing a file appearing after
// the locks were taken.
func withConfigLock(fn func(paths []string) error) error {
	paths, err := configFilePaths()
	if err != nil {
		return err
	}
	ordered := append([]string(nil), paths...)
	sort.Strings(ordered)

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	var held []*flock.Flock
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = held[i].Unlock()
		}
	}()
	for _, p := range ordered {
		lock := flock.New(p + ".lock")
		if err := lock.Lock(); err != nil {
			return fmt.Errorf("flock %s.lock: %w", p, err)
		}
		held = append(held, lock)
	}
	return fn(paths)
}

// UpdateCurrent applies mutator to the Folder matching the current working
// directory, creating a new Folder rooted at cwd if none matches. The whole
// read-modify-write cycle is atomic + serialised across processes.
//
// The mutation is written back to the file that defined the matched Folder —
// so a workspace declared in ~/.corezoid/config-<name>.json keeps its
// credentials there (token refresh, stage selection) instead of being copied
// into config.json. New Folders always go to config.json.
func UpdateCurrent(mutator func(*Folder)) error {
	if mutator == nil {
		return errors.New("UpdateCurrent: nil mutator")
	}
	cwd := resolveWorkDir()
	if cwd == "" {
		return errors.New("UpdateCurrent: cannot resolve working directory")
	}
	return withConfigLock(func(paths []string) error {
		merged, err := mergeConfigFiles(paths)
		if err != nil {
			return err
		}
		idx := matchFolder(merged.Folders, cwd)

		// No binding for this cwd anywhere: create one in the primary file.
		if idx < 0 {
			primary := paths[0]
			c, err := loadConfigFile(primary)
			if err != nil {
				return err
			}
			c.Folders = append(c.Folders, Folder{RootPath: cwd})
			mutator(&c.Folders[len(c.Folders)-1])
			return writeConfigAtomically(primary, c)
		}

		// Otherwise mutate the entry in place, in the file that declared it.
		target := merged.Folders[idx].sourcePath
		if target == "" {
			target = paths[0]
		}
		c, err := loadConfigFile(target)
		if err != nil {
			return err
		}
		tIdx := indexOfRootPath(c.Folders, merged.Folders[idx].RootPath)
		if tIdx < 0 {
			// Unreachable while target is the file the Folder was read from
			// under the same lock; guard rather than index out of range.
			return fmt.Errorf("UpdateCurrent: folder %q vanished from %s", merged.Folders[idx].RootPath, target)
		}
		mutator(&c.Folders[tIdx])
		return writeConfigAtomically(target, c)
	})
}

// indexOfRootPath returns the index of the Folder whose RootPath is the same
// workspace as root (normalized comparison), or -1.
func indexOfRootPath(folders []Folder, root string) int {
	key := normalizeRootPath(root)
	if key == "" {
		return -1
	}
	for i, f := range folders {
		if normalizeRootPath(f.RootPath) == key {
			return i
		}
	}
	return -1
}

// RemoveCurrent removes the Folder that matches the current cwd from every
// config file that declares it, not only from the highest-precedence one:
// logout must actually drop the credentials, and leaving a shadowed copy in an
// aux file would resurrect them on the next read. No-op if no Folder matches.
func RemoveCurrent() error { return removeCurrent(false) }

// removeCurrentFromPrimary removes the Folder matching the current cwd from
// ~/.corezoid/config.json only, leaving any aux config-<name>.json untouched.
// For automatic cleanup paths, which must never mutate files this server did
// not create.
func removeCurrentFromPrimary() error { return removeCurrent(true) }

func removeCurrent(primaryOnly bool) error {
	cwd := resolveWorkDir()
	if cwd == "" {
		return errors.New("RemoveCurrent: cannot resolve working directory")
	}
	return withConfigLock(func(paths []string) error {
		merged, err := mergeConfigFiles(paths)
		if err != nil {
			return err
		}
		idx := matchFolder(merged.Folders, cwd)
		if idx < 0 {
			return nil
		}
		key := normalizeRootPath(merged.Folders[idx].RootPath)

		if primaryOnly {
			paths = paths[:1]
		}
		for _, path := range paths {
			c, err := loadConfigFile(path)
			if err != nil {
				if path == paths[0] {
					return err
				}
				logger.Warn("RemoveCurrent: skipping %s: %v", path, err)
				continue
			}
			kept := make([]Folder, 0, len(c.Folders))
			for _, f := range c.Folders {
				if normalizeRootPath(f.RootPath) == key {
					continue
				}
				kept = append(kept, f)
			}
			if len(kept) == len(c.Folders) {
				continue
			}
			c.Folders = kept
			if err := writeConfigAtomically(path, c); err != nil {
				return err
			}
		}
		return nil
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
// ~/.corezoid/config.json — and only from there, never from an aux
// config-<name>.json — when its RootPath looks abandoned (see
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
	// Never auto-delete a binding declared in an aux config-<name>.json: those
	// files are provisioned by hand (or by another tool), and an empty
	// RootPath there usually means "provisioned but not pulled yet", not
	// "abandoned". Explicit logout still clears them (see RemoveCurrent).
	if primary, err := configFilePath(); err == nil && f.sourcePath != "" && f.sourcePath != primary {
		logger.Debug("pruneAbandonedFolder: %q looks abandoned but is declared in %s — leaving it alone", f.RootPath, f.sourcePath)
		return false
	}
	if err := removeCurrentFromPrimary(); err != nil {
		logger.Warn("pruneAbandonedFolder: removeCurrentFromPrimary failed: %v", err)
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
func syncGlobalsFromFolder(f *Folder) {
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
