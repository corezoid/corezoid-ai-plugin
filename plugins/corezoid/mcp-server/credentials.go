package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credentials holds the saved OAuth token for the Corezoid MCP server.
// AccessToken maps to ACCESS_TOKEN (the simulator_token returned by account.corezoid.com).
type Credentials struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	TokenType   string    `json:"token_type"`
}

// envFilePath returns the path to the project-level .env file (cwd).
// Project config (WORKSPACE_ID, STAGE_ID, API URLs) lives here.
func envFilePath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".env")
}

// credentialsFilePath returns ~/.corezoid/credentials — the user-level store
// for ACCESS_TOKEN and ACCESS_TOKEN_EXPIRES_AT.  The directory is created on
// first write; the file is kept outside the project tree so tokens can never
// be accidentally committed to git.
func credentialsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".corezoid")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	return filepath.Join(dir, "credentials"), nil
}

// updateEnvFile writes or updates key=value in the given .env file.
// If the key already exists, its value is replaced; otherwise the line is
// appended. The host can keep more than one server process alive at once
// (observed directly: up to 5 concurrently), and this read-modify-write has
// no cross-process lock, so two processes updating different keys around
// the same time can race — whichever writes last silently drops the other's
// change. Retrying with a read-back-and-verify closes that window for the
// common case without needing a real file lock.
func updateEnvFile(path, key, value string) error {
	const maxAttempts = 5
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Small jittered backoff so a colliding writer's own attempt has
			// time to finish before we retry, instead of both processes
			// immediately re-racing each other.
			time.Sleep(time.Duration(20+rand.Intn(60)) * time.Millisecond)
		}
		if err := writeEnvKey(path, key, value); err != nil {
			return err
		}
		if envFileHasKeyValue(path, key, value) {
			return nil
		}
		lastErr = fmt.Errorf("value did not stick — a concurrent writer likely overwrote it")
	}
	return fmt.Errorf("updateEnvFile: failed to persist %s after %d attempts: %w", key, maxAttempts, lastErr)
}

func writeEnvKey(path, key, value string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
		// Remove trailing empty line — we'll add it back after.
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

// envFileHasKeyValue reports whether path currently has key=value on disk.
func envFileHasKeyValue(path, key, value string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return line == prefix+value
		}
	}
	return false
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

	// Trim trailing empty lines.
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}

	content := ""
	if len(kept) > 0 {
		content = strings.Join(kept, "\n") + "\n"
	}
	return os.WriteFile(path, []byte(content), 0600)
}

// loadCredentials reads credentials from environment variables.
// The env vars are populated from .env by findAndLoadDotEnv() at startup.
// Returns nil, nil if ACCESS_TOKEN is not set.
func loadCredentials() (*Credentials, error) {
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

// saveCredentials writes ACCESS_TOKEN (and optionally ACCESS_TOKEN_EXPIRES_AT)
// to ~/.corezoid/credentials (user-level), not to the project .env.
func saveCredentials(creds *Credentials) error {
	path, err := credentialsFilePath()
	if err != nil {
		return err
	}
	if err := updateEnvFile(path, "ACCESS_TOKEN", creds.AccessToken); err != nil {
		return fmt.Errorf("failed to save token to %s: %w", path, err)
	}
	os.Setenv("ACCESS_TOKEN", creds.AccessToken)

	if !creds.ExpiresAt.IsZero() {
		expStr := creds.ExpiresAt.Format(time.RFC3339)
		if err := updateEnvFile(path, "ACCESS_TOKEN_EXPIRES_AT", expStr); err != nil {
			return fmt.Errorf("failed to save token expiry to %s: %w", path, err)
		}
		os.Setenv("ACCESS_TOKEN_EXPIRES_AT", expStr)
	}
	return nil
}

// deleteCredentials removes ACCESS_TOKEN and ACCESS_TOKEN_EXPIRES_AT
// from ~/.corezoid/credentials and from the in-process environment.
func deleteCredentials() error {
	path, err := credentialsFilePath()
	if err != nil {
		return err
	}
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

// isCredentialsExpired returns true if the credentials have a known expiry that has passed.
// Credentials with a zero ExpiresAt are treated as non-expiring.
func isCredentialsExpired(creds *Credentials) bool {
	if creds.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(creds.ExpiresAt)
}
