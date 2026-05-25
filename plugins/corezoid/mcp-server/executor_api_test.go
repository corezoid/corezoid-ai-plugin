package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// ---- doWithRetry -----------------------------------------------------------

// shortenRetryDelays swaps the retry tuning constants to near-zero so retry
// tests don't actually sleep for seconds. Restored on cleanup.
func shortenRetryDelays(t *testing.T) {
	t.Helper()
	origBase := apiRetryBaseDelayVar
	origMax := apiRetryMaxDelayVar
	apiRetryBaseDelayVar = 1 * time.Millisecond
	apiRetryMaxDelayVar = 4 * time.Millisecond
	t.Cleanup(func() {
		apiRetryBaseDelayVar = origBase
		apiRetryMaxDelayVar = origMax
	})
}

func TestDoWithRetry_RetriesOn503ThenSucceeds(t *testing.T) {
	shortenRetryDelays(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	resp, body, err := doWithRetry(context.Background(), srv.Client(), "POST", srv.URL, []byte(`{}`), "tkn", false)
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after retries, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 calls (2 retries + success), got %d", got)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestDoWithRetry_RetriesOn429(t *testing.T) {
	shortenRetryDelays(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	resp, _, err := doWithRetry(context.Background(), srv.Client(), "POST", srv.URL, []byte(`{}`), "tkn", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected exactly 1 retry on 429, got %d calls", calls)
	}
}

func TestDoWithRetry_DoesNotRetry4xx(t *testing.T) {
	shortenRetryDelays(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	resp, _, err := doWithRetry(context.Background(), srv.Client(), "POST", srv.URL, []byte(`{}`), "tkn", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 surfaced as-is, got %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected single call for 4xx, got %d", calls)
	}
}

func TestDoWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	shortenRetryDelays(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	resp, _, err := doWithRetry(context.Background(), srv.Client(), "POST", srv.URL, []byte(`{}`), "tkn", false)
	if err == nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if got := atomic.LoadInt32(&calls); got != int32(apiMaxAttempts) {
		t.Fatalf("expected %d attempts before giving up, got %d", apiMaxAttempts, got)
	}
}

func TestDoWithRetry_CancelDuringBackoff(t *testing.T) {
	shortenRetryDelays(t)
	// Stretch backoff so we have a window to cancel during the sleep.
	apiRetryBaseDelayVar = 200 * time.Millisecond
	apiRetryMaxDelayVar = 500 * time.Millisecond

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, _, err := doWithRetry(ctx, srv.Client(), "POST", srv.URL, []byte(`{}`), "tkn", false)
	if err == nil {
		t.Fatal("expected context.Canceled error, got nil")
	}
	if !errorIsCanceledOrTimeout(err) {
		t.Errorf("expected ctx-cancellation error, got %v", err)
	}
	// We allow either 1 or 2 attempts before cancel races in.
	if got := atomic.LoadInt32(&calls); got > 2 {
		t.Errorf("cancel should stop retries quickly, got %d attempts", got)
	}
}

func errorIsCanceledOrTimeout(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "canceled") || strings.Contains(s, "deadline")
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"abc", 0},
		{"-5", 0},
		{"0", 0},
		{"7", 7 * time.Second},
		{" 3 ", 3 * time.Second},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
