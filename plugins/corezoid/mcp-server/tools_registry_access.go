package main

// Sharing, group and API-key tools. Handlers live in mcp_handlers_access.go.

// accessTools manage sharing, groups and API keys.
var accessTools = []mcpTool{
	{
		Name:        "share-object",
		Description: "Grant or revoke access to a Corezoid object (process/folder/stage/project) for a user, API key, or group. To revoke, pass privs=\"none\" — that's the same wire operation as a share with empty privs. API keys share as obj_to=\"user\" with the api key's obj_id.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"obj": map[string]interface{}{
					"type":        "string",
					"description": "Object kind: conv | folder | stage | project",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Numeric ID of the object being shared",
				},
				"obj_to": map[string]interface{}{
					"type":        "string",
					"description": "Recipient kind: user (includes API keys) | group",
				},
				"obj_to_id": map[string]interface{}{
					"type":        "integer",
					"description": "Recipient obj_id (resolve via find-principal)",
				},
				"privs": map[string]interface{}{
					"type":        "string",
					"description": "Comma-separated list, JSON array, or keyword. Allowed values: view, create (task management), modify, delete, all (default), none (revoke all access).",
				},
				"notify": map[string]interface{}{
					"type":        "boolean",
					"description": "Send notification to recipient (default true). Ignored when revoking.",
				},
			},
			"required": []string{"obj", "obj_id", "obj_to", "obj_to_id"},
		},
	},
	{
		Name:        "list-shares",
		Description: "List users, API keys and groups that currently have access to a Corezoid object.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"obj": map[string]interface{}{
					"type":        "string",
					"description": "Object kind: conv | folder | stage | project",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Object ID",
				},
			},
			"required": []string{"obj", "obj_id"},
		},
	},
	{
		Name:        "create-group",
		Description: "Create a new user group in the current workspace. Returns the group's obj_id (use as obj_to_id when sharing).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Group title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional group description",
				},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        "modify-group",
		Description: "Rename a user group and/or update its description. At least one of title or description must be supplied.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "New group title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New group description",
				},
			},
			"required": []string{"group_id"},
		},
	},
	{
		Name:        "list-group-objects",
		Description: "List the processes (conv objects) currently shared with a group. Used to audit group impact before destructive operations. Note: folders/stages/projects shared to the group are not retrievable via this endpoint.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
			},
			"required": []string{"group_id"},
		},
	},
	{
		Name:        "delete-group",
		Description: "Delete a user group. By default refuses to delete if the group still has active shares — pass force=true to override. Existing share links are revoked when the group is deleted.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"force": map[string]interface{}{
					"type":        "boolean",
					"description": "Delete even if the group still has active shares (default false).",
				},
			},
			"required": []string{"group_id"},
		},
	},
	{
		Name:        "add-to-group",
		Description: "Add a user (or API key user) to a group.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"user_id": map[string]interface{}{
					"type":        "integer",
					"description": "User or API-key user obj_id",
				},
			},
			"required": []string{"group_id", "user_id"},
		},
	},
	{
		Name:        "remove-from-group",
		Description: "Remove a user from a group.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "integer",
					"description": "Group obj_id",
				},
				"user_id": map[string]interface{}{
					"type":        "integer",
					"description": "User or API-key user obj_id",
				},
			},
			"required": []string{"group_id", "user_id"},
		},
	},
	{
		Name:        "list-groups",
		Description: "List user groups visible in the current workspace.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Optional substring filter on group title",
				},
			},
		},
	},
	{
		Name:        "create-api-key",
		Description: "Create a new API key in the workspace. The secret is written to ~/.corezoid/api-keys/<slug>-<obj_id>.json (mode 0600) and the chat output reports only the file path — the secret is never printed in agent responses.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "API key title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional API key description",
				},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        "modify-api-key",
		Description: "Update title and/or description of an existing API key. Does not regenerate the secret.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"api_key_id": map[string]interface{}{
					"type":        "integer",
					"description": "API key obj_id",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "New title",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New description",
				},
			},
			"required": []string{"api_key_id"},
		},
	},
	{
		Name:        "delete-api-key",
		Description: "Delete an API key. The secret is invalidated immediately — subsequent requests return 401. Objects owned by the key are reassigned to the workspace owner.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"api_key_id": map[string]interface{}{
					"type":        "integer",
					"description": "API-key user obj_id",
				},
			},
			"required": []string{"api_key_id"},
		},
	},
	{
		Name:        "list-api-keys",
		Description: "List API keys visible in the current workspace.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Optional substring filter on key title",
				},
			},
		},
	},
	{
		Name:        "find-principal",
		Description: "Search users, groups or API keys in the workspace by substring. Returns obj_ids to pass as obj_to_id in share-object.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Substring to match against title (omit to list all)",
				},
				"kind": map[string]interface{}{
					"type":        "string",
					"description": "What to search: user | group | api_key | shared. Defaults to user.",
				},
			},
		},
	},
	{
		Name:        "invite-user",
		Description: "Invite an external email to the workspace AND share a process/folder/stage/project with them in one call. Returns the invite URL the recipient must open.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"email": map[string]interface{}{
					"type":        "string",
					"description": "Invitee email",
				},
				"login_type": map[string]interface{}{
					"type":        "string",
					"description": "Login type: google | corezoid | phone (defaults to google)",
				},
				"obj": map[string]interface{}{
					"type":        "string",
					"description": "Object to share: conv | folder | stage | project",
				},
				"obj_id": map[string]interface{}{
					"type":        "integer",
					"description": "Object ID",
				},
				"privs": map[string]interface{}{
					"type":        "string",
					"description": "Privs to grant (view, create, modify, delete, all). Defaults to view.",
				},
			},
			"required": []string{"email", "obj", "obj_id"},
		},
	},
}
