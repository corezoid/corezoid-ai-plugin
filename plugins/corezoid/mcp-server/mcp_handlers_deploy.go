package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// handleDeployStage promotes one stage's scheme onto another (e.g. develop →
// production) using Corezoid's obj_scheme compare/merge admin operations.
//
// The Deploy button in the Corezoid admin UI issues two calls:
//   POST /api/2/compare  {type:"compare", obj:"obj_scheme", obj_id:<target>,
//                          obj_to_id:<source>, diff_status:true, ...}
//   POST /api/2/merge    {type:"merge",   obj:"obj_scheme", obj_id:<target>,
//                          obj_to_id:<source>, apply_mode:true, ...}
// obj_id is the TARGET stage being changed (e.g. production); obj_to_id is the
// SOURCE stage the changes come from (e.g. develop). These are NOT on the
// /api/2/json endpoint, which is why the other tools never saw them.
//
// This handler always runs compare first and prints the diff. With apply=false
// (default) it stops there — a safe dry-run preview of what would deploy,
// including any conflicts. With apply=true it then runs merge to deploy.
func handleDeployStage(ctx context.Context, args map[string]interface{}) (string, bool) {
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	sourceStage, err := intArg(args, "source_stage_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	targetStage, err := intArg(args, "target_stage_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	apply := false
	if b, ok := args["apply"].(bool); ok {
		apply = b
	}
	confirm, _ := args["confirm"].(string)

	if sourceStage == targetStage {
		return "Error: source_stage_id and target_stage_id must differ.", true
	}

	v := NewValidator(ctx, 0)

	// Corezoid only merges INTO an immutable (read-only) stage. Check up front so
	// a deploy is never even attempted against a mutable target (it would fail
	// with "You have to merge only in immutable stage"). We do NOT auto-fix this
	// — flipping a stage to read-only is its own confirmed action (set-stage-immutable).
	immut, _, tTitle, _, ierr := v.stageInfo(targetStage, projectID)
	if ierr != nil {
		return "Error (checking target stage): " + ierr.Error(), true
	}
	if !immut {
		return fmt.Sprintf("⛔ Target stage %d (%q) is NOT immutable. Corezoid only deploys/merges INTO an immutable (read-only) stage — a mutable stage cannot be a deploy target.\nMake it read-only first with set-stage-immutable (which also requires confirmation), or choose an immutable target.", targetStage, tTitle), true
	}

	// Corezoid also only merges FROM a deployed stage. A source with undeployed
	// changes fails the merge with the opaque "You have to merge only from
	// deployed stage" — so check up front and explain what to do instead.
	_, srcUndeployed, sTitle, _, serr := v.stageInfo(sourceStage, projectID)
	if serr != nil {
		return "Error (checking source stage): " + serr.Error(), true
	}
	if srcUndeployed > 0 {
		return fmt.Sprintf("⛔ Source stage %d (%q) has %d undeployed change(s). Corezoid only merges FROM a deployed stage — publish the source first (the \"Deploy\" action on that stage in the Corezoid UI), then retry the deploy.", sourceStage, sTitle, srcUndeployed), true
	}

	// 1) compare: what differs between target and source.
	cmpOps := []map[string]any{{
		"type":        "compare",
		"obj":         "obj_scheme",
		"obj_id":      targetStage,
		"obj_type":    "stage",
		"obj_to_id":   sourceStage,
		"obj_to_type": "stage",
		"diff_status": true,
		"project_id":  projectID,
		"company_id":  companyID,
	}}
	cmpResp, err := v.req("compare", cmpOps)
	if err != nil {
		// A failed compare (e.g. "One or more processes has errors") still returns
		// a response whose op carries a nested `errors` tree naming the exact
		// stage → process → node and the reason. Surface it — the bare description
		// alone is undiagnosable through this tool.
		msg := fmt.Sprintf("Error (compare): %v", err)
		if tree := compareErrorsFromResp(cmpResp); tree != "" {
			msg += "\n" + tree
		}
		return msg, true
	}
	list, derr := deployDiffList(cmpResp)
	if derr != "" {
		return "Error (compare): " + derr, true
	}

	summary, conflicts, removed := formatDeployDiff(list, sourceStage, targetStage)
	if len(list) == 0 {
		return fmt.Sprintf("Nothing to deploy — stage %d is already in sync with stage %d.", targetStage, sourceStage), false
	}

	// Defensive: Corezoid's stage merge is a one-way overwrite (source wins) and
	// its compare only reports added/changed/deleted (or "" for in-sync) — there
	// is NO conflict status today. But if a status outside that set ever appears,
	// refuse and send the user to the UI rather than silently overwriting — and
	// name each object and its status so the refusal is diagnosable.
	if len(conflicts) > 0 {
		return fmt.Sprintf("⛔ Deploy NOT executed — %d object(s) returned a status this tool doesn't recognize and won't auto-merge:\n  %s\nResolve them in the Corezoid UI (or report this status so the tool can learn it), then retry.\n\n%s",
			len(conflicts), strings.Join(conflicts, "\n  "), summary), true
	}

	// This merge OVERWRITES the target with the source's version for every listed
	// object (source wins) — Corezoid offers no conflict resolution, so this diff
	// IS the safety surface the user must review.
	overwriteNote := fmt.Sprintf("\n\n⚠ This OVERWRITES stage %d (target) with stage %d (source) for every listed object — source wins.", targetStage, sourceStage)
	if removed > 0 {
		overwriteNote += fmt.Sprintf("\n⚠ %d object(s) exist only in the target and will be DELETED from it.", removed)
	}
	// The exact confirmation phrase for THIS source→target. Encoding both stage
	// ids means a confirm can never be reused for a different (wrong) deploy.
	wantConfirm := fmt.Sprintf("%d->%d", sourceStage, targetStage)

	// apply=false is a safe dry-run: show the diff, deploy nothing.
	if !apply {
		return fmt.Sprintf("%s%s\n\nDRY-RUN — nothing deployed. To deploy: get the user's explicit go-ahead, then re-run with apply=true and confirm=%q.",
			summary, overwriteNote, wantConfirm), false
	}

	// SAFETY GATE — a cross-stage merge is destructive. Never fire without an
	// explicit confirmation matching this exact source→target, so it cannot happen
	// by accident or on the wrong stages. The user must confirm every deploy.
	if strings.TrimSpace(confirm) != wantConfirm {
		return fmt.Sprintf("⛔ Deploy NOT executed — confirmation required.\n\n%s%s\n\nConfirm with the USER first, then re-run with apply=true and confirm=%q.",
			summary, overwriteNote, wantConfirm), true
	}

	// 2) merge: apply source → target (the actual deploy).
	mrgOps := []map[string]any{{
		"type":        "merge",
		"obj":         "obj_scheme",
		"obj_id":      targetStage,
		"obj_type":    "stage",
		"obj_to_id":   sourceStage,
		"obj_to_type": "stage",
		"apply_mode":  true,
		"company_id":  companyID,
	}}
	mrgResp, err := v.req("merge", mrgOps)
	if err != nil {
		return fmt.Sprintf("Error (merge/deploy): %v", err), true
	}
	if perr := deployOpProc(mrgResp); perr != "" {
		return "Error (merge/deploy): " + perr, true
	}

	// /api/2/merge is async: it returns a hash we watch on the progress WebSocket
	// until the merge finishes (via the deployMonitor seam / git_call WS machinery).
	// The collected progress log is surfaced so the user sees the phases reported.
	waitNote, progressLog := "", ""
	if hash := deployMergeHash(mrgResp); hash != "" {
		log, werr := deployMonitor(v, hash)
		progressLog = log
		if werr != nil {
			// The socket is a progress channel, not the source of truth — small
			// merges routinely finish and close it before the monitor subscribes.
			// The scheme itself decides: re-run compare and let the diff answer.
			// But keep the report honest about WHAT the socket said: a
			// close-before-done is routine; a server-reported failure or a
			// timeout is not, and its reason must never be swallowed.
			verified, vmsg := deployVerifyByCompare(v, cmpOps)
			if !verified {
				out := fmt.Sprintf("⚠ Deploy result UNCONFIRMED for stage %d → stage %d.\nWebSocket: %v\nCompare after merge: %s\n(The merge may still be running server-side — re-run with apply=false; an empty diff means it completed.)",
					sourceStage, targetStage, werr, vmsg)
				if progressLog != "" {
					out += "\n\nProgress:\n" + progressLog
				}
				return out, true
			}
			if strings.Contains(werr.Error(), "closed before done") {
				waitNote = "\nℹ The progress WebSocket closed before confirming (typical for small, fast merges) — completion verified by compare instead: the target is in sync with the source."
			} else {
				waitNote = fmt.Sprintf("\n⚠ The progress WebSocket reported: %v\nHowever, compare verifies the target IS in sync with the source — the deploy landed. If the socket reported a server-side error, review it: the sync may reflect a partial state the compare cannot see.", werr)
			}
		}
	}
	out := fmt.Sprintf("✅ Deployed stage %d → stage %d.%s\n\n%s", sourceStage, targetStage, waitNote, summary)
	if progressLog != "" {
		out += "\n\nProgress:\n" + progressLog
	}
	return out, false
}

// Verification retry tuning: a merge whose WS died early may still be copying
// server-side, so the first compare can legitimately show a leftover diff.
// Package vars so tests can dial the waits down.
var (
	deployVerifyAttempts = 3
	deployVerifyDelayVar = 2 * time.Second
)

// deployVerifyByCompare answers "did the merge actually land?" when the
// progress WebSocket could not: it re-runs the same compare and calls the
// deploy complete once the diff is empty (every listed object in sync). It
// retries a few times because the async merge may still be finishing.
// Returns verified=false with a short human explanation otherwise.
func deployVerifyByCompare(v *Executor, cmpOps []map[string]any) (bool, string) {
	msg := ""
	for attempt := 1; attempt <= deployVerifyAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-v.Ctx.Done():
				return false, "verification cancelled"
			case <-time.After(deployVerifyDelayVar):
			}
		}
		resp, err := v.req("compare", cmpOps)
		if err != nil {
			msg = fmt.Sprintf("compare failed: %v", err)
			continue
		}
		list, derr := deployDiffList(resp)
		if derr != "" {
			msg = "compare failed: " + derr
			continue
		}
		// A verified success needs the op to actually carry a `list` — a
		// malformed proc:"ok" response without one decodes to an empty slice
		// and must NOT count as "in sync" (the only silent-false-success hole).
		if !compareHasList(resp) {
			msg = "compare response carried no object list"
			continue
		}
		leftover := 0
		for _, m := range list {
			if s, _ := m["__status"].(string); s != "" {
				leftover++
			}
		}
		if leftover == 0 {
			return true, ""
		}
		msg = fmt.Sprintf("%d object(s) still differ", leftover)
	}
	return false, msg
}

// handleSetStageImmutable flips a stage's immutable (read-only) flag. Immutable
// stages are the only valid deploy/merge targets — but making a stage read-only
// (or editable again) is consequential, so it is gated behind an explicit
// per-stage confirmation just like deploy-stage.
func handleSetStageImmutable(ctx context.Context, args map[string]interface{}) (string, bool) {
	stageID, err := intArg(args, "stage_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	projectID, err := intArg(args, "project_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	companyID, err := strArg(args, "company_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	_ = companyID // company scoping comes from the executor's WorkspaceID
	immutable, ok := args["immutable"].(bool)
	if !ok {
		return "Error: 'immutable' (boolean) is required — true = read-only, false = editable.", true
	}
	confirm, _ := args["confirm"].(string)

	v := NewValidator(ctx, 0)
	cur, _, title, shortName, ierr := v.stageInfo(stageID, projectID)
	if ierr != nil {
		return "Error (show stage): " + ierr.Error(), true
	}
	if cur == immutable {
		return fmt.Sprintf("Stage %d (%q) is already %s — nothing to change.", stageID, title, immutableWord(immutable)), false
	}

	// SAFETY GATE — an immutable stage can no longer be edited directly (only via
	// deploy/merge); making one editable removes that protection. Require an
	// explicit confirm matching this exact stage + target state, and the user
	// must confirm.
	want := fmt.Sprintf("%d:%v", stageID, immutable)
	if strings.TrimSpace(confirm) != want {
		var risk string
		if immutable {
			risk = "\n\n(Making a stage immutable makes it read-only: it can then be changed ONLY via deploy/merge, not edited directly.)"
		} else {
			// Removing read-only is the dangerous direction — spell out the risks
			// so the user makes an informed decision.
			risk = "\n\n⚠ RISKS of making this stage EDITABLE (removing read-only) — the user must understand and accept ALL of these:\n" +
				"  • Deploy/merge INTO this stage becomes UNAVAILABLE (Corezoid only merges into immutable stages).\n" +
				"  • ANYONE with access to this stage — not only you — will be able to edit its processes directly.\n" +
				"  • This removes the read-only protection; for a production / deploy-target stage this is NOT safe."
		}
		return fmt.Sprintf("⛔ Not executed — confirmation required.\n\nThis will change stage %d (%q) to %s.%s\n\nConfirm with the USER (who must understand the above), then re-run with confirm=%q.",
			stageID, title, immutableWord(immutable), risk, want), true
	}

	if err := v.setStageImmutable(stageID, projectID, title, shortName, immutable); err != nil {
		return "Error: " + err.Error(), true
	}
	return fmt.Sprintf("✅ Stage %d (%q) is now %s.", stageID, title, immutableWord(immutable)), false
}

func immutableWord(immutable bool) string {
	if immutable {
		return "immutable (read-only)"
	}
	return "mutable (editable)"
}

// deployDiffList extracts the compare op's list of differing objects, returning
// a human error string if the op did not succeed. Compare failures (e.g. "One
// or more processes has errors") carry a nested `errors` tree that pinpoints
// the exact stage → process → node and the reason (empty scheme, orphan node,
// a reference into another project, …) — render it instead of swallowing it,
// otherwise the failure is undiagnosable through this tool.
func deployDiffList(resp map[string]interface{}) ([]map[string]interface{}, string) {
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return nil, "no ops in response"
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		if desc == "" {
			desc = "compare op did not succeed"
		}
		if tree := formatCompareErrors(opMap["errors"], 1); tree != "" {
			desc += "\n" + tree
		}
		return nil, desc
	}
	rawList, _ := opMap["list"].([]interface{})
	out := make([]map[string]interface{}, 0, len(rawList))
	for _, it := range rawList {
		if m, ok := it.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out, ""
}

// compareHasList reports whether the first op of a compare response actually
// carries a `list` key. An empty list is a legitimate "in sync" answer; a
// MISSING list is a malformed response and must not be read as one.
func compareHasList(resp map[string]interface{}) bool {
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return false
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	_, ok := opMap["list"].([]interface{})
	return ok
}

// compareErrorsFromResp pulls the first op's `errors` tree out of a (possibly
// failed) compare response and renders it. Returns "" when there is no tree.
func compareErrorsFromResp(resp map[string]interface{}) string {
	if resp == nil {
		return ""
	}
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return ""
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	return formatCompareErrors(opMap["errors"], 1)
}

// formatCompareErrors renders the nested `errors`/`destinations` tree a failed
// compare op returns. Each level is a slice of objects with obj/obj_id/title
// plus either child `destinations` or leaf `errors` (a list of message
// strings). Example rendering:
//
//	stage #685226 "dev"
//	  conv #1882660 "repro-external-ref"
//	    node #6a5114a6… "Copy to external"
//	      • Project of object do not matches projectId of request
//
// Unknown shapes are skipped rather than failing the whole message.
func formatCompareErrors(errs interface{}, depth int) string {
	arr, _ := errs.([]interface{})
	if len(arr) == 0 || depth > 6 { // depth cap guards against a cyclic/degenerate tree
		return ""
	}
	indent := strings.Repeat("  ", depth)
	var sb strings.Builder
	for _, e := range arr {
		switch node := e.(type) {
		case string: // leaf error message
			sb.WriteString(indent + "• " + node + "\n")
		case map[string]interface{}:
			obj, _ := node["obj"].(string)
			title, _ := node["title"].(string)
			id := ""
			switch v := node["obj_id"].(type) {
			case float64:
				id = fmt.Sprintf("#%d", int(v))
			case string:
				id = "#" + v
			}
			if obj != "" || title != "" || id != "" {
				sb.WriteString(fmt.Sprintf("%s%s %s %q\n", indent, obj, id, title))
			}
			if child := formatCompareErrors(node["errors"], depth+1); child != "" {
				sb.WriteString(child + "\n")
			}
			if child := formatCompareErrors(node["destinations"], depth+1); child != "" {
				sb.WriteString(child + "\n")
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// deployOpProc returns "" if the first op succeeded, else a human error string.
func deployOpProc(resp map[string]interface{}) string {
	opsArr, _ := resp["ops"].([]interface{})
	if len(opsArr) == 0 {
		return "no ops in response"
	}
	opMap, _ := opsArr[0].(map[string]interface{})
	if proc, _ := opMap["proc"].(string); proc != "ok" {
		desc, _ := opMap["description"].(string)
		if desc == "" {
			desc = "merge op did not succeed"
		}
		return desc
	}
	return ""
}

// formatDeployDiff renders the diff as a grouped, readable summary. It returns
// the list of objects whose status the tool does not recognize (one
// human-readable line per object, empty when all statuses are known) and the
// count of objects deleted in the source — a deploy DELETES those from the
// target, exactly like the UI merge does.
//
// Observed /api/2/compare status vocabulary (diff_status:true):
//   - "added"   — object exists only in the source
//   - "changed" — object differs between the stages (incl. renames)
//   - "deleted" — object was deleted in the source but still exists in the
//     target; merge propagates the deletion. NOT a conflict.
//   - ""        — object is in sync (compare may still list it after a merge)
//
// "removed" has never been observed in the API; it is kept only for backward
// compatibility in case some Corezoid version emits it for the deleted case.
func formatDeployDiff(list []map[string]interface{}, source, target int) (summary string, conflicts []string, removed int) {
	type item struct{ title, objType, status string }
	items := make([]item, 0, len(list))
	counts := map[string]int{}
	for _, m := range list {
		title, _ := m["title"].(string)
		objType, _ := m["obj_type"].(string)
		status, _ := m["__status"].(string)
		if status == "" {
			status = "unchanged"
		}
		items = append(items, item{title, objType, status})
		counts[status]++
		switch status {
		case "added", "changed", "unchanged":
		case "removed", "deleted":
			removed++
		default:
			objID, _ := m["obj_id"].(float64)
			conflicts = append(conflicts, fmt.Sprintf("status %q: %s #%d %q", status, objType, int(objID), title))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].status != items[j].status {
			return items[i].status < items[j].status
		}
		return items[i].title < items[j].title
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Diff: stage %d (source) → stage %d (target) — %d object(s):\n", source, target, len(list)))
	order := make([]string, 0, len(counts))
	for k := range counts {
		order = append(order, k)
	}
	sort.Strings(order)
	for _, k := range order {
		sb.WriteString(fmt.Sprintf("  %s: %d\n", k, counts[k]))
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  %-10s  %-8s  %s\n", "STATUS", "TYPE", "TITLE"))
	sb.WriteString("  " + strings.Repeat("-", 48) + "\n")
	for _, it := range items {
		if it.status == "unchanged" {
			continue
		}
		sb.WriteString(fmt.Sprintf("  %-10s  %-8s  %s\n", it.status, it.objType, it.title))
	}
	return strings.TrimRight(sb.String(), "\n"), conflicts, removed
}
