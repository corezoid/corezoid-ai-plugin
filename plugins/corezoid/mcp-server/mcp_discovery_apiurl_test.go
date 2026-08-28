package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The headless setup the README documents as sufficient: account_url + stage_id
// + a token, and no COREZOID_API_URL. Resolving the API base URL used to happen
// only inside the login tool, which a CI job or container cannot run — so
// ensureAuth reported success and the first API call went out with an empty
// base URL. The lookup now happens where it is needed.
//
// stubAPIURLDiscovery replaces the account-clients call and counts it, so the
// tests can assert both the result and that the network is touched once.
func stubAPIURLDiscovery(t *testing.T, url string, err error) *int {
	t.Helper()
	calls := 0
	orig := fetchCorezoidAPIURLFn
	fetchCorezoidAPIURLFn = func(string, string) (string, error) {
		calls++
		return url, err
	}
	t.Cleanup(func() { fetchCorezoidAPIURLFn = orig })
	resetAPIURLDiscovery()
	t.Cleanup(resetAPIURLDiscovery)
	return &calls
}

func TestEnsureAuth_EnvOnlyDerivesAPIURLFromAccountURL(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	saveAuthGlobals(t)
	t.Setenv("HOME", t.TempDir())
	calls := stubAPIURLDiscovery(t, "https://admin.example.com", nil)

	// Exactly the minimal set the README promises.
	t.Setenv(envAccountURL, "https://account.example.com")
	t.Setenv(envStageID, "777")
	t.Setenv(envAccessToken, "tok-env")

	syncGlobalsFromFolder(nil)
	if apiURL != "" {
		t.Fatalf("precondition: no COREZOID_API_URL, so apiURL must start empty, got %q", apiURL)
	}

	if err := ensureAuth(); err != nil {
		t.Fatalf("the documented minimal env set must authenticate: %v", err)
	}
	if apiURL != "https://admin.example.com" {
		t.Errorf("apiURL = %q, want the discovered URL", apiURL)
	}

	// The result is cached for the session: a per-tool-call HTTP round trip to
	// the account host would be paid on every single operation otherwise.
	syncGlobalsFromFolder(nil)
	if err := ensureAuth(); err != nil {
		t.Fatalf("second ensureAuth failed: %v", err)
	}
	if *calls != 1 {
		t.Errorf("discovery must run once per session, ran %d times", *calls)
	}
}

// An explicit COREZOID_API_URL is what the README calls the way to skip the
// lookup — so it must actually skip it, not merely win the merge.
func TestEnsureAuth_ExplicitAPIURLSkipsDiscovery(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	saveAuthGlobals(t)
	t.Setenv("HOME", t.TempDir())
	calls := stubAPIURLDiscovery(t, "https://discovered.example.com", nil)

	t.Setenv(envAccountURL, "https://account.example.com")
	t.Setenv(envCorezoidURL, "https://configured.example.com")
	t.Setenv(envStageID, "777")
	t.Setenv(envAccessToken, "tok-env")

	syncGlobalsFromFolder(nil)
	if err := ensureAuth(); err != nil {
		t.Fatalf("ensureAuth failed: %v", err)
	}
	if apiURL != "https://configured.example.com" {
		t.Errorf("apiURL = %q, want the configured value untouched", apiURL)
	}
	if *calls != 0 {
		t.Errorf("discovery must not run when the URL was supplied, ran %d times", *calls)
	}
}

// The clients endpoint needs a bearer token, so an API-key-only setup cannot
// use it. handleLogin falls back to account_url there, and this path has to
// agree with it — in most deployments the API is served from the admin host.
func TestEnsureAuth_APIKeyOnlyFallsBackToAccountURL(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	saveAuthGlobals(t)
	t.Setenv("HOME", t.TempDir())
	calls := stubAPIURLDiscovery(t, "https://discovered.example.com", nil)

	t.Setenv(envAccountURL, "https://account.example.com/")
	t.Setenv(envStageID, "777")
	t.Setenv(envAPILogin, "login-env")
	t.Setenv(envAPISecret, "secret-env")

	syncGlobalsFromFolder(nil)
	if err := ensureAuth(); err != nil {
		t.Fatalf("API-key credentials must authenticate without COREZOID_API_URL: %v", err)
	}
	if apiURL != "https://account.example.com" {
		t.Errorf("apiURL = %q, want account_url with the trailing slash trimmed", apiURL)
	}
	if *calls != 0 {
		t.Errorf("the clients endpoint needs a token — it must not be called, ran %d times", *calls)
	}
}

// A failed lookup must not silently guess a host: a wrong base URL sends the
// user's credentials somewhere they never named. It must also not be cached as
// a failure — a flaky network on the first tool call would otherwise make the
// whole session unusable.
func TestEnsureAuth_APIURLDiscoveryFailureIsActionableAndRetried(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	saveAuthGlobals(t)
	t.Setenv("HOME", t.TempDir())

	failure := fmt.Errorf("clients API request failed: connection refused")
	calls := 0
	orig := fetchCorezoidAPIURLFn
	fetchCorezoidAPIURLFn = func(string, string) (string, error) {
		calls++
		if calls == 1 {
			return "", failure
		}
		return "https://admin.example.com", nil
	}
	t.Cleanup(func() { fetchCorezoidAPIURLFn = orig })
	resetAPIURLDiscovery()
	t.Cleanup(resetAPIURLDiscovery)

	t.Setenv(envAccountURL, "https://account.example.com")
	t.Setenv(envStageID, "777")
	t.Setenv(envAccessToken, "tok-env")

	syncGlobalsFromFolder(nil)
	err := ensureAuth()
	if err == nil {
		t.Fatal("a failed API-URL lookup must not report successful authentication")
	}
	for _, want := range []string{envCorezoidURL, "corezoid-init", "connection refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q so the user can act on it, got: %v", want, err)
		}
	}
	if apiURL != "" {
		t.Errorf("a failed lookup must not guess a host, apiURL = %q", apiURL)
	}

	// Same session, network back: the next tool call must work.
	syncGlobalsFromFolder(nil)
	if err := ensureAuth(); err != nil {
		t.Fatalf("a transient lookup failure must not poison the session: %v", err)
	}
	if apiURL != "https://admin.example.com" {
		t.Errorf("apiURL = %q after recovery, want the discovered URL", apiURL)
	}
}

// The discovery cache is keyed on account_url so a second working directory
// pointing at a different Corezoid never inherits the first one's API host —
// which would send credentials to the wrong installation.
func TestEnsureAPIURL_CacheIsScopedToAccountURL(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	saveAuthGlobals(t)
	t.Setenv("HOME", t.TempDir())

	byAccount := map[string]string{
		"https://account-a.example.com": "https://admin-a.example.com",
		"https://account-b.example.com": "https://admin-b.example.com",
	}
	orig := fetchCorezoidAPIURLFn
	fetchCorezoidAPIURLFn = func(account, _ string) (string, error) {
		return byAccount[account], nil
	}
	t.Cleanup(func() { fetchCorezoidAPIURLFn = orig })
	resetAPIURLDiscovery()
	t.Cleanup(resetAPIURLDiscovery)

	t.Setenv(envStageID, "777")
	t.Setenv(envAccessToken, "tok-env")

	for account, wantAPI := range byAccount {
		t.Setenv(envAccountURL, account)
		syncGlobalsFromFolder(nil)
		if err := ensureAuth(); err != nil {
			t.Fatalf("ensureAuth for %s failed: %v", account, err)
		}
		if apiURL != wantAPI {
			t.Errorf("account %s resolved to apiURL %q, want %q", account, apiURL, wantAPI)
		}
	}
}

// The token-only tools are exempt from the stage_id requirement because they
// are what the init flow uses to FIND a stage — but they still call the API.
// Gating them on ensureTokenAuth alone left them talking to an empty host in
// exactly the headless setup this discovery exists for.
func TestHandleToolCall_TokenOnlyToolResolvesAPIURL(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	resetGlobals(t)
	t.Setenv("HOME", t.TempDir())

	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return okResponse(ops)
	})

	calls := 0
	orig := fetchCorezoidAPIURLFn
	fetchCorezoidAPIURLFn = func(string, string) (string, error) {
		calls++
		return srv.URL, nil
	}
	t.Cleanup(func() { fetchCorezoidAPIURLFn = orig })
	resetAPIURLDiscovery()
	t.Cleanup(resetAPIURLDiscovery)

	// A token and an account, no api_url and — deliberately — no stage_id:
	// list-workspaces is how the user gets one.
	withAuthLock(func() {
		apiToken = "tok-env"
		accountURL = "https://account.example.com"
		apiURL = ""
		stageID = 0
	})

	result, _ := handleToolCall(context.Background(), "list-workspaces", map[string]interface{}{})
	if strings.Contains(result, "Not authenticated") {
		t.Fatalf("a token-only tool must not report missing credentials here, got:\n%s", result)
	}
	if calls != 1 {
		t.Errorf("the token-only path must resolve the API URL, discovery ran %d times", calls)
	}
	if apiURL != srv.URL {
		t.Errorf("apiURL = %q, want the discovered %q", apiURL, srv.URL)
	}
}

// account_url is the only thing discovery can derive from, and ensureTokenAuth
// deliberately does not require it — the token-only tools are gated on
// credentials alone. Resolution must therefore stay silent when there is
// nothing to derive from, rather than turning a field ensureTokenAuth never
// checked into a new hard failure. ensureAuth still reports it as missing in
// its own words.
func TestEnsureAPIURL_NoAccountURLIsNotAnError(t *testing.T) {
	clearCorezoidEnv(t)
	resetEnvConfigDiagnostics(t)
	saveAuthGlobals(t)
	calls := stubAPIURLDiscovery(t, "https://admin.example.com", nil)

	withAuthLock(func() {
		apiToken = "tok-env"
		accountURL = ""
		apiURL = ""
	})

	if err := ensureAPIURL(); err != nil {
		t.Errorf("nothing to derive from must not be an error here, got: %v", err)
	}
	if *calls != 0 {
		t.Errorf("discovery cannot run without an account_url, ran %d times", *calls)
	}
	if apiURL != "" {
		t.Errorf("apiURL must stay empty, got %q", apiURL)
	}

	// ensureAuth is where the missing field is reported, and it must name it.
	err := ensureAuth()
	if err == nil || !strings.Contains(err.Error(), "account_url") {
		t.Errorf("ensureAuth must report the missing account_url, got: %v", err)
	}
}
