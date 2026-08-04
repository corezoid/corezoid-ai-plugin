package main

import (
	"context"
	"os"
	"sync"
	"testing"
)

// chdirWithCleanup snapshots os.Getwd, chdirs into dir, and restores the
// original working directory on test cleanup. Shared test helper.
func chdirWithCleanup(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// ---- withAuthLock ----------------------------------------------------------

func TestWithAuthLock_RunsFn(t *testing.T) {
	called := false
	withAuthLock(func() { called = true })
	if !called {
		t.Error("expected fn to run")
	}
}

func TestWithAuthLock_SerializesAccess(t *testing.T) {
	// Two goroutines both increment via withAuthLock; without the lock we'd
	// expect races. With the lock, the final count equals the sum.
	var counter int
	var wg sync.WaitGroup
	const iters = 200
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				withAuthLock(func() { counter++ })
			}
		}()
	}
	wg.Wait()
	if counter != 2*iters {
		t.Errorf("expected counter %d, got %d", 2*iters, counter)
	}
}

// ---- loadConfig ------------------------------------------------------------

// Snapshot/restore the globals loadConfig writes so the test is isolated.
func snapshotConfigGlobals(t *testing.T) {
	t.Helper()
	prevAPI, prevAcc, prevWS, prevTok, prevGW := apiURL, accountURL, workspaceID, apiToken, apigwURL
	prevStage, prevInsecure := stageID, insecureTLS
	prevAPILogin, prevAPISecret := apiLogin, apiSecret
	prevGitURL, prevGitStage := gitURL, gitStagePath
	prevCachedPID := cachedProjectID
	t.Cleanup(func() {
		apiURL, accountURL, workspaceID, apiToken, apigwURL = prevAPI, prevAcc, prevWS, prevTok, prevGW
		stageID, insecureTLS = prevStage, prevInsecure
		apiLogin, apiSecret = prevAPILogin, prevAPISecret
		gitURL, gitStagePath = prevGitURL, prevGitStage
		cachedProjectID = prevCachedPID
	})
}

func TestLoadConfig_PopulatesGlobalsFromConfig(t *testing.T) {
	snapshotConfigGlobals(t)
	rootDir := tmpHomeAndCWD(t)

	// Seed a Folder for the current working directory.
	if err := UpdateCurrent(func(f *Folder) {
		f.CorezoidURL = "https://api.example"
		f.AccountURL = "https://account.example"
		f.WorkspaceID = "ws-1"
		f.AccessToken = "tok-abc"
		f.APIGwURL = "https://gw.example"
		f.APILogin = "login-x"
		f.APISecret = "secret-y"
	}); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	writeTestStageMarker(t, rootDir, 4242, 999, "develop")
	t.Setenv("COREZOID_INSECURE_TLS", "1")

	loadConfig()

	if apiURL != "https://api.example" {
		t.Errorf("apiURL = %q", apiURL)
	}
	if accountURL != "https://account.example" {
		t.Errorf("accountURL = %q", accountURL)
	}
	if workspaceID != "ws-1" {
		t.Errorf("workspaceID = %q", workspaceID)
	}
	if apiToken != "tok-abc" {
		t.Errorf("apiToken = %q", apiToken)
	}
	if stageID != 4242 {
		t.Errorf("stageID = %d, want 4242", stageID)
	}
	if !insecureTLS {
		t.Error("expected insecureTLS=true when COREZOID_INSECURE_TLS is set")
	}
	if apigwURL != "https://gw.example" {
		t.Errorf("apigwURL = %q", apigwURL)
	}
	if apiLogin != "login-x" || apiSecret != "secret-y" {
		t.Errorf("api-key mismatch: login=%q secret=%q", apiLogin, apiSecret)
	}
}

// TestLoadConfig_LoadsProjectIDFromStageMarker pins that stage_id and
// project_id are both loaded from the current Folder in
// ~/.corezoid/config.json — one stage per workspace, both persisted on login.
func TestLoadConfig_LoadsProjectIDFromStageMarker(t *testing.T) {
	snapshotConfigGlobals(t)
	rootDir := tmpHomeAndCWD(t)

	if err := UpdateCurrent(func(f *Folder) {
		f.WorkspaceID = "ws"
	}); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	writeTestStageMarker(t, rootDir, 4242, 555, "develop")

	loadConfig()

	if stageID != 4242 {
		t.Errorf("expected stageID=4242, got %d", stageID)
	}
	if cachedProjectID != 555 {
		t.Errorf("expected cachedProjectID=555, got %d", cachedProjectID)
	}
}

// TestLoadConfig_KeepsExistingFolderProjectID pins that setting stage_id
// afterwards does not clobber a pre-existing Folder.ProjectID — the fields
// live side-by-side in the same Folder.
func TestLoadConfig_KeepsExistingFolderProjectID(t *testing.T) {
	snapshotConfigGlobals(t)
	rootDir := tmpHomeAndCWD(t)

	if err := UpdateCurrent(func(f *Folder) {
		f.WorkspaceID = "ws"
		f.ProjectID = 777
	}); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	writeTestStageMarker(t, rootDir, 4242, 0, "develop")

	loadConfig()

	if stageID != 4242 {
		t.Errorf("expected stageID=4242, got %d", stageID)
	}
	if cachedProjectID != 777 {
		t.Errorf("expected cachedProjectID=777, got %d", cachedProjectID)
	}
}

func TestLoadConfig_DefaultsApigwURL(t *testing.T) {
	snapshotConfigGlobals(t)
	tmpHome(t)

	// Create a Folder with no APIGwURL set.
	if err := UpdateCurrent(func(f *Folder) { f.WorkspaceID = "ws" }); err != nil {
		t.Fatalf("UpdateCurrent: %v", err)
	}
	loadConfig()

	if apigwURL != defaultAPIGwURL {
		t.Errorf("expected default apigwURL %q, got %q", defaultAPIGwURL, apigwURL)
	}
}

// TestDebugFlagFollowsEnv pins the COREZOID_DEBUG wiring for the Executor
// trace end-to-end. `debug` is one of the few remaining env-driven flags
// (dev-only, not user-visible auth).
func TestDebugFlagFollowsEnv(t *testing.T) {
	snapshotConfigGlobals(t)
	tmpHome(t)

	t.Setenv("COREZOID_DEBUG", "")
	loadConfig()
	if v := NewValidator(context.Background(), 0); v.Debug {
		t.Fatal("empty COREZOID_DEBUG must not enable the executor trace")
	}
	t.Setenv("COREZOID_DEBUG", "1")
	loadConfig()
	if v := NewValidator(context.Background(), 0); !v.Debug {
		t.Fatal("COREZOID_DEBUG=1 must enable the executor trace")
	}
}
