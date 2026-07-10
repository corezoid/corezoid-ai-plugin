package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
