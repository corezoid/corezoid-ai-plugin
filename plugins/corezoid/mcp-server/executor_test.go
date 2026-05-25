package main

import (
	"os"
	"testing"
)

func TestBeforeValidation_ApiCopyToItself(t *testing.T) {
	data, err := os.ReadFile("./samples/api_copy_to_itself.json")
	if err != nil {
		t.Fatalf("failed to read sample file: %v", err)
	}
	executor := &Executor{ProcessID: 123}
	err = executor.BeforeValidation(string(data), nil)
	if err == nil {
		t.Error("expected error for api_copy to itself, got nil")
	}
	t.Logf("Expected error: %v", err)
}

func TestBeforeValidation_ApiRpcToItself(t *testing.T) {
	// Same validation rule applies to api_rpc
	data, err := os.ReadFile("./samples/api_copy_to_itself.json")
	if err != nil {
		t.Fatalf("failed to read sample file: %v", err)
	}

	// Replace api_copy with api_rpc in the content
	import_content := string(data)
	content := replaceFirst(import_content, `"api_copy"`, `"api_rpc"`)

	executor := &Executor{ProcessID: 123}
	err = executor.BeforeValidation(content, nil)
	if err == nil {
		t.Error("expected error for api_rpc to itself, got nil")
	}
	t.Logf("Expected error: %v", err)
}

func TestBeforeValidation_ValidProcess(t *testing.T) {
	data, err := os.ReadFile("./samples/valid_process.json")
	if err != nil {
		t.Fatalf("failed to read sample file: %v", err)
	}
	executor := &Executor{ProcessID: 999}
	err = executor.BeforeValidation(string(data), nil)
	if err != nil {
		t.Errorf("expected no error for valid process, got: %v", err)
	}
}

func TestBeforeValidation_TaskDataMissingParam(t *testing.T) {
	data, err := os.ReadFile("./samples/task_data_missing_param.json")
	if err != nil {
		t.Fatalf("failed to read sample file: %v", err)
	}
	executor := &Executor{ProcessID: 0}
	taskData := map[string]interface{}{
		"email": "test@example.com",
		"phone": "+1234567890", // not declared in params
	}
	err = executor.BeforeValidation(string(data), taskData)
	if err == nil {
		t.Error("expected error for task data key not in params, got nil")
	}
	t.Logf("Expected error: %v", err)
}

func TestBeforeValidation_TaskDataValidParam(t *testing.T) {
	data, err := os.ReadFile("./samples/task_data_missing_param.json")
	if err != nil {
		t.Fatalf("failed to read sample file: %v", err)
	}
	executor := &Executor{ProcessID: 0}
	taskData := map[string]interface{}{
		"email": "test@example.com", // declared in params
	}
	err = executor.BeforeValidation(string(data), taskData)
	if err != nil {
		t.Errorf("expected no error for valid task data, got: %v", err)
	}
}

func TestBeforeValidation_MultiActionLogics(t *testing.T) {
	data, err := os.ReadFile("./samples/multi_action_logics.json")
	if err != nil {
		t.Fatalf("failed to read sample file: %v", err)
	}
	executor := &Executor{ProcessID: 0}
	err = executor.BeforeValidation(string(data), nil)
	if err == nil {
		t.Error("expected error for node with multiple action logics, got nil")
	}
	t.Logf("Expected error: %v", err)
}

func TestBeforeValidation_InvalidJSON(t *testing.T) {
	executor := &Executor{ProcessID: 0}
	err := executor.BeforeValidation("{invalid json}", nil)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// replaceFirst replaces the first occurrence of old with new in s.
func replaceFirst(s, old, new string) string {
	idx := len(s)
	for i := 0; i <= len(s)-len(old); i++ {
		if s[i:i+len(old)] == old {
			idx = i
			break
		}
	}
	if idx == len(s) {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}
