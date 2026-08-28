package main

import (
	"fmt"
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
//
// "Field by field" has two deliberate exceptions, both credential pairs that
// are meaningless when assembled from two sources — see mergeEnvFallback.
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
// mergeEnvFallback can distinguish "not provided" from "provided".
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

// envConfigNotes carries env-fallback diagnostics to the *agent*, not just to
// the host's stderr. logger.Warn writes to stderr, which the host may show to
// the human but never puts in a tool result — so a typo in COREZOID_STAGE_ID
// used to surface to the model only as "missing stage_id", with no hint that a
// variable had been set and rejected. Auth errors append these notes.
//
// Values are never recorded beyond what warnMalformedEnv already logs: the
// malformed literal for an ID or timestamp (never a token or a secret).
var envConfigNoteMu sync.Mutex
var envConfigNotes []string

// recordEnvConfigNote stores a diagnostic once. applyEnvFallback runs on every
// auth check, so an unguarded append would grow without bound.
func recordEnvConfigNote(note string) {
	envConfigNoteMu.Lock()
	defer envConfigNoteMu.Unlock()
	for _, existing := range envConfigNotes {
		if existing == note {
			return
		}
	}
	envConfigNotes = append(envConfigNotes, note)
}

// envConfigNotesSnapshot returns a copy of the diagnostics collected so far.
func envConfigNotesSnapshot() []string {
	envConfigNoteMu.Lock()
	defer envConfigNoteMu.Unlock()
	if len(envConfigNotes) == 0 {
		return nil
	}
	return append([]string(nil), envConfigNotes...)
}

// describeEnvConfigProblems renders the collected diagnostics as a block to
// append to an auth error, or "" when there is nothing to say.
func describeEnvConfigProblems() string {
	notes := envConfigNotesSnapshot()
	if len(notes) == 0 {
		return ""
	}
	return "\n\nEnvironment configuration was rejected:\n- " + strings.Join(notes, "\n- ")
}

// envMalformedWarned dedupes the "malformed value" warning per variable.
// applyEnvFallback runs on every auth check, so a single typo would otherwise
// repeat in the log for the whole session.
var envMalformedWarned sync.Map

func warnMalformedEnv(key, raw, want string) {
	if _, loaded := envMalformedWarned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	logger.Warn("env fallback: %s=%q is not %s — ignored", key, raw, want)
	recordEnvConfigNote(fmt.Sprintf("%s=%q is not %s and was ignored.", key, raw, want))
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
//
// Deliberately fail-open rather than rejecting the whole token: expiry is
// enforced server-side anyway (an expired token gets a 401), so refusing to use
// a probably-valid token because its *expiry literal* was typo'd would turn a
// working CI job into a hard failure while preventing nothing. The malformed
// value is reported through warnMalformedEnv either way.
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
func envConfigActive() bool { return !folderIsEmpty(envFolder()) }

// envCredentialsActive reports whether the environment supplies credentials the
// server would actually authenticate with — a token, or a complete API-key pair.
//
// Distinct from envConfigActive because only this answer contradicts a logout.
// Half an API-key pair does not count: mergeEnvAPIKey refuses it, so it can
// authenticate as nothing.
func envCredentialsActive() bool {
	env := envFolder()
	return env.AccessToken != "" || (env.APILogin != "" && env.APISecret != "")
}

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

	base := Folder{}
	hadStored := f != nil
	if hadStored {
		base = *f
	} else {
		base.RootPath = resolveWorkDir()
	}

	out, from := mergeEnvFallback(base, env)
	logEnvFallback(from, hadStored)
	return &out
}

// mergeEnvFallback fills every field out leaves unset from env, and returns the
// result together with the names of the fields the environment actually
// supplied.
//
// Provenance is collected *here*, as each field is filled, rather than
// reconstructed afterwards by diffing the result against the stored Folder.
// The diff cannot express this function's two non-trivial cases: an expired
// stored token is cleared below, so a comparison against the original Folder
// reads its still-present token as "the config supplied it" and silently omits
// access_token from the report — in the common headless case (full config, dead
// token, COREZOID_ACCESS_TOKEN set) that emptied the list entirely and printed
// no provenance line at all.
func mergeEnvFallback(out Folder, env Folder) (Folder, []string) {
	var from []string
	str := func(name string, dst *string, src string) {
		if *dst == "" && src != "" {
			*dst = src
			from = append(from, name)
		}
	}
	num := func(name string, dst *int, src int) {
		if *dst == 0 && src != 0 {
			*dst = src
			from = append(from, name)
		}
	}

	// An expired stored token counts as absent: without this the env token
	// would be shadowed by a token syncGlobalsFromFolder is about to discard,
	// leaving the process with no credentials at all.
	if out.AccessToken != "" && !out.ExpiresAt.IsZero() && time.Now().After(out.ExpiresAt) {
		out.AccessToken = ""
		out.ExpiresAt = time.Time{}
	}

	str("account_url", &out.AccountURL, env.AccountURL)
	str("corezoid_url", &out.CorezoidURL, env.CorezoidURL)
	str("apigw_url", &out.APIGwURL, env.APIGwURL)
	str("workspace_id", &out.WorkspaceID, env.WorkspaceID)
	num("project_id", &out.ProjectID, env.ProjectID)
	num("stage_id", &out.StageID, env.StageID)
	str("git_url", &out.GitURL, env.GitURL)
	str("git_stage_path", &out.GitStagePath, env.GitStagePath)

	// access_token and its expiry are one credential, not two fields: the
	// expiry describes the token it was issued with, so pairing a fresh env
	// token with a lifetime left over from the config would misreport when the
	// token dies. Taken together or not at all.
	if out.AccessToken == "" && env.AccessToken != "" {
		out.AccessToken = env.AccessToken
		out.ExpiresAt = env.ExpiresAt
		from = append(from, "access_token")
	}

	mergeEnvAPIKey(&out, env, &from)
	return out, from
}

// mergeEnvAPIKey applies the api_login/api_secret pair, which is the second
// exception to field-by-field merging.
//
// The pair is verified server-side as a unit: apiKeySign computes the signature
// from the secret and doWithRetry puts the login in the URL, so Corezoid checks
// that signature against the secret belonging to *that* login. A login from the
// config combined with a secret from the environment therefore cannot
// authenticate as anything — it can only produce an opaque 401 from a remote
// host. Refusing the half-pair here turns that into a local failure that names
// the cause.
func mergeEnvAPIKey(out *Folder, env Folder, from *[]string) {
	if env.APILogin == "" && env.APISecret == "" {
		return
	}

	// The config entry already has a usable pair: it wins, as every other field does.
	if out.APILogin != "" && out.APISecret != "" {
		return
	}

	// The config entry has exactly one half. Completing it from the environment
	// would build a pair that can never verify.
	if out.APILogin != "" || out.APISecret != "" {
		stored, missing := "api_login", "api_secret"
		if out.APILogin == "" {
			stored, missing = "api_secret", "api_login"
		}
		recordEnvConfigNote(fmt.Sprintf(
			"%s is set in the environment but ~/.corezoid/config.json already supplies %s without %s. "+
				"API-key credentials are verified as a pair, so a login and a secret from different sources can never "+
				"authenticate — the environment value was not used. Set both %s and %s, or remove %s from the config entry.",
			envAPIKeyNames(env), stored, missing, envAPILogin, envAPISecret, stored))
		return
	}

	// Nothing stored: the environment must supply both halves or neither.
	if env.APILogin == "" || env.APISecret == "" {
		recordEnvConfigNote(fmt.Sprintf(
			"%s is set but its counterpart is not. API-key authentication needs %s and %s together, so neither was used.",
			envAPIKeyNames(env), envAPILogin, envAPISecret))
		return
	}

	out.APILogin, out.APISecret = env.APILogin, env.APISecret
	*from = append(*from, "api_login", "api_secret")
}

// envAPIKeyNames names the API-key variables the environment sets. Names only —
// the secret is never rendered, and the login is masked in request URLs for the
// same reason (see doWithRetry), so it is not printed here either.
func envAPIKeyNames(env Folder) string {
	switch {
	case env.APILogin != "" && env.APISecret != "":
		return envAPILogin + " and " + envAPISecret
	case env.APILogin != "":
		return envAPILogin
	default:
		return envAPISecret
	}
}

// logEnvFallback reports once which fields the environment supplied. Field
// names only — token and secret values are never logged.
func logEnvFallback(from []string, hadStored bool) {
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
	if hadStored {
		scope = "gaps in the config entry for this directory"
	}
	logger.Info("env fallback: %s — filled %s from environment variables", scope, strings.Join(from, ", "))
}
