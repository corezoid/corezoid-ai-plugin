package main

// gitTools — Git-mirror context tools (mcp_handlers_git.go).
// Composed into toolRegistry by tools_registry.go; the order of this
// slice is part of the tools/list golden snapshot.
var gitTools = []mcpTool{
	{
		Name:        "git-pull-context",
		Description: "Clone or pull the Corezoid git mirror for the current workspace into .git-context/. Requires COREZOID_GIT_URL, API_LOGIN, and API_SECRET. Silently skipped if not configured.",
		Annotations: annDestructiveRemote,
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "git-push-context",
		Description: "Commit and push local _ext/ changes to the Corezoid git mirror. Requires API_LOGIN and API_SECRET. Returns a warning (not an error) if nothing changed.",
		Annotations: annCreateRemote,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"commit_message": map[string]interface{}{
					"type":        "string",
					"description": "Optional git commit message. Defaults to 'docs: update _ext/ after task session <timestamp>'.",
				},
			},
		},
	},
	{
		Name:        "read-context-file",
		Description: "Read a file from .git-context/ of the current workspace (git mirror local copy). Returns content and a found flag.",
		Annotations: annReadOnlyLocal,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path inside .git-context/, e.g. projects/123_Name/stages/456_Stage/_ext/docs/context.md",
				},
			},
			"required": []string{"path"},
		},
	},
	{
		Name:        "update-context-file",
		Description: "Write or append to a file inside _ext/ of the git mirror local copy (.git-context/). Path must start with _ext/. Use git-push-context to publish the changes.",
		Annotations: annDestructiveLocal,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path inside .git-context/, must start with _ext/ (e.g. _ext/docs/context.md or projects/123/stages/456/_ext/docs/context.md)",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Text content to write",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"description": "Write mode: 'replace' (default) overwrites the file; 'append' adds to the end",
				},
			},
			"required": []string{"path", "content"},
		},
	},
}
