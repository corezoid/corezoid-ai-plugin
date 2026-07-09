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
	// gitCallBuildLog accumulates a human-readable build log per git_call node
	// built during a push, so the push handler can show the user what the
	// container build service reported (progress + result) — not just on failure.
	gitCallBuildLog []string
}

// checkCancel returns v.Ctx.Err() if the executor's context has been
// cancelled, nil otherwise. Call it at the top of long-running methods and
// inside loops over many sub-calls so cancellation is observed between API
// calls — not only mid-flight inside doWithRetry. The HTTP layer already
// honours ctx, but non-HTTP work (file IO, JSON parsing, response post-
// processing) would otherwise run to completion after a cancel signal.
func (v *Executor) checkCancel() error {
	if v == nil || v.Ctx == nil {
		return nil
	}
	return v.Ctx.Err()
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
