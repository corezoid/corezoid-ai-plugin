package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// floodWSServer stands up a WebSocket endpoint that, after consuming the
// client's start frame, streams `frame` in a tight loop until the client goes
// away. The flood keeps the executor's 1-slot `reads` buffer permanently full,
// so the reader goroutine is (almost) always parked on the channel send rather
// than inside conn.ReadMessage — the state in which conn.Close() cannot free it.
func floodWSServer(t *testing.T, frame string) {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, _, _ = c.ReadMessage() // consume the start frame
		for {
			if err := c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	os.Setenv("COREZOID_WS_URL", "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/1/sock_json")
	t.Cleanup(func() { os.Unsetenv("COREZOID_WS_URL") })
}

// assertNoGoroutine fails if any goroutine stack still mentions `frag` after a
// grace period. Used to prove the WS reader goroutine is released when its
// owning function returns.
func assertNoGoroutine(t *testing.T, frag string) {
	t.Helper()
	buf := make([]byte, 1<<20)
	deadline := time.Now().Add(3 * time.Second)
	for {
		dump := string(buf[:runtime.Stack(buf, true)])
		if !strings.Contains(dump, frag) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine %q still running after its owner returned (leak):\n%s", frag, dump)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMonitorDeployProgress_NoReaderLeakOnTimeout covers the deadline-vs-full-
// channel race: the deploy times out while the reader is blocked sending a
// frame nobody will read. Before the `done` escape hatch the reader stayed
// parked on that send for the process lifetime.
func TestMonitorDeployProgress_NoReaderLeakOnTimeout(t *testing.T) {
	orig := deployTimeout
	deployTimeout = 150 * time.Millisecond
	t.Cleanup(func() { deployTimeout = orig })
	floodWSServer(t, frameProgressMid)

	if _, err := newWSExecutor().monitorDeployProgress("H"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	assertNoGoroutine(t, "monitorDeployProgress.func")
}

// TestBuildGitCallNode_NoReaderLeakOnCancel is the git_call counterpart: the
// build is abandoned (context cancelled) while the reader is blocked on the
// full channel. gitCallBuildTimeout is a const, so cancellation stands in for
// the deadline — both leave via the same return path.
func TestBuildGitCallNode_NoReaderLeakOnCancel(t *testing.T) {
	floodWSServer(t, `{"ops":[{"obj_type":"function_build","log":{"type":"stdout","message":"compiling"}}]}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(150*time.Millisecond, cancel)
	defer timer.Stop()

	v := &Executor{Ctx: ctx, Token: "t", APIUrl: "https://admin.corezoid.com"}
	g := gitCallBuild{lang: "python", nodeServerID: "n1", nodeTitle: "fn"}
	if err := v.buildGitCallNode(gitCallWSURL(v.APIUrl), g); err == nil {
		t.Fatal("expected cancellation error")
	}
	assertNoGoroutine(t, "buildGitCallNode.func")
}
