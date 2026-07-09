package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestBuildGitCallNodeRecordsLog verifies that a successful build records a
// human-readable log on the Executor (so the push handler can surface it),
// using a fake build WebSocket pointed at via COREZOID_WS_URL.
func TestBuildGitCallNodeRecordsLog(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, _, _ = c.ReadMessage() // consume the start-build frame
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"ops":[{"obj_type":"function_build","log":{"type":"stdout","message":"Compiling my-fn..."}}]}`))
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"ops":[{"obj_type":"function_build","log":{"type":"done","message":""}}]}`))
	}))
	defer srv.Close()
	os.Setenv("COREZOID_WS_URL", "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/1/sock_json")
	defer os.Unsetenv("COREZOID_WS_URL")

	v := &Executor{Ctx: context.Background(), Token: "t", APIUrl: "https://admin.corezoid.com"}
	g := gitCallBuild{lang: "python", nodeServerID: "n1", nodeTitle: "my-fn"}
	if err := v.buildGitCallNode(gitCallWSURL(v.APIUrl), g); err != nil {
		t.Fatalf("buildGitCallNode: %v", err)
	}
	joined := strings.Join(v.gitCallBuildLog, "\n")
	for _, want := range []string{"my-fn", "built ✓", "Compiling my-fn"} {
		if !strings.Contains(joined, want) {
			t.Errorf("build log missing %q:\n%s", want, joined)
		}
	}
}
