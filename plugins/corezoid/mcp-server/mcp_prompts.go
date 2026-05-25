package main

import "fmt"

type mcpPromptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type mcpPrompt struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Arguments   []mcpPromptArg `json:"arguments,omitempty"`
}

type mcpPromptMessage struct {
	Role    string     `json:"role"`
	Content mcpContent `json:"content"`
}

type mcpGetPromptResult struct {
	Description string             `json:"description,omitempty"`
	Messages    []mcpPromptMessage `json:"messages"`
}

var builtinPrompts = []mcpPrompt{
	{
		Name:        "pull-workspace",
		Description: "Pull the Corezoid workspace and explore available processes",
	},
	{
		Name:        "create-process",
		Description: "Create a new Corezoid process from a natural-language description",
		Arguments: []mcpPromptArg{
			{Name: "description", Description: "What the process should do", Required: true},
		},
	},
	{
		Name:        "edit-process",
		Description: "Edit an existing Corezoid process",
		Arguments: []mcpPromptArg{
			{Name: "process_id", Description: "Process ID or local file path", Required: true},
			{Name: "change", Description: "What to change", Required: true},
		},
	},
	{
		Name:        "review-process",
		Description: "Review a Corezoid process for dead nodes, missing error handlers, and hardcoded values",
		Arguments: []mcpPromptArg{
			{Name: "process_id", Description: "Process ID or local file path", Required: true},
		},
	},
	{
		Name:        "push-process",
		Description: "Push a local process file to Corezoid",
		Arguments: []mcpPromptArg{
			{Name: "process_path", Description: "Path to the .conv.json file", Required: false},
		},
	},
}

// getPrompt returns the resolved prompt messages for the given name and arguments.
func getPrompt(name string, arguments map[string]string) (*mcpGetPromptResult, error) {
	if arguments == nil {
		arguments = map[string]string{}
	}
	switch name {
	case "pull-workspace":
		return &mcpGetPromptResult{
			Description: "Pull the Corezoid workspace",
			Messages: []mcpPromptMessage{{
				Role:    "user",
				Content: mcpContent{Type: "text", Text: "Pull my Corezoid workspace and show me what processes are available."},
			}},
		}, nil

	case "create-process":
		desc := arguments["description"]
		text := "Create a new Corezoid process"
		if desc != "" {
			text = "Create a new Corezoid process: " + desc
		}
		return &mcpGetPromptResult{
			Description: "Create a new Corezoid process",
			Messages: []mcpPromptMessage{{
				Role:    "user",
				Content: mcpContent{Type: "text", Text: text},
			}},
		}, nil

	case "edit-process":
		pid := arguments["process_id"]
		change := arguments["change"]
		text := fmt.Sprintf("Edit process %s — %s", pid, change)
		return &mcpGetPromptResult{
			Description: "Edit a Corezoid process",
			Messages: []mcpPromptMessage{{
				Role:    "user",
				Content: mcpContent{Type: "text", Text: text},
			}},
		}, nil

	case "review-process":
		pid := arguments["process_id"]
		text := fmt.Sprintf("Review process %s for dead nodes, missing error handlers, and hardcoded values.", pid)
		return &mcpGetPromptResult{
			Description: "Review a Corezoid process",
			Messages: []mcpPromptMessage{{
				Role:    "user",
				Content: mcpContent{Type: "text", Text: text},
			}},
		}, nil

	case "push-process":
		path := arguments["process_path"]
		text := "Push my process to Corezoid"
		if path != "" {
			text = fmt.Sprintf("Push %s to Corezoid", path)
		}
		return &mcpGetPromptResult{
			Description: "Push a process to Corezoid",
			Messages: []mcpPromptMessage{{
				Role:    "user",
				Content: mcpContent{Type: "text", Text: text},
			}},
		}, nil

	default:
		return nil, fmt.Errorf("prompt not found: %s", name)
	}
}
