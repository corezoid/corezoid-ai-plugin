package main

import (
	"strings"
	"testing"
)

func TestGetPrompt_PullWorkspace(t *testing.T) {
	result, err := getPrompt("pull-workspace", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("expected role user, got %q", result.Messages[0].Role)
	}
}

func TestGetPrompt_CreateProcess_WithDescription(t *testing.T) {
	result, err := getPrompt("create-process", map[string]string{"description": "send email"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Messages[0].Content.Text, "send email") {
		t.Errorf("expected description in prompt text, got %q", result.Messages[0].Content.Text)
	}
}

func TestGetPrompt_CreateProcess_NoDescription(t *testing.T) {
	result, err := getPrompt("create-process", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Messages[0].Content.Text == "" {
		t.Error("expected non-empty text")
	}
}

func TestGetPrompt_EditProcess(t *testing.T) {
	result, err := getPrompt("edit-process", map[string]string{
		"process_id": "12345",
		"change":     "add error handler",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "12345") {
		t.Errorf("expected process_id in text, got %q", text)
	}
	if !strings.Contains(text, "add error handler") {
		t.Errorf("expected change in text, got %q", text)
	}
}

func TestGetPrompt_ReviewProcess(t *testing.T) {
	result, err := getPrompt("review-process", map[string]string{"process_id": "99"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Messages[0].Content.Text, "99") {
		t.Errorf("expected process_id in review text, got %q", result.Messages[0].Content.Text)
	}
}

func TestGetPrompt_PushProcess_WithPath(t *testing.T) {
	result, err := getPrompt("push-process", map[string]string{"process_path": "my.conv.json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Messages[0].Content.Text, "my.conv.json") {
		t.Errorf("expected path in text, got %q", result.Messages[0].Content.Text)
	}
}

func TestGetPrompt_PushProcess_NoPath(t *testing.T) {
	result, err := getPrompt("push-process", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Messages[0].Content.Text == "" {
		t.Error("expected non-empty push text")
	}
}

func TestGetPrompt_Unknown(t *testing.T) {
	_, err := getPrompt("nonexistent-prompt", nil)
	if err == nil {
		t.Error("expected error for unknown prompt, got nil")
	}
}

func TestGetPrompt_NilArguments(t *testing.T) {
	// nil arguments should not panic.
	result, err := getPrompt("pull-workspace", nil)
	if err != nil || result == nil {
		t.Errorf("expected valid result with nil args, got (%v, %v)", result, err)
	}
}

func TestBuiltinPrompts_AllReturnable(t *testing.T) {
	for _, p := range builtinPrompts {
		args := map[string]string{}
		for _, arg := range p.Arguments {
			args[arg.Name] = "test-value"
		}
		result, err := getPrompt(p.Name, args)
		if err != nil {
			t.Errorf("getPrompt(%q) returned error: %v", p.Name, err)
		}
		if result == nil || len(result.Messages) == 0 {
			t.Errorf("getPrompt(%q) returned empty result", p.Name)
		}
	}
}
