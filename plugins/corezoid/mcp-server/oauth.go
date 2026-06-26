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
	"strings"
	"time"
)

const (
	oauthDefaultClientID = "5ec679f5a2710f0da6000005"
)

// PKCEResult holds the token returned after a successful OAuth2 PKCE flow.
type PKCEResult struct {
	AccessToken string
	ExpiresAt   time.Time
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

// oauthPKCEFlow runs the OAuth2 PKCE authorization flow against the given account URL.
// It opens the user's browser, starts a local callback server, and exchanges the
// authorization code for an access token.
func oauthPKCEFlow(accountURL, clientID string) (*PKCEResult, error) {
	accountURL = strings.TrimRight(accountURL, "/")
	verifier, err := generateVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	challenge := generateChallenge(verifier)

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("failed to generate OAuth state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	port, err := findFreePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find free port for callback: %w", err)
	}
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
	authorizeURL := accountURL + "/oauth2/authorize"
	tokenURL := accountURL + "/oauth2/token"
	authURL := authorizeURL + "?" + params.Encode()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              fmt.Sprintf("localhost:%d", port),
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
		if srvErr := srv.ListenAndServe(); srvErr != nil && srvErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server error: %w", srvErr)
		}
	}()

	fmt.Fprintf(os.Stderr, "Opening browser for Corezoid authentication...\nIf it did not open automatically, visit:\n%s\n", authURL)
	openBrowser(authURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
	case oauthErr := <-errCh:
		_ = srv.Shutdown(context.Background())
		return nil, oauthErr
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return nil, fmt.Errorf("authentication timed out after 5 minutes")
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
	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.PostForm(tokenURL, tokenParams)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
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

	// Corezoid and Simulator share account.corezoid.com — token field is simulator_token.
	// Fall back to standard access_token if absent.
	var accessToken string
	if t, ok := tokenResp["simulator_token"].(string); ok && t != "" {
		accessToken = t
	} else if t, ok := tokenResp["access_token"].(string); ok && t != "" {
		accessToken = t
	}
	if accessToken == "" {
		return nil, fmt.Errorf("no token in OAuth response: %s", string(body))
	}

	return &PKCEResult{
		AccessToken: accessToken,
		ExpiresAt:   parseJWTExpiry(accessToken),
	}, nil
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
