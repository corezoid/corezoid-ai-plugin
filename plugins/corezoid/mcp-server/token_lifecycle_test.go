package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHome points HOME (and the process cwd, for envFilePath) at temp dirs so
// the credential tests never touch the real ~/.corezoid.
func fakeHome(t *testing.T) (home, workDir string) {
	t.Helper()
	home = t.TempDir()
	workDir = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COREZOID_WORK_DIR", workDir)
	t.Chdir(workDir)
	return home, workDir
}

// mockAPIServerCapturingAuth is mockAPIServer that also records the
// Authorization header of the last request.
func mockAPIServerCapturingAuth(t *testing.T, gotAuth *string, fn func(ops []map[string]interface{}) interface{}) (*httptest.Server, *Executor) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		var body struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fn(body.Ops)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, nil
}

// ---- probeExistingToken -----------------------------------------------------

// A stale/revoked token must fail the probe AS A REJECTION, not silently pass
// as "already authenticated" — that false "Setup complete" made re-login
// impossible.
func TestProbeExistingToken_RejectedByServer(t *testing.T) {
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "error", "description": "cookie or headers are not valid"}}}
	})
	setProjectAuth(t, srv.URL)
	rejected, err := probeExistingToken(context.Background(), "https://account.example", "stale")
	if err == nil || !rejected {
		t.Fatalf("server-rejected token must probe as rejected, got (rejected=%v, err=%v)", rejected, err)
	}
}

func TestProbeExistingToken_ValidToken(t *testing.T) {
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "list": []interface{}{
				map[string]interface{}{"company_id": "i1", "title": "ws"}}}}}
	})
	setProjectAuth(t, srv.URL)
	rejected, err := probeExistingToken(context.Background(), "https://account.example", "good")
	if err != nil || rejected {
		t.Fatalf("valid token must pass the probe, got (rejected=%v, err=%v)", rejected, err)
	}
}

// Transport trouble (5xx, network) must NOT read as a rejection — discarding
// a working session and popping a surprise OAuth browser over a blip is worse
// than the disease.
func TestProbeExistingToken_TransportErrorIsNotRejection(t *testing.T) {
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "error", "description": "internal server error"}
	})
	setProjectAuth(t, srv.URL)
	rejected, err := probeExistingToken(context.Background(), "https://account.example", "good")
	if err == nil {
		t.Fatal("expected an error from the failing server")
	}
	if rejected {
		t.Fatalf("transport/5xx trouble must not classify as token rejection: %v", err)
	}
}

// The probe must validate the token it was HANDED, not the global state.
func TestProbeExistingToken_UsesGivenToken(t *testing.T) {
	resetGlobals(t)
	var gotAuth string
	srv, _ := mockAPIServerCapturingAuth(t, &gotAuth, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "list": []interface{}{}}}}
	})
	setProjectAuth(t, srv.URL)
	apiToken = "GLOBAL-token"
	if _, err := probeExistingToken(context.Background(), "https://account.example", "HANDED-token"); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if !strings.Contains(gotAuth, "HANDED-token") {
		t.Fatalf("probe must send the handed token, sent: %q", gotAuth)
	}
}

// ---- logout ------------------------------------------------------------------

// Logout must clear every resurrection path: the credentials file, the
// in-memory session, AND an ACCESS_TOKEN carried by the project .env — which
// would otherwise silently re-authenticate the very next login.
func TestLogout_ClearsEnvTokenAndMemory(t *testing.T) {
	home, workDir := fakeHome(t)
	resetGlobals(t)

	credDir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(credDir, "credentials")
	if err := os.WriteFile(credPath, []byte("ACCESS_TOKEN=tok-cred\n"), 0600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(workDir, ".env")
	if err := os.WriteFile(envPath, []byte("ACCOUNT_URL=https://a\nACCESS_TOKEN=tok-env\nWORKSPACE_ID=w1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	apiToken = "tok-mem"

	res, isErr := handleLogout(context.Background(), nil)
	if isErr {
		t.Fatalf("logout failed: %s", res)
	}
	if apiToken != "" {
		t.Error("in-memory token must be cleared")
	}
	if data, err := os.ReadFile(credPath); err == nil && strings.Contains(string(data), "tok-cred") {
		t.Error("credentials file still contains the token")
	}
	envData, _ := os.ReadFile(envPath)
	if strings.Contains(string(envData), "tok-env") {
		t.Errorf(".env still contains ACCESS_TOKEN after logout:\n%s", envData)
	}
	if !strings.Contains(string(envData), "WORKSPACE_ID=w1") {
		t.Errorf("logout must not destroy unrelated .env keys:\n%s", envData)
	}
	if !strings.Contains(res, ".env") {
		t.Errorf("logout result must mention the .env cleanup: %s", res)
	}
}

// A credentials file whose last key was removed must be deleted, not left as
// a confusing 0-byte husk (observed in the field).
func TestLogout_RemovesEmptyCredentialsFile(t *testing.T) {
	home, _ := fakeHome(t)
	resetGlobals(t)

	credDir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(credDir, "credentials")
	if err := os.WriteFile(credPath, []byte("ACCESS_TOKEN=tok\nACCESS_TOKEN_EXPIRES_AT=2030-01-01T00:00:00Z\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if res, isErr := handleLogout(context.Background(), nil); isErr {
		t.Fatalf("logout failed: %s", res)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		data, _ := os.ReadFile(credPath)
		t.Errorf("empty credentials file must be removed, found %d bytes: %q", len(data), data)
	}
}

// Logout must clean the .env that actually feeds the process — findAndLoadDotEnv
// walks UP from cwd, so a token in a PARENT directory's .env counts too.
func TestLogout_ClearsParentDirEnvToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := t.TempDir()
	child := filepath.Join(parent, "sub")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COREZOID_WORK_DIR", child)
	t.Chdir(child)
	resetGlobals(t)
	envPath := filepath.Join(parent, ".env")
	if err := os.WriteFile(envPath, []byte("ACCESS_TOKEN=tok-parent\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if res, isErr := handleLogout(context.Background(), nil); isErr {
		t.Fatalf("logout failed: %s", res)
	}
	data, _ := os.ReadFile(envPath)
	if strings.Contains(string(data), "tok-parent") {
		t.Errorf("parent .env still holds the token after logout:\n%s", data)
	}
}

// ---- envHasKey ----------------------------------------------------------------

func TestEnvHasKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("A=1\nACCESS_TOKEN=x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !envHasKey(p, "ACCESS_TOKEN") {
		t.Error("expected ACCESS_TOKEN to be found")
	}
	if envHasKey(p, "MISSING") {
		t.Error("MISSING must not be found")
	}
	if envHasKey(filepath.Join(dir, "nope"), "A") {
		t.Error("missing file must report false")
	}
}

// A credentials-file deletion failure must not leave the in-memory session
// alive — the wipe happens BEFORE any file operation (parity with the .env
// step, pinned after review caught the comment overpromising the order).
func TestLogout_MemoryClearedEvenIfCredentialsDeleteFails(t *testing.T) {
	home, _ := fakeHome(t)
	resetGlobals(t)
	credDir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(credDir, "credentials")
	if err := os.WriteFile(credPath, []byte("ACCESS_TOKEN=tok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Make the FILE read-only so removeEnvKey's rewrite fails (directory
	// permissions only gate removal, which is warn-only).
	if err := os.Chmod(credPath, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(credPath, 0600) })

	apiToken = "tok-mem"
	res, isErr := handleLogout(context.Background(), nil)
	if !isErr {
		t.Fatalf("expected an error for the failed file op, got: %s", res)
	}
	if apiToken != "" {
		t.Fatal("in-memory session must be dead even when the file op fails")
	}
	if !strings.Contains(res, "Logged out of this session") {
		t.Errorf("result must say the session itself IS logged out: %s", res)
	}
}

// ACCOUNT_URL pointing at the admin UI (the classic misconfiguration) must be
// named explicitly — the probe used to classify the HTML answer as generic
// transport trouble and the OAuth flow then opened the admin UI dead-end.
func TestProbeExistingToken_AdminHostDiagnosed(t *testing.T) {
	resetGlobals(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html><html>admin ui</html>"))
	}))
	t.Cleanup(srv.Close)
	// apiURL empty -> the probe derives it via the (fake admin) account host.
	rejected, err := probeExistingToken(context.Background(), srv.URL, "tok")
	if rejected {
		t.Fatal("misconfigured host must not read as token rejection")
	}
	if err == nil || !strings.Contains(err.Error(), "admin UI host") {
		t.Fatalf("expected an explicit admin-host diagnosis, got: %v", err)
	}
}

func TestAssertAccountService(t *testing.T) {
	htmlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>ui</html>"))
	}))
	t.Cleanup(htmlSrv.Close)
	if err := assertAccountService(htmlSrv.URL); err == nil || !strings.Contains(err.Error(), "consent page cannot open") {
		t.Fatalf("HTML host must be refused with the consent-page explanation, got: %v", err)
	}
	jsonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"corezoid","url":"https://admin.example"}]`))
	}))
	t.Cleanup(jsonSrv.Close)
	if err := assertAccountService(jsonSrv.URL); err != nil {
		t.Fatalf("JSON account host must pass: %v", err)
	}
}

// ---- auth-DX hardening round ---------------------------------------------------

func TestLogout_WritesCredentialsBackup(t *testing.T) {
	home, workDir := fakeHome(t)
	resetGlobals(t)
	credDir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(credDir, "credentials")
	if err := os.WriteFile(credPath, []byte("ACCESS_TOKEN=tok-cred\nACCESS_TOKEN_EXPIRES_AT=2030-01-01T00:00:00Z\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".env"), []byte("ACCESS_TOKEN=tok-env\n"), 0600); err != nil {
		t.Fatal(err)
	}
	res, isErr := handleLogout(context.Background(), nil)
	if isErr {
		t.Fatalf("logout failed: %s", res)
	}
	bak := credPath + ".bak"
	info, err := os.Stat(bak)
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("backup mode = %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(bak)
	if !strings.Contains(string(data), "ACCESS_TOKEN=tok-cred") {
		t.Errorf("backup missing the credentials token:\n%s", data)
	}
	if !strings.Contains(string(data), "# ACCESS_TOKEN=tok-env") {
		t.Errorf("backup missing the annotated .env token:\n%s", data)
	}
	if !strings.Contains(res, bak) || !strings.Contains(res, "ONLY credential") {
		t.Errorf("logout result must name the backup and the warning: %s", res)
	}
}

func TestLogout_NoBackupWhenNothingToBackUp(t *testing.T) {
	home, _ := fakeHome(t)
	resetGlobals(t)
	credDir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(credDir, "credentials.bak")
	if err := os.WriteFile(sentinel, []byte("PRECIOUS=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	res, isErr := handleLogout(context.Background(), nil)
	if isErr {
		t.Fatalf("logout failed: %s", res)
	}
	data, _ := os.ReadFile(sentinel)
	if string(data) != "PRECIOUS=1\n" {
		t.Fatalf("a no-token logout must not touch an existing backup, got: %q", data)
	}
	if strings.Contains(res, "backup of the removed token") {
		t.Errorf("no-token logout must not claim a backup was written: %s", res)
	}
}

func stubOAuthFlow(t *testing.T, res *PKCEResult, authURL string, err error) {
	t.Helper()
	orig := runOAuthFlow
	runOAuthFlow = func(ctx context.Context, accountURL, clientID string) (*PKCEResult, string, error) {
		return res, authURL, err
	}
	t.Cleanup(func() { runOAuthFlow = orig })
}

// jsonAccountServer answers the clients endpoint with valid JSON so
// assertAccountService and probe derivation pass.
func jsonAccountServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"corezoid","url":"https://api.example"}]`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLogin_OAuthFailureMessageHasAuthURLAndFallback(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	acc := jsonAccountServer(t)
	t.Setenv("ACCOUNT_URL", acc.URL)
	stubOAuthFlow(t, nil, acc.URL+"/oauth2/authorize?x=1", fmt.Errorf("authentication timed out after 5 minutes"))
	res, isErr := handleLogin(context.Background(), map[string]interface{}{})
	if !isErr {
		t.Fatalf("expected error result, got: %s", res)
	}
	for _, want := range []string{"/oauth2/authorize?x=1", "/access_tokens", "ACCOUNT host", "re-run login"} {
		if !strings.Contains(res, want) {
			t.Errorf("failure message missing %q:\n%s", want, res)
		}
	}
}

func TestLogin_OAuthCtxCancelledMessage(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	acc := jsonAccountServer(t)
	t.Setenv("ACCOUNT_URL", acc.URL)
	stubOAuthFlow(t, nil, acc.URL+"/oauth2/authorize?x=1",
		fmt.Errorf("authentication wait cancelled by the client: %w", context.Canceled))
	res, isErr := handleLogin(context.Background(), map[string]interface{}{})
	if !isErr || !strings.Contains(res, "client cancelled the tool call") || !strings.Contains(res, "not a crash") {
		t.Fatalf("expected client-cancel preamble, got isErr=%v: %s", isErr, res)
	}
}

func TestOAuthPKCEFlow_HonorsCtxCancel(t *testing.T) {
	origOpen := openBrowserFn
	openBrowserFn = func(string) {}
	t.Cleanup(func() { openBrowserFn = origOpen })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	var authURL string
	var err error
	go func() {
		_, authURL, err = oauthPKCEFlow(ctx, "https://acc.example", "client1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("flow did not return promptly on a cancelled ctx")
	}
	if err == nil || !strings.Contains(err.Error(), "cancelled by the client") {
		t.Fatalf("expected client-cancel error, got: %v", err)
	}
	if authURL == "" {
		t.Fatal("authURL must be returned even on cancellation")
	}
}

func TestSanitizeAPIURL(t *testing.T) {
	cases := []struct{ in, want string; stripped bool }{
		{"https://h.example/api/2/json", "https://h.example", true},
		{"https://h.example/api/2/json/", "https://h.example", true},
		{"https://h.example/api/2/download", "https://h.example", true},
		{"https://h.example", "https://h.example", false},
		{"https://h.example/", "https://h.example", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, stripped := sanitizeAPIURL(c.in)
		if got != c.want || stripped != c.stripped {
			t.Errorf("sanitizeAPIURL(%q) = (%q, %v), want (%q, %v)", c.in, got, stripped, c.want, c.stripped)
		}
	}
}

func TestResolveProjectIDByStage_PropagatesAuthError(t *testing.T) {
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "error", "description": "cookie or headers are not valid"}}}
	})
	setProjectAuth(t, srv.URL)
	v := NewValidator(context.Background(), 0)
	id, err := v.resolveProjectIDByStage(400)
	if id != 0 || err == nil {
		t.Fatalf("expected (0, err), got (%d, %v)", id, err)
	}
	if !strings.Contains(err.Error(), "re-run login (force=true") {
		t.Errorf("auth hint missing: %v", err)
	}
	if got := v.GetProjectIDByStageID(400); got != 0 {
		t.Errorf("legacy shim must still return 0, got %d", got)
	}
}

func TestLogin_APIURLDeriveFailureIsExplicit(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	// Clients endpoint: JSON when unauthenticated (assertAccountService passes),
	// plain 500 when the new token arrives (derivation fails — NOT HTML, so it
	// is not misdiagnosed as the admin-host case).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"corezoid","url":"https://api.example"}]`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ACCOUNT_URL", srv.URL)
	stubOAuthFlow(t, &PKCEResult{AccessToken: "fresh-token"}, srv.URL+"/oauth2/authorize?x=1", nil)
	res, isErr := handleLogin(context.Background(), map[string]interface{}{})
	if !isErr {
		t.Fatalf("expected explicit error, got: %s", res)
	}
	for _, want := range []string{"token is saved", "COREZOID_API_URL", "admin.corezoid.com", "no /api/2/json suffix"} {
		if !strings.Contains(res, want) {
			t.Errorf("derive-failure message missing %q:\n%s", want, res)
		}
	}
	// The token really must be saved despite the abort.
	data, _ := os.ReadFile(os.Getenv("HOME") + "/.corezoid/credentials")
	if !strings.Contains(string(data), "fresh-token") {
		t.Errorf("token must be persisted before the derive abort: %q", data)
	}
}

// ---- status tool ----------------------------------------------------------------

func TestStatus_NoAuthNeeded(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	res, isErr := handleToolCall(context.Background(), "status", map[string]interface{}{})
	if isErr {
		t.Fatalf("status must work with zero auth: %s", res)
	}
	for _, want := range []string{"corezoid-mcp", "token:", "absent — run login", "must be RESTARTED"} {
		if !strings.Contains(res, want) {
			t.Errorf("status output missing %q:\n%s", want, res)
		}
	}
}

func TestStatus_FlagsAdminAccountURL(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	accountURL = "https://admin.corezoid.com"
	t.Cleanup(func() { accountURL = "" })
	res, _ := handleToolCall(context.Background(), "status", map[string]interface{}{})
	if !strings.Contains(res, "admin UI host") {
		t.Errorf("status must flag an admin ACCOUNT_URL:\n%s", res)
	}
}

func TestStatus_ReportsExpiredToken(t *testing.T) {
	home, _ := fakeHome(t)
	resetGlobals(t)
	credDir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(credDir, "credentials"),
		[]byte("ACCESS_TOKEN=tok-old\nACCESS_TOKEN_EXPIRES_AT=2020-01-01T00:00:00Z\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACCESS_TOKEN", "tok-old")
	t.Setenv("ACCESS_TOKEN_EXPIRES_AT", "2020-01-01T00:00:00Z")
	apiToken = "tok-old"
	res, _ := handleToolCall(context.Background(), "status", map[string]interface{}{})
	if !strings.Contains(res, "EXPIRED") || !strings.Contains(res, "force=true") {
		t.Errorf("status must flag the expired token with a re-login hint:\n%s", res)
	}
}

func TestStatus_AdminDetectionAnchorsOnHost(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	// "admin." must only match as a host prefix, not anywhere in the name —
	// on-prem hosts like myadmin.example.com are legitimate OAuth hosts.
	accountURL = "https://myadmin.example.com"
	t.Cleanup(func() { accountURL = "" })
	res, _ := handleToolCall(context.Background(), "status", map[string]interface{}{})
	if strings.Contains(res, "admin UI host") {
		t.Errorf("status must not flag myadmin.example.com as an admin host:\n%s", res)
	}
}

func TestStatus_ProbeOKAndFailedKeepFooter(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	srv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "list": []interface{}{}},
		}}
	})
	apiURL = srv.URL + "/api/2/json"
	apiToken = "tok-probe"
	res, isErr := handleToolCall(context.Background(), "status", map[string]interface{}{"probe": true})
	if isErr || !strings.Contains(res, "probe: OK") {
		t.Errorf("want probe OK, got (isErr=%v):\n%s", isErr, res)
	}

	srvBad, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "error", "ops": []interface{}{
			map[string]interface{}{"proc": "error", "description": "Unauthorized"},
		}}
	})
	apiURL = srvBad.URL + "/api/2/json"
	res, isErr = handleToolCall(context.Background(), "status", map[string]interface{}{"probe": true})
	if !isErr || !strings.Contains(res, "probe: FAILED") {
		t.Errorf("want probe FAILED, got (isErr=%v):\n%s", isErr, res)
	}
	if !strings.Contains(res, "must be RESTARTED") {
		t.Errorf("probe failure must not drop the restart footer:\n%s", res)
	}
}

func TestStatus_ProbeSkippedWithoutToken(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	res, isErr := handleToolCall(context.Background(), "status", map[string]interface{}{"probe": "true"})
	if isErr || !strings.Contains(res, "probe: SKIPPED") {
		t.Errorf("probe without token must be SKIPPED, got (isErr=%v):\n%s", isErr, res)
	}
}

func TestInitOAuthClientID_Precedence(t *testing.T) {
	t.Setenv("COREZOID_OAUTH_CLIENT_ID", "")
	os.Unsetenv("COREZOID_OAUTH_CLIENT_ID")
	oauthClientID = ""
	t.Cleanup(func() { oauthClientID = "" })
	initOAuthClientID()
	if oauthClientID != oauthDefaultClientID {
		t.Fatalf("want built-in default, got %q", oauthClientID)
	}
	t.Setenv("COREZOID_OAUTH_CLIENT_ID", "custom-id")
	initOAuthClientID()
	if oauthClientID != "custom-id" {
		t.Fatalf("env override must win, got %q", oauthClientID)
	}
}

func TestLoggerLinesCarryTimestamps(t *testing.T) {
	var buf strings.Builder
	l := &Logger{writer: &buf}
	l.Info("hello %d", 42)
	line := buf.String()
	if !strings.Contains(line, "INFO:hello 42") {
		t.Fatalf("unexpected line: %q", line)
	}
	// RFC3339 timestamp prefix, e.g. 2026-07-12T10:00:00Z
	if _, err := time.Parse(time.RFC3339, strings.Fields(line)[0]); err != nil {
		t.Errorf("line must start with an RFC3339 timestamp, got: %q", line)
	}
}
