package main

import (
	"context"
	"regexp"
	"time"
)

var reEnvVar = regexp.MustCompile(`\{\{env_var\[@([a-z0-9-]+)]}}`)

type Executor struct {
	// Ctx propagates cancellation/timeout from the MCP request layer down to
	// the HTTP calls inside Executor.req. Never nil — NewValidator seeds it
	// with the caller's context or context.Background() as a fallback.
	Ctx           context.Context
	NodeResponses []map[string]interface{}
	ProcessID     int
	APILogin      string
	Token         string
	APISecret     string
	APIUrl        string
	WorkspaceID   string
	StageID       int
	NodeIDMap     map[string]NodeInfo
	Debug         bool
	Version       int
	NewProc       bool
}

// NewValidator constructs an Executor with a snapshot of the current auth
// state. Snapshotting at construction means the rest of the executor methods
// can read v.WorkspaceID/v.StageID/v.Token without any further locking — the
// snapshot is immutable for the lifetime of this Executor.
//
// ctx becomes v.Ctx and is threaded into every HTTP call this Executor makes,
// so cancelling ctx aborts in-flight requests. A nil ctx is replaced with
// context.Background() so callers (and tests) need not supply one explicitly.
func NewValidator(ctx context.Context, inProcessID int) *Executor {
	if ctx == nil {
		ctx = context.Background()
	}
	apiURLv, tokenv, workspaceIDv, _, stageIDv := authSnapshot()
	v := &Executor{
		Ctx:         ctx,
		APILogin:    "",
		APISecret:   "",
		APIUrl:      apiURLv,
		Token:       tokenv,
		WorkspaceID: workspaceIDv,
		StageID:     stageIDv,
		NodeIDMap:   make(map[string]NodeInfo),
		Debug:       debug,
		Version:     int(time.Now().Unix()),
		ProcessID:   inProcessID,
	}
	return v
}
