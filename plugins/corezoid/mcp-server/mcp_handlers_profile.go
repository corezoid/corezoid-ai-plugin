package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Connection profiles let one running server target many Corezoid environments
// (markets / projects / dev+prod) by name, WITHOUT rewriting the shared
// cwd/.env that login persists to. login writing to cwd/.env is what lets two
// parallel chats rooted in the same directory clobber each other's environment;
// use-profile sidesteps that by mutating only this process's in-memory auth
// state. Each chat is a separate MCP process, so in-memory state is isolated.

type profileMatch struct {
	Aliases    []string `json:"aliases"`
	Hosts      []string `json:"hosts"`
	JiraPrefix []string `json:"jira_prefix"`
}

type profileEntry struct {
	AccountURL  string       `json:"account_url"`
	WorkspaceID string       `json:"workspace_id"`
	StageID     int          `json:"stage_id"`
	Match       profileMatch `json:"match"`
	IsProd      bool         `json:"is_prod"`
	// EnvFile (optional): path to a .env holding ACCESS_TOKEN for this profile.
	// Tokens are NOT stored in the registry; this points at a per-user file.
	EnvFile string `json:"env_file"`
}

type profileRegistry struct {
	Profiles map[string]profileEntry `json:"profiles"`
}

func profileRegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".corezoid", "profiles", "registry.json"), nil
}

func loadProfileRegistry() (*profileRegistry, error) {
	path, err := profileRegistryPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read profile registry %s: %w", path, err)
	}
	var reg profileRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return &reg, nil
}

// readEnvKey returns a single key's value from a .env file without mutating the
// process environment (unlike loadDotEnv, which Setenv's everything). Used so a
// profile's env_file contributes only its ACCESS_TOKEN, never stale coords.
func readEnvKey(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, key+"=") {
			continue
		}
		v := strings.TrimSpace(line[len(key)+1:])
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		return v
	}
	return ""
}

// resolveProfile picks a profile from a free-form signal. Priority:
//  1. exact registry key
//  2. host substring (a Corezoid URL implies its environment, unambiguously)
//  3. JIRA prefix (e.g. "AZ-123" -> "AZ")
//  4. alias substring
//
// For prefix/alias matches that hit several profiles (e.g. AZ dev + prod share
// prefix "AZ"), the non-prod profile wins unless the signal contains "prod" —
// dev is the safe default.
func resolveProfile(reg *profileRegistry, signal string) (string, profileEntry, error) {
	s := strings.ToLower(strings.TrimSpace(signal))
	wantsProd := strings.Contains(s, "prod")

	// 1. exact key
	if e, ok := reg.Profiles[signal]; ok {
		return signal, e, nil
	}
	if e, ok := reg.Profiles[s]; ok {
		return s, e, nil
	}

	// 2. host substring — unambiguous, return immediately.
	for k, e := range reg.Profiles {
		for _, h := range e.Match.Hosts {
			if h != "" && strings.Contains(s, strings.ToLower(h)) {
				return k, e, nil
			}
		}
	}

	pickWithProdPref := func(candidates map[string]profileEntry) (string, profileEntry, bool) {
		if len(candidates) == 0 {
			return "", profileEntry{}, false
		}
		var fallbackKey string
		var fallbackEntry profileEntry
		for k, e := range candidates {
			if e.IsProd == wantsProd {
				return k, e, true
			}
			fallbackKey, fallbackEntry = k, e
		}
		return fallbackKey, fallbackEntry, true
	}

	// 3. JIRA prefix
	upper := strings.ToUpper(s)
	jiraCands := map[string]profileEntry{}
	for k, e := range reg.Profiles {
		for _, p := range e.Match.JiraPrefix {
			if p != "" && strings.HasPrefix(upper, strings.ToUpper(p)+"-") {
				jiraCands[k] = e
			}
		}
	}
	if k, e, ok := pickWithProdPref(jiraCands); ok {
		return k, e, nil
	}

	// 4. alias substring
	aliasCands := map[string]profileEntry{}
	for k, e := range reg.Profiles {
		for _, a := range e.Match.Aliases {
			if a != "" && strings.Contains(s, strings.ToLower(a)) {
				aliasCands[k] = e
			}
		}
	}
	if k, e, ok := pickWithProdPref(aliasCands); ok {
		return k, e, nil
	}

	keys := make([]string, 0, len(reg.Profiles))
	for k := range reg.Profiles {
		keys = append(keys, k)
	}
	return "", profileEntry{}, fmt.Errorf("no profile matched %q. Available profiles: %s", signal, strings.Join(keys, ", "))
}

// handleUseProfile activates a connection profile for THIS process only.
func handleUseProfile(_ context.Context, args map[string]interface{}) (string, bool) {
	signal := optStrArg(args, "profile")
	if signal == "" {
		signal = optStrArg(args, "signal")
	}
	if signal == "" {
		return "Provide 'profile' (a registry key) or 'signal' (a market name, JIRA key like AZ-123, or a Corezoid URL).", true
	}

	reg, err := loadProfileRegistry()
	if err != nil {
		return err.Error(), true
	}
	key, e, err := resolveProfile(reg, signal)
	if err != nil {
		return err.Error(), true
	}
	if e.AccountURL == "" {
		return fmt.Sprintf("Profile %q has no account_url in registry.json — fill it in first.", key), true
	}
	if e.IsProd && optStrArg(args, "confirm") != "true" {
		return fmt.Sprintf("Profile %q is PRODUCTION (%s). Re-call use-profile with confirm=true to activate.", key, e.AccountURL), true
	}

	token := ""
	if e.EnvFile != "" {
		token = readEnvKey(e.EnvFile, "ACCESS_TOKEN")
	}

	withAuthLock(func() {
		accountURL = e.AccountURL
		apiURL = e.AccountURL // in FF installs the account host IS the API host
		workspaceID = e.WorkspaceID
		stageID = e.StageID
		if token != "" {
			apiToken = token
		}
	})
	// Mirror into this process's env so any os.Getenv reads stay consistent —
	// but deliberately NOT into the shared cwd/.env file.
	os.Setenv("ACCOUNT_URL", e.AccountURL)
	os.Setenv("COREZOID_API_URL", e.AccountURL)
	os.Setenv("WORKSPACE_ID", e.WorkspaceID)
	os.Setenv("COREZOID_STAGE_ID", strconv.Itoa(e.StageID))

	_, tok, _, _, _ := authSnapshot()
	tokNote := "token: kept current session token"
	if token != "" {
		tokNote = "token: loaded from " + e.EnvFile
	}
	if tok == "" {
		tokNote += " — WARNING: no token set; run login or set env_file in the registry"
	}
	prodNote := ""
	if e.IsProd {
		prodNote = " [PRODUCTION]"
	}
	return fmt.Sprintf(
		"Active profile: %s%s\n  account_url = %s\n  workspace_id = %s\n  stage_id = %d\n  %s\n(in-memory only — shared .env untouched, so parallel chats won't collide)",
		key, prodNote, e.AccountURL, e.WorkspaceID, e.StageID, tokNote,
	), false
}
