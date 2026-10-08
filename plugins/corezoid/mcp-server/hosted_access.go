package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// hostedAccess is one line of the hosted access log: which JSON-RPC method and
// tool were called, how it ended and how long it took. It never carries the
// token, the arguments or the result — only a short hash that groups the
// calls of one caller, and the client's User-Agent.
type hostedAccess struct {
	Method  string `json:"method,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Status  int    `json:"status"`
	IsError bool   `json:"is_error,omitempty"`
	Ms      int64  `json:"ms"`
	Caller  string `json:"caller,omitempty"`
	Client  string `json:"client,omitempty"`
}

// callerHash is a stable, non-reversible id for a token (12 hex chars).
func callerHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:6])
}

// hostedClip cuts s to n bytes (User-Agent is client-controlled).
func hostedClip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func logHostedAccess(r *http.Request, token string, a *hostedAccess, start time.Time) {
	a.Ms = time.Since(start).Milliseconds()
	a.Caller = callerHash(token)
	a.Client = hostedClip(r.UserAgent(), 80)
	line, _ := json.Marshal(a)
	logger.Info("access %s", line)
}

// toolCallIsError reports whether a dispatch result is a failed tool call.
func toolCallIsError(resp interface{}) bool {
	switch v := resp.(type) {
	case mcpResponse:
		if tr, ok := v.Result.(mcpToolResult); ok {
			return tr.IsError
		}
		return v.Error != nil
	}
	return false
}
