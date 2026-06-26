package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ---- envFilePath -----------------------------------------------------------

func TestEnvFilePath(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	got := envFilePath()
	// On macOS /var and /private/var are the same path via symlink, so compare
	// via EvalSymlinks to avoid flaking.
	wantBase := filepath.Base(got)
	if wantBase != ".env" {
		t.Errorf("expected basename .env, got %q", wantBase)
	}
	gotDir, _ := filepath.EvalSymlinks(filepath.Dir(got))
	wantDir, _ := filepath.EvalSymlinks(dir)
	if gotDir != wantDir {
		t.Errorf("expected dir %q, got %q", wantDir, gotDir)
	}
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

// ---- findAndLoadDotEnv -----------------------------------------------------

// Helper to snapshot+restore os.Getwd so changing dirs in tests doesn't leak.
func chdirWithCleanup(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestFindAndLoadDotEnv_LocatesInParentDir(t *testing.T) {
	root := t.TempDir()
	// Make root a project root so the search has a definitive stopping point.
	if err := os.WriteFile(filepath.Join(root, "1_project.stage.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	// .env lives at the root.
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TEST_FA_VAR=found\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// We start two levels deep.
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	chdirWithCleanup(t, deep)

	os.Unsetenv("TEST_FA_VAR")
	t.Cleanup(func() { os.Unsetenv("TEST_FA_VAR") })

	findAndLoadDotEnv()

	if got := os.Getenv("TEST_FA_VAR"); got != "found" {
		t.Errorf("expected TEST_FA_VAR=found, got %q", got)
	}
}

func TestFindAndLoadDotEnv_StopsAtProjectRoot(t *testing.T) {
	// Layout:
	//   ancestor/.env  (must NOT be loaded)
	//   ancestor/root/<stage.json>  (project root — search stops here)
	//   ancestor/root/sub  (cwd)
	ancestor := t.TempDir()
	if err := os.WriteFile(filepath.Join(ancestor, ".env"), []byte("TEST_FA_BLOCKED=oops\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(ancestor, "root")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "1_p.stage.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	chdirWithCleanup(t, sub)

	os.Unsetenv("TEST_FA_BLOCKED")
	t.Cleanup(func() { os.Unsetenv("TEST_FA_BLOCKED") })

	findAndLoadDotEnv()

	if got := os.Getenv("TEST_FA_BLOCKED"); got != "" {
		t.Errorf("expected TEST_FA_BLOCKED to remain unset (search must stop at project root), got %q", got)
	}
}

// ---- loadConfig ------------------------------------------------------------

// Snapshot/restore the globals loadConfig writes so the test is isolated.
func snapshotConfigGlobals(t *testing.T) {
	t.Helper()
	prevAPI, prevAcc, prevWS, prevTok, prevGW := apiURL, accountURL, workspaceID, apiToken, apigwURL
	prevStage, prevInsecure := stageID, insecureTLS
	t.Cleanup(func() {
		apiURL, accountURL, workspaceID, apiToken, apigwURL = prevAPI, prevAcc, prevWS, prevTok, prevGW
		stageID, insecureTLS = prevStage, prevInsecure
	})
}

func TestLoadConfig_ReadsEnvVars(t *testing.T) {
	snapshotConfigGlobals(t)

	// Isolate from any real ~/.corezoid/credentials and project .env.
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	chdirWithCleanup(t, dir)

	t.Setenv("COREZOID_API_URL", "https://api.example")
	t.Setenv("ACCOUNT_URL", "https://account.example")
	t.Setenv("WORKSPACE_ID", "ws-1")
	t.Setenv("ACCESS_TOKEN", "tok-abc")
	t.Setenv("COREZOID_STAGE_ID", "4242")
	t.Setenv("COREZOID_INSECURE_TLS", "1")
	// Force apigw URL default to be overridden so we exercise the explicit branch.
	t.Setenv("COREZOID_APIGW_URL", "https://gw.example")

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
}

func TestLoadConfig_DefaultsApigwURL(t *testing.T) {
	snapshotConfigGlobals(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	chdirWithCleanup(t, dir)

	// Explicitly unset so loadConfig picks the default.
	t.Setenv("COREZOID_APIGW_URL", "")
	loadConfig()

	if apigwURL != "https://api-apigw.corezoid.com" {
		t.Errorf("expected default apigwURL, got %q", apigwURL)
	}
}
