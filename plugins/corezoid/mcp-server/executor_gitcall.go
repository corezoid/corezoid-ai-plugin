package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Git Call container builds run on Corezoid's build service and are driven over
// a WebSocket, not the JSON-RPC HTTP API. push-process therefore needs a
// dedicated build step for git_call nodes: after the source is uploaded
// (ModifyNodes) and before Commit — otherwise Commit rejects an unbuilt node
// with "source has to be built".
//
// Interpreted runtimes (JavaScript) execute the source directly and need no
// build, so they are skipped.
const (
	// gitCallBuildTimeout caps how long we wait for a single node's container
	// build to report done. Real builds observed at ~20-60s; the cap is
	// generous to absorb dependency installs (pip/gradle/composer).
	gitCallBuildTimeout = 5 * time.Minute
	// gitCallWSPingEvery is the application-level keepalive cadence. The build
	// socket expects the client to send "0" and answers "1".
	gitCallWSPingEvery = 15 * time.Second
	// gitCallWSHandshakeTimeout bounds the WebSocket upgrade.
	gitCallWSHandshakeTimeout = 15 * time.Second
)

// interpretedGitCallLangs need no container build. Only an unset lang is treated
// as a non-git_call/no-build block; every real runtime (js included) is built on
// the container build service before commit.
var interpretedGitCallLangs = map[string]bool{"": true}

// gitCallBuild is the resolved, build-relevant view of one git_call logic block.
type gitCallBuild struct {
	nodeServerID string // server-assigned node id (24-hex), not the local uuid
	nodeTitle    string
	lang         string
	code         string
	repo         string
	path         string
	script       string
	commit       string
}

// needsBuild reports whether this runtime requires a container build.
func (g gitCallBuild) needsBuild() bool { return !interpretedGitCallLangs[strings.ToLower(g.lang)] }

// collectGitCallBuilds walks the process nodes and returns every git_call /
// api_git logic that carries source, resolved to its server-assigned node id.
func (v *Executor) collectGitCallBuilds(nodes []interface{}) []gitCallBuild {
	var out []gitCallBuild
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}
		localID, _ := nodeMap["id"].(string)
		serverID := localID
		if info, ok := v.NodeIDMap[localID]; ok && info.ServerID != "" {
			serverID = info.ServerID
		}
		title, _ := nodeMap["title"].(string)
		condition, ok := nodeMap["condition"].(map[string]interface{})
		if !ok {
			continue
		}
		logics, ok := condition["logics"].([]interface{})
		if !ok {
			continue
		}
		for _, logic := range logics {
			lm, ok := logic.(map[string]interface{})
			if !ok {
				continue
			}
			lt, _ := lm["type"].(string)
			if lt != "git_call" && lt != "api_git" {
				continue
			}
			g := gitCallBuild{
				nodeServerID: serverID,
				nodeTitle:    title,
				lang:         strFromLogic(lm, "lang"),
				code:         firstNonEmpty(strFromLogic(lm, "src"), strFromLogic(lm, "code")),
				repo:         strFromLogic(lm, "repo"),
				path:         strFromLogic(lm, "path"),
				script:       strFromLogic(lm, "script"),
				commit:       strFromLogic(lm, "commit"),
			}
			out = append(out, g)
		}
	}
	return out
}

// BuildGitCallNodes builds every compiled git_call node in the process over the
// Corezoid build WebSocket, blocking until each reports done. It must run after
// ModifyNodes and before Commit. Interpreted runtimes (js) are skipped.
func (v *Executor) BuildGitCallNodes(nodes []interface{}) error {
	builds := v.collectGitCallBuilds(nodes)
	if len(builds) == 0 {
		return nil
	}
	wsURL := gitCallWSURL(v.APIUrl)
	for _, g := range builds {
		if !g.needsBuild() {
			logger.Debug("git_call node %s lang=%s is interpreted — no build", g.nodeServerID, g.lang)
			continue
		}
		if err := v.checkCancel(); err != nil {
			return err
		}
		logger.Debug("Building git_call node %s (lang=%s) via %s", g.nodeServerID, g.lang, wsURL)
		if err := v.buildGitCallNode(wsURL, g); err != nil {
			return fmt.Errorf("git_call build failed for node %q (%s): %w", g.nodeTitle, g.lang, err)
		}
		logger.Debug("git_call node %s built", g.nodeServerID)
	}
	return nil
}

// buildGitCallNode opens the build WebSocket, starts the build for one node,
// keeps the socket alive, and returns when the build reports done — or fails /
// times out. The returned error carries the tail of the build log for context.
func (v *Executor) buildGitCallNode(wsURL string, g gitCallBuild) error {
	header := http.Header{}
	header.Set("Authorization", fmt.Sprintf("Simulator %s", v.Token))
	header.Set("Origin", gitCallWSOrigin(v.APIUrl))

	dialer := &websocket.Dialer{HandshakeTimeout: gitCallWSHandshakeTimeout}
	conn, resp, err := dialer.DialContext(v.Ctx, wsURL, header)
	if err != nil {
		status := ""
		if resp != nil {
			status = " (" + resp.Status + ")"
		}
		return fmt.Errorf("build websocket dial %s%s: %w", wsURL, status, err)
	}
	defer conn.Close()

	// Start-build frame. obj_id is a fresh build id we generate; user_id is not
	// required. status:"on" starts the build.
	startFrame, _ := json.Marshal(map[string]any{"ops": []map[string]any{{
		"type":     "monitor_show",
		"obj":      "git_call",
		"obj_type": "function_build",
		"node_id":  g.nodeServerID,
		"obj_id":   newBuildID(),
		"conv_id":  v.ProcessID,
		"code":     g.code,
		"lang":     g.lang,
		"repo":     g.repo,
		"path":     g.path,
		"script":   g.script,
		"commit":   g.commit,
		"status":   "on",
	}}})
	if err := conn.WriteMessage(websocket.TextMessage, startFrame); err != nil {
		return fmt.Errorf("send build start: %w", err)
	}

	type readResult struct {
		msg []byte
		err error
	}
	reads := make(chan readResult, 1)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			reads <- readResult{msg, err}
			if err != nil {
				return
			}
		}
	}()

	deadline := time.NewTimer(gitCallBuildTimeout)
	defer deadline.Stop()
	ping := time.NewTicker(gitCallWSPingEvery)
	defer ping.Stop()

	var logTail []string
	appendLog := func(s string) {
		logTail = append(logTail, s)
		if len(logTail) > 12 {
			logTail = logTail[len(logTail)-12:]
		}
	}

	for {
		select {
		case <-v.Ctx.Done():
			return v.Ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("build timed out after %s; last log: %s", gitCallBuildTimeout, strings.Join(logTail, " | "))
		case <-ping.C:
			// Application-level keepalive: client sends "0", server answers "1".
			_ = conn.WriteMessage(websocket.TextMessage, []byte("0"))
		case r := <-reads:
			if r.err != nil {
				return fmt.Errorf("build websocket closed before done: %w; last log: %s", r.err, strings.Join(logTail, " | "))
			}
			s := string(r.msg)
			if s == "1" || s == "0" { // keepalive pong/ping frames
				continue
			}
			status, message := parseGitCallBuildLog(r.msg)
			switch status {
			case "done":
				// Record the build outcome so the push handler can show it. On
				// success the log is otherwise discarded (only errors surfaced it).
				v.gitCallBuildLog = append(v.gitCallBuildLog, fmt.Sprintf("• %s (%s): built ✓", g.nodeTitle, g.lang))
				for _, l := range logTail {
					v.gitCallBuildLog = append(v.gitCallBuildLog, "    "+l)
				}
				return nil
			case "error":
				return fmt.Errorf("build reported error: %s; log: %s", message, strings.Join(logTail, " | "))
			case "stdout", "stderr":
				if message != "" {
					appendLog(message)
				}
			}
		}
	}
}

// parseGitCallBuildLog extracts log.type and log.message from a build monitor
// frame. Unrecognized frames yield an empty status.
func parseGitCallBuildLog(msg []byte) (status, message string) {
	var frame struct {
		Ops []struct {
			ObjType string `json:"obj_type"`
			Log     struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"log"`
		} `json:"ops"`
	}
	if err := json.Unmarshal(msg, &frame); err != nil {
		return "", ""
	}
	for _, op := range frame.Ops {
		if op.ObjType == "function_build" {
			return op.Log.Type, op.Log.Message
		}
	}
	return "", ""
}

// gitCallWSURL derives the build-WebSocket URL from the configured API URL.
// Cloud: https://admin.corezoid.com -> wss://ws.corezoid.com/api/1/sock_json.
// Override with COREZOID_WS_URL for on-prem / non-standard deployments.
func gitCallWSURL(apiURL string) string {
	if override := strings.TrimSpace(os.Getenv("COREZOID_WS_URL")); override != "" {
		return override
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "wss://ws.corezoid.com/api/1/sock_json"
	}
	host := u.Host
	// admin.<domain> -> ws.<domain>; otherwise prefix ws. onto the host.
	if strings.HasPrefix(host, "admin.") {
		host = "ws." + strings.TrimPrefix(host, "admin.")
	} else if !strings.HasPrefix(host, "ws.") {
		host = "ws." + host
	}
	return "wss://" + host + "/api/1/sock_json"
}

// gitCallWSOrigin returns the Origin header the build WebSocket expects
// (the admin UI origin the API URL belongs to).
func gitCallWSOrigin(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "https://admin.corezoid.com"
	}
	return "https://" + u.Host
}

// newBuildID returns a random UUIDv4 string used as the build's obj_id.
func newBuildID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// strFromLogic reads a string field from a logic map (missing -> "").
func strFromLogic(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, s := range vals {
		if s != "" {
			return s
		}
	}
	return ""
}
