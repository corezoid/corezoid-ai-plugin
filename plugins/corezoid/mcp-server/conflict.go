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

// conflictAction is what the push handler should do after the concurrency gate.
type conflictAction int

const (
	conflictProceed conflictAction = iota // no conflict (or force/advisory) — continue the push
	conflictBlock                         // stop the push and report (treated as an error result)
	conflictMerged                        // a merged file was written for review — stop, not an error
)

type conflictResult struct {
	action  conflictAction
	message string
}

// resolveConflict runs BEFORE any push mutation. It compares the process's live
// server version against the baseline recorded at pull time (baseline.go) and,
// when the server has moved, either blocks with a 3-way impact report, grafts a
// merge (merge=true), or overrides (force=true).
//
// A check that cannot run (no baseline, unreachable server) must never stop a
// push that would otherwise succeed — only a genuine conflict does.
func resolveConflict(v *Executor, filePath string, procID int, localJSON string, force, merge bool) conflictResult {
	dir := filepath.Dir(filePath)
	base, ok := lookupBaseline(dir, procID)
	if !ok {
		return conflictResult{conflictProceed, fmt.Sprintf(
			"Note: no pull baseline recorded for process #%d — concurrent-change detection is off for this file. Re-pull to enable it.", procID)}
	}

	proc, err := v.GetProcessByID(procID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return conflictResult{conflictBlock, fmt.Sprintf(
				"Push blocked: process #%d is no longer on the server (deleted since your pull). The local file is stale — re-pull the folder to reconcile before pushing.", procID)}
		}
		logger.Warn("conflict check: could not fetch server state for %d: %v", procID, err)
		return conflictResult{conflictProceed, ""}
	}

	current := baselineFromServer(proc)
	if !serverMovedSince(base, current) {
		return conflictResult{conflictProceed, ""} // in sync
	}

	// Server moved. Build a 3-way plan when we have the ancestor and can export
	// the current server scheme; without it we fall back to a delete-only impact.
	plan, theirsNodes, theirsConv, havePlan := computeMergePlan(v, dir, procID, localJSON)

	if force {
		fmt.Fprintf(os.Stderr, "[conflict] process #%d changed on the server since pull; overridden with force=true\n", procID)
		return conflictResult{conflictProceed, ""}
	}

	// Who last touched it on the server (for the block report only).
	editorName, editorTime := serverEditor(v, procID, proc)

	if merge {
		if !havePlan {
			return conflictResult{conflictBlock, "Cannot merge: no pull ancestor recorded for this file (pre-feature or capture failed). Re-pull the process, re-apply your edits, then push.\n\n" +
				formatConflict(procID, base, current, proc, localJSON, mergePlan{}, false, editorName, editorTime)}
		}
		return applyMerge(v, dir, filePath, procID, localJSON, current, theirsConv, plan, theirsNodes, editorName, editorTime)
	}

	return conflictResult{conflictBlock, formatConflict(procID, base, current, proc, localJSON, plan, havePlan, editorName, editorTime)}
}

// computeMergePlan gathers base (ancestor) / theirs (live export) / mine (local)
// and classifies every node. ok is false when the ancestor is missing or the
// server scheme can't be exported — the caller then uses the delete-only path.
func computeMergePlan(v *Executor, dir string, procID int, localJSON string) (plan mergePlan, theirsNodes []map[string]any, theirsConv string, ok bool) {
	ancestorConv, hasAnc := readAncestorScheme(dir, procID)
	if !hasAnc {
		return mergePlan{}, nil, "", false
	}
	tConv, hasT := exportConv(v)
	if !hasT {
		return mergePlan{}, nil, "", false
	}
	baseNodes := localSchemeNodes(ancestorConv)
	mineNodes := localSchemeNodes(localJSON)
	theirsNodes = localSchemeNodes(tConv)
	plan = buildMergePlan(baseNodes, theirsNodes, mineNodes)
	return plan, theirsNodes, tConv, true
}

// exportConv downloads the current server scheme in .conv.json shape (the same
// path pull uses), so all three merge inputs share one format.
func exportConv(v *Executor) (string, bool) {
	raw, err := v.ExportProcess()
	if err != nil {
		logger.Warn("conflict merge: export current server scheme failed: %v", err)
		return "", false
	}
	obj := raw
	if arr, ok := raw.([]any); ok && len(arr) > 0 {
		obj = arr[0]
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// applyMerge writes the materialised merge to the local file for review. When
// there are no true conflicts the baseline advances to theirs so the follow-up
// push proceeds cleanly; when conflicts remain the baseline is left untouched so
// the user must consciously resolve them and force (the auto-snapshot protects
// the server version either way).
func applyMerge(v *Executor, dir, filePath string, procID int, localJSON string, current baselineEntry, theirsConv string, plan mergePlan, theirsNodes []map[string]any, editorName string, editorTime int64) conflictResult {
	merged, err := materializeMerge(localJSON, plan, theirsNodes)
	if err != nil {
		return conflictResult{conflictBlock, fmt.Sprintf("Merge could not be built: %v — re-pull and re-apply your edits instead.", err)}
	}
	if err := os.WriteFile(filePath, []byte(merged), 0644); err != nil {
		return conflictResult{conflictBlock, fmt.Sprintf("Merge built but could not write %s: %v", filePath, err)}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Reconciled process #%d with the current server version.\n", procID)
	if editorName != "" {
		if editorTime > 0 {
			fmt.Fprintf(&sb, "(server last changed by %s at %s)\n", editorName, unixToUTC(editorTime))
		} else {
			fmt.Fprintf(&sb, "(server last changed by %s)\n", editorName)
		}
	}
	sb.WriteString("\n")
	sb.WriteString(formatMergePlan(plan))
	if len(plan.Conflicts) == 0 {
		if berr := writeBaseline(dir, procID, current); berr != nil {
			logger.Warn("merge: baseline advance failed for %d: %v", procID, berr)
		}
		if aerr := writeAncestorScheme(dir, procID, theirsConv); aerr != nil {
			logger.Warn("merge: ancestor advance failed for %d: %v", procID, aerr)
		}
		fmt.Fprintf(&sb, "\nMerged into %s — no conflicts. Review it, then push again; it will proceed cleanly.\n", filePath)
	} else {
		fmt.Fprintf(&sb, "\nGrafted the non-conflicting changes into %s. The %d conflicting node(s) above were kept as YOUR version.\nReview them, then push with force=true to deploy (the server version is auto-snapshotted, so it stays recoverable).\n",
			filePath, len(plan.Conflicts))
	}
	return conflictResult{conflictMerged, sb.String()}
}

// formatConflict renders the block report: version divergence, who/when, the
// impact, and options. With a 3-way plan it itemises modifications, adds,
// deletes and true conflicts; without one it falls back to a delete-only view.
func formatConflict(procID int, base, current baselineEntry, proc map[string]any, localJSON string, plan mergePlan, havePlan bool, editorName string, editorTime int64) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Push blocked: process #%d changed on the server since your pull.\n\n", procID)
	if editorName != "" {
		if editorTime > 0 {
			fmt.Fprintf(&sb, "  last changed by: %s (%s)\n", editorName, unixToUTC(editorTime))
		} else {
			fmt.Fprintf(&sb, "  last changed by: %s\n", editorName)
		}
	}
	fmt.Fprintf(&sb, "  server now:    change_time=%d (%s), version=%d\n",
		current.ChangeTime, unixToUTC(current.ChangeTime), current.Version)
	fmt.Fprintf(&sb, "  your baseline: change_time=%d (%s), version=%d\n",
		base.ChangeTime, unixToUTC(base.ChangeTime), base.Version)
	sb.WriteString("\n")

	if havePlan {
		sb.WriteString(formatMergePlan(plan))
		sb.WriteString("\nChoose one — nothing has been pushed yet:\n\n")
		if len(plan.Conflicts) == 0 {
			sb.WriteString("  [1] merge=true   COMBINE both — recommended (nothing overlaps)\n")
			sb.WriteString("        keeps ALL your edits AND adds ALL the server's changes above — nothing is lost.\n")
			sb.WriteString("        Writes the merged file for you to review, then push again → deploys cleanly.\n\n")
		} else {
			sb.WriteString("  [1] merge=true   COMBINE what doesn't overlap\n")
			sb.WriteString("        keeps your edits AND adds the server's NON-overlapping changes above.\n")
			sb.WriteString("        The overlapping node(s) are KEPT AS YOURS — resolve those by hand, then push with force=true.\n\n")
		}
		sb.WriteString("  [2] re-pull      THEIRS WINS — take the server version\n")
		sb.WriteString("        overwrites your local file with the server's; YOUR local edits are DISCARDED and\n")
		sb.WriteString("        you re-apply them by hand. Use when the overlap is too tangled to merge.\n\n")
		sb.WriteString("  [3] force=true   YOURS WINS — deploy your file as-is\n")
		sb.WriteString("        the live process becomes EXACTLY your version; the server's changes above are DROPPED\n")
		sb.WriteString("        (auto-snapshotted first, so recoverable).\n")
		return sb.String()
	}

	// Fallback: no ancestor recorded — show the delete-only impact.
	del := serverNodesAbsentLocally(proc, localJSON)
	sCount, lCount := nodeCount(proc, localJSON)
	sb.WriteString("Impact if you push as-is:\n")
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
	sb.WriteString("\nChoose one — nothing has been pushed yet:\n\n")
	sb.WriteString("  [1] re-pull      THEIRS WINS — take the server version, re-apply your edits by hand (your local edits are discarded)\n")
	sb.WriteString("  [2] force=true   YOURS WINS — deploy your file as-is; the server's changes are DROPPED (auto-snapshotted first, so recoverable)\n")
	sb.WriteString("  (the node-level 3-way merge needs a pull ancestor for this file — re-pull once to enable it)\n")
	return sb.String()
}

// serverEditor answers "who last changed this on the server, and when". It
// prefers the process's own commit list; when that carries no author (the
// download response often omits it) it falls back to the most recent snapshot,
// which records the user_name and time of whoever last pushed. Best-effort:
// returns "" when neither source has a name.
func serverEditor(v *Executor, procID int, proc map[string]any) (name string, when int64) {
	if n, t := latestCommitter(proc); n != "" {
		return n, t
	}
	projectID, _ := resolveAndCacheProjectID(v)
	if projectID != 0 && v.StageID != 0 {
		if snaps, err := v.ListSnapshots(procID, projectID, v.StageID); err == nil {
			return latestSnapshotAuthor(snaps)
		}
	}
	return "", 0
}

// latestSnapshotAuthor returns the user_name and time of the newest snapshot.
func latestSnapshotAuthor(snaps []Snapshot) (string, int64) {
	var best Snapshot
	for _, s := range snaps {
		if s.CreateTime >= best.CreateTime {
			best = s
		}
	}
	return best.UserName, best.CreateTime
}

// latestCommitter returns the author name and unix time of the most recent
// entry in commits.list (both zero when unavailable). The download response
// frequently omits the author, so callers fall back to serverEditor's snapshot.
func latestCommitter(proc map[string]any) (name string, when int64) {
	commits, ok := proc["commits"].(map[string]any)
	if !ok {
		return "", 0
	}
	list, ok := commits["list"].([]interface{})
	if !ok {
		return "", 0
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
		if n := commitName(m); n != "" {
			bestT = ct
			name = n
		}
	}
	return name, int64(bestT)
}

// commitName pulls an author label out of a commit entry, trying the field
// names Corezoid has used across responses before falling back to the id.
func commitName(m map[string]any) string {
	for _, k := range []string{"nick", "user_name", "login", "name"} {
		if s, _ := m[k].(string); s != "" {
			return s
		}
	}
	if uid, ok := m["user_id"].(float64); ok {
		return fmt.Sprintf("user %d", int(uid))
	}
	return ""
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

// localSchemeNodes parses scheme.nodes out of a conv JSON string.
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
