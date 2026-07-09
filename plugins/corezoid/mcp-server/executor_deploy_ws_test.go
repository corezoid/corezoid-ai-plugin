package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeWSServer stands up a WebSocket endpoint that (after reading the client's
// monitor_stat start frame) streams `frames`, then either closes or holds the
// connection open. It points the deploy WS at itself via COREZOID_WS_URL.
func fakeWSServer(t *testing.T, frames []string, holdOpen bool) {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, _, _ = c.ReadMessage() // consume the monitor_stat start frame
		for _, f := range frames {
			if err := c.WriteMessage(websocket.TextMessage, []byte(f)); err != nil {
				return
			}
		}
		if !holdOpen {
			return
		}
		for { // keep the socket open, draining keepalives, until the client leaves
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	os.Setenv("COREZOID_WS_URL", "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/1/sock_json")
	t.Cleanup(func() { os.Unsetenv("COREZOID_WS_URL") })
}

func newWSExecutor() *Executor {
	return &Executor{Ctx: context.Background(), Token: "t", APIUrl: "https://admin.corezoid.com", WorkspaceID: "w"}
}

func TestMonitorDeployProgress_Success(t *testing.T) {
	fakeWSServer(t, []string{frameUploadComplete, frameMergeComplete}, false)
	log, err := newWSExecutor().monitorDeployProgress("H")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(log, "upload") || !strings.Contains(log, "merge") {
		t.Errorf("progress log missing phases: %q", log)
	}
}

func TestMonitorDeployProgress_NotifyDone(t *testing.T) {
	fakeWSServer(t, []string{frameNotifyMerge}, false)
	if _, err := newWSExecutor().monitorDeployProgress("H"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMonitorDeployProgress_Error(t *testing.T) {
	fakeWSServer(t, []string{frameError}, false)
	if _, err := newWSExecutor().monitorDeployProgress("H"); err == nil {
		t.Fatalf("expected error frame to fail")
	}
}

func TestMonitorDeployProgress_CloseBeforeDone(t *testing.T) {
	fakeWSServer(t, []string{frameProgressMid}, false) // mid-progress, then close
	if _, err := newWSExecutor().monitorDeployProgress("H"); err == nil {
		t.Fatalf("expected close-before-done error")
	}
}

func TestMonitorDeployProgress_Timeout(t *testing.T) {
	orig := deployTimeout
	deployTimeout = 150 * time.Millisecond
	t.Cleanup(func() { deployTimeout = orig })
	fakeWSServer(t, []string{frameProgressMid}, true) // never completes, stays open
	_, err := newWSExecutor().monitorDeployProgress("H")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestMonitorDeployProgress_DialError(t *testing.T) {
	os.Setenv("COREZOID_WS_URL", "ws://127.0.0.1:1/nope")
	t.Cleanup(func() { os.Unsetenv("COREZOID_WS_URL") })
	if _, err := newWSExecutor().monitorDeployProgress("H"); err == nil {
		t.Fatalf("expected dial error")
	}
}
