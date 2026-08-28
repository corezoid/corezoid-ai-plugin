package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// wsItem holds a single workspace entry returned by the list-workspaces API.
type wsItem struct {
	companyID string
	title     string
	role      string // "owner", "admin", or "member"
}

type projectItem struct {
	projectID int64
	title     string
	shortName string
}

type stageItem struct {
	stageID   int64
	title     string
	shortName string
	immutable bool
}

// fetchWorkspaceList calls the Corezoid API and returns the list of workspaces
// available to the authenticated user. Requires apiURL and apiToken to be set.
func fetchWorkspaceList(ctx context.Context) ([]wsItem, error) {
	v := NewValidator(ctx, 0)
	ops := []map[string]any{{"type": "list", "obj": "company"}}
	resp, err := v.req("list_workspaces", ops)
	if err != nil {
		return nil, err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	list, _ := opMap["list"].([]interface{})

	var result []wsItem
	for _, item := range list {
		ws, _ := item.(map[string]interface{})
		companyID, _ := ws["company_id"].(string)
		title, _ := ws["title"].(string)
		isOwner, _ := ws["is_owner"].(bool)
		isAdmin, _ := ws["is_admin"].(bool)
		if companyID == "" {
			continue
		}
		role := "member"
		if isOwner {
			role = "owner"
		} else if isAdmin {
			role = "admin"
		}
		result = append(result, wsItem{companyID: companyID, title: title, role: role})
	}
	return result, nil
}

// fetchProjectList calls the Corezoid API and returns the projects in a workspace.
func fetchProjectList(ctx context.Context, companyID string) ([]projectItem, error) {
	v := NewValidator(ctx, 0)
	ops := []map[string]any{
		{"type": "list", "obj": "projects", "obj_id": 0, "id": companyID, "company_id": companyID, "sort": "title"},
	}
	resp, err := v.req("list_projects", ops)
	if err != nil {
		return nil, err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		return nil, fmt.Errorf("%s", desc)
	}
	list, _ := opMap["list"].([]interface{})

	var result []projectItem
	for _, item := range list {
		p, _ := item.(map[string]interface{})
		projectID := int64(0)
		if f, ok := p["project_id"].(float64); ok {
			projectID = int64(f)
		}
		if projectID == 0 {
			continue
		}
		title, _ := p["title"].(string)
		shortName, _ := p["short_name"].(string)
		result = append(result, projectItem{projectID: projectID, title: title, shortName: shortName})
	}
	return result, nil
}

// fetchStageList calls the Corezoid API and returns the stages (folders) in a project.
func fetchStageList(ctx context.Context, companyID string, projectID int64) ([]stageItem, error) {
	v := NewValidator(ctx, 0)
	ops := []map[string]any{
		{"type": "list", "obj": "project", "obj_id": projectID, "id": companyID, "company_id": companyID, "sort": "date", "order": "asc"},
	}
	resp, err := v.req("list_stages", ops)
	if err != nil {
		return nil, err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		return nil, fmt.Errorf("%s", desc)
	}
	list, _ := opMap["list"].([]interface{})

	var result []stageItem
	for _, item := range list {
		s, _ := item.(map[string]interface{})
		sid := int64(0)
		if f, ok := s["obj_id"].(float64); ok {
			sid = int64(f)
		}
		if sid == 0 {
			continue
		}
		title, _ := s["title"].(string)
		shortName, _ := s["short_name"].(string)
		immutable, _ := s["immutable"].(bool)
		result = append(result, stageItem{stageID: sid, title: title, shortName: shortName, immutable: immutable})
	}
	return result, nil
}

// ensureTokenAuth checks that a valid API token or API key credentials are
// present. If the in-memory token is empty it re-reads ~/.corezoid/config.json
// in case another process (or a login flow in this process) wrote fresh
// credentials since startup; if it's still empty, API key credentials
// (api_login + api_secret) are checked as a fallback.
func ensureTokenAuth() error {
	_, snapToken, _, _, _ := authSnapshot()
	if snapToken == "" {
		syncGlobalsFromCurrent()
		_, snapToken, _, _, _ = authSnapshot()
	}
	if snapToken == "" {
		// Fall back to API key authentication if both login and secret are set.
		snapAPILogin, snapAPISecret := apiKeySnapshot()
		if snapAPILogin != "" && snapAPISecret != "" {
			return nil
		}
		// describeEnvConfigProblems is appended because the most common headless
		// failure is a COREZOID_* variable that *was* set and then rejected —
		// a typo'd ID, or half an API-key pair. Without this the model sees
		// only "missing credentials" and re-runs the login flow that cannot
		// work here, while the real reason sits in the host's stderr.
		return fmt.Errorf("[Error] Not authenticated: missing access_token or api_login+api_secret. Invoke the 'corezoid-init' skill to set up credentials (use the Skill tool with skill=\"corezoid-init\").%s",
			describeEnvConfigProblems())
	}
	return nil
}

// ensureAuth checks that all required credentials are set.
// Returns an error with instructions if any value is missing.
func ensureAuth() error {
	if err := ensureTokenAuth(); err != nil {
		return err
	}

	_, _, _, snapAccountURL, snapStageID := authSnapshot()
	var missing []string
	if snapAccountURL == "" {
		missing = append(missing, "account_url")
	}
	// workspace_id is optional: personal-workspace accounts have no companyID.
	// `Executor.req` strips the empty placeholder from outbound ops in that case.
	if snapStageID == 0 {
		missing = append(missing, "stage_id")
	}

	if len(missing) > 0 {
		// A rejected COREZOID_STAGE_ID lands here as a bare "missing stage_id",
		// which reads as "never configured" rather than "configured wrongly".
		return fmt.Errorf("[Error] Not authenticated: missing %v. Invoke the 'corezoid-init' skill to set up credentials (use the Skill tool with skill=\"corezoid-init\").%s",
			missing, describeEnvConfigProblems())
	}

	// api_url is checked last because, unlike the two above, it can be derived
	// rather than demanded.
	return ensureAPIURL()
}

// fetchCorezoidAPIURLFn indirects the account-clients lookup so tests can drive
// discovery without a network.
var fetchCorezoidAPIURLFn = fetchCorezoidAPIURL

// discoveredAPIURL caches an api_url resolved by ensureAPIURL, together with the
// account_url it was derived from. It is deliberately NOT persisted: the value
// belongs to whatever credentials are in effect right now, and the login flow
// remains the only writer of corezoid_url in ~/.corezoid/config.json.
//
// syncGlobalsFromFolder resets apiURL from the config entry on every auth check,
// so the cache has to live outside it — and it is keyed on account_url so a
// second working directory pointing at a different Corezoid never inherits the
// first one's host.
var (
	apiURLDiscoveryMu   sync.Mutex
	discoveredAPIURL    string
	discoveredAPIURLFor string
)

// ensureAPIURL resolves the API base URL when nothing supplied it.
//
// Only the login tool used to do this, which was fine while login was the only
// way to get credentials. Since credentials can also come from COREZOID_*
// variables — for CI jobs, containers and the HTTP transport, none of which can
// run the browser flow — a headless setup passing account_url + stage_id + a
// token was left with an empty base URL: ensureAuth reported success and the
// first API call went to "/api/2/json" with no host. Deriving the URL here is
// the same step handleLogin takes, moved to the point where it is actually
// needed, so COREZOID_API_URL stays what the README says it is — an optional
// way to skip the lookup.
func ensureAPIURL() error {
	snapAPIURL, snapToken, _, snapAccountURL, _ := authSnapshot()
	if snapAPIURL != "" {
		return nil
	}
	if snapAccountURL == "" {
		// Nothing to derive from. ensureAuth reports account_url as missing in
		// its own words; ensureTokenAuth deliberately does not require it, and
		// must not start failing here over a field it never checked.
		return nil
	}

	apiURLDiscoveryMu.Lock()
	defer apiURLDiscoveryMu.Unlock()
	// A concurrent tool call may have resolved it while we waited.
	if snapAPIURL, _, _, _, _ = authSnapshot(); snapAPIURL != "" {
		return nil
	}
	if discoveredAPIURL != "" && discoveredAPIURLFor == snapAccountURL {
		adoptDiscoveredAPIURL(discoveredAPIURL)
		return nil
	}

	resolved := strings.TrimRight(snapAccountURL, "/")
	if snapToken != "" {
		// The clients endpoint needs a bearer token; API-key-only setups skip
		// straight to the account_url fallback, exactly as handleLogin does.
		// In most deployments the API is served from the admin UI's host.
		fetched, err := fetchCorezoidAPIURLFn(snapAccountURL, snapToken)
		switch {
		case err != nil:
			// Do not silently guess a host after a failed lookup: a wrong base
			// URL would send credentials somewhere the user never named. The
			// failure is not cached — a lookup that failed on a flaky network
			// must be allowed to succeed on the next tool call.
			return fmt.Errorf("[Error] Could not determine the Corezoid API URL from account_url %q: %v. Set COREZOID_API_URL (or corezoid_url in ~/.corezoid/config.json) to the API base URL — no /api/2/json suffix — or re-run the 'corezoid-init' skill.%s",
				snapAccountURL, err, describeEnvConfigProblems())
		case fetched != "":
			resolved = fetched
		default:
			logger.Info("api_url discovery returned no Corezoid client — using account_url %q", resolved)
		}
	}
	if resolved == "" {
		return fmt.Errorf("[Error] Not authenticated: missing api_url and it could not be derived from account_url %q. Set COREZOID_API_URL, or re-run the 'corezoid-init' skill.%s",
			snapAccountURL, describeEnvConfigProblems())
	}

	discoveredAPIURL, discoveredAPIURLFor = resolved, snapAccountURL
	adoptDiscoveredAPIURL(resolved)
	logger.Info("api_url resolved to %q (not persisted)", resolved)
	return nil
}

// adoptDiscoveredAPIURL publishes the resolved URL to the auth globals.
func adoptDiscoveredAPIURL(resolved string) {
	authStateMu.Lock()
	defer authStateMu.Unlock()
	if apiURL == "" {
		apiURL = resolved
	}
}

// resetAPIURLDiscovery drops the cached lookup. Called when the credentials it
// was derived under are replaced (login, logout), so the next check re-derives
// against whatever is in effect then.
func resetAPIURLDiscovery() {
	apiURLDiscoveryMu.Lock()
	defer apiURLDiscoveryMu.Unlock()
	discoveredAPIURL, discoveredAPIURLFor = "", ""
}
