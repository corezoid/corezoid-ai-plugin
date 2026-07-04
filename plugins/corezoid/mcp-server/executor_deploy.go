package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// deployTimeout bounds how long we wait for a stage merge (deploy) to finish.
// A merge copies an entire stage's scheme; large stages take longer than a
// git_call build, so this is generous. It's a var (not const) so tests can
// shorten it.
var deployTimeout = 10 * time.Minute

// deployMonitor is the seam through which handleDeployStage watches a merge on
// the progress WebSocket. It's a package var so tests can stub it and avoid a
// real WS connection.
var deployMonitor = (*Executor).monitorDeployProgress

// monitorDeployProgress blocks until the async stage-merge identified by `hash`
// finishes, returning a human-readable progress log (one line per phase update)
// so the caller can show the user what happened over the socket — useful for
// long deploys. /api/2/merge is asynchronous: it returns a hash that the admin
// UI then watches over the progress WebSocket with a
//   {type:"monitor_stat", obj:"scheme_progress_info", obj_id:<hash>, status:"on"}
// op. The server streams phase progress (mode "upload" then "merge") and, when
// done, a {type:"notify", action_type:"merge"} frame. Reuses the same WS
// endpoint/origin/keepalive machinery as the git_call build (executor_gitcall.go).
func (v *Executor) monitorDeployProgress(hash string) (string, error) {
	var progress []string
	appendLine := func(line string) {
		if line == "" {
			return
		}
		if n := len(progress); n > 0 && progress[n-1] == line {
			return // collapse repeated frames
		}
		progress = append(progress, line)
	}
	logOut := func() string { return strings.Join(progress, "\n") }

	wsURL := gitCallWSURL(v.APIUrl)
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
		return "", fmt.Errorf("deploy progress websocket dial %s%s: %w", wsURL, status, err)
	}
	defer conn.Close()

	startFrame, _ := json.Marshal(map[string]any{"ops": []map[string]any{{
		"type":       "monitor_stat",
		"obj":        "scheme_progress_info",
		"obj_id":     hash,
		"company_id": v.WorkspaceID,
		"status":     "on",
	}}})
	if err := conn.WriteMessage(websocket.TextMessage, startFrame); err != nil {
		return "", fmt.Errorf("send deploy monitor start: %w", err)
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

	deadline := time.NewTimer(deployTimeout)
	defer deadline.Stop()
	ping := time.NewTicker(gitCallWSPingEvery)
	defer ping.Stop()

	mergeComplete := false
	for {
		select {
		case <-v.Ctx.Done():
			return logOut(), v.Ctx.Err()
		case <-deadline.C:
			return logOut(), fmt.Errorf("deploy timed out after %s", deployTimeout)
		case <-ping.C:
			// Application-level keepalive: client sends "0", server answers "1".
			_ = conn.WriteMessage(websocket.TextMessage, []byte("0"))
		case r := <-reads:
			if r.err != nil {
				// The server closes the socket once the deploy notify has been
				// delivered; a close after the merge phase completed is success.
				if mergeComplete {
					return logOut(), nil
				}
				return logOut(), fmt.Errorf("deploy progress websocket closed before done: %w", r.err)
			}
			s := string(r.msg)
			if s == "1" || s == "0" { // keepalive pong/ping frames
				continue
			}
			done, failed, line := parseDeployProgress(r.msg)
			appendLine(line)
			if failed {
				return logOut(), fmt.Errorf("deploy reported error: %s", line)
			}
			if done {
				mergeComplete = true
				return logOut(), nil
			}
		}
	}
}

// parseDeployProgress classifies a progress frame. done=true when the merge
// phase reports complete or a notify/merge arrives; failed=true when an op
// reports a non-ok proc. `line` is a short human-readable summary of the frame
// (empty for keepalive/garbage) used to build the progress log. The "upload"
// phase completing does NOT end the wait — only the "merge" phase / notify does.
func parseDeployProgress(msg []byte) (done, failed bool, line string) {
	var frame struct {
		Ops []struct {
			Type        string        `json:"type"`
			ActionType  string        `json:"action_type"`
			Mode        string        `json:"mode"`
			Proc        string        `json:"proc"`
			Status      string        `json:"status"`
			Description string        `json:"description"`
			Errors      []interface{} `json:"errors"`
			List        []struct {
				Status   string `json:"status"`
				Progress int    `json:"progress"`
			} `json:"list"`
		} `json:"ops"`
	}
	if err := json.Unmarshal(msg, &frame); err != nil {
		return false, false, ""
	}
	for _, op := range frame.Ops {
		// SUCCESS completion. Corezoid signals a finished merge two ways:
		//   • a {type:"notify", action_type:"merge"} frame, and/or
		//   • an op with description "Merging is finished" — which, confusingly,
		//     carries proc:"error" but status:"complete" and an EMPTY errors list.
		// Both mean the deploy SUCCEEDED, so check them BEFORE the proc!=ok gate,
		// otherwise the completion frame is misread as a failure.
		if op.Type == "notify" && op.ActionType == "merge" {
			return true, false, "merge: finished ✓"
		}
		if strings.Contains(op.Description, "finished") ||
			(op.Status == "complete" && len(op.Errors) == 0 && op.Type == "") {
			return true, false, "merge: finished ✓"
		}
		// Genuine failure: a non-ok proc that is NOT the "finished" completion above.
		if op.Proc != "" && op.Proc != "ok" {
			detail := op.Proc
			if op.Description != "" {
				detail = op.Description
			}
			return false, true, "error: " + detail
		}
		if op.Type == "monitor_stat" && (op.Mode == "upload" || op.Mode == "merge") {
			st, pct := "", 0
			if len(op.List) > 0 {
				st, pct = op.List[0].Status, op.List[0].Progress
			}
			l := fmt.Sprintf("%s: %s (%d%%)", op.Mode, st, pct)
			// The merge phase completing is the effective end of the copy.
			if op.Mode == "merge" && (st == "complete" || pct >= 100) {
				return true, false, l
			}
			return false, false, l
		}
	}
	return false, false, ""
}

// stageInfo shows a stage and reports whether it is immutable (read-only — the
// only valid merge TARGET in Corezoid), how many undeployed changes it has (a
// stage with undeployed changes can't be a merge SOURCE — Corezoid rejects it
// with "You have to merge only from deployed stage"), and its title/short_name,
// which a follow-up modify must preserve so it doesn't blank them.
func (v *Executor) stageInfo(stageID, projectID int) (immutable bool, undeployed int, title, shortName string, err error) {
	resp, err := v.req("json", []map[string]any{{
		"type":       "show",
		"obj":        "stage",
		"obj_id":     stageID,
		"project_id": projectID,
		"company_id": v.WorkspaceID,
	}})
	if err != nil {
		return false, 0, "", "", err
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return false, 0, "", "", fmt.Errorf("show stage %d: empty response", stageID)
	}
	op, _ := opsArr[0].(map[string]interface{})
	if proc, _ := op["proc"].(string); proc != "ok" {
		desc, _ := op["description"].(string)
		if desc == "" {
			desc = "show stage did not succeed"
		}
		return false, 0, "", "", fmt.Errorf("show stage %d: %s", stageID, desc)
	}
	immutable, _ = op["immutable"].(bool)
	title, _ = op["title"].(string)
	shortName, _ = op["short_name"].(string)
	if u, ok := op["undeployed"].(float64); ok {
		undeployed = int(u)
	}
	return immutable, undeployed, title, shortName, nil
}

// setStageImmutable flips a stage's immutable (read-only) flag, preserving its
// title/short_name so the modify does not clear them.
func (v *Executor) setStageImmutable(stageID, projectID int, title, shortName string, immutable bool) error {
	resp, err := v.req("json", []map[string]any{{
		"type":       "modify",
		"obj":        "stage",
		"obj_id":     stageID,
		"immutable":  immutable,
		"title":      title,
		"short_name": shortName,
		"project_id": projectID,
		"company_id": v.WorkspaceID,
	}})
	if err != nil {
		return err
	}
	if e := deployOpProc(resp); e != "" {
		return fmt.Errorf("modify stage %d immutable=%v: %s", stageID, immutable, e)
	}
	return nil
}

// deployMergeHash pulls the merge job hash out of a /api/2/merge response so the
// caller can watch it on the progress WebSocket.
func deployMergeHash(resp map[string]interface{}) string {
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return ""
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	h, _ := opMap["hash"].(string)
	return h
}
