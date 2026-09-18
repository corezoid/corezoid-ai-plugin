package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// taskStubServer answers create_task / show_task and counts each. create is
// driven by createStatus: a non-200 is returned verbatim, 200 returns the
// proc value in createProc. show returns found/not-found per showFound.
func taskStubServer(t *testing.T, createStatus int, createProc string, showFound bool) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var creates, shows int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ops []map[string]interface{} `json:"ops"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		typ := ""
		if len(body.Ops) > 0 {
			typ, _ = body.Ops[0]["type"].(string)
		}
		switch typ {
		case "create":
			atomic.AddInt32(&creates, 1)
			if createStatus != http.StatusOK {
				w.WriteHeader(createStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"request_proc": "ok",
				"ops":          []interface{}{map[string]interface{}{"proc": createProc, "description": "ref already exists"}},
			})
		case "show":
			atomic.AddInt32(&shows, 1)
			if !showFound {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"request_proc": "ok",
				"ops":          []interface{}{map[string]interface{}{"proc": "ok", "ref": "R1"}},
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &creates, &shows
}

func newTaskExecutor(t *testing.T, srvURL string) *Executor {
	t.Helper()
	resetGlobals(t)
	setProjectAuth(t, srvURL)
	return NewValidator(context.Background(), 12345)
}

// Creating a task starts a live process, and whatever that process does to the
// outside world is not undoable from here. A 429/503 is not proof of
// rejection, so a second delivery can mean a second charge, message or partner
// call. The op must leave exactly one create on the wire.
func TestCreateTask_TransientFailureIsNeverResent(t *testing.T) {
	shortenRetryDelays(t)
	srv, creates, _ := taskStubServer(t, http.StatusServiceUnavailable, "", false)
	v := newTaskExecutor(t, srv.URL)

	if err := v.createTask("R1", map[string]interface{}{"a": 1}); err == nil {
		t.Fatal("expected an error when create_task 503s and the task cannot be found")
	}
	if n := atomic.LoadInt32(creates); n != 1 {
		t.Fatalf("create_task was delivered %d times, want exactly 1 — a retry duplicates the process run", n)
	}
}

// A transport failure leaves the outcome genuinely unknown: Corezoid can accept
// the task and still fail to answer. `ref` is the task's server-side identity,
// so the honest move is to ask whether it exists rather than report a failure
// for work that is already running — a false failure is what drives the user to
// re-run by hand, which is the duplicate execution nobody wanted.
func TestCreateTask_TransportFailureResolvedByRefProbe(t *testing.T) {
	shortenRetryDelays(t)
	srv, creates, shows := taskStubServer(t, http.StatusServiceUnavailable, "", true)
	v := newTaskExecutor(t, srv.URL)

	if err := v.createTask("R1", map[string]interface{}{"a": 1}); err != nil {
		t.Fatalf("task exists on the server under this ref; createTask must report success, got %v", err)
	}
	if n := atomic.LoadInt32(creates); n != 1 {
		t.Fatalf("create_task delivered %d times, want 1", n)
	}
	if n := atomic.LoadInt32(shows); n == 0 {
		t.Fatal("expected a show_task probe to settle the ambiguous outcome")
	}
}

// A server-side rejection (HTTP 200, proc != "ok") is a definitive answer, and
// must not be second-guessed by the probe. The case that makes this matter is a
// caller-supplied ref that collides with an earlier run: create is rejected as
// a duplicate, the probe would find that OLD task, and reporting success would
// silently attach the caller to somebody else's result.
func TestCreateTask_ServerRejectionIsNotProbedAway(t *testing.T) {
	shortenRetryDelays(t)
	srv, creates, shows := taskStubServer(t, http.StatusOK, "error", true)
	v := newTaskExecutor(t, srv.URL)

	if err := v.createTask("R1", map[string]interface{}{"a": 1}); err == nil {
		t.Fatal("a server-side rejection must surface as an error")
	}
	if n := atomic.LoadInt32(creates); n != 1 {
		t.Fatalf("create_task delivered %d times, want 1", n)
	}
	if n := atomic.LoadInt32(shows); n != 0 {
		t.Fatalf("show_task was called %d times; an explicit rejection must not be resolved by probing an unrelated task under the same ref", n)
	}
}
