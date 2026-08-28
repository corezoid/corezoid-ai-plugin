package main

import (
	"os"
	"testing"
	"time"
)

// clearCorezoidEnv unsets every COREZOID_* variable applyEnvFallback reads so a
// developer's shell (or another test) cannot leak into the assertions.
func clearCorezoidEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		envAccountURL, envCorezoidURL, envAPIGwURL, envWorkspaceID,
		envProjectID, envStageID, envGitURL, envGitStagePath,
		envAccessToken, envExpiresAt, envAPILogin, envAPISecret,
	} {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v) // registers the restore
			os.Unsetenv(k) //nolint:errcheck
		}
	}
}

// saveAuthGlobals restores the auth-state globals after a test mutates them
// through syncGlobalsFromFolder.
func saveAuthGlobals(t *testing.T) {
	t.Helper()
	o := struct {
		token, account, api, apigw, ws, login, secret, git, gitPath string
		stage, project                                              int
	}{apiToken, accountURL, apiURL, apigwURL, workspaceID, apiLogin, apiSecret, gitURL, gitStagePath, stageID, cachedProjectID}
	t.Cleanup(func() {
		apiToken, accountURL, apiURL, apigwURL = o.token, o.account, o.api, o.apigw
		workspaceID, apiLogin, apiSecret = o.ws, o.login, o.secret
		gitURL, gitStagePath = o.git, o.gitPath
		stageID, cachedProjectID = o.stage, o.project
	})
}

func TestEnvFolder_ReadsAllSupportedFields(t *testing.T) {
	clearCorezoidEnv(t)
	t.Setenv(envAccountURL, "https://account.example.com")
	t.Setenv(envCorezoidURL, "https://admin.example.com")
	t.Setenv(envAPIGwURL, "https://apigw.example.com")
	t.Setenv(envWorkspaceID, "9911")
	t.Setenv(envProjectID, "4242")
	t.Setenv(envStageID, "777")
	t.Setenv(envGitURL, "https://git.example.com/org")
	t.Setenv(envGitStagePath, "projects/4242_Foo/stages/777_Bar")
	t.Setenv(envAccessToken, "tok-env")
	t.Setenv(envExpiresAt, "2030-01-02T03:04:05Z")
	t.Setenv(envAPILogin, "login-env")
	t.Setenv(envAPISecret, "secret-env")

	f := envFolder()
	if f.AccountURL != "https://account.example.com" || f.CorezoidURL != "https://admin.example.com" {
		t.Errorf("URLs not read: %+v", f)
	}
	if f.APIGwURL != "https://apigw.example.com" {
		t.Errorf("apigw not read: %q", f.APIGwURL)
	}
	if f.WorkspaceID != "9911" || f.ProjectID != 4242 || f.StageID != 777 {
		t.Errorf("ids not read: ws=%q project=%d stage=%d", f.WorkspaceID, f.ProjectID, f.StageID)
	}
	if f.GitURL != "https://git.example.com/org" || f.GitStagePath != "projects/4242_Foo/stages/777_Bar" {
		t.Errorf("git fields not read: %+v", f)
	}
	if f.AccessToken != "tok-env" || f.APILogin != "login-env" || f.APISecret != "secret-env" {
		t.Errorf("credentials not read: %+v", f)
	}
	want := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if !f.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", f.ExpiresAt, want)
	}
}

func TestEnvFolder_IgnoresBlankAndMalformedValues(t *testing.T) {
	clearCorezoidEnv(t)
	t.Setenv(envAccountURL, "   ")
	t.Setenv(envStageID, "not-a-number")
	t.Setenv(envProjectID, "-5")
	t.Setenv(envExpiresAt, "yesterday")

	f := envFolder()
	if f.AccountURL != "" {
		t.Errorf("blank account_url should be treated as unset, got %q", f.AccountURL)
	}
	if f.StageID != 0 || f.ProjectID != 0 {
		t.Errorf("malformed ids should be ignored, got stage=%d project=%d", f.StageID, f.ProjectID)
	}
	if !f.ExpiresAt.IsZero() {
		t.Errorf("malformed expires_at should be ignored, got %v", f.ExpiresAt)
	}
	if !folderIsEmpty(f) {
		t.Errorf("folder built from junk env should be empty: %+v", f)
	}
}

func TestApplyEnvFallback_NoEnvLeavesInputUntouched(t *testing.T) {
	clearCorezoidEnv(t)

	if got := applyEnvFallback(nil); got != nil {
		t.Errorf("nil folder + no env should stay nil, got %+v", got)
	}
	stored := &Folder{RootPath: "/ws", AccountURL: "https://a", StageID: 1}
	got := applyEnvFallback(stored)
	if got != stored {
		t.Errorf("no env should return the input pointer unchanged")
	}
	if envConfigActive() {
		t.Error("envConfigActive() should be false with no COREZOID_* set")
	}
}

func TestApplyEnvFallback_NilFolderBuiltFromEnv(t *testing.T) {
	clearCorezoidEnv(t)
	t.Setenv(envAccountURL, "https://account.example.com")
	t.Setenv(envStageID, "555")
	t.Setenv(envAccessToken, "tok-env")

	got := applyEnvFallback(nil)
	if got == nil {
		t.Fatal("expected an env-derived Folder when no config entry matches")
	}
	if got.AccountURL != "https://account.example.com" || got.StageID != 555 || got.AccessToken != "tok-env" {
		t.Errorf("env values not applied: %+v", got)
	}
	if got.RootPath != resolveWorkDir() {
		t.Errorf("RootPath = %q, want resolved work dir %q", got.RootPath, resolveWorkDir())
	}
	if !envConfigActive() {
		t.Error("envConfigActive() should be true")
	}
}

func TestApplyEnvFallback_ConfigWinsEnvFillsGaps(t *testing.T) {
	clearCorezoidEnv(t)
	t.Setenv(envAccountURL, "https://env.example.com")
	t.Setenv(envWorkspaceID, "env-ws")
	t.Setenv(envStageID, "999")
	t.Setenv(envAPISecret, "secret-env")

	stored := &Folder{
		RootPath:    "/ws",
		AccountURL:  "https://config.example.com",
		WorkspaceID: "config-ws",
		AccessToken: "tok-config",
	}
	got := applyEnvFallback(stored)

	if got.AccountURL != "https://config.example.com" || got.WorkspaceID != "config-ws" {
		t.Errorf("config values must win over env: %+v", got)
	}
	if got.StageID != 999 || got.APISecret != "secret-env" {
		t.Errorf("env must fill gaps: stage=%d secret=%q", got.StageID, got.APISecret)
	}
	if got.AccessToken != "tok-config" {
		t.Errorf("stored token must be kept, got %q", got.AccessToken)
	}
	if stored.StageID != 0 || stored.APISecret != "" {
		t.Errorf("input Folder must not be mutated: %+v", stored)
	}
}

func TestApplyEnvFallback_ExpiredStoredTokenYieldsToEnv(t *testing.T) {
	clearCorezoidEnv(t)
	t.Setenv(envAccessToken, "tok-env")

	stored := &Folder{
		RootPath:    "/ws",
		AccessToken: "tok-expired",
		ExpiresAt:   time.Now().Add(-time.Hour),
	}
	got := applyEnvFallback(stored)
	if got.AccessToken != "tok-env" {
		t.Errorf("expired stored token should yield to env, got %q", got.AccessToken)
	}
	if !got.ExpiresAt.IsZero() {
		t.Errorf("env token without expiry should carry a zero ExpiresAt, got %v", got.ExpiresAt)
	}
}

func TestApplyEnvFallback_UnexpiredStoredTokenKept(t *testing.T) {
	clearCorezoidEnv(t)
	t.Setenv(envAccessToken, "tok-env")

	exp := time.Now().Add(time.Hour)
	stored := &Folder{RootPath: "/ws", AccessToken: "tok-config", ExpiresAt: exp}
	got := applyEnvFallback(stored)
	if got.AccessToken != "tok-config" || !got.ExpiresAt.Equal(exp) {
		t.Errorf("valid stored token must be kept: token=%q exp=%v", got.AccessToken, got.ExpiresAt)
	}
}

func TestSyncGlobalsFromFolder_EnvFallbackPopulatesGlobals(t *testing.T) {
	clearCorezoidEnv(t)
	saveAuthGlobals(t)

	t.Setenv(envAccountURL, "https://account.example.com")
	t.Setenv(envCorezoidURL, "https://admin.example.com")
	t.Setenv(envAPIGwURL, "https://apigw.example.com")
	t.Setenv(envWorkspaceID, "9911")
	t.Setenv(envProjectID, "4242")
	t.Setenv(envStageID, "777")
	t.Setenv(envAPILogin, "login-env")
	t.Setenv(envAPISecret, "secret-env")

	// nil Folder = nothing in config.json matches this working directory.
	syncGlobalsFromFolder(nil)

	if accountURL != "https://account.example.com" || apiURL != "https://admin.example.com" {
		t.Errorf("URL globals not set from env: account=%q api=%q", accountURL, apiURL)
	}
	if apigwURL != "https://apigw.example.com" {
		t.Errorf("apigwURL = %q, want the env override", apigwURL)
	}
	if workspaceID != "9911" || stageID != 777 || cachedProjectID != 4242 {
		t.Errorf("id globals not set: ws=%q stage=%d project=%d", workspaceID, stageID, cachedProjectID)
	}
	if apiLogin != "login-env" || apiSecret != "secret-env" {
		t.Errorf("api key globals not set: login=%q secret set=%v", apiLogin, apiSecret != "")
	}
	if err := ensureAuth(); err != nil {
		t.Errorf("ensureAuth() with env-provided API key credentials failed: %v", err)
	}
}

func TestSyncGlobalsFromFolder_NoEnvStillResetsGlobals(t *testing.T) {
	clearCorezoidEnv(t)
	saveAuthGlobals(t)

	apiToken, accountURL, workspaceID, stageID = "stale", "https://stale", "stale-ws", 42

	syncGlobalsFromFolder(nil)

	if apiToken != "" || accountURL != "" || workspaceID != "" || stageID != 0 {
		t.Errorf("nil folder without env must reset globals: token=%q account=%q ws=%q stage=%d",
			apiToken, accountURL, workspaceID, stageID)
	}
	if apigwURL != defaultAPIGwURL {
		t.Errorf("apigwURL = %q, want default %q", apigwURL, defaultAPIGwURL)
	}
}

func TestSyncGlobalsFromFolder_EnvTokenExpiryStillEnforced(t *testing.T) {
	clearCorezoidEnv(t)
	saveAuthGlobals(t)

	t.Setenv(envAccessToken, "tok-env")
	t.Setenv(envExpiresAt, "2000-01-01T00:00:00Z")

	syncGlobalsFromFolder(nil)

	if apiToken != "" {
		t.Errorf("an env token with a past expiry must not be used, got %q", apiToken)
	}
}
