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
// An UNREACHABLE server when we DO have a baseline means the check cannot run
// and lost-update detection is silently off — we block, because the same
// Corezoid API that just failed here is the one ProcessJSON is about to call
// anyway. Retry once the API recovers.
//
// A missing baseline means this file was never pulled, so there is nothing to
// compare against. What that implies depends entirely on the server:
//
//   - the process has no deployed version yet (the create-process → push-process
//     flow, or a re-push of a never-deployed conv): nothing can be lost, so the
//     push proceeds with a note;
//   - the process IS deployed: the local file is an import, a hand-copy or a
//     file whose sidecar was lost, and pushing it overwrites a live version with
//     no idea what that version contains. That is the exact silent overwrite the
//     whole baseline subsystem exists to prevent, so it blocks and asks for a
//     pull (or an explicit adopt_existing waiver);
//   - the server could not answer: fail closed, same as above.
func resolveConflict(v *Executor, filePath string, procID int, localJSON string, force, merge, adoptExisting bool) conflictResult {
	dir := filepath.Dir(filePath)
	base, ok, baselineErr := lookupBaseline(dir, procID)
	if baselineErr != nil {
		return conflictResult{conflictBlock, fmt.Sprintf(
			"Push blocked: the concurrency baseline for process #%d is unreadable: %v. Re-pull the process to rebuild the sidecar before pushing; continuing would disable lost-update protection.", procID, baselineErr)}
	}
	if !ok {
		switch {
		case processNeverDeployed(v, procID):
			return conflictResult{conflictProceed, fmt.Sprintf(
				"Note: no pull baseline recorded for process #%d, but it has no deployed version yet, so there is nothing a concurrent edit could overwrite.", procID)}
		case adoptExisting:
			return conflictResult{conflictProceed, fmt.Sprintf(
				"Warning: no pull baseline recorded for process #%d and adopt_existing=true was passed — this push overwrites the deployed version WITHOUT knowing what it contains. Concurrent-change detection was off for this push.", procID)}
		default:
			return conflictResult{conflictBlock, fmt.Sprintf(
				"Push blocked: process #%d already has a deployed version on the server, but this file has no pull baseline — so lost-update detection cannot run and pushing would overwrite that version blind. Run pull-process for #%d first (that records the baseline and lets a real conflict be reported), or re-run with adopt_existing=true to overwrite the server version deliberately. adopt_existing is separate from force on purpose: force resolves a conflict you have been shown, this one declares you have no idea what is on the server.", procID, procID)}
		}
	}

	proc, err := v.GetProcessByID(procID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return conflictResult{conflictBlock, fmt.Sprintf(
				"Push blocked: process #%d is no longer on the server (deleted since your pull). The local file is stale — re-pull the folder to reconcile before pushing.", procID)}
		}
		return conflictResult{conflictBlock, fmt.Sprintf(
			"Push blocked: could not fetch the current server state for process #%d (%v). Concurrent-change detection cannot run, so a lost update would be silent. Retry once the Corezoid API is reachable — the deploy call that follows would fail against the same endpoint anyway.", procID, err)}
	}

	current := baselineFromServer(proc)
	serverChanged := serverMovedSince(base, current)
	// Equal-timestamp fallback: for list-sourced (pull-folder) or legacy
	// (pre-Source-tag) baselines, serverMovedSince intentionally skips the
	// sub-second version tiebreak because the two Version fields don't share
	// semantics. That leaves a same-second lost-update blind spot when a
	// concurrent commit lands in the same second as our pull. Close the gap
	// by content-diffing the recorded ancestor against the live server scheme
	// only in the suspicious case — no extra work on the common in-sync path.
	if !serverChanged && base.ChangeTime == current.ChangeTime && base.Source != baselineSourceDetail {
		switch serverContentChangedSince(v, dir, procID) {
		case contentCheckChanged:
			serverChanged = true
		case contentCheckNoAncestor:
			// Legacy sidecars predate the ancestor snapshot; without a recorded
			// scheme we can't run the content diff. Preserve the old warn+proceed
			// behaviour so pre-v3.1.3 pulls don't regress into a hard block.
			logger.Warn("conflict: equal-timestamp content check for process #%d skipped (no ancestor recorded — legacy sidecar) — proceeding without the extra safety net", procID)
		case contentCheckExportFailed:
			if !force {
				return conflictResult{conflictBlock, fmt.Sprintf(
					"Push blocked: process #%d has an equal-timestamp baseline (list-sourced or legacy) and the supplementary content diff could not complete (export or parse failed). A concurrent same-second overwrite cannot be ruled out. Retry once the export endpoint responds, or pass force=true to override at your own risk.", procID)}
			}
			logger.Warn("conflict: equal-timestamp content check for process #%d failed (export or parse) — force=true overrides the block", procID)
		}
	}
	if !serverChanged {
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
		return applyMerge(dir, filePath, procID, localJSON, current, theirsConv, plan, theirsNodes, editorName, editorTime)
	}

	return conflictResult{conflictBlock, formatConflict(procID, base, current, proc, localJSON, plan, havePlan, editorName, editorTime)}
}

// contentCheckResult classifies the outcome of the equal-timestamp content diff.
// The caller reacts differently to each: an actual change blocks, a legacy
// sidecar (no ancestor) warns-and-proceeds for pre-v3.1.3 compatibility, and an
// export/parse failure blocks unless the user forces because the same API path
// is about to be used to deploy — a broken export usually foreshadows trouble.
type contentCheckResult int

const (
	contentCheckUnchanged     contentCheckResult = iota // ancestor == server scheme
	contentCheckChanged                                 // server scheme differs from ancestor
	contentCheckNoAncestor                              // legacy sidecar: nothing to diff against
	contentCheckExportFailed                            // export or parse could not complete
)

// serverContentChangedSince decides whether the live server scheme differs
// from the ancestor recorded at pull time. It's the supplementary check for
// the equal-timestamp case where serverMovedSince cannot decide — list-sourced
// baselines (pull-folder) and legacy sidecars written before the Source tag
// existed both skip the version tiebreak because their Version field isn't
// comparable to the current detail response.
func serverContentChangedSince(v *Executor, dir string, procID int) contentCheckResult {
	ancestorConv, hasAnc := readAncestorScheme(dir, procID)
	if !hasAnc {
		return contentCheckNoAncestor
	}
	theirsConv, hasT := exportConv(v)
	if !hasT {
		return contentCheckExportFailed
	}
	baseNodes := localSchemeNodes(ancestorConv)
	theirsNodes := localSchemeNodes(theirsConv)
	// mine==base isolates server-only changes: any node different from the
	// ancestor becomes a theirs-side class; everything else clsUnchanged.
	plan := buildMergePlan(baseNodes, theirsNodes, baseNodes)
	if err := addProcessFields(&plan, ancestorConv, theirsConv, ancestorConv); err != nil {
		logger.Warn("equal-timestamp content check: process-field diff failed for #%d: %v", procID, err)
		return contentCheckExportFailed
	}
	hasAmbiguous := false
	for _, n := range plan.Nodes {
		// Ambiguous keys (duplicate non-empty titles) cannot be safely classified
		// per-key: canonicalizeNodes keeps only the first occurrence's body, so a
		// server change to a later duplicate can hide as clsUnchanged. Skip the
		// class check for these and rely on the whole-scheme multiset compare
		// below — that catches any change without false-positiving in-sync
		// schemes that legitimately contain duplicate titles.
		if n.Ambiguous {
			hasAmbiguous = true
			continue
		}
		switch n.Class {
		// clsConflict is possible in the mine==base call only via the duplicate-
		// title path in classify() — include it so a server edit that shifts a
		// duplicate's first occurrence is not silently ignored.
		case clsTheirs, clsAddedTheirs, clsDeletedTheirs, clsDeleteEditConflict, clsConflict:
			return contentCheckChanged
		}
	}
	if len(plan.FieldGrafts) > 0 || len(plan.FieldConflicts) > 0 {
		return contentCheckChanged
	}
	if hasAmbiguous && !schemeNodesEqual(baseNodes, theirsNodes) {
		return contentCheckChanged
	}
	return contentCheckUnchanged
}

// schemeNodesEqual compares two node lists as multisets of canonical bodies.
// It exists so serverContentChangedSince can rule out false positives when the
// scheme has duplicate titles: canonicalizeNodes deduplicates by key and hides
// changes to later duplicates, but a sorted multiset of every node's canonical
// body sees every occurrence and stays stable under id regeneration.
func schemeNodesEqual(a, b []map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	ab := schemeCanonBodies(a)
	bb := schemeCanonBodies(b)
	for i := range ab {
		if ab[i] != bb[i] {
			return false
		}
	}
	return true
}

// schemeCanonBodies returns every node's canonical body, sorted, so two scheme
// snapshots with the same semantic content produce identical slices regardless
// of node order or id regeneration.
func schemeCanonBodies(nodes []map[string]any) []string {
	keys := matchKeys(nodes)
	idToKey := map[string]string{}
	for i, n := range nodes {
		if id, _ := n["id"].(string); id != "" {
			idToKey[id] = keys[i]
		}
	}
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, canonNodeBody(n, idToKey))
	}
	sort.Strings(out)
	return out
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
	if err := addProcessFields(&plan, ancestorConv, tConv, localJSON); err != nil {
		logger.Warn("conflict merge: process-level comparison failed: %v", err)
		return mergePlan{}, nil, "", false
	}
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
func applyMerge(dir, filePath string, procID int, localJSON string, current baselineEntry, theirsConv string, plan mergePlan, theirsNodes []map[string]any, editorName string, editorTime int64) conflictResult {
	merged, err := materializeMerge(localJSON, plan, theirsNodes)
	if err != nil {
		return conflictResult{conflictBlock, fmt.Sprintf("Merge could not be built: %v — re-pull and re-apply your edits instead.", err)}
	}
	mode := os.FileMode(0644)
	if info, statErr := os.Stat(filePath); statErr == nil {
		mode = info.Mode().Perm()
	}
	backupPath := filePath + ".pre-merge"
	if err := writeFileAtomically(backupPath, []byte(localJSON), mode); err != nil {
		return conflictResult{conflictBlock, fmt.Sprintf("Merge built but the local backup could not be written to %s: %v. The original file was not changed.", backupPath, err)}
	}
	if err := writeFileAtomically(filePath, []byte(merged), mode); err != nil {
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
	fmt.Fprintf(&sb, "Original local file saved to %s.\n\n", backupPath)
	sb.WriteString(formatMergePlan(plan))
	if mergeConflictCount(plan) == 0 {
		// Both writes must succeed to declare the merge fully reconciled: baseline
		// advance is what makes the next push proceed cleanly, and the ancestor
		// advance is what a later 3-way merge relies on. If either fails we still
		// leave the materialised merge on disk (the user's work is preserved) but
		// tell them the state is not clean and the next push will re-report the
		// conflict — better than the earlier silent "proceed cleanly" claim (#151).
		baselineErr := writeBaseline(dir, procID, current)
		if baselineErr != nil {
			logger.Warn("merge: baseline advance failed for %d: %v", procID, baselineErr)
		}
		ancestorErr := writeAncestorScheme(dir, procID, theirsConv)
		if ancestorErr != nil {
			logger.Warn("merge: ancestor advance failed for %d: %v", procID, ancestorErr)
		}
		switch {
		case baselineErr != nil && ancestorErr != nil:
			fmt.Fprintf(&sb, "\nMerged into %s — no conflicts, BUT baseline and ancestor could not be updated (%v; %v). Your merged file is safe, but the next push will re-report this conflict until the sidecar can be written. Fix the local filesystem/permissions and re-run.\n",
				filePath, baselineErr, ancestorErr)
		case baselineErr != nil:
			fmt.Fprintf(&sb, "\nMerged into %s — no conflicts, BUT the baseline sidecar could not be updated (%v). The next push will re-report this conflict; force=true would still overwrite the server. Fix the sidecar (permissions/disk) and re-run, or accept that a repeat push needs force=true.\n",
				filePath, baselineErr)
		case ancestorErr != nil:
			fmt.Fprintf(&sb, "\nMerged into %s — no conflicts, BUT the merge ancestor could not be refreshed (%v). Push will proceed cleanly, but a future 3-way merge will fall back to the delete-only impact report until the ancestor file is writable again.\n",
				filePath, ancestorErr)
		default:
			fmt.Fprintf(&sb, "\nMerged into %s — no conflicts. Review it, then push again; it will proceed cleanly.\n", filePath)
		}
	} else {
		fmt.Fprintf(&sb, "\nGrafted the non-conflicting changes into %s. The %d overlapping node/field value(s) above were kept as YOUR version.\nReview them, then push with force=true to deploy (a snapshot of the server version is attempted first; recoverable only if it succeeds — check the push result).\n",
			filePath, mergeConflictCount(plan))
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
		if mergeConflictCount(plan) == 0 {
			sb.WriteString("  [1] merge=true   COMBINE both — recommended (nothing overlaps)\n")
			sb.WriteString("        keeps ALL your edits AND adds ALL the server's changes above — nothing is lost.\n")
			sb.WriteString("        Writes the merged file for you to review, then push again → deploys cleanly.\n\n")
		} else {
			sb.WriteString("  [1] merge=true   COMBINE what doesn't overlap\n")
			sb.WriteString("        keeps your edits AND adds the server's NON-overlapping changes above.\n")
			sb.WriteString("        The overlapping node/field value(s) are KEPT AS YOURS — resolve those by hand, then push with force=true.\n\n")
		}
		sb.WriteString("  [2] re-pull      THEIRS WINS — take the server version\n")
		sb.WriteString("        overwrites your local file with the server's; YOUR local edits are DISCARDED and\n")
		sb.WriteString("        you re-apply them by hand. Use when the overlap is too tangled to merge.\n\n")
		sb.WriteString("  [3] force=true   YOURS WINS — deploy your file as-is\n")
		sb.WriteString("        the live process becomes EXACTLY your version; the server's changes above are DROPPED.\n")
		sb.WriteString("        A snapshot of the server version is attempted first — if it succeeds (see the push result)\n")
		sb.WriteString("        their version stays recoverable; if the snapshot fails, the drop is permanent.\n")
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
	sb.WriteString("  [2] force=true   YOURS WINS — deploy your file as-is; the server's changes are DROPPED (a snapshot is attempted first; recoverable only if it succeeds)\n")
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
