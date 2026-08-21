package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeConfigFileForTest writes folders into an arbitrary config file inside
// ~/.corezoid, bypassing UpdateCurrent — that is how aux config files appear
// in practice: dropped in by an operator or another tool.
func writeConfigFileForTest(t *testing.T, name string, folders ...Folder) string {
	t.Helper()
	dir, err := configDirPath()
	if err != nil {
		t.Fatalf("configDirPath: %v", err)
	}
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(Config{Version: currentConfigVersion, Folders: folders}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func readConfigFileForTest(t *testing.T, path string) Config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return c
}

func TestLoadConfig_MergesAuxFiles(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	if err := UpdateCurrent(func(f *Folder) { f.WorkspaceID = "primary" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	writeConfigFileForTest(t, "config-extra.json", Folder{
		RootPath:    filepath.Join(cwd, "other"),
		WorkspaceID: "from-aux",
		AccessToken: "aux-tok",
	})

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(c.Folders) != 2 {
		t.Fatalf("expected 2 merged folders, got %d: %+v", len(c.Folders), c.Folders)
	}
	if c.Folders[0].WorkspaceID != "primary" {
		t.Errorf("expected primary folder first, got %+v", c.Folders[0])
	}
	if c.Folders[1].WorkspaceID != "from-aux" || c.Folders[1].AccessToken != "aux-tok" {
		t.Errorf("aux folder not merged: %+v", c.Folders[1])
	}
}

func TestLoadConfig_PrimaryWinsOnSameRootPath(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	if err := UpdateCurrent(func(f *Folder) { f.WorkspaceID = "primary" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	// Same workspace, declared again in an aux file — and with a trailing
	// separator, to pin that identity is compared on normalized paths.
	writeConfigFileForTest(t, "config-dup.json", Folder{
		RootPath:    cwd + string(filepath.Separator),
		WorkspaceID: "aux-shadowed",
	})

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(c.Folders) != 1 {
		t.Fatalf("expected the duplicate to be dropped, got %d: %+v", len(c.Folders), c.Folders)
	}
	if c.Folders[0].WorkspaceID != "primary" {
		t.Errorf("expected config.json to win, got %+v", c.Folders[0])
	}
	if f := Current(); f == nil || f.WorkspaceID != "primary" {
		t.Errorf("Current() should resolve to the primary folder, got %+v", f)
	}
}

func TestLoadConfig_EarlierAuxFileWinsOverLater(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	writeConfigFileForTest(t, "config-b.json", Folder{RootPath: cwd, WorkspaceID: "b"})
	writeConfigFileForTest(t, "config-a.json", Folder{RootPath: cwd, WorkspaceID: "a"})

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(c.Folders) != 1 {
		t.Fatalf("expected 1 folder, got %d: %+v", len(c.Folders), c.Folders)
	}
	if c.Folders[0].WorkspaceID != "a" {
		t.Errorf("expected config-a.json to win over config-b.json, got %+v", c.Folders[0])
	}
}

func TestLoadConfig_SkipsMalformedAuxFile(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	dir, _ := configDirPath()

	if err := UpdateCurrent(func(f *Folder) { f.WorkspaceID = "primary" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config-broken.json"), []byte("{not json"), 0600); err != nil {
		t.Fatalf("write broken aux: %v", err)
	}
	writeConfigFileForTest(t, "config-good.json", Folder{
		RootPath:    filepath.Join(cwd, "other"),
		WorkspaceID: "good",
	})

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("a malformed aux file must not fail LoadConfig: %v", err)
	}
	if len(c.Folders) != 2 {
		t.Fatalf("expected primary + good aux, got %d: %+v", len(c.Folders), c.Folders)
	}
}

func TestLoadConfig_IgnoresUnrelatedFileNames(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	dir, _ := configDirPath()

	// Neither of these matches config-*.json.
	writeConfigFileForTest(t, "config.backup.json", Folder{RootPath: cwd, WorkspaceID: "backup"})
	if err := os.WriteFile(filepath.Join(dir, "config-notjson.txt"), []byte("junk"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(c.Folders) != 0 {
		t.Errorf("expected no folders, got %+v", c.Folders)
	}
}

func TestUpdateCurrent_WritesBackToOwningAuxFile(t *testing.T) {
	primary := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	aux := writeConfigFileForTest(t, "config-team.json", Folder{
		RootPath:    cwd,
		WorkspaceID: "ws-aux",
	})

	if err := UpdateCurrent(func(f *Folder) { f.AccessToken = "refreshed" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}

	got := readConfigFileForTest(t, aux)
	if len(got.Folders) != 1 || got.Folders[0].AccessToken != "refreshed" || got.Folders[0].WorkspaceID != "ws-aux" {
		t.Errorf("aux file not updated in place: %+v", got.Folders)
	}
	if _, err := os.Stat(primary); !os.IsNotExist(err) {
		c := readConfigFileForTest(t, primary)
		t.Errorf("config.json must not gain a copy of the aux workspace, got %+v", c.Folders)
	}
}

func TestUpdateCurrent_NewFolderGoesToPrimary(t *testing.T) {
	primary := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	// Aux file describes an unrelated workspace, so cwd has no match.
	aux := writeConfigFileForTest(t, "config-other.json", Folder{
		RootPath:    filepath.Join(cwd, "unrelated-sibling"),
		WorkspaceID: "elsewhere",
	})

	if err := UpdateCurrent(func(f *Folder) { f.WorkspaceID = "fresh" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}

	got := readConfigFileForTest(t, primary)
	if len(got.Folders) != 1 || got.Folders[0].WorkspaceID != "fresh" || got.Folders[0].RootPath != cwd {
		t.Errorf("new folder should land in config.json, got %+v", got.Folders)
	}
	if auxGot := readConfigFileForTest(t, aux); len(auxGot.Folders) != 1 || auxGot.Folders[0].WorkspaceID != "elsewhere" {
		t.Errorf("aux file must be untouched, got %+v", auxGot.Folders)
	}
}

func TestUpdateCurrent_DoesNotPersistSourcePath(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	aux := writeConfigFileForTest(t, "config-src.json", Folder{RootPath: cwd, WorkspaceID: "ws"})

	if err := UpdateCurrent(func(f *Folder) { f.AccessToken = "tok" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	data, err := os.ReadFile(aux)
	if err != nil {
		t.Fatalf("read aux: %v", err)
	}
	var raw struct {
		Folders []map[string]any `json:"folders"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse aux: %v", err)
	}
	if len(raw.Folders) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(raw.Folders))
	}
	for k := range raw.Folders[0] {
		if k == "sourcePath" || k == "source_path" {
			t.Errorf("internal sourcePath leaked into %s", aux)
		}
	}
}

func TestRemoveCurrent_RemovesFromAuxFile(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	aux := writeConfigFileForTest(t, "config-team.json",
		Folder{RootPath: cwd, WorkspaceID: "ws-aux"},
		Folder{RootPath: filepath.Join(cwd, "..", "keep-me"), WorkspaceID: "keep"},
	)

	if err := RemoveCurrent(); err != nil {
		t.Fatalf("RemoveCurrent: %v", err)
	}
	got := readConfigFileForTest(t, aux)
	if len(got.Folders) != 1 || got.Folders[0].WorkspaceID != "keep" {
		t.Errorf("expected only the matched folder removed, got %+v", got.Folders)
	}
	if f := Current(); f != nil {
		t.Errorf("Current() should be nil after logout, got %+v", f)
	}
}

func TestRemoveCurrent_RemovesShadowedDuplicates(t *testing.T) {
	primary := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	if err := UpdateCurrent(func(f *Folder) { f.AccessToken = "primary-tok" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	aux := writeConfigFileForTest(t, "config-dup.json", Folder{
		RootPath:    cwd,
		AccessToken: "aux-tok",
	})

	if err := RemoveCurrent(); err != nil {
		t.Fatalf("RemoveCurrent: %v", err)
	}
	if got := readConfigFileForTest(t, primary); len(got.Folders) != 0 {
		t.Errorf("config.json still has the folder: %+v", got.Folders)
	}
	if got := readConfigFileForTest(t, aux); len(got.Folders) != 0 {
		t.Errorf("shadowed credentials survived logout in %s: %+v", aux, got.Folders)
	}
	if f := Current(); f != nil {
		t.Errorf("Current() should be nil after logout, got %+v", f)
	}
}

// TestPruneAbandonedFolder_LeavesAuxFileAlone pins that the automatic
// abandoned-workspace cleanup (which runs on every tool call) never deletes a
// binding from a hand-provisioned config-<name>.json — an empty RootPath there
// commonly means "provisioned, not pulled yet".
func TestPruneAbandonedFolder_LeavesAuxFileAlone(t *testing.T) {
	tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	empty := filepath.Join(cwd, "not-pulled-yet")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("COREZOID_WORK_DIR", empty)

	aux := writeConfigFileForTest(t, "config-provisioned.json", Folder{
		RootPath:    empty,
		WorkspaceID: "ws",
		AccessToken: "tok",
	})

	if pruneAbandonedFolder() {
		t.Error("prune must not touch a binding owned by an aux config file")
	}
	if got := readConfigFileForTest(t, aux); len(got.Folders) != 1 {
		t.Errorf("aux binding was deleted: %+v", got.Folders)
	}
	// Explicit logout still clears it.
	if err := RemoveCurrent(); err != nil {
		t.Fatalf("RemoveCurrent: %v", err)
	}
	if got := readConfigFileForTest(t, aux); len(got.Folders) != 0 {
		t.Errorf("explicit logout should remove the aux binding: %+v", got.Folders)
	}
}

// TestPruneAbandonedFolder_StillPrunesPrimary pins that restricting prune to
// the primary file did not disable it there.
func TestPruneAbandonedFolder_StillPrunesPrimary(t *testing.T) {
	primary := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	gone := filepath.Join(cwd, "deleted-workspace")
	t.Setenv("COREZOID_WORK_DIR", gone)

	if err := UpdateCurrent(func(f *Folder) { f.AccessToken = "tok" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	if !pruneAbandonedFolder() {
		t.Fatal("expected the missing-RootPath binding in config.json to be pruned")
	}
	if got := readConfigFileForTest(t, primary); len(got.Folders) != 0 {
		t.Errorf("expected the folder removed from config.json, got %+v", got.Folders)
	}
}

// TestUpdateCurrent_AppendsWhenNothingMatches pins that an entry which cannot
// match the cwd (unnormalized RootPath) is not silently hijacked as the write
// target — the old behaviour was to append a fresh, matchable Folder.
func TestUpdateCurrent_AppendsWhenNothingMatches(t *testing.T) {
	primary := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	writeConfigFileForTest(t, "config.json", Folder{
		// Same directory after Clean(), but matchFolder does not normalize,
		// so this entry can never bind to the cwd.
		RootPath:    filepath.Dir(cwd) + "/./" + filepath.Base(cwd),
		WorkspaceID: "unmatchable",
	})

	if err := UpdateCurrent(func(f *Folder) { f.WorkspaceID = "fresh" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	got := readConfigFileForTest(t, primary)
	if len(got.Folders) != 2 {
		t.Fatalf("expected a new folder appended, got %d: %+v", len(got.Folders), got.Folders)
	}
	if got.Folders[1].RootPath != cwd || got.Folders[1].WorkspaceID != "fresh" {
		t.Errorf("appended folder wrong: %+v", got.Folders[1])
	}
	if f := Current(); f == nil || f.WorkspaceID != "fresh" {
		t.Errorf("Current() must resolve the new folder, got %+v", f)
	}
}

// TestLoadConfig_AuxOnlyDrivesAuthGlobals is the end-to-end check that a
// workspace declared only in an aux file authenticates the server: no
// config.json at all, credentials come from config-<name>.json.
func TestLoadConfig_AuxOnlyDrivesAuthGlobals(t *testing.T) {
	primary := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")

	writeConfigFileForTest(t, "config-only.json", Folder{
		RootPath:    cwd,
		AccountURL:  "https://acct.example.com",
		CorezoidURL: "https://api.example.com",
		WorkspaceID: "ws-aux",
		StageID:     4242,
		AccessToken: "aux-token",
	})
	if _, err := os.Stat(primary); !os.IsNotExist(err) {
		t.Fatalf("precondition: config.json should not exist")
	}

	loadConfig()

	url, tok, ws, acct, stage := authSnapshot()
	if tok != "aux-token" {
		t.Errorf("access_token from aux file not loaded: %q", tok)
	}
	if url != "https://api.example.com" || acct != "https://acct.example.com" || ws != "ws-aux" {
		t.Errorf("aux folder not mirrored into globals: url=%q acct=%q ws=%q", url, acct, ws)
	}
	if stage != 4242 {
		t.Errorf("stage_id from aux file not loaded: %d", stage)
	}
	if got := currentConfigFilePath(); filepath.Base(got) != "config-only.json" {
		t.Errorf("user-facing config path should name the owning file, got %q", got)
	}
}

// TestPruneAbandonedFolder_KeepsShadowedAuxBinding is the regression test for
// the collateral damage found by an end-to-end run: the workspace was declared
// in both config.json and an aux file, and the automatic prune of the empty
// RootPath emptied *both* files. Automatic cleanup must only ever touch
// config.json; the operator's aux binding stays and takes over.
func TestPruneAbandonedFolder_KeepsShadowedAuxBinding(t *testing.T) {
	primary := tmpHome(t)
	cwd := os.Getenv("COREZOID_WORK_DIR")
	empty := filepath.Join(cwd, "empty-ws")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("COREZOID_WORK_DIR", empty)

	if err := UpdateCurrent(func(f *Folder) {
		f.AccessToken = "primary-token"
		f.CorezoidURL = "https://api.primary"
	}); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	aux := writeConfigFileForTest(t, "config-team.json", Folder{
		RootPath:    empty,
		AccessToken: "aux-token",
		CorezoidURL: "https://api.aux",
	})

	if !pruneAbandonedFolder() {
		t.Fatal("expected the stale config.json binding to be pruned")
	}
	if got := readConfigFileForTest(t, primary); len(got.Folders) != 0 {
		t.Errorf("config.json binding should be gone, got %+v", got.Folders)
	}
	got := readConfigFileForTest(t, aux)
	if len(got.Folders) != 1 || got.Folders[0].AccessToken != "aux-token" {
		t.Fatalf("aux binding must survive automatic prune, got %+v", got.Folders)
	}
	// The aux binding is now the effective one.
	if f := Current(); f == nil || f.AccessToken != "aux-token" {
		t.Errorf("expected the aux binding to take over, got %+v", f)
	}
}
