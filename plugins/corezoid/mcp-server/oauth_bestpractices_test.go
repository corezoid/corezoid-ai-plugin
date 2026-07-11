package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestValidateAccountURLScheme(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://account.corezoid.com", true},
		{"https://account.onprem.example:8443", true},
		{"http://localhost:9000", true},
		{"http://127.0.0.1:9000", true},
		{"http://account.onprem.example", false},
		{"ftp://account.corezoid.com", false},
		{"account.corezoid.com", false},
	}
	for _, c := range cases {
		err := validateAccountURLScheme(c.url)
		if (err == nil) != c.ok {
			t.Errorf("validateAccountURLScheme(%q): got err=%v, want ok=%v", c.url, err, c.ok)
		}
	}
}

func TestDiscoverOAuthEndpoints_UsesMetadata(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + srvURL + `","authorization_endpoint":"` + srvURL + `/custom/auth","token_endpoint":"` + srvURL + `/custom/token"}`))
	}))
	defer srv.Close()
	srvURL = srv.URL

	// httptest server is http://127.0.0.1 — passes the loopback scheme check.
	authz, token := discoverOAuthEndpoints(context.Background(), srv.URL)
	if authz != srv.URL+"/custom/auth" || token != srv.URL+"/custom/token" {
		t.Errorf("metadata endpoints not used: %q %q", authz, token)
	}
}

func TestDiscoverOAuthEndpoints_FallsBackOn404(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	authz, token := discoverOAuthEndpoints(context.Background(), srv.URL)
	if authz != srv.URL+"/oauth2/authorize" || token != srv.URL+"/oauth2/token" {
		t.Errorf("want conventional fallback, got %q %q", authz, token)
	}
}

func TestDiscoverOAuthEndpoints_RejectsInsecureMetadataEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authorization_endpoint":"http://evil.example/auth","token_endpoint":"http://evil.example/token"}`))
	}))
	defer srv.Close()
	authz, token := discoverOAuthEndpoints(context.Background(), srv.URL)
	if strings.Contains(authz, "evil") || strings.Contains(token, "evil") {
		t.Errorf("insecure metadata endpoints must be rejected: %q %q", authz, token)
	}
}

func TestRefreshAccessToken_ExchangesAndRotates(t *testing.T) {
	tok := testJWTWithExp(t, 4102444800) // 2100-01-01
	var gotGrant, gotRefresh string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/oauth2/token" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		gotGrant = r.Form.Get("grant_type")
		gotRefresh = r.Form.Get("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"simulator_token":"` + tok + `","refresh_token":"rt-rotated"}`))
	}))
	defer srv.Close()

	res, err := refreshAccessToken(context.Background(), srv.URL, "client-1", "rt-old")
	if err != nil {
		t.Fatal(err)
	}
	if gotGrant != "refresh_token" || gotRefresh != "rt-old" {
		t.Errorf("wrong grant sent: grant_type=%q refresh_token=%q", gotGrant, gotRefresh)
	}
	if res.AccessToken != tok || res.RefreshToken != "rt-rotated" {
		t.Errorf("rotated pair not captured: %+v", res)
	}
	if res.ExpiresAt.IsZero() {
		t.Errorf("expiry must be parsed from the JWT")
	}
}

func TestLogin_SilentRefreshSkipsBrowser(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	// saveCredentials in sibling tests exports ACCESS_TOKEN into the process
	// env — clear it so this login starts tokenless.
	t.Setenv("ACCESS_TOKEN", "")
	os.Unsetenv("ACCESS_TOKEN")
	t.Setenv("ACCESS_TOKEN_EXPIRES_AT", "")
	os.Unsetenv("ACCESS_TOKEN_EXPIRES_AT")
	t.Setenv("REFRESH_TOKEN", "rt-stored")
	t.Setenv("ACCOUNT_URL", "https://account.example.com")

	browserOpened := false
	origFlow := runOAuthFlow
	runOAuthFlow = func(ctx context.Context, accountURL, clientID string) (*PKCEResult, string, error) {
		browserOpened = true
		return nil, "", nil
	}
	t.Cleanup(func() { runOAuthFlow = origFlow })

	tok := testJWTWithExp(t, 4102444800)
	origRefresh := refreshAccessTokenFn
	refreshAccessTokenFn = func(ctx context.Context, accountURL, clientID, refreshToken string) (*PKCEResult, error) {
		if refreshToken != "rt-stored" {
			t.Errorf("stored refresh token not used: %q", refreshToken)
		}
		return &PKCEResult{AccessToken: tok, RefreshToken: "rt-next"}, nil
	}
	t.Cleanup(func() { refreshAccessTokenFn = origRefresh })

	// The refreshed token is only trusted after a live probe — serve a
	// working API and a clients endpoint whose homepage points at it.
	probeSrv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "ok", "ops": []interface{}{
			map[string]interface{}{"proc": "ok", "list": []interface{}{}},
		}}
	})
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"name":"corezoid","homepage":"` + probeSrv.URL + `"}]`))
	}))
	defer apiSrv.Close()
	// fetchCorezoidAPIURL hits {ACCOUNT_URL}/face/api/1/clients; point the
	// account at the test server for that step only.
	t.Setenv("ACCOUNT_URL", apiSrv.URL)
	t.Setenv("WORKSPACE_ID", "ws-1")
	t.Setenv("COREZOID_STAGE_ID", "7")

	res, isErr := handleLogin(context.Background(), map[string]interface{}{})
	if isErr {
		t.Fatalf("login must succeed via silent refresh: %s", res)
	}
	if browserOpened {
		t.Errorf("browser flow must not run when the refresh grant succeeds")
	}
	if !strings.Contains(res, "renewed silently") {
		t.Errorf("result must tell the user the token was renewed without a browser:\n%s", res)
	}
	if got := os.Getenv("REFRESH_TOKEN"); got != "rt-next" {
		t.Errorf("rotated refresh token must be persisted, got %q", got)
	}
}

func TestLogin_RefreshMintedTokenRejectedByAPIFallsBack(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	t.Setenv("ACCESS_TOKEN", "")
	os.Unsetenv("ACCESS_TOKEN")
	t.Setenv("ACCESS_TOKEN_EXPIRES_AT", "")
	os.Unsetenv("ACCESS_TOKEN_EXPIRES_AT")
	t.Setenv("REFRESH_TOKEN", "rt-stored")

	origRefresh := refreshAccessTokenFn
	refreshAccessTokenFn = func(ctx context.Context, accountURL, clientID, refreshToken string) (*PKCEResult, error) {
		// The live account service answers the refresh grant with an opaque
		// session token (atn_...) that the corezoid API rejects.
		return &PKCEResult{AccessToken: "atn_opaque_session_token"}, nil
	}
	t.Cleanup(func() { refreshAccessTokenFn = origRefresh })

	// The API rejects the probe with the auth-rejection marker.
	rejectSrv, _ := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		return map[string]interface{}{"request_proc": "error", "ops": []interface{}{
			map[string]interface{}{"proc": "error", "description": "cookie or headers are not valid"},
		}}
	})
	apiURL = rejectSrv.URL + "/api/2/json"

	flowRan := false
	origFlow := runOAuthFlow
	runOAuthFlow = func(ctx context.Context, accountURL, clientID string) (*PKCEResult, string, error) {
		flowRan = true
		return nil, "https://acc/auth", context.Canceled
	}
	t.Cleanup(func() { runOAuthFlow = origFlow })

	accSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer accSrv.Close()

	res, _ := handleLogin(context.Background(), map[string]interface{}{"account_url": accSrv.URL})
	if !flowRan {
		t.Fatalf("an API-rejected refreshed token must fall back to the browser flow:\n%s", res)
	}
}

func TestPostTokenRequest_ErrorNeverLeaksBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"ok","unknown_token_field":"secret-token-value-123"}`))
	}))
	defer srv.Close()
	_, err := postTokenRequest(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("unrecognized response must error")
	}
	if strings.Contains(err.Error(), "secret-token-value-123") {
		t.Fatalf("error text must not leak response values: %v", err)
	}
	if !strings.Contains(err.Error(), "unknown_token_field") {
		t.Fatalf("error should name the fields seen: %v", err)
	}
}

func TestPostTokenRequest_ParsesRefreshGrantShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"x","result":"ok","user_id":1,"new_access_token":"atn_abc","new_access_token_expire":1784061715,"refresh_token_expire":1786394513,"session_id":"s"}`))
	}))
	defer srv.Close()
	res, err := postTokenRequest(context.Background(), srv.URL, url.Values{"grant_type": {"refresh_token"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken != "atn_abc" {
		t.Errorf("new_access_token must be accepted: %+v", res)
	}
	if res.ExpiresAt.Unix() != 1784061715 {
		t.Errorf("expiry must come from new_access_token_expire, got %v", res.ExpiresAt)
	}
}

func TestLogin_RefreshFailureFallsBackToBrowser(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	// saveCredentials in sibling tests exports ACCESS_TOKEN into the process
	// env — clear it so this login starts tokenless.
	t.Setenv("ACCESS_TOKEN", "")
	os.Unsetenv("ACCESS_TOKEN")
	t.Setenv("ACCESS_TOKEN_EXPIRES_AT", "")
	os.Unsetenv("ACCESS_TOKEN_EXPIRES_AT")
	t.Setenv("REFRESH_TOKEN", "rt-dead")

	origRefresh := refreshAccessTokenFn
	refreshAccessTokenFn = func(ctx context.Context, accountURL, clientID, refreshToken string) (*PKCEResult, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { refreshAccessTokenFn = origRefresh })

	flowRan := false
	origFlow := runOAuthFlow
	runOAuthFlow = func(ctx context.Context, accountURL, clientID string) (*PKCEResult, string, error) {
		flowRan = true
		return nil, "https://acc/auth?x=1", context.Canceled
	}
	t.Cleanup(func() { runOAuthFlow = origFlow })

	// assertAccountService must pass — serve a JSON clients endpoint.
	accSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer accSrv.Close()

	res, isErr := handleLogin(context.Background(), map[string]interface{}{"account_url": accSrv.URL})
	if !flowRan {
		t.Fatalf("browser flow must run after a failed refresh (isErr=%v):\n%s", isErr, res)
	}
}

// testJWTWithExp builds an unsigned JWT-shaped token with the given exp claim.
func testJWTWithExp(t *testing.T, exp int64) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
	return header + "." + payload + ".sig"
}

func TestPostTokenRequest_ParsesServiceErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"x","result":"error","message":"Wrong refresh token"}`))
	}))
	defer srv.Close()
	_, err := postTokenRequest(context.Background(), srv.URL, url.Values{"grant_type": {"refresh_token"}})
	if err == nil || !strings.Contains(err.Error(), "Wrong refresh token") {
		t.Fatalf("service error envelope must surface its message, got %v", err)
	}
}

func TestLogin_ForceSkipsRefreshAndDropsToken(t *testing.T) {
	fakeHome(t)
	resetGlobals(t)
	t.Setenv("ACCESS_TOKEN", "")
	os.Unsetenv("ACCESS_TOKEN")
	t.Setenv("REFRESH_TOKEN", "rt-poisoned")

	refreshTried := false
	origRefresh := refreshAccessTokenFn
	refreshAccessTokenFn = func(ctx context.Context, accountURL, clientID, refreshToken string) (*PKCEResult, error) {
		refreshTried = true
		return nil, context.Canceled
	}
	t.Cleanup(func() { refreshAccessTokenFn = origRefresh })

	origFlow := runOAuthFlow
	runOAuthFlow = func(ctx context.Context, accountURL, clientID string) (*PKCEResult, string, error) {
		return nil, "https://acc/auth", context.Canceled
	}
	t.Cleanup(func() { runOAuthFlow = origFlow })

	accSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer accSrv.Close()

	_, _ = handleLogin(context.Background(), map[string]interface{}{"account_url": accSrv.URL, "force": true})
	if refreshTried {
		t.Errorf("force=true must be unconditional — the silent refresh must not run")
	}
	if os.Getenv("REFRESH_TOKEN") != "" {
		t.Errorf("force=true must discard the stored refresh token")
	}
}

func TestLogout_RemovesRefreshTokenFromEnvFile(t *testing.T) {
	_, workDir := fakeHome(t)
	resetGlobals(t)
	envPath := filepath.Join(workDir, ".env")
	if err := os.WriteFile(envPath, []byte("ACCESS_TOKEN=tok\nREFRESH_TOKEN=rt-1\nOTHER=x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACCESS_TOKEN", "tok")
	t.Setenv("REFRESH_TOKEN", "rt-1")
	apiToken = "tok"

	res, isErr := handleLogout(context.Background(), nil)
	if isErr {
		t.Fatalf("logout failed: %s", res)
	}
	data, _ := os.ReadFile(envPath)
	if strings.Contains(string(data), "REFRESH_TOKEN") {
		t.Errorf("logout must remove REFRESH_TOKEN from .env (it silently re-authenticates the next login): %s", data)
	}
	if !strings.Contains(string(data), "OTHER=x") {
		t.Errorf("logout must keep unrelated keys: %s", data)
	}
}
