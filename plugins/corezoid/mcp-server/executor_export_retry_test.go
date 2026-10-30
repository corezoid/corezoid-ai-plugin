package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastExportRetry shrinks the export backoff so retry tests run in milliseconds.
func fastExportRetry(t *testing.T) {
	t.Helper()
	orig := exportRetryDelaysVar
	exportRetryDelaysVar = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { exportRetryDelaysVar = orig })
}

// exportServer answers export ops through fn (called with the 1-based export
// attempt) and serves "ZIPDATA" from /dl, the download_url it hands out.
func exportServer(t *testing.T, fn func(attempt int, ops []map[string]interface{}) interface{}) (*Executor, *int32) {
	t.Helper()
	var calls int32
	var srvURL string
	_, e := mockAPIServer(t, func(ops []map[string]interface{}) interface{} {
		n := int(atomic.AddInt32(&calls, 1))
		resp := fn(n, ops)
		if resp == nil {
			results := make([]interface{}, len(ops))
			for i := range ops {
				results[i] = map[string]interface{}{"proc": "ok", "download_url": srvURL + "/dl"}
			}
			resp = map[string]interface{}{"request_proc": "ok", "ops": results}
		}
		return resp
	})
	// The mock API server answers every path with JSON, so the archive is
	// served from a second server.
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ZIPDATA")) //nolint:errcheck
	}))
	t.Cleanup(dl.Close)
	srvURL = dl.URL
	e.Ctx = context.Background()
	return e, &calls
}

func timeoutOp() interface{} {
	return map[string]interface{}{
		"request_proc": "ok",
		"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "Timeout export_scheme"}},
	}
}

// TestPullZip_RetriesPlatformTimeout reproduces issue #249: a cold export of a
// large stage times out on the platform while an identical repeat succeeds.
// PullZip used to hand the first timeout straight to the model.
func TestPullZip_RetriesPlatformTimeout(t *testing.T) {
	fastExportRetry(t)
	e, calls := exportServer(t, func(attempt int, _ []map[string]interface{}) interface{} {
		if attempt == 1 {
			return timeoutOp()
		}
		return nil
	})

	data, err := e.PullZip(42, "stage")
	if err != nil {
		t.Fatalf("PullZip should recover from a single platform timeout, got: %v", err)
	}
	if string(data) != "ZIPDATA" {
		t.Errorf("data = %q, want ZIPDATA", data)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("export calls = %d, want 2", got)
	}
}

func TestPullZip_RetriesInternalErrorThenTimeout(t *testing.T) {
	fastExportRetry(t)
	e, calls := exportServer(t, func(attempt int, _ []map[string]interface{}) interface{} {
		switch attempt {
		case 1, 2:
			return timeoutOp()
		case 3:
			return map[string]interface{}{
				"request_proc": "ok",
				"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "Internal server error"}},
			}
		}
		return nil
	})

	if _, err := e.PullZip(42, "stage"); err != nil {
		t.Fatalf("PullZip should recover on the fourth attempt, got: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 4 {
		t.Errorf("export calls = %d, want 4", got)
	}
}

func TestPullZip_PersistentTimeoutSaysRetryLikelySucceeds(t *testing.T) {
	fastExportRetry(t)
	e, calls := exportServer(t, func(int, []map[string]interface{}) interface{} { return timeoutOp() })

	_, err := e.PullZip(42, "stage")
	if err == nil {
		t.Fatal("expected an error when every attempt times out")
	}
	if !errors.Is(err, errExportStillTransient) {
		t.Errorf("error should wrap errExportStillTransient: %v", err)
	}
	if !strings.Contains(err.Error(), "calling the same tool again usually succeeds") {
		t.Errorf("error should tell the model a retry is likely to succeed: %v", err)
	}
	if !strings.Contains(err.Error(), "Timeout export_scheme") {
		t.Errorf("error should keep the platform's message: %v", err)
	}
	if want := int32(len(exportRetryDelaysVar) + 1); atomic.LoadInt32(calls) != want {
		t.Errorf("export calls = %d, want %d", atomic.LoadInt32(calls), want)
	}
}

func TestPullZip_PermanentErrorNotRetried(t *testing.T) {
	fastExportRetry(t)
	e, calls := exportServer(t, func(int, []map[string]interface{}) interface{} {
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "Object folder not found"}},
		}
	})

	_, err := e.PullZip(42, "folder")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, errExportStillTransient) {
		t.Errorf("a not-found error must not carry the retry hint: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("export calls = %d, want 1 (no retry on a permanent error)", got)
	}
}

func TestBatchExportSchemes_RetriesPlatformTimeout(t *testing.T) {
	fastExportRetry(t)
	e, calls := exportServer(t, func(attempt int, _ []map[string]interface{}) interface{} {
		if attempt == 1 {
			return timeoutOp()
		}
		return nil
	})

	urls, err := e.BatchExportSchemes([]WorkspaceRootItem{{ObjType: "folder", ObjID: 1}})
	if err != nil {
		t.Fatalf("BatchExportSchemes should recover from a timeout, got: %v", err)
	}
	if len(urls) != 1 {
		t.Errorf("urls = %v, want one", urls)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("export calls = %d, want 2", got)
	}
}

// A sub-folder pull probes folder, stage, project in turn. When the folder
// export keeps timing out, the probe must stop there and report the timeout,
// not the "not found" the later obj_types produce.
func TestDownloadStageRecursively_TimeoutStopsObjTypeProbe(t *testing.T) {
	fastExportRetry(t)
	var probed []string
	e, _ := exportServer(t, func(_ int, ops []map[string]interface{}) interface{} {
		ot, _ := ops[0]["obj_type"].(string)
		probed = append(probed, ot)
		if ot == "folder" {
			return timeoutOp()
		}
		return map[string]interface{}{
			"request_proc": "ok",
			"ops":          []interface{}{map[string]interface{}{"proc": "error", "description": "Object not found"}},
		}
	})

	_, err := downloadStageRecursively(e, 77, t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errExportStillTransient) {
		t.Errorf("error should report the timeout with the retry hint, got: %v", err)
	}
	for _, ot := range probed {
		if ot != "folder" {
			t.Errorf("probe continued to obj_type %q after the folder export timed out", ot)
		}
	}
}

func TestIsTransientExportError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("API op error: Timeout export_scheme"), true},
		{errors.New("API op error: Internal server error"), true},
		{errors.New("download failed with HTTP 504: gateway"), true},
		{errors.New("API returned 503 after 3 attempts"), true},
		{errors.New("API op error: Object not found"), false},
		{errors.New("API op error: Access denied"), false},
		{&nonJSONResponseError{Status: 502, Err: errors.New("invalid character '<'")}, true},
		{&nonJSONResponseError{Status: 403, Err: errors.New("invalid character '<'")}, false},
		{&nonJSONResponseError{Status: 404, Err: errors.New("invalid character '<'")}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := isTransientExportError(c.err); got != c.want {
			t.Errorf("isTransientExportError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// htmlExportServer answers every request with an HTML page and the given
// status, the way a gateway or WAF in front of Corezoid does.
func htmlExportServer(t *testing.T, status int) (*Executor, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		w.Write([]byte("<html><body>blocked</body></html>")) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return &Executor{APIUrl: srv.URL, Token: "test", Ctx: context.Background(), NodeIDMap: make(map[string]NodeInfo)}, &calls
}

// A 4xx HTML page (WAF block, wrong API URL) fails the same way every time:
// repeating it would only delay the error and wrap it in a "retry the tool"
// hint that sends the model into a loop.
func TestPullZip_HTMLClientErrorNotRetried(t *testing.T) {
	fastExportRetry(t)
	e, calls := htmlExportServer(t, http.StatusForbidden)

	_, err := e.PullZip(42, "stage")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, errExportStillTransient) {
		t.Errorf("a 403 page must not carry the retry hint: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("error should name the HTTP status: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("export calls = %d, want 1", got)
	}
}

func TestPullZip_HTMLGatewayErrorRetried(t *testing.T) {
	fastExportRetry(t)
	e, calls := htmlExportServer(t, http.StatusBadGateway)

	_, err := e.PullZip(42, "stage")
	if !errors.Is(err, errExportStillTransient) {
		t.Errorf("a 502 page should be retried and carry the hint: %v", err)
	}
	if want := int32(len(exportRetryDelaysVar) + 1); atomic.LoadInt32(calls) != want {
		t.Errorf("export calls = %d, want %d", atomic.LoadInt32(calls), want)
	}
}

// Once the budget is spent, no repeat after the first one starts, however many
// are left in exportRetryDelaysVar.
func TestWithExportRetry_BudgetStopsLaterRepeats(t *testing.T) {
	fastExportRetry(t)
	origBudget := exportRetryBudgetVar
	exportRetryBudgetVar = 0
	t.Cleanup(func() { exportRetryBudgetVar = origBudget })

	calls := 0
	_, err := withExportRetry(&Executor{Ctx: context.Background()}, "stage #1", func() (int, error) {
		calls++
		return 0, errors.New("API op error: Timeout export_scheme")
	})
	if !errors.Is(err, errExportStillTransient) {
		t.Errorf("error should carry the retry hint: %v", err)
	}
	if calls != 2 {
		t.Errorf("export calls = %d, want 2 (the first repeat only)", calls)
	}
}
