package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Access-control operations: sharing processes/folders/stages/projects to
// users, groups and API keys; creating and managing groups and API keys.
//
// All of these hit POST /api/2/json (the unified Corezoid endpoint that
// Executor.req already dispatches to) and follow the same ops envelope as
// folder / conv / dashboard mutations.

// Principal describes an entity that can hold privileges on a Corezoid
// object: a regular user, a user group, or an API key. From the link API's
// point of view an API key is just a "user" record whose login is of type
// "api" — so callers pass Kind="user" for both real users and API keys, and
// Kind="group" for groups.
type Principal struct {
	Kind     string // "user" | "group"  (api_key shares as "user")
	ID       int
	Title    string
	IsAPI    bool   // true when this user is actually an API key
	APILogin int    // login obj_id (logins[0].obj_id); populated for IsAPI=true
	APIKey   string // populated for IsAPI=true after CreateAPIKey
}

// PrivType is one of the four Corezoid privilege flavours.
type PrivType string

const (
	PrivView   PrivType = "view"
	PrivCreate PrivType = "create"
	PrivModify PrivType = "modify"
	PrivDelete PrivType = "delete"
)

// AllPrivs is the standard read+write+delete bundle returned for
// "full access" share requests.
var AllPrivs = []PrivType{PrivCreate, PrivModify, PrivDelete, PrivView}

// privsPayload converts a list of privilege types into the
// [{type, list_obj:["all"]}] structure the API expects.
func privsPayload(privs []PrivType) []map[string]any {
	if len(privs) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(privs))
	for _, p := range privs {
		out = append(out, map[string]any{
			"type":     string(p),
			"list_obj": []string{"all"},
		})
	}
	return out
}

// validateObjKind rejects callers that supplied a sharing target the link API
// does not accept. Kept separate from the API call so unit tests can exercise
// it without hitting the network.
func validateObjKind(kind string) error {
	switch kind {
	case "conv", "folder", "stage", "project":
		return nil
	}
	return fmt.Errorf("obj must be one of conv|folder|stage|project, got %q", kind)
}

// validatePrincipalKind ensures the recipient kind is something the link API
// recognises. API keys share as kind="user" because they are user records in
// the data model.
func validatePrincipalKind(kind string) error {
	switch kind {
	case "user", "group":
		return nil
	}
	return fmt.Errorf("obj_to must be one of user|group, got %q", kind)
}

// ShareObject grants privileges on an object (conv/folder/stage/project) to
// a user, API key or group. Passing an empty privs slice unshares — Corezoid
// uses the same link op shape for grant and revoke, distinguished only by
// whether privs is populated.
func (v *Executor) ShareObject(objKind string, objID int, toKind string, toID int, privs []PrivType, notify bool) (map[string]any, error) {
	if err := validateObjKind(objKind); err != nil {
		return nil, err
	}
	if err := validatePrincipalKind(toKind); err != nil {
		return nil, err
	}
	op := map[string]any{
		"type":              "link",
		"obj":               objKind,
		"obj_id":            objID,
		"obj_to":            toKind,
		"obj_to_id":         toID,
		"is_need_to_notify": notify,
		"company_id":        v.WorkspaceID,
		"privs":             privsPayload(privs),
	}
	resp, err := v.req("share_object", []map[string]any{op})
	if err != nil {
		return nil, fmt.Errorf("ShareObject failed: %w", err)
	}
	return firstOp(resp)
}

// ListShares returns the groups, users and API keys that currently have
// access to the given object. The API uses list_obj:"group" for this query
// but the response includes user and api_key entries too.
func (v *Executor) ListShares(objKind string, objID int) ([]map[string]any, error) {
	if err := validateObjKind(objKind); err != nil {
		return nil, err
	}
	op := map[string]any{
		"type":       "list",
		"obj":        objKind,
		"obj_id":     objID,
		"list_obj":   "group",
		"company_id": v.WorkspaceID,
	}
	resp, err := v.req("list_shares", []map[string]any{op})
	if err != nil {
		return nil, fmt.Errorf("ListShares failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return nil, err
	}
	list, _ := first["list"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// CreateGroup creates a new user group in the current workspace and returns
// the new group's obj_id. obj_type is "admins" — the only kind end users can
// create ("supers" is system-owned). description is optional and only sent
// when non-empty.
func (v *Executor) CreateGroup(title, description string) (int, error) {
	if strings.TrimSpace(title) == "" {
		return 0, fmt.Errorf("group title must not be empty")
	}
	op := map[string]any{
		"type":       "create",
		"obj":        "group",
		"obj_type":   "admins",
		"title":      title,
		"company_id": v.WorkspaceID,
	}
	if description != "" {
		op["description"] = description
	}
	resp, err := v.req("create_group", []map[string]any{op})
	if err != nil {
		return 0, fmt.Errorf("CreateGroup failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return 0, err
	}
	id, ok := first["obj_id"].(float64)
	if !ok {
		return 0, fmt.Errorf("CreateGroup: obj_id missing in response")
	}
	return int(id), nil
}

// ModifyGroup updates a group's title and/or description. Empty fields are
// not sent so callers can patch one field without overwriting others.
func (v *Executor) ModifyGroup(groupID int, title, description string) error {
	if strings.TrimSpace(title) == "" && description == "" {
		return fmt.Errorf("ModifyGroup: at least one of title or description must be provided")
	}
	op := map[string]any{
		"type":       "modify",
		"obj":        "group",
		"obj_id":     groupID,
		"obj_type":   "admins",
		"company_id": v.WorkspaceID,
	}
	if title != "" {
		op["title"] = title
	}
	if description != "" {
		op["description"] = description
	}
	_, err := v.req("modify_group", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("ModifyGroup failed: %w", err)
	}
	return nil
}

// ListGroupObjects returns the processes (conv objects) currently shared with
// the group. Corezoid only documents list_obj="conv" for this endpoint —
// folders / stages / projects shared to the group are not retrievable in one
// call. Caller uses this list to warn before destructive group operations.
func (v *Executor) ListGroupObjects(groupID int) ([]map[string]any, error) {
	op := map[string]any{
		"type":       "list",
		"obj":        "group",
		"obj_id":     groupID,
		"list_obj":   "conv",
		"company_id": v.WorkspaceID,
	}
	resp, err := v.req("list_group_objects", []map[string]any{op})
	if err != nil {
		return nil, fmt.Errorf("ListGroupObjects failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return nil, err
	}
	list, _ := first["list"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// DeleteGroup removes a group. Existing share links that referenced the group
// are revoked server-side. Pass force=false to refuse deletion when the group
// still has objects attached — the caller then explicitly retries with force.
func (v *Executor) DeleteGroup(groupID int, force bool) ([]map[string]any, error) {
	if !force {
		objs, err := v.ListGroupObjects(groupID)
		if err != nil {
			return nil, fmt.Errorf("DeleteGroup precheck failed: %w", err)
		}
		if len(objs) > 0 {
			return objs, fmt.Errorf("group #%d still has %d shared object(s); pass force=true to delete anyway", groupID, len(objs))
		}
	}
	op := map[string]any{
		"type":       "delete",
		"obj":        "group",
		"obj_id":     groupID,
		"company_id": v.WorkspaceID,
	}
	_, err := v.req("delete_group", []map[string]any{op})
	if err != nil {
		return nil, fmt.Errorf("DeleteGroup failed: %w", err)
	}
	return nil, nil
}

// AddToGroup links a user (or API-key user) to a group. level=1 is the
// default admin role for the group; the API rejects 0 silently.
func (v *Executor) AddToGroup(groupID, userID int) error {
	op := map[string]any{
		"type":       "link",
		"obj":        "user",
		"obj_id":     userID,
		"group_id":   groupID,
		"company_id": v.WorkspaceID,
		"level":      1,
	}
	_, err := v.req("add_to_group", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("AddToGroup failed: %w", err)
	}
	return nil
}

// RemoveFromGroup unlinks a user from a group. Corezoid keeps the same
// type:"link" envelope but expects level="" (empty) to mean remove —
// level=0 and type:"unlink" are both rejected by the server.
func (v *Executor) RemoveFromGroup(groupID, userID int) error {
	op := map[string]any{
		"type":       "link",
		"obj":        "user",
		"obj_id":     userID,
		"group_id":   groupID,
		"company_id": v.WorkspaceID,
		"level":      "",
	}
	_, err := v.req("remove_from_group", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("RemoveFromGroup failed: %w", err)
	}
	return nil
}

// ListGroups returns all groups visible in the current workspace.
// "name" filters server-side via substring match; pass "" for no filter.
func (v *Executor) ListGroups(name string) ([]map[string]any, error) {
	op := map[string]any{
		"type":       "list",
		"obj":        "company_users",
		"filter":     "group",
		"sort":       "title",
		"order":      "asc",
		"company_id": v.WorkspaceID,
	}
	if name != "" {
		op["name"] = name
	}
	resp, err := v.req("list_groups", []map[string]any{op})
	if err != nil {
		return nil, fmt.Errorf("ListGroups failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return nil, err
	}
	list, _ := first["list"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// CreateAPIKey provisions a new API key under the current workspace and
// returns the principal (with APILogin/APIKey populated). The secret is
// also persisted to ~/.corezoid/api-keys/<slug>-<obj_id>.json with mode 0600
// so the caller can surface a file path instead of leaking the secret into
// chat output. description is optional.
func (v *Executor) CreateAPIKey(title, description string) (*Principal, string, error) {
	if strings.TrimSpace(title) == "" {
		return nil, "", fmt.Errorf("api key title must not be empty")
	}
	op := map[string]any{
		"type":       "create",
		"obj":        "user",
		"title":      title,
		"company_id": v.WorkspaceID,
		"logins":     []map[string]any{{"type": "api"}},
	}
	if description != "" {
		op["description"] = description
	}
	resp, err := v.req("create_api_key", []map[string]any{op})
	if err != nil {
		return nil, "", fmt.Errorf("CreateAPIKey failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return nil, "", err
	}
	users, _ := first["users"].([]any)
	if len(users) == 0 {
		return nil, "", fmt.Errorf("CreateAPIKey: empty users list in response")
	}
	u, _ := users[0].(map[string]any)
	p := &Principal{
		Kind:  "user",
		Title: stringValue(u, "title"),
		IsAPI: true,
	}
	if f, ok := u["obj_id"].(float64); ok {
		p.ID = int(f)
	}
	if logins, ok := u["logins"].([]any); ok && len(logins) > 0 {
		if l, ok := logins[0].(map[string]any); ok {
			if f, ok := l["obj_id"].(float64); ok {
				p.APILogin = int(f)
			}
			p.APIKey = stringValue(l, "key")
		}
	}
	if p.ID == 0 {
		return nil, "", fmt.Errorf("CreateAPIKey: obj_id missing in response")
	}
	if p.APILogin == 0 {
		return nil, "", fmt.Errorf("CreateAPIKey: login obj_id missing in response")
	}
	path, err := writeAPIKeySecret(p, title, description)
	if err != nil {
		return p, "", fmt.Errorf("CreateAPIKey: created obj_id=%d but failed to persist secret: %w", p.ID, err)
	}
	return p, path, nil
}

// ModifyAPIKey updates the title and/or description of an existing API key
// user record. Empty arguments are omitted so callers can patch a single
// field without overwriting the other.
func (v *Executor) ModifyAPIKey(apiKeyUserID int, title, description string) error {
	if strings.TrimSpace(title) == "" && description == "" {
		return fmt.Errorf("ModifyAPIKey: at least one of title or description must be provided")
	}
	op := map[string]any{
		"type":       "modify",
		"obj":        "user",
		"obj_id":     apiKeyUserID,
		"company_id": v.WorkspaceID,
	}
	if title != "" {
		op["title"] = title
	}
	if description != "" {
		op["description"] = description
	}
	_, err := v.req("modify_api_key", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("ModifyAPIKey failed: %w", err)
	}
	return nil
}

// reSlugUnsafe matches characters not allowed in our secret-filename slug.
// Anything outside ASCII letters/digits/dash/underscore is collapsed to '-'.
var reSlugUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// secretsDir returns ~/.corezoid/api-keys, creating it with mode 0700 if
// missing. Reusing the same root directory as credentials.go means a single
// `rm -rf ~/.corezoid` wipes all sensitive material at once.
func secretsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".corezoid", "api-keys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// writeAPIKeySecret persists a freshly generated key to a 0600 JSON file and
// returns the file path. The secret never leaves disk after this call — the
// MCP handler reports only the path back to the user.
func writeAPIKeySecret(p *Principal, title, description string) (string, error) {
	dir, err := secretsDir()
	if err != nil {
		return "", err
	}
	slug := strings.Trim(reSlugUnsafe.ReplaceAllString(title, "-"), "-")
	if slug == "" {
		slug = "api-key"
	}
	name := fmt.Sprintf("%s-%d.json", slug, p.ID)
	path := filepath.Join(dir, name)
	payload := map[string]any{
		"title":       title,
		"description": description,
		"obj_id":      p.ID,
		"login":       p.APILogin,
		"secret":      p.APIKey,
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// DeleteAPIKey removes an API-key user record. After deletion every object
// owned by it is reassigned to the workspace owner, per the platform docs.
func (v *Executor) DeleteAPIKey(apiKeyUserID int) error {
	op := map[string]any{
		"type":       "delete",
		"obj":        "user",
		"obj_id":     apiKeyUserID,
		"company_id": v.WorkspaceID,
	}
	_, err := v.req("delete_api_key", []map[string]any{op})
	if err != nil {
		return fmt.Errorf("DeleteAPIKey failed: %w", err)
	}
	return nil
}

// ListAPIKeys returns all API keys visible to the current user in the
// workspace. The optional name filter is a substring match on key title.
func (v *Executor) ListAPIKeys(name string) ([]map[string]any, error) {
	op := map[string]any{
		"type":       "list",
		"obj":        "company_users",
		"filter":     "api_key",
		"sort":       "title",
		"order":      "asc",
		"company_id": v.WorkspaceID,
	}
	if name != "" {
		op["name"] = name
	}
	resp, err := v.req("list_api_keys", []map[string]any{op})
	if err != nil {
		return nil, fmt.Errorf("ListAPIKeys failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return nil, err
	}
	list, _ := first["list"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// FindPrincipal does substring search across users, groups and API keys
// inside the current workspace. filter is "user" | "group" | "api_key"
// (or "" — defaults to "user"). The single endpoint is company_users with
// the requested filter applied.
func (v *Executor) FindPrincipal(name, filter string) ([]map[string]any, error) {
	if filter == "" {
		filter = "user"
	}
	switch filter {
	case "user", "group", "api_key", "shared":
	default:
		return nil, fmt.Errorf("filter must be one of user|group|api_key|shared, got %q", filter)
	}
	op := map[string]any{
		"type":       "list",
		"obj":        "company_users",
		"filter":     filter,
		"sort":       "title",
		"order":      "asc",
		"company_id": v.WorkspaceID,
	}
	if name != "" {
		op["name"] = name
	}
	resp, err := v.req("find_principal", []map[string]any{op})
	if err != nil {
		return nil, fmt.Errorf("FindPrincipal failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return nil, err
	}
	list, _ := first["list"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// InviteUser invites an external email (not yet a workspace member) and
// simultaneously shares a single object with them. loginType is typically
// "google" or "corezoid". Returns the activation URL the recipient must
// open to accept the invite.
func (v *Executor) InviteUser(email, loginType, linkObjKind string, linkObjID int, privs []PrivType) (string, error) {
	if err := validateObjKind(linkObjKind); err != nil {
		return "", err
	}
	if strings.TrimSpace(email) == "" {
		return "", fmt.Errorf("email must not be empty")
	}
	if loginType == "" {
		loginType = "google"
	}
	op := map[string]any{
		"type":           "create",
		"obj":            "invite",
		"login":          email,
		"login_type":     loginType,
		"company_id":     v.WorkspaceID,
		"link_to_obj":    linkObjKind,
		"link_to_obj_id": linkObjID,
		"privs":          privsPayload(privs),
	}
	resp, err := v.req("invite_user", []map[string]any{op})
	if err != nil {
		return "", fmt.Errorf("InviteUser failed: %w", err)
	}
	first, err := firstOp(resp)
	if err != nil {
		return "", err
	}
	return stringValue(first, "url"), nil
}

// firstOp extracts ops[0] from a Corezoid response envelope and verifies it
// reports proc=="ok". Centralising the unwrap keeps every executor method in
// this file free of boilerplate response-shape checks.
func firstOp(resp map[string]any) (map[string]any, error) {
	if resp == nil {
		return nil, fmt.Errorf("empty response")
	}
	opsRaw, ok := resp["ops"].([]any)
	if !ok || len(opsRaw) == 0 {
		return nil, fmt.Errorf("response has no ops")
	}
	op, ok := opsRaw[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected op format")
	}
	if proc, _ := op["proc"].(string); proc != "ok" {
		desc, _ := op["description"].(string)
		if desc == "" {
			desc = fmt.Sprintf("proc=%v", op["proc"])
		}
		return op, fmt.Errorf("%s", desc)
	}
	return op, nil
}

// stringValue safely reads a string field from a JSON-decoded map.
func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}
