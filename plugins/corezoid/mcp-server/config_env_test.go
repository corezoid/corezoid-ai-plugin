package main

import (
	"os"
	"strings"
	"sync"
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
	// A complete pair: api_login/api_secret are merged as one credential, so
	// half of it would be refused rather than filling a gap (mergeEnvAPIKey).
	t.Setenv(envAPILogin, "login-env")
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
	if got.StageID != 999 || got.APILogin != "login-env" || got.APISecret != "secret-env" {
		t.Errorf("env must fill gaps: stage=%d login=%q secret set=%v", got.StageID, got.APILogin, got.APISecret != "")
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

// resetEnvConfigDiagnostics clears the process-wide diagnostic state so the
// per-variable and per-note dedupes cannot carry a message between tests.
func resetEnvConfigDiagnostics(t *testing.T) {
	t.Helper()
	clear := func() {
		envConfigNoteMu.Lock()
		envConfigNotes = nil
		envConfigNoteMu.Unlock()
		envMalformedWarned = sync.Map{}
	}
	clear()
	t.Cleanup(clear)
}

// The regression this guards: provenance used to be reconstructed by diffing
// the effective Folder against the stored one, and mergeEnvFallback clears an
// expired stored token before filling it — so the diff read the still-present
// stored token as "the config supplied it". In the common headless shape below
// (complete config, dead token, COREZOID_ACCESS_TOKEN set) that emptied the
// field list and suppressed the provenance line entirely.
func TestMergeEnvFallback_ExpiredStoredTokenReportedAsEnvSourced(t *testing.T) {
	clearCorezoidEnv(t)

	stored := Folder{
		RootPath:    "/ws",
		AccountURL:  "https://config.example.com",
		WorkspaceID: "config-ws",
		StageID:     555,
		AccessToken: "tok-expired",
		ExpiresAt:   time.Now().Add(-time.Hour),
	}
	env := Folder{AccessToken: "tok-env"}

	got, from := mergeEnvFallback(stored, env)

	if got.AccessToken != "tok-env" {
		t.Errorf("expired stored token should yield to env, got %q", got.AccessToken)
	}
	if len(from) != 1 || from[0] != "access_token" {
		t.Errorf("provenance must name access_token and nothing else, got %v", from)
	}
}

func TestMergeEnvFallback_ProvenanceNamesOnlyFilledFields(t *testing.T) {
	clearCorezoidEnv(t)

	stored := Folder{RootPath: "/ws", AccountURL: "https://config.example.com"}
	env := Folder{
		AccountURL:  "https://env.example.com", // stored wins — must not be listed
		WorkspaceID: "env-ws",
		StageID:     777,
	}

	got, from := mergeEnvFallback(stored, env)

	if got.AccountURL != "https://config.example.com" {
		t.Errorf("config must win, got %q", got.AccountURL)
	}
	want := []string{"workspace_id", "stage_id"}
	if strings.Join(from, ",") != strings.Join(want, ",") {
		t.Errorf("provenance = %v, want %v", from, want)
	}
}

// A login from the config paired with a secret from the environment can never
// verify — Corezoid checks the signature against the secret belonging to that
// login — so the half-pair must be refused locally instead of producing a 401.
func TestMergeEnvAPIKey_StoredHalfIsNotCompletedFromEnv(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)

	stored := Folder{RootPath: "/ws", APILogin: "login-config"}
	env := Folder{APISecret: "secret-env"}

	got, from := mergeEnvFallback(stored, env)

	if got.APISecret != "" {
		t.Errorf("env secret must not complete a stored login, got a secret of len %d", len(got.APISecret))
	}
	if len(from) != 0 {
		t.Errorf("nothing should be reported as env-sourced, got %v", from)
	}
	note := describeEnvConfigProblems()
	if !strings.Contains(note, envAPISecret) || !strings.Contains(note, "verified as a pair") {
		t.Errorf("refusal must be explained to the agent, got %q", note)
	}
	if strings.Contains(note, "secret-env") || strings.Contains(note, "login-config") {
		t.Errorf("diagnostic must name variables, never values: %q", note)
	}
}

func TestMergeEnvAPIKey_EnvHalfPairRefused(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)

	got, from := mergeEnvFallback(Folder{RootPath: "/ws"}, Folder{APILogin: "login-env"})

	if got.APILogin != "" {
		t.Errorf("a lone env login must not be used, got %q", got.APILogin)
	}
	if len(from) != 0 {
		t.Errorf("nothing should be reported as env-sourced, got %v", from)
	}
	if note := describeEnvConfigProblems(); !strings.Contains(note, envAPISecret) {
		t.Errorf("diagnostic must name the missing counterpart, got %q", note)
	}
}

func TestMergeEnvAPIKey_EnvSuppliesCompletePair(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)

	env := Folder{APILogin: "login-env", APISecret: "secret-env"}
	got, from := mergeEnvFallback(Folder{RootPath: "/ws"}, env)

	if got.APILogin != "login-env" || got.APISecret != "secret-env" {
		t.Errorf("a complete env pair must be used: login=%q secret set=%v", got.APILogin, got.APISecret != "")
	}
	want := []string{"api_login", "api_secret"}
	if strings.Join(from, ",") != strings.Join(want, ",") {
		t.Errorf("provenance = %v, want %v", from, want)
	}
	if note := describeEnvConfigProblems(); note != "" {
		t.Errorf("a complete pair must not be flagged, got %q", note)
	}
}

func TestMergeEnvAPIKey_StoredPairWins(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)

	stored := Folder{RootPath: "/ws", APILogin: "login-config", APISecret: "secret-config"}
	env := Folder{APILogin: "login-env", APISecret: "secret-env"}

	got, from := mergeEnvFallback(stored, env)

	if got.APILogin != "login-config" || got.APISecret != "secret-config" {
		t.Errorf("stored pair must win: login=%q", got.APILogin)
	}
	if len(from) != 0 {
		t.Errorf("nothing env-sourced expected, got %v", from)
	}
	if note := describeEnvConfigProblems(); note != "" {
		t.Errorf("a complete stored pair must not be flagged, got %q", note)
	}
}

func TestWarnMalformedEnv_ReachesTheAgent(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	t.Setenv(envStageID, "77x")

	if got := envFolder().StageID; got != 0 {
		t.Errorf("malformed stage id must be ignored, got %d", got)
	}
	note := describeEnvConfigProblems()
	if !strings.Contains(note, envStageID) || !strings.Contains(note, "77x") {
		t.Errorf("the rejected variable and its value must be reported, got %q", note)
	}
}

// The end-to-end shape of the third fix: a variable that was set and rejected
// must not reach the model as a bare "missing credentials".
func TestEnsureTokenAuth_ErrorCarriesEnvConfigProblems(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	saveAuthGlobals(t)
	// Isolate ~/.corezoid/config.json: ensureTokenAuth re-reads it when the
	// in-memory token is empty, and the developer's real config must not decide
	// the outcome of this test.
	t.Setenv("HOME", t.TempDir())

	t.Setenv(envAPISecret, "secret-env") // half a pair — refused

	syncGlobalsFromFolder(nil)

	err := ensureTokenAuth()
	if err == nil {
		t.Fatal("half an API-key pair must not authenticate")
	}
	if !strings.Contains(err.Error(), envAPILogin) {
		t.Errorf("auth error must explain the rejected env config, got %q", err)
	}
}

// envConfigActive answers "is any COREZOID_* value in play", which is the right
// question for the fallback machinery and the wrong one for logout. Reporting
// "you are still authenticated" because a stray COREZOID_ACCOUNT_URL is exported
// sends the user hunting for a credential that was never set — and makes the
// warning meaningless on the day it is true.
func TestEnvCredentialsActive_SeparatesCredentialsFromConfiguration(t *testing.T) {
	cases := []struct {
		name            string
		env             map[string]string
		wantConfig      bool
		wantCredentials bool
	}{
		{name: "nothing set"},
		{
			name:       "configuration only",
			env:        map[string]string{envAccountURL: "https://account.example.com", envStageID: "777"},
			wantConfig: true,
		},
		{
			name:            "access token",
			env:             map[string]string{envAccessToken: "tok-env"},
			wantConfig:      true,
			wantCredentials: true,
		},
		{
			name:            "complete API-key pair",
			env:             map[string]string{envAPILogin: "login-env", envAPISecret: "secret-env"},
			wantConfig:      true,
			wantCredentials: true,
		},
		{
			// mergeEnvAPIKey refuses half a pair, so it authenticates as
			// nothing and must not be reported as a credential either.
			name:       "half an API-key pair",
			env:        map[string]string{envAPILogin: "login-env"},
			wantConfig: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearCorezoidEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := envConfigActive(); got != tc.wantConfig {
				t.Errorf("envConfigActive() = %v, want %v", got, tc.wantConfig)
			}
			if got := envCredentialsActive(); got != tc.wantCredentials {
				t.Errorf("envCredentialsActive() = %v, want %v", got, tc.wantCredentials)
			}
		})
	}
}
