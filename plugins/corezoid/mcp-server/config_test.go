package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// tmpHome points HOME at t.TempDir() so configFilePath resolves to a fresh
// per-test file, and unsets COREZOID_WORK_DIR so tests that don't override it
// see the process cwd (which the test also controls). Returns the resolved
// config path for convenience.
func tmpHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Force os.UserHomeDir to see the new HOME on macOS/Linux; on darwin the
	// runtime caches /Users/... via getpwuid otherwise.
	t.Setenv("USERPROFILE", dir)
	// Point cwd at a subdir so pathIsAncestor matches don't accidentally hit
	// entries from other tests via the temp-parent path.
	t.Setenv("COREZOID_WORK_DIR", dir)
	path, err := configFilePath()
	if err != nil {
		t.Fatalf("configFilePath: %v", err)
	}
	return path
}

// tmpHomeAndCWD is a thin wrapper around tmpHome that returns the working
// directory rather than the config path — convenient for tests that need to
// drop a stage marker file into the workspace root.
func tmpHomeAndCWD(t *testing.T) string {
	t.Helper()
	tmpHome(t)
	return os.Getenv("COREZOID_WORK_DIR")
}

// writeTestStageMarker writes a <stageID>_<name>.stage.json file into dir with
// the given stage_id/project_id so resolveStageFromCWD picks it up.
func writeTestStageMarker(t *testing.T, dir string, stageID, projectID int, shortName string) {
	t.Helper()
	payload := []byte(fmt.Sprintf(
		`{"obj_id":%d,"obj_type":10,"parent_id":%d,"short_name":%q,"title":%q}`,
		stageID, projectID, shortName, shortName,
	))
	name := fmt.Sprintf("%d_%s.stage.json", stageID, shortName)
	if err := os.WriteFile(filepath.Join(dir, name), payload, 0644); err != nil {
		t.Fatalf("write stage marker: %v", err)
	}
}

func TestLoadConfig_MissingFileReturnsEmpty(t *testing.T) {
	tmpHome(t)
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Config")
	}
	if c.Version != currentConfigVersion {
		t.Errorf("expected version %d, got %d", currentConfigVersion, c.Version)
	}
	if len(c.Folders) != 0 {
		t.Errorf("expected 0 folders, got %d", len(c.Folders))
	}
}

func TestLoadConfig_MalformedJSONReturnsError(t *testing.T) {
	path := tmpHome(t)
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestLoadConfig_EmptyFileReturnsEmpty(t *testing.T) {
	path := tmpHome(t)
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Version != currentConfigVersion {
		t.Errorf("expected version %d, got %d", currentConfigVersion, c.Version)
	}
}

func TestUpdateCurrent_CreatesFolderIfMissing(t *testing.T) {
	path := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	err := UpdateCurrent(func(f *Folder) {
		f.AccountURL = "https://acct.example.com"
		f.WorkspaceID = "42"
	})
	if err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(c.Folders) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(c.Folders))
	}
	f := c.Folders[0]
	if f.RootPath != cwd {
		t.Errorf("expected root_path=%q, got %q", cwd, f.RootPath)
	}
	if f.AccountURL != "https://acct.example.com" {
		t.Errorf("account_url not persisted: %+v", f)
	}
	if f.WorkspaceID != "42" {
		t.Errorf("workspace_id not persisted: %+v", f)
	}
	data, _ := os.ReadFile(path)
	// Round-trip check that JSON matches expected keys.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("could not re-parse written config: %v", err)
	}
	if raw["version"].(float64) != float64(currentConfigVersion) {
		t.Errorf("version mismatch in on-disk JSON: %v", raw["version"])
	}
}

func TestUpdateCurrent_MutatesExistingFolder(t *testing.T) {
	tmpHome(t)
	_ = UpdateCurrent(func(f *Folder) { f.AccountURL = "one" })
	_ = UpdateCurrent(func(f *Folder) { f.WorkspaceID = "ws-2" })

	c, _ := LoadConfig()
	if len(c.Folders) != 1 {
		t.Fatalf("expected 1 folder after two updates, got %d", len(c.Folders))
	}
	if c.Folders[0].AccountURL != "one" || c.Folders[0].WorkspaceID != "ws-2" {
		t.Errorf("expected both fields persisted, got %+v", c.Folders[0])
	}
}

func TestRemoveCurrent_DeletesFolder(t *testing.T) {
	tmpHome(t)
	_ = UpdateCurrent(func(f *Folder) { f.WorkspaceID = "abc" })
	if err := RemoveCurrent(); err != nil {
		t.Fatalf("RemoveCurrent: %v", err)
	}
	c, _ := LoadConfig()
	if len(c.Folders) != 0 {
		t.Errorf("expected 0 folders after remove, got %d", len(c.Folders))
	}
}

func TestRemoveCurrent_NoMatchIsNoOp(t *testing.T) {
	tmpHome(t)
	if err := RemoveCurrent(); err != nil {
		t.Errorf("expected nil error when nothing matches, got %v", err)
	}
}

func TestCurrent_ReturnsNilWhenNoMatch(t *testing.T) {
	tmpHome(t)
	if got := Current(); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestCurrent_ReturnsCopy(t *testing.T) {
	tmpHome(t)
	_ = UpdateCurrent(func(f *Folder) { f.AccessToken = "tok" })
	f := Current()
	if f == nil || f.AccessToken != "tok" {
		t.Fatalf("expected AccessToken=tok, got %+v", f)
	}
	// Mutating the returned copy must not affect config on disk.
	f.AccessToken = "mutated"
	again := Current()
	if again.AccessToken != "tok" {
		t.Errorf("Current() returned an aliased pointer; want token=tok, got %q", again.AccessToken)
	}
}

func TestMatchFolder_LongestPrefixWins(t *testing.T) {
	folders := []Folder{
		{RootPath: "/a"},
		{RootPath: "/a/b"},
		{RootPath: "/a/b/c"},
		{RootPath: "/z"},
	}
	if got := matchFolder(folders, "/a/b/c/deep"); got != 2 {
		t.Errorf("expected index 2 (/a/b/c), got %d", got)
	}
	if got := matchFolder(folders, "/a/b"); got != 1 {
		t.Errorf("expected exact match on /a/b (index 1), got %d", got)
	}
	if got := matchFolder(folders, "/a/x"); got != 0 {
		t.Errorf("expected fallback to /a (index 0), got %d", got)
	}
	if got := matchFolder(folders, "/nothing/here"); got != -1 {
		t.Errorf("expected no match (-1), got %d", got)
	}
}

func TestMatchFolder_NoPartialSegmentMatch(t *testing.T) {
	// /a/b must NOT match /a/bc — the boundary check has to be at path
	// separators, not arbitrary character boundaries.
	folders := []Folder{{RootPath: "/a/b"}}
	if got := matchFolder(folders, "/a/bc"); got != -1 {
		t.Errorf("expected no match for /a/bc under /a/b, got %d", got)
	}
}

func TestResolveWorkDir_HonorsWorkDirEnv(t *testing.T) {
	t.Setenv("COREZOID_WORK_DIR", "/opt/pinned")
	if got := resolveWorkDir(); got != "/opt/pinned" {
		t.Errorf("expected /opt/pinned, got %q", got)
	}
}

func TestResolveWorkDir_FallsBackToGetwd(t *testing.T) {
	// Explicitly unset so we exercise the os.Getwd path — no t.Setenv("", "")
	// because that still records COREZOID_WORK_DIR as an empty string on
	// t.Cleanup, which resolveWorkDir happens to already treat as absent.
	t.Setenv("COREZOID_WORK_DIR", "")
	cwd, _ := os.Getwd()
	if got := resolveWorkDir(); got != cwd {
		t.Errorf("expected cwd=%q, got %q", cwd, got)
	}
}

func TestUpdateCurrent_ConcurrentGoroutinesSerialize(t *testing.T) {
	tmpHome(t)
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			_ = UpdateCurrent(func(f *Folder) {
				// Each goroutine writes a distinct WorkspaceID; the mutex +
				// flock must ensure a coherent single writer at a time. If
				// they race, the final Folders slice can end up with
				// duplicates or an inconsistent state.
				f.WorkspaceID = "ws-" + itoaShort(i)
			})
		}()
	}
	wg.Wait()

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig after concurrent writes: %v", err)
	}
	if len(c.Folders) != 1 {
		t.Errorf("expected exactly 1 folder after concurrent writes, got %d: %+v", len(c.Folders), c.Folders)
	}
}

// itoaShort avoids pulling in strconv just for a test helper.
func itoaShort(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func TestSyncGlobalsFromCurrent_ExpiredTokenClearsInMemory(t *testing.T) {
	tmpHome(t)
	past := time.Now().Add(-time.Hour).UTC()
	_ = UpdateCurrent(func(f *Folder) {
		f.AccessToken = "expired"
		f.ExpiresAt = past
		f.WorkspaceID = "42"
	})
	// Manually run sync — the API contract is that an expired token must not
	// end up in the apiToken global, but the other fields should still load.
	syncGlobalsFromCurrent()
	_, snapToken, snapWorkspaceID, _, _ := authSnapshot()
	if snapToken != "" {
		t.Errorf("expected apiToken cleared for expired credentials, got %q", snapToken)
	}
	if snapWorkspaceID != "42" {
		t.Errorf("expected non-token fields to still load; got workspace_id=%q", snapWorkspaceID)
	}
}

func TestSyncGlobalsFromCurrent_NoFolderResetsAllGlobals(t *testing.T) {
	tmpHome(t)
	// Seed globals to non-zero values.
	authStateMu.Lock()
	apiToken = "stale"
	workspaceID = "stale-ws"
	stageID = 99
	apiLogin = "stale-login"
	authStateMu.Unlock()

	// Point cwd at a directory with no matching Folder.
	t.Setenv("COREZOID_WORK_DIR", filepath.Join(t.TempDir(), "unregistered"))
	syncGlobalsFromCurrent()

	_, snapToken, snapWorkspaceID, _, snapStageID := authSnapshot()
	snapAPILogin, _ := apiKeySnapshot()
	if snapToken != "" || snapWorkspaceID != "" || snapStageID != 0 || snapAPILogin != "" {
		t.Errorf("expected all globals reset, got token=%q ws=%q stage=%d login=%q", snapToken, snapWorkspaceID, snapStageID, snapAPILogin)
	}
}
