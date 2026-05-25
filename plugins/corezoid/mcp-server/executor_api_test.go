package main

import (
	"testing"
)

// ---- checkError ------------------------------------------------------------

func TestCheckError_OK(t *testing.T) {
	rsp := map[string]interface{}{
		"request_proc": "ok",
		"ops": []interface{}{
			map[string]interface{}{"proc": "ok"},
		},
	}
	e := &Executor{}
	if err := e.checkError(rsp); err != nil {
		t.Errorf("expected nil error for ok response, got: %v", err)
	}
}

func TestCheckError_Nil(t *testing.T) {
	e := &Executor{}
	if err := e.checkError(nil); err == nil {
		t.Error("expected error for nil response, got nil")
	}
}

func TestCheckError_RequestProcNotOK(t *testing.T) {
	rsp := map[string]interface{}{
		"request_proc": "fail",
	}
	e := &Executor{}
	if err := e.checkError(rsp); err == nil {
		t.Error("expected error when request_proc != ok, got nil")
	}
}

func TestCheckError_RequestProcMissing(t *testing.T) {
	e := &Executor{}
	if err := e.checkError(map[string]interface{}{}); err == nil {
		t.Error("expected error when request_proc missing, got nil")
	}
}

func TestCheckError_OpProcNotOK_WithDescription(t *testing.T) {
	rsp := map[string]interface{}{
		"request_proc": "ok",
		"ops": []interface{}{
			map[string]interface{}{
				"proc":        "error",
				"description": "something went wrong",
			},
		},
	}
	e := &Executor{}
	err := e.checkError(rsp)
	if err == nil {
		t.Error("expected error for op proc != ok, got nil")
	}
}

func TestCheckError_OpProcNotOK_WithErrors(t *testing.T) {
	rsp := map[string]interface{}{
		"request_proc": "ok",
		"ops": []interface{}{
			map[string]interface{}{
				"proc": "error",
				"errors": map[string]interface{}{
					"node123": []interface{}{"bad logic", "missing field"},
				},
			},
		},
	}
	e := &Executor{}
	err := e.checkError(rsp)
	if err == nil {
		t.Error("expected error for op with errors map, got nil")
	}
}

func TestCheckError_MultipleOpsAllOK(t *testing.T) {
	rsp := map[string]interface{}{
		"request_proc": "ok",
		"ops": []interface{}{
			map[string]interface{}{"proc": "ok"},
			map[string]interface{}{"proc": "ok"},
		},
	}
	e := &Executor{}
	if err := e.checkError(rsp); err != nil {
		t.Errorf("expected nil error for all-ok ops, got: %v", err)
	}
}

// ---- newHTTPClient ---------------------------------------------------------

func TestNewHTTPClient_SecureByDefault(t *testing.T) {
	orig := insecureTLS
	insecureTLS = false
	t.Cleanup(func() { insecureTLS = orig })

	client := newHTTPClient()
	if client == nil {
		t.Fatal("expected non-nil http.Client")
	}
	if client.Transport != nil {
		t.Error("expected nil transport for default secure client")
	}
}

func TestNewHTTPClient_InsecureMode(t *testing.T) {
	orig := insecureTLS
	insecureTLS = true
	t.Cleanup(func() { insecureTLS = orig })

	client := newHTTPClient()
	if client == nil {
		t.Fatal("expected non-nil http.Client")
	}
	if client.Transport == nil {
		t.Error("expected custom transport for insecure mode")
	}
}
