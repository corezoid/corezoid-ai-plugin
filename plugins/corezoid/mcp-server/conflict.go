package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// conflictCheck runs BEFORE any push mutation. It compares the process's live
// server version against the baseline recorded at pull time (baseline.go).
//
// Returns blocked=true with a message when the push must stop: a real
// concurrent-edit conflict and force is false. Returns blocked=false with an
// optional advisory message otherwise (no baseline / in sync / force override /
// unreachable server). Never returns an error — a check that can't run must not
// stop a push that would otherwise succeed, except for a genuine conflict.
func conflictCheck(v *Executor, filePath string, procID int, localJSON string, force bool) (blocked bool, message string) {
	dir := filepath.Dir(filePath)
	base, ok := lookupBaseline(dir, procID)
	if !ok {
		// No baseline: freshness cannot be verified. Advisory only — pre-feature
		// files and never-pulled ids must stay pushable.
		return false, fmt.Sprintf("Note: no pull baseline recorded for process #%d — concurrent-change detection is off for this file. Re-pull to enable it.", procID)
	}

	proc, err := v.GetProcessByID(procID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return true, fmt.Sprintf("Push blocked: process #%d is no longer on the server (deleted since your pull). The local file is stale — re-pull the folder to reconcile before pushing.", procID)
		}
		// Can't reach the server to check — don't block a push on that.
		logger.Warn("conflict check: could not fetch server state for %d: %v", procID, err)
		return false, ""
	}

	current := baselineFromServer(proc)
	if !serverMovedSince(base, current) {
		return false, "" // in sync — no conflict
	}

	report := formatConflict(procID, base, current, proc, localJSON)
	if force {
		fmt.Fprintf(os.Stderr, "[conflict] process #%d changed on the server since pull; overridden with force=true\n", procID)
		return false, ""
	}
	return true, report
}

// formatConflict renders the human-facing conflict report: version divergence,
// who/when last changed it, the impact of pushing as-is, and the options.
func formatConflict(procID int, base, current baselineEntry, proc map[string]any, localJSON string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Push blocked: process #%d changed on the server since your pull.\n\n", procID)
	fmt.Fprintf(&sb, "  server now:    change_time=%d (%s), version=%d\n",
		current.ChangeTime, unixToUTC(current.ChangeTime), current.Version)
	fmt.Fprintf(&sb, "  your baseline: change_time=%d (%s), version=%d\n",
		base.ChangeTime, unixToUTC(base.ChangeTime), base.Version)
	if who, when := latestCommitter(proc); who != "" {
		fmt.Fprintf(&sb, "  last changed by: %s%s\n", who, when)
	}

	del := serverNodesAbsentLocally(proc, localJSON)
	sCount, lCount := nodeCount(proc, localJSON)
	sb.WriteString("\nImpact if you push as-is:\n")
	fmt.Fprintf(&sb, "  server has %d node(s); your local copy has %d.\n", sCount, lCount)
	if len(del) > 0 {
		shown := del
		more := 0
		if len(shown) > 12 {
			more = len(shown) - 12
			shown = shown[:12]
		}
		fmt.Fprintf(&sb, "  your push would DELETE %d server node(s) that are not in your local copy: %s",
			len(del), strings.Join(shown, ", "))
		if more > 0 {
			fmt.Fprintf(&sb, " (+%d more)", more)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nOptions:\n")
	sb.WriteString("  • inspect  — the impact above shows what your push changes vs the current server\n")
	sb.WriteString("  • re-pull  — run pull-process to fetch the current version, then re-apply your edits on top\n")
	sb.WriteString("  • overwrite — re-run push with force=true (an auto-snapshot of the server version is taken first, so it is recoverable)\n")
	return sb.String()
}

// latestCommitter returns the nick and formatted time of the most recent entry
// in commits.list ("" when unavailable).
func latestCommitter(proc map[string]any) (who string, when string) {
	commits, ok := proc["commits"].(map[string]any)
	if !ok {
		return "", ""
	}
	list, ok := commits["list"].([]interface{})
	if !ok {
		return "", ""
	}
	var bestT float64
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := m["change_time"].(float64)
		if ct < bestT {
			continue
		}
		nick, _ := m["nick"].(string)
		if nick == "" {
			if uid, ok := m["user_id"].(float64); ok {
				nick = fmt.Sprintf("user %d", int(uid))
			}
		}
		if nick != "" {
			bestT = ct
			who = nick
		}
	}
	if who != "" && bestT > 0 {
		when = " at " + unixToUTC(int64(bestT))
	}
	return who, when
}

// serverNodesAbsentLocally lists titles of server nodes (non-empty, non-start)
// that have no same-title node in the local scheme — the nodes a push would
// delete. Best-effort by title (node ids are not stable across pull/push).
func serverNodesAbsentLocally(proc map[string]any, localJSON string) []string {
	localTitles := map[string]bool{}
	for _, n := range localSchemeNodes(localJSON) {
		if t, _ := n["title"].(string); t != "" {
			localTitles[t] = true
		}
	}
	var absent []string
	seen := map[string]bool{}
	for _, raw := range serverList(proc) {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ot, _ := n["obj_type"].(float64); int(ot) == 1 {
			continue // start node always present
		}
		t, _ := n["title"].(string)
		if t == "" || localTitles[t] || seen[t] {
			continue
		}
		seen[t] = true
		absent = append(absent, t)
	}
	sort.Strings(absent)
	return absent
}

func nodeCount(proc map[string]any, localJSON string) (server, local int) {
	return len(serverList(proc)), len(localSchemeNodes(localJSON))
}

// serverList returns the server node list from a GetProcessByID response.
func serverList(proc map[string]any) []interface{} {
	if l, ok := proc["list"].([]interface{}); ok {
		return l
	}
	return nil
}

// localSchemeNodes parses scheme.nodes out of a local process file's content.
func localSchemeNodes(localJSON string) []map[string]any {
	var doc map[string]any
	if err := json.Unmarshal([]byte(localJSON), &doc); err != nil {
		return nil
	}
	scheme, ok := doc["scheme"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := scheme["nodes"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func unixToUTC(sec int64) string {
	if sec <= 0 {
		return "unknown"
	}
	return time.Unix(sec, 0).UTC().Format("2006-01-02 15:04 UTC")
}
