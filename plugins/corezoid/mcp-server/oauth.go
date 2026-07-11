package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	oauthDefaultClientID = "5ec679f5a2710f0da6000005"
)

// PKCEResult holds the token returned after a successful OAuth2 PKCE flow.
type PKCEResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func generateVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func findFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func openBrowser(u string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{u}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", u}
	default:
		cmd = "xdg-open"
		args = []string{u}
	}
	_ = exec.Command(cmd, args...).Start()
}

// openBrowserFn is a seam so tests can run the flow without exec'ing a real
// browser command (mirrors the deployMonitor seam pattern).
var openBrowserFn = openBrowser

// oauthPKCEFlow runs the OAuth2 PKCE authorization flow against the given account URL.
// It opens the user's browser, starts a local callback server, and exchanges the
// authorization code for an access token. The wait honours the caller's ctx —
// an MCP client that caps tool-call duration cancels it, and the flow returns
// immediately instead of holding the (already abandoned) call for 5 minutes.
// authURL is returned in every path that reaches URL construction, so failure
// messages can hand the user the exact consent link.
func oauthPKCEFlow(ctx context.Context, accountURL, clientID string) (res *PKCEResult, authURL string, err error) {
	accountURL = strings.TrimRight(accountURL, "/")
	if err := validateAccountURLScheme(accountURL); err != nil {
		return nil, "", err
	}
	verifier, err := generateVerifier()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	challenge := generateChallenge(verifier)

	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, "", fmt.Errorf("failed to generate OAuth state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// Keep the listener we probed — closing it and re-listening later (the old
	// findFreePort pattern) left a window where another local process could
	// grab the port and receive the callback (RFC 8252 §8.3 concern), and made
	// login flaky under port churn.
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, "", fmt.Errorf("failed to start callback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	params := url.Values{
		"response_type":         {"code"},
		"scope":                 {"single_account:full_access"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	authorizeURL, tokenURL := discoverOAuthEndpoints(ctx, accountURL)
	authURL = authorizeURL + "?" + params.Encode()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("state"); got != state {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(oauthPageHTML("Authentication Failed", "error",
				"Authentication failed",
				"State parameter mismatch.",
				"You may close this window.")))
			errCh <- fmt.Errorf("OAuth state mismatch: possible CSRF")
			return
		}
		if errCode := q.Get("error"); errCode != "" {
			desc := q.Get("error_description")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(oauthPageHTML("Authentication Failed", "error",
				"Authentication failed",
				"<strong>"+html.EscapeString(errCode)+"</strong>: "+html.EscapeString(desc),
				"You may close this window.")))
			errCh <- fmt.Errorf("OAuth error: %s — %s", errCode, desc)
			return
		}
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(oauthPageHTML("Authentication Failed", "error",
				"Authentication failed",
				"No authorization code received.",
				"You may close this window.")))
			errCh <- fmt.Errorf("no authorization code in callback")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(oauthPageHTML("Authentication Successful", "success",
			"Authentication successful",
			"You are now connected to Corezoid.",
			"You may close this window and return to Claude Code.")))
		codeCh <- code
	})

	go func() {
		if srvErr := srv.Serve(listener); srvErr != nil && srvErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server error: %w", srvErr)
		}
	}()

	fmt.Fprintf(os.Stderr, "Opening browser for Corezoid authentication...\nIf it did not open automatically, visit:\n%s\n", authURL)
	openBrowserFn(authURL)

	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
	case oauthErr := <-errCh:
		_ = srv.Shutdown(context.Background())
		return nil, authURL, oauthErr
	case <-waitCtx.Done():
		_ = srv.Shutdown(context.Background())
		if ctx.Err() != nil {
			// The CALLER's context ended — typically the MCP client cancelling
			// a long tool call. The server is fine; the wait just stopped.
			return nil, authURL, fmt.Errorf("authentication wait cancelled by the client: %w", ctx.Err())
		}
		return nil, authURL, fmt.Errorf("authentication timed out after 5 minutes")
	}

	go func() { _ = srv.Shutdown(context.Background()) }()

	// Exchange authorization code for access token
	tokenParams := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}
	res, err = postTokenRequest(ctx, tokenURL, tokenParams)
	if err != nil {
		return nil, authURL, err
	}
	return res, authURL, nil
}

// postTokenRequest POSTs to the token endpoint and parses the response into a
// PKCEResult. Shared by the authorization-code exchange and the refresh grant.
func postTokenRequest(ctx context.Context, tokenURL string, form url.Values) (*PKCEResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	var tokenResp map[string]interface{}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	if errMsg, ok := tokenResp["error"].(string); ok {
		desc, _ := tokenResp["error_description"].(string)
		return nil, fmt.Errorf("token error: %s — %s", errMsg, desc)
	}
	// The account service reports failures in its own envelope
	// ({"result":"error","message":"..."}) rather than the RFC 6749 error
	// shape — surface the message instead of dumping raw JSON.
	if res, ok := tokenResp["result"].(string); ok && res == "error" {
		msg, _ := tokenResp["message"].(string)
		return nil, fmt.Errorf("token error: %s", msg)
	}

	// Corezoid and Simulator share account.corezoid.com — token field is
	// simulator_token. Fall back to standard access_token (RFC 6749), then to
	// new_access_token (the shape the refresh grant answers with, verified live).
	var accessToken string
	if t, ok := tokenResp["simulator_token"].(string); ok && t != "" {
		accessToken = t
	} else if t, ok := tokenResp["access_token"].(string); ok && t != "" {
		accessToken = t
	} else if t, ok := tokenResp["new_access_token"].(string); ok && t != "" {
		accessToken = t
	}
	if accessToken == "" {
		// Do NOT include the body: a response we merely failed to recognize
		// may still carry a live token, and error strings end up in logs.
		return nil, fmt.Errorf("no token in OAuth response (fields: %s)", strings.Join(jsonKeys(tokenResp), ", "))
	}
	refreshToken, _ := tokenResp["refresh_token"].(string)

	expiry := parseJWTExpiry(accessToken)
	if expiry.IsZero() {
		// Opaque (non-JWT) tokens carry their expiry as a unix-seconds field.
		if e, ok := tokenResp["new_access_token_expire"].(float64); ok && e > 0 {
			expiry = time.Unix(int64(e), 0)
		}
	}

	return &PKCEResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiry,
	}, nil
}

// jsonKeys returns the sorted key list of a decoded JSON object — safe to put
// in error messages where the values must not leak.
func jsonKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// refreshAccessToken exchanges a refresh token for a fresh access token
// (grant_type=refresh_token) — the OAuth 2.1 silent-renewal path, so an
// expired session does not force a browser round-trip. The account service
// advertises refresh_token in grants_types_supported; if it rotates the
// refresh token, the new one is returned in the result.
func refreshAccessToken(ctx context.Context, accountURL, clientID, refreshToken string) (*PKCEResult, error) {
	accountURL = strings.TrimRight(accountURL, "/")
	if err := validateAccountURLScheme(accountURL); err != nil {
		return nil, err
	}
	_, tokenURL := discoverOAuthEndpoints(ctx, accountURL)
	return postTokenRequest(ctx, tokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
	})
}

// refreshAccessTokenFn is a seam so login tests can exercise the silent-refresh
// path without a live token endpoint.
var refreshAccessTokenFn = refreshAccessToken

// validateAccountURLScheme enforces the OAuth 2.1 rule that authorization
// server endpoints are HTTPS; plain http is allowed only for loopback hosts
// (local/on-prem development). Catching this before the browser opens turns a
// confusing consent-page failure into an actionable message.
func validateAccountURLScheme(accountURL string) error {
	u, err := url.Parse(accountURL)
	if err != nil {
		return fmt.Errorf("ACCOUNT_URL %q is not a valid URL: %w", accountURL, err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" {
			return nil
		}
		return fmt.Errorf("ACCOUNT_URL %q uses plain http to a non-local host — the OAuth flow would send secrets unencrypted; use https", accountURL)
	default:
		return fmt.Errorf("ACCOUNT_URL %q must start with https:// (or http:// for localhost only)", accountURL)
	}
}

// discoverOAuthEndpoints resolves the authorization and token endpoints via
// RFC 8414 metadata ({accountURL}/.well-known/oauth-authorization-server) and
// falls back to the conventional /oauth2/authorize + /oauth2/token paths when
// the metadata is unavailable (older on-prem account services). Discovery
// keeps us honest against whatever the AS actually serves instead of
// hardcoding its layout.
func discoverOAuthEndpoints(ctx context.Context, accountURL string) (authorizeURL, tokenURL string) {
	authorizeURL = accountURL + "/oauth2/authorize"
	tokenURL = accountURL + "/oauth2/token"
	if ctx == nil {
		ctx = context.Background()
	}
	mdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(mdCtx, http.MethodGet, accountURL+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return authorizeURL, tokenURL
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		logger.Debug("oauth discovery: metadata fetch failed (%v) — using conventional endpoints", err)
		return authorizeURL, tokenURL
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return authorizeURL, tokenURL
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return authorizeURL, tokenURL
	}
	var meta struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(body, &meta); err != nil || meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return authorizeURL, tokenURL
	}
	if validateAccountURLScheme(meta.AuthorizationEndpoint) != nil || validateAccountURLScheme(meta.TokenEndpoint) != nil {
		logger.Warn("oauth discovery: metadata endpoints have an unacceptable scheme — using conventional endpoints")
		return authorizeURL, tokenURL
	}
	return meta.AuthorizationEndpoint, meta.TokenEndpoint
}

type accountClient struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Homepage string `json:"homepage"`
}

// fetchCorezoidAPIURL calls {accountURL}/face/api/1/clients and returns the homepage
// of the Corezoid client entry (matched first by name=="corezoid", then by
// full_name=="Corezoid"). This URL is used as COREZOID_API_URL.
func fetchCorezoidAPIURL(accountURL, token string) (string, error) {
	clientsURL := strings.TrimRight(accountURL, "/") + "/face/api/1/clients"
	req, err := http.NewRequest("GET", clientsURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create clients request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("clients API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read clients response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clients API returned %d: %s", resp.StatusCode, string(body))
	}

	var clients []accountClient
	if err := json.Unmarshal(body, &clients); err != nil {
		return "", fmt.Errorf("failed to parse clients response: %w", err)
	}

	var byFullName string
	for _, c := range clients {
		if strings.EqualFold(c.Name, "corezoid") {
			return strings.TrimRight(c.Homepage, "/"), nil
		}
		if strings.EqualFold(c.FullName, "Corezoid") && byFullName == "" {
			byFullName = c.Homepage
		}
	}
	if byFullName != "" {
		return strings.TrimRight(byFullName, "/"), nil
	}
	return "", fmt.Errorf("corezoid client not found in account clients list")
}

// parseJWTExpiry extracts the exp claim from a JWT without verifying the signature.
func parseJWTExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return time.Time{}
	}
	return time.Unix(int64(exp), 0)
}

func oauthPageHTML(title, kind, heading, detail, action string) string {
	accent := "#4f8ef7"
	iconBg := "#e8f0fe"
	iconColor := "#4f8ef7"
	symbol := "✓"
	if kind == "error" {
		accent = "#e05252"
		iconBg = "#fdecea"
		iconColor = "#e05252"
		symbol = "✕"
	}
	html := "<!DOCTYPE html>\n" +
		"<html lang=\"en\">\n" +
		"<head>\n" +
		"  <meta charset=\"utf-8\"/>\n" +
		"  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"/>\n" +
		"  <title>" + title + "</title>\n" +
		"  <style>\n" +
		"    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }\n" +
		"    body {\n" +
		"      font-family: -apple-system, BlinkMacSystemFont, \"Segoe UI\", Roboto, sans-serif;\n" +
		"      background: #f4f6fb;\n" +
		"      display: flex; align-items: center; justify-content: center;\n" +
		"      min-height: 100vh;\n" +
		"      color: #1a1a2e;\n" +
		"    }\n" +
		"    .card {\n" +
		"      background: #ffffff;\n" +
		"      border-radius: 16px;\n" +
		"      box-shadow: 0 8px 40px rgba(0,0,0,.10);\n" +
		"      padding: 48px 56px;\n" +
		"      max-width: 440px;\n" +
		"      width: 100%;\n" +
		"      text-align: center;\n" +
		"      position: relative;\n" +
		"    }\n" +
		"    .icon {\n" +
		"      width: 72px; height: 72px;\n" +
		"      border-radius: 50%;\n" +
		"      background: " + iconBg + ";\n" +
		"      color: " + iconColor + ";\n" +
		"      font-size: 32px;\n" +
		"      line-height: 72px;\n" +
		"      margin: 0 auto 24px;\n" +
		"      overflow: hidden;\n" +
		"    }\n" +
		"    h1 { font-size: 22px; font-weight: 700; margin-bottom: 12px; }\n" +
		"    .detail { font-size: 14px; color: #555; margin-bottom: 8px; line-height: 1.5; }\n" +
		"    .action { font-size: 13px; color: #888; margin-top: 20px; }\n" +
		"    .bar {\n" +
		"      height: 4px; border-radius: 0 0 16px 16px;\n" +
		"      background: " + accent + ";\n" +
		"      position: absolute; bottom: 0; left: 0; right: 0;\n" +
		"    }\n" +
		"  </style>\n" +
		"</head>\n" +
		"<body>\n" +
		"  <div class=\"card\">\n" +
		"    <div class=\"icon\">" + symbol + "</div>\n" +
		"    <h1>" + heading + "</h1>\n" +
		"    <p class=\"detail\">" + detail + "</p>\n" +
		"    <p class=\"action\">" + action + "</p>\n" +
		"    <div class=\"bar\"></div>\n" +
		"  </div>\n" +
		"</body>\n" +
		"</html>"
	return html
}
