package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
		return fmt.Sprintf("Error (compare): %v", err), true
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
	// its compare only reports add/change/remove — there is NO conflict status
	// today. But if a genuine conflict status ever appears, refuse and send the
	// user to the UI rather than silently overwriting.
	if conflicts > 0 {
		return fmt.Sprintf("⛔ Deploy NOT executed — %d object(s) returned an unexpected/conflicting status that this tool won't auto-merge. Resolve them in the Corezoid UI, then retry.\n\n%s", conflicts, summary), true
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
			waitNote = fmt.Sprintf("\n\n⚠ Merge started but completion could not be confirmed over the WebSocket: %v\n(It may still finish server-side — re-run with apply=false to check whether the diff is gone.)", werr)
		}
	}
	out := fmt.Sprintf("✅ Deployed stage %d → stage %d.%s\n\n%s", sourceStage, targetStage, waitNote, summary)
	if progressLog != "" {
		out += "\n\nProgress:\n" + progressLog
	}
	return out, false
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
// a human error string if the op did not succeed.
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

// formatDeployDiff renders the diff as a grouped, readable summary and returns
// the count of conflicting objects (any status that is not a plain
// add/change/remove/unchanged) and the count of "removed" objects (present only
// in the target — a deploy DELETES them from it).
func formatDeployDiff(list []map[string]interface{}, source, target int) (summary string, conflicts, removed int) {
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
		case "removed":
			removed++
		default:
			conflicts++
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
