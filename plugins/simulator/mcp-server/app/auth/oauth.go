package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	DefaultAccountURL = "https://account.corezoid.com"

	// DefaultClientID is the built-in OAuth2 client ID for the Simulator Claude Code plugin.
	DefaultClientID = "5ec679f5a2710f0da6000005"
)

// PKCEFlow runs the full OAuth2 PKCE authorization code flow.
// It starts a local HTTP server to receive the callback, opens the user's browser,
// waits for the authorization code, exchanges it for tokens, and returns Credentials.
// accountURL defaults to DefaultAccountURL if empty (also checks ACCOUNT_URL env var).
// clientID defaults to DefaultClientID if empty.
func PKCEFlow(accountURL, clientID string, scopes []string) (*Credentials, error) {
	if accountURL == "" {
		accountURL = os.Getenv("ACCOUNT_URL")
	}
	if accountURL == "" {
		accountURL = DefaultAccountURL
	}
	accountURL = strings.TrimRight(accountURL, "/")

	if clientID == "" {
		clientID = DefaultClientID
	}

	authorizeURL := accountURL + "/oauth2/authorize"
	tokenURL := accountURL + "/oauth2/token"

	// Generate PKCE code verifier (random 32 bytes → base64url, no padding)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Compute code challenge = base64url(SHA256(verifier))
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Pick a random available port for the redirect server
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start callback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// Build authorization URL
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	if len(scopes) > 0 {
		params.Set("scope", strings.Join(scopes, " "))
	}
	authURL := authorizeURL + "?" + params.Encode()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errCode := q.Get("error"); errCode != "" {
			desc := q.Get("error_description")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(oauthPageHTML("Authentication Failed", "error",
				"Authentication failed",
				"<strong>"+errCode+"</strong>: "+desc,
				"You may close this tab.")))
			errCh <- fmt.Errorf("OAuth error: %s – %s", errCode, desc)
			return
		}
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(oauthPageHTML("Authentication Failed", "error",
				"Authentication failed",
				"No authorization code received.",
				"You may close this tab.")))
			errCh <- fmt.Errorf("no authorization code in callback")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(oauthPageHTML("Authorization successful!", "success",
			"Authorization successful!",
			"You are now connected to Simulator.Company.",
			"You may close this tab and return to Claude Code.")))
		codeCh <- code
	})

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("OAuth callback server error: %v", err)
		}
	}()

	log.Printf("Opening browser for Simulator authorization...\nIf it did not open automatically, visit:\n  %s\n", authURL)
	_ = openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case oauthErr := <-errCh:
		_ = srv.Shutdown(context.Background())
		return nil, oauthErr
	case <-time.After(5 * time.Minute):
		_ = srv.Shutdown(context.Background())
		return nil, fmt.Errorf("timed out waiting for OAuth callback (5 minutes)")
	}
	_ = srv.Shutdown(context.Background())

	return exchangeCode(tokenURL, clientID, code, codeVerifier, redirectURI)
}

// exchangeCode exchanges an authorization code for access and refresh tokens.
func exchangeCode(tokenURL, clientID, code, codeVerifier, redirectURI string) (*Credentials, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", clientID)
	data.Set("code", code)
	data.Set("code_verifier", codeVerifier)
	data.Set("redirect_uri", redirectURI)

	return postTokenRequest(tokenURL, data)
}

// tokenResponse is the raw JSON response from the Simulator token endpoint.
type tokenResponse struct {
	SimulatorToken string `json:"simulator_token"` // JWT — the actual MCP auth token
	Error          string `json:"error"`
	ErrorDesc      string `json:"error_description"`
}

func postTokenRequest(tokenURL string, data url.Values) (*Credentials, error) {
	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	if tr.Error != "" {
		return nil, fmt.Errorf("token error: %s – %s", tr.Error, tr.ErrorDesc)
	}
	if tr.SimulatorToken == "" {
		return nil, fmt.Errorf("no simulator_token in response: %s", string(body))
	}

	creds := &Credentials{
		AccessToken: tr.SimulatorToken,
		TokenType:   "Simulator",
		ExpiresAt:   jwtExpiry(tr.SimulatorToken),
	}
	return creds, nil
}

// jwtExpiry extracts the exp claim from a JWT without verifying the signature.
// Returns zero time if parsing fails (treated as "no expiry").
func jwtExpiry(token string) time.Time {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// oauthPageHTML generates a styled HTML page for OAuth2 callback responses.
// kind is "success" or "error".
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
	return "<!DOCTYPE html>\n" +
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
}

// openBrowser opens the given URL in the default system browser (macOS / Linux / Windows).
func openBrowser(u string) error {
	if err := exec.Command("open", u).Start(); err == nil {
		return nil
	}
	if err := exec.Command("xdg-open", u).Start(); err == nil {
		return nil
	}
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start(); err == nil {
		return nil
	}
	return fmt.Errorf("could not open browser automatically")
}
