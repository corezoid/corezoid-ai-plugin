package main

// Authentication tools. Handlers live in mcp_handlers_auth.go.

// authLoginTools establish a Corezoid session.
var authLoginTools = []mcpTool{
	{
		Name:        "login",
		Description: "Authenticate with Corezoid. Supports two auth methods: (1) OAuth2 browser flow — opens a browser window and saves the token so it persists across sessions; (2) API key — provide api_login and api_secret to skip the browser flow (credentials saved to project .env). Optionally accepts account_url, workspace_id, and stage_id to skip interactive prompts.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"account_url": map[string]interface{}{
					"type":        "string",
					"description": "Account API URL, e.g. https://account.corezoid.com",
				},
				"workspace_id": map[string]interface{}{
					"type":        "string",
					"description": "Corezoid workspace (company) ID",
				},
				"stage_id": map[string]interface{}{
					"type":        "string",
					"description": "Corezoid stage (root folder) ID",
				},
				"api_login": map[string]interface{}{
					"type":        "string",
					"description": "API key login (alternative to OAuth2 browser flow). If both api_login and api_secret are provided, browser authentication is skipped.",
				},
				"api_secret": map[string]interface{}{
					"type":        "string",
					"description": "API key secret (alternative to OAuth2 browser flow). Must be paired with api_login. Stored in project .env — ensure .env is in .gitignore.",
				},
			},
		},
	},
}

// authLogoutTools tear a Corezoid session down.
var authLogoutTools = []mcpTool{
	{
		Name:        "logout",
		Description: "Remove saved Corezoid credentials from disk.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
}
