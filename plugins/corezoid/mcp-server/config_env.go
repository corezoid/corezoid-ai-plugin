package main

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Environment variables that can supply Folder fields when
// ~/.corezoid/config.json has nothing for the current working directory.
//
// They are a *fallback*, never an override: a value persisted for the matching
// Folder always wins, field by field. The environment only fills the gaps.
// This exists for hosts that cannot run the interactive browser login and have
// no writable config — CI jobs, containers, the Streamable HTTP transport.
const (
	envAccountURL   = "COREZOID_ACCOUNT_URL"
	envCorezoidURL  = "COREZOID_API_URL"
	envAPIGwURL     = "COREZOID_APIGW_URL"
	envWorkspaceID  = "COREZOID_WORKSPACE_ID"
	envProjectID    = "COREZOID_PROJECT_ID"
	envStageID      = "COREZOID_STAGE_ID"
	envGitURL       = "COREZOID_GIT_URL"
	envGitStagePath = "COREZOID_GIT_STAGE_PATH"
	envAccessToken  = "COREZOID_ACCESS_TOKEN"
	envExpiresAt    = "COREZOID_TOKEN_EXPIRES_AT"
	envAPILogin     = "COREZOID_API_LOGIN"
	envAPISecret    = "COREZOID_API_SECRET"
)

// envFolder reads the supported Folder fields from the process environment.
// Every field is optional; an unset or blank variable leaves the field zero so
// applyEnvFallback can distinguish "not provided" from "provided".
func envFolder() Folder {
	return Folder{
		AccountURL:   envTrimmed(envAccountURL),
		CorezoidURL:  envTrimmed(envCorezoidURL),
		APIGwURL:     envTrimmed(envAPIGwURL),
		WorkspaceID:  envTrimmed(envWorkspaceID),
		ProjectID:    envPositiveInt(envProjectID),
		StageID:      envPositiveInt(envStageID),
		GitURL:       envTrimmed(envGitURL),
		GitStagePath: envTrimmed(envGitStagePath),
		AccessToken:  envTrimmed(envAccessToken),
		ExpiresAt:    envTimestamp(envExpiresAt),
		APILogin:     envTrimmed(envAPILogin),
		APISecret:    envTrimmed(envAPISecret),
	}
}

func envTrimmed(key string) string { return strings.TrimSpace(os.Getenv(key)) }

// envMalformedWarned dedupes the "malformed value" warning per variable.
// applyEnvFallback runs on every auth check, so a single typo would otherwise
// repeat in the log for the whole session.
var envMalformedWarned sync.Map

func warnMalformedEnv(key, raw, want string) {
	if _, loaded := envMalformedWarned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	logger.Warn("env fallback: %s=%q is not %s — ignored", key, raw, want)
}

// envPositiveInt parses key as a positive integer ID. Anything unparseable or
// non-positive is treated as unset and warned about — silently dropping a
// typo'd stage ID would send operations to the wrong place.
func envPositiveInt(key string) int {
	raw := envTrimmed(key)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		warnMalformedEnv(key, raw, "a positive integer")
		return 0
	}
	return n
}

// envTimestamp parses key as an RFC 3339 timestamp. An absent or malformed
// value yields the zero time, which syncGlobalsFromFolder treats as "no known
// expiry" — the right default for a long-lived CI token.
func envTimestamp(key string) time.Time {
	raw := envTrimmed(key)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		warnMalformedEnv(key, raw, "an RFC 3339 timestamp")
		return time.Time{}
	}
	return t
}

// folderIsEmpty reports whether f carries no usable configuration. RootPath is
// excluded on purpose: it identifies a Folder, it is not something the
// environment can supply.
func folderIsEmpty(f Folder) bool {
	return f.AccountURL == "" && f.CorezoidURL == "" && f.APIGwURL == "" &&
		f.WorkspaceID == "" && f.ProjectID == 0 && f.StageID == 0 &&
		f.GitURL == "" && f.GitStagePath == "" && f.AccessToken == "" &&
		f.APILogin == "" && f.APISecret == ""
}

// envConfigActive reports whether the environment supplies any Folder field.
// Used to tell the user that logout cannot remove ambient credentials.
func envConfigActive() bool { return !folderIsEmpty(envFolder()) }

// envFallbackLogged keeps the "using env fallback" notice to a single line per
// process — syncGlobalsFromFolder runs on every auth check. Deliberately not a
// sync.Once: the flag is set only once something was actually taken from the
// environment, so a first call that needed no fallback (complete config entry)
// does not swallow the notice for a later one that does (e.g. after logout).
var envFallbackLogMu sync.Mutex
var envFallbackLogged bool

// applyEnvFallback returns the effective Folder: f with every field that the
// config file left unset filled in from the environment. A nil f — no entry in
// ~/.corezoid/config.json matches the current working directory — yields an
// env-only Folder rooted at the resolved work dir, or nil when the environment
// carries nothing usable (the fresh-install state).
//
// The returned Folder is a copy; nothing here is persisted. Writers take their
// values from the login buffer (handleLogin) or from something they resolved
// themselves (the project_id / git_url / git_stage_path caches), never from
// these globals, so env-provided values do not leak into the user's config file.
func applyEnvFallback(f *Folder) *Folder {
	env := envFolder()
	if folderIsEmpty(env) {
		return f
	}
	if f == nil {
		env.RootPath = resolveWorkDir()
		logEnvFallback(env, nil)
		return &env
	}

	out := *f
	// An expired stored token counts as absent: without this the env token
	// would be shadowed by a token syncGlobalsFromFolder is about to discard,
	// leaving the process with no credentials at all.
	if out.AccessToken != "" && !out.ExpiresAt.IsZero() && time.Now().After(out.ExpiresAt) {
		out.AccessToken = ""
		out.ExpiresAt = time.Time{}
	}

	if out.AccountURL == "" {
		out.AccountURL = env.AccountURL
	}
	if out.CorezoidURL == "" {
		out.CorezoidURL = env.CorezoidURL
	}
	if out.APIGwURL == "" {
		out.APIGwURL = env.APIGwURL
	}
	if out.WorkspaceID == "" {
		out.WorkspaceID = env.WorkspaceID
	}
	if out.ProjectID == 0 {
		out.ProjectID = env.ProjectID
	}
	if out.StageID == 0 {
		out.StageID = env.StageID
	}
	if out.GitURL == "" {
		out.GitURL = env.GitURL
	}
	if out.GitStagePath == "" {
		out.GitStagePath = env.GitStagePath
	}
	if out.AccessToken == "" {
		out.AccessToken = env.AccessToken
		out.ExpiresAt = env.ExpiresAt
	}
	if out.APILogin == "" {
		out.APILogin = env.APILogin
	}
	if out.APISecret == "" {
		out.APISecret = env.APISecret
	}
	logEnvFallback(out, f)
	return &out
}

// logEnvFallback reports once which fields the environment supplied. Field
// names only — token and secret values are never logged.
func logEnvFallback(effective Folder, stored *Folder) {
	var from []string
	add := func(name string, taken bool) {
		if taken {
			from = append(from, name)
		}
	}
	var s Folder
	if stored != nil {
		s = *stored
	}
	add("account_url", s.AccountURL == "" && effective.AccountURL != "")
	add("corezoid_url", s.CorezoidURL == "" && effective.CorezoidURL != "")
	add("apigw_url", s.APIGwURL == "" && effective.APIGwURL != "")
	add("workspace_id", s.WorkspaceID == "" && effective.WorkspaceID != "")
	add("project_id", s.ProjectID == 0 && effective.ProjectID != 0)
	add("stage_id", s.StageID == 0 && effective.StageID != 0)
	add("git_url", s.GitURL == "" && effective.GitURL != "")
	add("git_stage_path", s.GitStagePath == "" && effective.GitStagePath != "")
	add("access_token", s.AccessToken == "" && effective.AccessToken != "")
	add("api_login", s.APILogin == "" && effective.APILogin != "")
	add("api_secret", s.APISecret == "" && effective.APISecret != "")
	if len(from) == 0 {
		return
	}

	envFallbackLogMu.Lock()
	defer envFallbackLogMu.Unlock()
	if envFallbackLogged {
		return
	}
	envFallbackLogged = true

	scope := "no config entry for this directory"
	if stored != nil {
		scope = "gaps in the config entry for this directory"
	}
	logger.Info("env fallback: %s — filled %s from environment variables", scope, strings.Join(from, ", "))
}
