package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credentials holds the Simulator JWT token.
type Credentials struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	TokenType   string    `json:"token_type"` // always "Simulator"
}

// AuthorizationHeader returns the value to use for the Authorization header.
func (c *Credentials) AuthorizationHeader() string {
	tokenType := c.TokenType
	if tokenType == "" {
		tokenType = "Simulator"
	}
	return tokenType + " " + c.AccessToken
}

// envFilePath returns the path to the .env file in the current working directory.
func envFilePath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".env")
}

// updateEnvFile writes or updates key=value in the given .env file.
// If the key already exists, its value is replaced; otherwise the line is appended.
func updateEnvFile(path, key, value string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	prefix := key + "="
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = prefix + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, prefix+value)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

// removeEnvKey removes a key from the .env file.
// Returns nil if the file does not exist.
func removeEnvKey(path, key string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	prefix := key + "="
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, prefix) {
			kept = append(kept, line)
		}
	}

	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}

	content := ""
	if len(kept) > 0 {
		content = strings.Join(kept, "\n") + "\n"
	}
	return os.WriteFile(path, []byte(content), 0600)
}

// Load reads credentials from environment variables.
// The env vars are populated from .env by FindAndLoadDotEnv() at startup.
// Returns nil, nil if ACCESS_TOKEN is not set.
func Load() (*Credentials, error) {
	token := os.Getenv("ACCESS_TOKEN")
	if token == "" {
		return nil, nil
	}
	creds := &Credentials{
		AccessToken: token,
		TokenType:   "Simulator",
	}
	if expiryStr := os.Getenv("ACCESS_TOKEN_EXPIRES_AT"); expiryStr != "" {
		if t, err := time.Parse(time.RFC3339, expiryStr); err == nil {
			creds.ExpiresAt = t
		}
	}
	return creds, nil
}

// Save writes ACCESS_TOKEN (and optionally ACCESS_TOKEN_EXPIRES_AT)
// to the .env file in the current working directory, and updates the in-process env vars.
func Save(creds *Credentials) error {
	path := envFilePath()
	if err := updateEnvFile(path, "ACCESS_TOKEN", creds.AccessToken); err != nil {
		return fmt.Errorf("failed to save token to .env: %w", err)
	}
	os.Setenv("ACCESS_TOKEN", creds.AccessToken)

	if !creds.ExpiresAt.IsZero() {
		expStr := creds.ExpiresAt.Format(time.RFC3339)
		if err := updateEnvFile(path, "ACCESS_TOKEN_EXPIRES_AT", expStr); err != nil {
			return fmt.Errorf("failed to save token expiry to .env: %w", err)
		}
		os.Setenv("ACCESS_TOKEN_EXPIRES_AT", expStr)
	}
	return nil
}

// Delete removes ACCESS_TOKEN and ACCESS_TOKEN_EXPIRES_AT
// from the .env file and from the in-process environment.
func Delete() error {
	path := envFilePath()
	if err := removeEnvKey(path, "ACCESS_TOKEN"); err != nil {
		return err
	}
	if err := removeEnvKey(path, "ACCESS_TOKEN_EXPIRES_AT"); err != nil {
		return err
	}
	os.Unsetenv("ACCESS_TOKEN")
	os.Unsetenv("ACCESS_TOKEN_EXPIRES_AT")
	return nil
}

// SaveAccountURL saves ACCOUNT_URL to the .env file.
func SaveAccountURL(accountURL string) error {
	path := envFilePath()
	if err := updateEnvFile(path, "ACCOUNT_URL", accountURL); err != nil {
		return fmt.Errorf("failed to save ACCOUNT_URL to .env: %w", err)
	}
	os.Setenv("ACCOUNT_URL", accountURL)
	return nil
}

// SaveWorkspaceID saves WORKSPACE_ID to the .env file.
func SaveWorkspaceID(accID string) error {
	path := envFilePath()
	if err := updateEnvFile(path, "WORKSPACE_ID", accID); err != nil {
		return fmt.Errorf("failed to save workspace ID to .env: %w", err)
	}
	os.Setenv("WORKSPACE_ID", accID)
	return nil
}

// IsExpired reports whether the credentials are expired.
func IsExpired(creds *Credentials) bool {
	if creds == nil || creds.AccessToken == "" {
		return true
	}
	if creds.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(creds.ExpiresAt)
}
