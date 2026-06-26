package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// privsExplicitNone reports whether the caller explicitly asked to revoke
// access. We need three-way detection — "missing", "explicitly empty"
// (revoke) and "explicit list" (grant) — because the underlying link op
// uses the same shape for both grant and revoke, distinguished only by an
// empty privs array.
func privsExplicitNone(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch strings.ToLower(s) {
	case "none", "[]", "revoke", "unshare":
		return true
	}
	return false
}

// parsePrivs accepts either a comma-separated string ("view,modify") or a
// JSON array (`["view","modify"]`) and returns the canonical PrivType slice.
// "all" expands to the full bundle. Returns (nil, nil) on empty input — the
// caller decides whether absent means "grant all" or is an error. To request
// an explicit revoke, callers pass one of the privsExplicitNone keywords
// rather than going through this function.
func parsePrivs(s string) ([]PrivType, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var tokens []string
	if strings.HasPrefix(s, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err != nil {
			return nil, fmt.Errorf("privs JSON array invalid: %v", err)
		}
		tokens = arr
	} else {
		for _, t := range strings.Split(s, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tokens = append(tokens, t)
			}
		}
	}
	var out []PrivType
	for _, t := range tokens {
		t = strings.ToLower(t)
		if t == "all" {
			return AllPrivs, nil
		}
		switch PrivType(t) {
		case PrivView, PrivCreate, PrivModify, PrivDelete:
			out = append(out, PrivType(t))
		default:
			return nil, fmt.Errorf("unknown priv %q (allowed: view, create, modify, delete, all, none)", t)
		}
	}
	return out, nil
}

// handleShareObject grants (or revokes) privileges on a process / folder /
// stage / project for a user, API key (kind=user) or group. Pass
// privs="none" (or "[]") to revoke — that's the same wire operation as
// share with privs=[]. Absent privs defaults to "all".
func handleShareObject(ctx context.Context, args map[string]interface{}) (string, bool) {
	objKind, err := strArg(args, "obj")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	objID, err := intArg(args, "obj_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	toKind, err := strArg(args, "obj_to")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	toID, err := intArg(args, "obj_to_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	privsArg := optStrArg(args, "privs")
	revoke := privsExplicitNone(privsArg)
	var privs []PrivType
	if !revoke {
		p, err := parsePrivs(privsArg)
		if err != nil {
			return "Error: " + err.Error(), true
		}
		if len(p) == 0 {
			p = AllPrivs
		}
		privs = p
	}
	notify := true
	if v, ok := args["notify"]; ok {
		if b, ok := v.(bool); ok {
			notify = b
		}
	}

	v := NewValidator(ctx, 0)
	op, err := v.ShareObject(objKind, objID, toKind, toID, privs, notify)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	title := stringValue(op, "obj_to_title")
	if revoke {
		return fmt.Sprintf("Revoked %s #%d access for %s #%d (%s)",
			objKind, objID, toKind, toID, title), false
	}
	return fmt.Sprintf("Shared %s #%d with %s #%d (%s) — privs: %s",
		objKind, objID, toKind, toID, title, privsString(privs)), false
}

// handleListShares prints who currently has access to an object.
func handleListShares(ctx context.Context, args map[string]interface{}) (string, bool) {
	objKind, err := strArg(args, "obj")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	objID, err := intArg(args, "obj_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	list, err := v.ListShares(objKind, objID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if len(list) == 0 {
		return fmt.Sprintf("No shares on %s #%d", objKind, objID), false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Shares on %s #%d (%d):\n\n", objKind, objID, len(list)))
	sb.WriteString(fmt.Sprintf("  %-8s %-10s %-30s %s\n", "kind", "id", "title", "privs"))
	sb.WriteString("  " + strings.Repeat("-", 70) + "\n")
	for _, item := range list {
		kind := stringValue(item, "obj")
		if kind == "" {
			kind = "?"
		}
		title := stringValue(item, "title")
		id := 0
		if f, ok := item["obj_id"].(float64); ok {
			id = int(f)
		}
		privs := formatItemPrivs(item)
		sb.WriteString(fmt.Sprintf("  %-8s %-10d %-30s %s\n", kind, id, truncate(title, 28), privs))
	}
	return sb.String(), false
}

// formatItemPrivs renders the privs array from a list-shares response item.
func formatItemPrivs(item map[string]any) string {
	pp, ok := item["privs"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, p := range pp {
		m, _ := p.(map[string]any)
		if t := stringValue(m, "type"); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, ",")
}

func privsString(p []PrivType) string {
	out := make([]string, 0, len(p))
	for _, v := range p {
		out = append(out, string(v))
	}
	return strings.Join(out, ",")
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// handleCreateGroup creates a new user group and returns its obj_id.
func handleCreateGroup(ctx context.Context, args map[string]interface{}) (string, bool) {
	title, err := strArg(args, "title")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	description := optStrArg(args, "description")
	v := NewValidator(ctx, 0)
	id, err := v.CreateGroup(title, description)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf("Group %q created — obj_id=%d (use this as obj_to_id with obj_to=group when sharing).", title, id), false
}

// handleModifyGroup renames or re-describes a group. At least one of title
// or description must be supplied.
func handleModifyGroup(ctx context.Context, args map[string]interface{}) (string, bool) {
	groupID, err := intArg(args, "group_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title := optStrArg(args, "title")
	description := optStrArg(args, "description")
	v := NewValidator(ctx, 0)
	if err := v.ModifyGroup(groupID, title, description); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	parts := []string{}
	if title != "" {
		parts = append(parts, fmt.Sprintf("title=%q", title))
	}
	if description != "" {
		parts = append(parts, fmt.Sprintf("description=%q", description))
	}
	return fmt.Sprintf("Group #%d updated (%s)", groupID, strings.Join(parts, ", ")), false
}

// handleListGroupObjects shows processes the group currently has access to.
func handleListGroupObjects(ctx context.Context, args map[string]interface{}) (string, bool) {
	groupID, err := intArg(args, "group_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	v := NewValidator(ctx, 0)
	list, err := v.ListGroupObjects(groupID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if len(list) == 0 {
		return fmt.Sprintf("Group #%d has no processes attached.", groupID), false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Processes shared with group #%d (%d):\n\n", groupID, len(list)))
	sb.WriteString(fmt.Sprintf("  %-10s %-40s %s\n", "obj_id", "title", "status"))
	sb.WriteString("  " + strings.Repeat("-", 70) + "\n")
	for _, item := range list {
		id := 0
		if f, ok := item["obj_id"].(float64); ok {
			id = int(f)
		}
		title := stringValue(item, "title")
		status := stringValue(item, "status")
		sb.WriteString(fmt.Sprintf("  %-10d %-40s %s\n", id, truncate(title, 38), status))
	}
	sb.WriteString("\nNote: this list covers processes only — folders, stages and projects shared with the group are not retrievable via this endpoint.\n")
	return sb.String(), false
}

// handleDeleteGroup removes a group. Refuses by default if the group still
// has active shares — caller must pass force=true to proceed.
func handleDeleteGroup(ctx context.Context, args map[string]interface{}) (string, bool) {
	groupID, err := intArg(args, "group_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	force := false
	if v, ok := args["force"]; ok {
		if b, ok := v.(bool); ok {
			force = b
		} else if s, ok := v.(string); ok {
			force = strings.EqualFold(s, "true") || s == "1"
		}
	}
	v := NewValidator(ctx, 0)
	blockers, err := v.DeleteGroup(groupID, force)
	if err != nil {
		if len(blockers) > 0 {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Refused to delete group #%d — still has %d active share(s):\n\n", groupID, len(blockers)))
			for _, b := range blockers {
				id := 0
				if f, ok := b["obj_id"].(float64); ok {
					id = int(f)
				}
				sb.WriteString(fmt.Sprintf("  conv #%d  %s\n", id, stringValue(b, "title")))
			}
			sb.WriteString("\nRe-run with force=true to delete anyway. Members will lose all access inherited from this group.\n")
			return sb.String(), true
		}
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf("Deleted group #%d", groupID), false
}

// handleAddToGroup attaches a user (or API key user) to a group.
func handleAddToGroup(ctx context.Context, args map[string]interface{}) (string, bool) {
	groupID, err := intArg(args, "group_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	userID, err := intArg(args, "user_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	v := NewValidator(ctx, 0)
	if err := v.AddToGroup(groupID, userID); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf("Added user #%d to group #%d", userID, groupID), false
}

// handleRemoveFromGroup detaches a user from a group.
func handleRemoveFromGroup(ctx context.Context, args map[string]interface{}) (string, bool) {
	groupID, err := intArg(args, "group_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	userID, err := intArg(args, "user_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	v := NewValidator(ctx, 0)
	if err := v.RemoveFromGroup(groupID, userID); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf("Removed user #%d from group #%d", userID, groupID), false
}

// handleListGroups prints all groups in the workspace, optionally filtered by substring.
func handleListGroups(ctx context.Context, args map[string]interface{}) (string, bool) {
	name := optStrArg(args, "name")
	v := NewValidator(ctx, 0)
	list, err := v.ListGroups(name)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if len(list) == 0 {
		return "No groups found", false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Groups in workspace (%d):\n\n", len(list)))
	sb.WriteString(fmt.Sprintf("  %-10s %-30s %-6s %s\n", "id", "title", "size", "owner"))
	sb.WriteString("  " + strings.Repeat("-", 70) + "\n")
	for _, g := range list {
		id := 0
		if f, ok := g["obj_id"].(float64); ok {
			id = int(f)
		}
		size := 0
		if f, ok := g["size"].(float64); ok {
			size = int(f)
		}
		title := stringValue(g, "title")
		owner := stringValue(g, "owner_name")
		sb.WriteString(fmt.Sprintf("  %-10d %-30s %-6d %s\n", id, truncate(title, 28), size, owner))
	}
	return sb.String(), false
}

// handleCreateAPIKey provisions a new API key. The secret is written to a
// 0600 JSON file under ~/.corezoid/api-keys/ and the chat output reports
// only the file path — the secret itself is never printed to the agent
// response.
func handleCreateAPIKey(ctx context.Context, args map[string]interface{}) (string, bool) {
	title, err := strArg(args, "title")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	description := optStrArg(args, "description")
	v := NewValidator(ctx, 0)
	p, path, err := v.CreateAPIKey(title, description)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf(
		"API key %q created.\n  obj_id (use as obj_to_id when sharing): %d\n  login: %d\n  secret: <written to file, never printed in chat>\n  secret file: %s   (chmod 600, JSON with login+secret+metadata)\n\n  ⚠ Corezoid only shows the secret on creation — back up or import the file before deleting it.",
		title, p.ID, p.APILogin, path), false
}

// handleModifyAPIKey updates an API key's title/description.
func handleModifyAPIKey(ctx context.Context, args map[string]interface{}) (string, bool) {
	apiKeyID, err := intArg(args, "api_key_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title := optStrArg(args, "title")
	description := optStrArg(args, "description")
	v := NewValidator(ctx, 0)
	if err := v.ModifyAPIKey(apiKeyID, title, description); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	parts := []string{}
	if title != "" {
		parts = append(parts, fmt.Sprintf("title=%q", title))
	}
	if description != "" {
		parts = append(parts, fmt.Sprintf("description=%q", description))
	}
	return fmt.Sprintf("API key #%d updated (%s)", apiKeyID, strings.Join(parts, ", ")), false
}


// handleDeleteAPIKey removes an API key user record.
func handleDeleteAPIKey(ctx context.Context, args map[string]interface{}) (string, bool) {
	id, err := intArg(args, "api_key_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	v := NewValidator(ctx, 0)
	if err := v.DeleteAPIKey(id); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf("Deleted API key #%d", id), false
}

// handleListAPIKeys prints API keys in the workspace.
func handleListAPIKeys(ctx context.Context, args map[string]interface{}) (string, bool) {
	name := optStrArg(args, "name")
	v := NewValidator(ctx, 0)
	list, err := v.ListAPIKeys(name)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if len(list) == 0 {
		return "No API keys found", false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("API keys in workspace (%d):\n\n", len(list)))
	sb.WriteString(fmt.Sprintf("  %-10s %-30s %-10s %s\n", "id", "title", "status", "login"))
	sb.WriteString("  " + strings.Repeat("-", 70) + "\n")
	for _, k := range list {
		id := 0
		if f, ok := k["obj_id"].(float64); ok {
			id = int(f)
		}
		title := stringValue(k, "title")
		status := stringValue(k, "status")
		login := ""
		if logins, ok := k["logins"].([]any); ok && len(logins) > 0 {
			if l, ok := logins[0].(map[string]any); ok {
				login = stringValue(l, "login")
			}
		}
		sb.WriteString(fmt.Sprintf("  %-10d %-30s %-10s %s\n", id, truncate(title, 28), status, login))
	}
	return sb.String(), false
}

// handleFindPrincipal resolves user / group / API-key names to obj_ids.
func handleFindPrincipal(ctx context.Context, args map[string]interface{}) (string, bool) {
	name := optStrArg(args, "name")
	filter := optStrArg(args, "kind")
	if filter == "" {
		filter = "user"
	}
	v := NewValidator(ctx, 0)
	list, err := v.FindPrincipal(name, filter)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if len(list) == 0 {
		return fmt.Sprintf("No %s entries match %q", filter, name), false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s matches for %q (%d):\n\n", filter, name, len(list)))
	sb.WriteString(fmt.Sprintf("  %-10s %-35s %s\n", "obj_id", "title", "extra"))
	sb.WriteString("  " + strings.Repeat("-", 70) + "\n")
	for _, item := range list {
		id := 0
		if f, ok := item["obj_id"].(float64); ok {
			id = int(f)
		}
		title := stringValue(item, "title")
		extra := ""
		if logins, ok := item["logins"].([]any); ok && len(logins) > 0 {
			if l, ok := logins[0].(map[string]any); ok {
				extra = fmt.Sprintf("%s: %s", stringValue(l, "type"), stringValue(l, "login"))
			}
		} else if owner := stringValue(item, "owner_name"); owner != "" {
			extra = "owner: " + owner
		}
		sb.WriteString(fmt.Sprintf("  %-10d %-35s %s\n", id, truncate(title, 33), extra))
	}
	sb.WriteString("\nUse obj_id as obj_to_id when sharing.\n")
	return sb.String(), false
}

// handleInviteUser invites an external email and shares an object with them.
func handleInviteUser(ctx context.Context, args map[string]interface{}) (string, bool) {
	email, err := strArg(args, "email")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	linkObj, err := strArg(args, "obj")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	linkObjID, err := intArg(args, "obj_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	loginType := optStrArg(args, "login_type")
	privs, err := parsePrivs(optStrArg(args, "privs"))
	if err != nil {
		return "Error: " + err.Error(), true
	}
	if len(privs) == 0 {
		privs = []PrivType{PrivView}
	}
	v := NewValidator(ctx, 0)
	url, err := v.InviteUser(email, loginType, linkObj, linkObjID, privs)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf(
		"Invited %s — share link: %s\n  Object: %s #%d, privs: %s",
		email, url, linkObj, linkObjID, privsString(privs)), false
}
