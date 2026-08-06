package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// burstWSServer stands up a WebSocket endpoint that, after consuming the
// client's start frame, writes `frame` in a tight loop until the client goes
// away. The burst keeps the socket permanently readable, so the client's reader
// goroutine spends nearly all of its time blocked on the channel send rather
// than inside ReadMessage — the state in which conn.Close() cannot free it.
func burstWSServer(t *testing.T) {
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
			if err := c.WriteMessage(websocket.TextMessage, []byte(frameProgressMid)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	os.Setenv("COREZOID_WS_URL", "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/1/sock_json")
	t.Cleanup(func() { os.Unsetenv("COREZOID_WS_URL") })
}

// assertNoGoroutine polls the goroutine dump until no stack frame mentions
// `needle`, failing if any survives. The reader goroutines are released as the
// monitored function returns, so a short poll absorbs scheduling delay while a
// genuine leak still fails (a leaked goroutine never goes away).
func assertNoGoroutine(t *testing.T, needle string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); ; {
		dump := goroutineDump()
		if !strings.Contains(dump, needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reader goroutine %q still running after return (leak):\n%s", needle, dump)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// goroutineDump returns the full stack dump of every goroutine, growing the
// buffer until runtime.Stack stops truncating.
func goroutineDump() string {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// TestMonitorDeployProgress_NoReaderLeakOnTimeout covers the deadline-vs-full-
// channel race: the deploy times out while the reader goroutine is blocked
// handing over a frame. The goroutine must not survive the return.
func TestMonitorDeployProgress_NoReaderLeakOnTimeout(t *testing.T) {
	orig := deployTimeout
	deployTimeout = 150 * time.Millisecond
	t.Cleanup(func() { deployTimeout = orig })
	burstWSServer(t)

	// Assert the *timeout* specifically: any other error (a dial failure because
	// the WS override stopped being honoured, say) would mean the reader goroutine
	// was never even started, and the leak assertion below would pass vacuously.
	_, err := newWSExecutor().monitorDeployProgress("H")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	assertNoGoroutine(t, "monitorDeployProgress.func")
}

// TestBuildGitCallNode_NoReaderLeakOnCancel covers the same race on the
// git_call build socket, triggered through context cancellation (the build
// timeout is a const and too long to wait out).
func TestBuildGitCallNode_NoReaderLeakOnCancel(t *testing.T) {
	burstWSServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	v := &Executor{Ctx: ctx, Token: "t", APIUrl: "https://admin.corezoid.com"}
	g := gitCallBuild{lang: "python", nodeServerID: "n1", nodeTitle: "my-fn"}
	// Must be the cancellation, not a dial error — see the note in the deploy test.
	if err := v.buildGitCallNode(gitCallWSURL(v.APIUrl), g); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	assertNoGoroutine(t, "buildGitCallNode.func")
}
