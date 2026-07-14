package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// This file implements the env-var lifecycle tools: list-variables (read-only),
// modify-variable and delete-variable (both confirm-gated).
//
// Server semantics, verified live against admin.corezoid.com (2026-07-11):
//   - list returns full details; a secret's value comes back as null — the
//     plaintext is NEVER retrievable, only MD5/SHA256 fingerprints.
//   - modify is PARTIAL: omitted keys keep their value (a secret survives a
//     title-only modify — fingerprints unchanged). short_name is required in
//     every modify. env_var_type changes are silently IGNORED by the server
//     in both directions, so the tool does not offer them.
//   - renaming onto an existing short_name fails with "env_var name already
//     exists" (we pre-check anyway for a friendlier message).
//   - delete requires obj_id + company_id + project_id + stage_id and is
//     PERMANENT: there is no recycle bin for env vars.

// resolveEnvVarTarget resolves the stage (arg or COREZOID_STAGE_ID) and the
// target variable by name and/or obj_id against the live server list. It
// returns the full stage listing too, so callers can reuse it (rename
// collision check) without a second API call.
func resolveEnvVarTarget(v *Executor, args map[string]interface{}) (stage int, target EnvVar, all []EnvVar, errMsg string) {
	stage = v.StageID
	if _, ok := args["stage_id"]; ok {
		s, err := intArg(args, "stage_id")
		if err != nil {
			return 0, EnvVar{}, nil, "Error: " + err.Error()
		}
		stage = s
	}
	if stage == 0 {
		return 0, EnvVar{}, nil, "Error: stage_id not provided and COREZOID_STAGE_ID environment variable is not set or invalid"
	}
	name, err := strArg(args, "name")
	if err != nil {
		return 0, EnvVar{}, nil, "Error: " + err.Error()
	}
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")

	all, lerr := v.ListEnvVars(stage)
	if lerr != nil {
		return 0, EnvVar{}, nil, fmt.Sprintf("Error listing variables in stage %d: %v", stage, lerr)
	}

	objID := 0
	if _, ok := args["obj_id"]; ok {
		if objID, err = intArg(args, "obj_id"); err != nil {
			return 0, EnvVar{}, nil, "Error: " + err.Error()
		}
	}
	for _, ev := range all {
		if (objID != 0 && ev.ObjID == objID) || (objID == 0 && ev.ShortName == name) {
			if objID != 0 && ev.ShortName != name {
				return 0, EnvVar{}, nil, fmt.Sprintf("Error: obj_id %d is variable '@%s', not '@%s' — name and obj_id disagree. Re-run list-variables and retry with matching values.", objID, ev.ShortName, name)
			}
			return stage, ev, all, ""
		}
	}
	if objID != 0 {
		return 0, EnvVar{}, nil, fmt.Sprintf("Error: no env variable with obj_id %d in stage %d ('@%s' was given as name — run list-variables to see current ids).", objID, stage, name)
	}
	return 0, EnvVar{}, nil, envVarNotFoundMsg(name, stage, all)
}

// envVarNotFoundMsg builds a not-found error that names near matches first
// and then the available variables (capped), so a typo is a one-step fix.
func envVarNotFoundMsg(name string, stage int, all []EnvVar) string {
	var near, rest []string
	lower := strings.ToLower(name)
	for _, ev := range all {
		sn := strings.ToLower(ev.ShortName)
		if sn != "" && (strings.Contains(sn, lower) || strings.Contains(lower, sn)) {
			near = append(near, "@"+ev.ShortName)
		} else {
			rest = append(rest, "@"+ev.ShortName)
		}
	}
	sort.Strings(near)
	sort.Strings(rest)
	msg := fmt.Sprintf("Error: env variable '@%s' not found in stage %d.", name, stage)
	if len(near) > 0 {
		msg += " Did you mean: " + strings.Join(near, ", ") + "?"
	}
	names := append(near, rest...)
	if len(names) == 0 {
		return msg + " The stage has no variables — run list-variables to confirm."
	}
	if len(names) > 20 {
		names = append(names[:20], "…")
	}
	return msg + " Variables in this stage: " + strings.Join(names, ", ") + " (run list-variables for details)."
}

// maskedEnvVarValue is the ONLY way a variable's value may be rendered.
// Secrets are always masked — the server does not return their plaintext,
// and no code path should ever echo whatever it does return.
func maskedEnvVarValue(ev EnvVar) string {
	// Fail safe: anything that is not explicitly "visible" is masked, so an
	// unknown or empty type from the server can never echo a value.
	if ev.EnvVarType != "visible" {
		return "•••••• (secret — value is not retrievable, only fingerprints)"
	}
	if ev.Value == "" {
		return "(empty)"
	}
	return ev.Value
}

// boolishArg reads a boolean argument, tolerating the CLI's string form
// ("apply=true") — the same failure mode deploy-stage had, where a silently
// unread boolean turned an apply into a dry-run.
func boolishArg(args map[string]interface{}, key string) bool {
	if b, ok := args[key].(bool); ok {
		return b
	}
	if s, ok := args[key].(string); ok {
		return strings.EqualFold(s, "true") || s == "1"
	}
	return false
}

func fmtUnix(t int64) string {
	if t == 0 {
		return "-"
	}
	return time.Unix(t, 0).UTC().Format("2006-01-02 15:04")
}

// truncateValue keeps table cells readable (rune-safe).
func truncateValue(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// rePluginProcFile matches the <id>_<name>.json skeletons this plugin's own
// create-process/create-state-diagram write (they lack the .conv suffix).
var rePluginProcFile = regexp.MustCompile(`^\d+_[^/]*\.json$`)

// scanEnvVarRefs walks the working tree looking for local .conv.json files
// that reference the variable. It matches the raw token "env_var[@name]"
// rather than the full {{…}} form so partially-templated usages are caught
// too. Hidden directories (.git, .processes, …) are skipped and read errors
// tolerated, matching pull-folder's behavior. scanned==0 means NOTHING could
// be verified — callers must say so, never imply "no references".
func scanEnvVarRefs(name string) (matches []string, scanned int) {
	needle := []byte("env_var[@" + name + "]")
	_ = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		base := filepath.Base(p)
		if !strings.HasSuffix(base, ".conv.json") && !rePluginProcFile.MatchString(base) {
			return nil
		}
		scanned++
		if data, rerr := os.ReadFile(p); rerr == nil && bytes.Contains(data, needle) {
			matches = append(matches, p)
		}
		return nil
	})
	sort.Strings(matches)
	return matches, scanned
}

// renderRefScan formats a reference-scan result block shared by the delete
// preview and the rename warning.
func renderRefScan(name string, matches []string, scanned int, consequence string) string {
	switch {
	case scanned == 0:
		return fmt.Sprintf("  Reference scan: NO local .conv.json files found — references CANNOT be verified.\n"+
			"  Server-side processes may still use {{env_var[@%s]}}. Run pull-folder first to check locally.", name)
	case len(matches) == 0:
		return fmt.Sprintf("  Reference scan: no references to {{env_var[@%s]}} in %d local .conv.json file(s).\n"+
			"  (Local files only — a stale or partial checkout does not cover everything on the server.)", name, scanned)
	default:
		return fmt.Sprintf("  ⚠ Reference scan: %d of %d local .conv.json file(s) STILL REFERENCE {{env_var[@%s]}} — %s:\n    - %s",
			len(matches), scanned, name, consequence, strings.Join(matches, "\n    - "))
	}
}

// ---- list-variables ---------------------------------------------------------

func handleListVariables(ctx context.Context, args map[string]interface{}) (string, bool) {
	v := NewValidator(ctx, 0)
	stage := v.StageID
	if _, ok := args["stage_id"]; ok {
		s, err := intArg(args, "stage_id")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		stage = s
	}
	if stage == 0 {
		return "Error: stage_id not provided and COREZOID_STAGE_ID environment variable is not set or invalid", true
	}
	vars, err := v.ListEnvVars(stage)
	if err != nil {
		return fmt.Sprintf("Error listing variables: %v", err), true
	}
	if len(vars) == 0 {
		return fmt.Sprintf("No environment variables in stage %d.", stage), false
	}
	sort.Slice(vars, func(i, j int) bool { return vars[i].ShortName < vars[j].ShortName })

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Environment variables in stage %d (%d):\n\n", stage, len(vars)))
	sb.WriteString(fmt.Sprintf("  %-28s %-8s %-5s %-10s %-24s %s\n", "SHORT_NAME", "OBJ_ID", "TYPE", "VISIBILITY", "TITLE", "VALUE"))
	sb.WriteString("  " + strings.Repeat("-", 100) + "\n")
	for _, ev := range vars {
		sb.WriteString(fmt.Sprintf("  %-28s %-8d %-5s %-10s %-24s %s\n",
			"@"+ev.ShortName, ev.ObjID, ev.DataType, ev.EnvVarType,
			truncateValue(ev.Title, 24), truncateValue(maskedEnvVarValue(ev), 60)))
	}
	sb.WriteString("\nReference in processes as {{env_var[@name]}}. Use obj_id with modify-variable / delete-variable.")
	return sb.String(), false
}

// ---- modify-variable --------------------------------------------------------

func handleModifyVariable(ctx context.Context, args map[string]interface{}) (string, bool) {
	v := NewValidator(ctx, 0)
	stage, cur, all, errMsg := resolveEnvVarTarget(v, args)
	if errMsg != "" {
		return errMsg, true
	}

	ch := EnvVarChanges{}
	summaryChanges := []string{}
	if raw, ok := args["new_name"]; ok {
		s, _ := raw.(string)
		s = strings.TrimPrefix(strings.TrimSpace(s), "@")
		if s != "" && s != cur.ShortName {
			for _, ev := range all {
				if ev.ShortName == s && ev.ObjID != cur.ObjID {
					return fmt.Sprintf("Error: a variable named '@%s' already exists in stage %d (obj_id %d) — the server refuses duplicate names.", s, stage, ev.ObjID), true
				}
			}
			ch.ShortName = &s
			summaryChanges = append(summaryChanges, fmt.Sprintf("  short_name:  @%s → @%s", cur.ShortName, s))
		}
	}
	if raw, ok := args["description"]; ok {
		s, _ := raw.(string)
		if s != "" && s != cur.Title {
			ch.Title = &s
			summaryChanges = append(summaryChanges, fmt.Sprintf("  title:       %q → %q", cur.Title, s))
		}
	}
	if raw, ok := args["data_type"]; ok {
		s, _ := raw.(string)
		if s != "" {
			if s != "raw" && s != "json" {
				return fmt.Sprintf("Error: data_type must be \"raw\" or \"json\", got %q.", s), true
			}
			if s != cur.DataType {
				ch.DataType = &s
				summaryChanges = append(summaryChanges, fmt.Sprintf("  data_type:   %s → %s", cur.DataType, s))
			}
		}
	}
	if raw, ok := args["value"]; ok {
		s, isStr := raw.(string)
		if !isStr {
			// A failed assertion would silently yield "" — and for a secret that
			// means irrecoverably wiping it while the diff claims a new value
			// was provided. Refuse non-string values outright.
			return fmt.Sprintf("Error: value must be a string, got %T. To clear a value, pass an explicit empty string.", raw), true
		}
		ch.Value = &s
		if cur.EnvVarType == "secret" {
			summaryChanges = append(summaryChanges, "  value:       •••••• (current secret) → •••••• (new value provided)")
		} else {
			summaryChanges = append(summaryChanges, fmt.Sprintf("  value:       %q → %q", truncateValue(cur.Value, 60), truncateValue(s, 60)))
		}
	}
	if len(summaryChanges) == 0 {
		return "Error: nothing to modify — provide at least one of new_name, description, value, data_type (with a value different from the current one). Note: env_var_type (visible/secret) CANNOT be changed after creation — the server ignores such changes; create a new variable instead.", true
	}

	header := fmt.Sprintf("Modify variable '@%s' (obj_id %d, stage %d, %s/%s):\n\n%s",
		cur.ShortName, cur.ObjID, stage, cur.DataType, cur.EnvVarType, strings.Join(summaryChanges, "\n"))

	warnings := ""
	if ch.Value != nil {
		warnings += "\n\nNote: the new value takes effect IMMEDIATELY in every process referencing it — no redeploy."
	}
	if ch.ShortName != nil {
		matches, scanned := scanEnvVarRefs(cur.ShortName)
		warnings += fmt.Sprintf("\n\n⚠ RENAME @%s → @%s breaks EVERY {{env_var[@%s]}} reference in this stage's processes.\n%s",
			cur.ShortName, *ch.ShortName, cur.ShortName,
			renderRefScan(cur.ShortName, matches, scanned, "these processes WILL BREAK until updated and re-pushed"))
	}

	// The confirm phrase always encodes the CURRENT (pre-rename) identity, so
	// it can never be reused for a different variable or after the rename.
	wantConfirm := fmt.Sprintf("%s#%d", cur.ShortName, cur.ObjID)
	apply := boolishArg(args, "apply")
	confirm, _ := args["confirm"].(string)

	if !apply {
		return fmt.Sprintf("%s%s\n\nDRY-RUN — nothing modified. Show this diff to the USER, get their explicit go-ahead, then re-run with apply=true and confirm=%q.",
			header, warnings, wantConfirm), false
	}
	if strings.TrimSpace(confirm) != wantConfirm {
		return fmt.Sprintf("⛔ Modification NOT executed — confirmation required.\n\n%s%s\n\nConfirm with the USER first, then re-run with apply=true and confirm=%q.",
			header, warnings, wantConfirm), true
	}

	if err := v.ModifyEnvVar(stage, cur.ObjID, cur.ShortName, ch); err != nil {
		return fmt.Sprintf("Error modifying variable: %v", err), true
	}

	// Post-verify against the server and report the applied state.
	finalName := cur.ShortName
	if ch.ShortName != nil {
		finalName = *ch.ShortName
	}
	// A verification that could not run must never read as a clean success —
	// same rule as the delete path.
	verified := ""
	if after, lerr := v.ListEnvVars(stage); lerr != nil {
		verified = fmt.Sprintf("\n⚠ Modify op reported ok, but the state could not be re-verified against the server (%v) — check list-variables before relying on it.", lerr)
	} else {
		for _, ev := range after {
			if ev.ObjID == cur.ObjID {
				verified = fmt.Sprintf("\nVerified on server: @%s (obj_id %d, %s/%s, changed %s).",
					ev.ShortName, ev.ObjID, ev.DataType, ev.EnvVarType, fmtUnix(ev.ChangeTime))
				finalName = ev.ShortName
			}
		}
		if verified == "" {
			verified = fmt.Sprintf("\n⚠ Modify op reported ok, but obj_id %d is no longer in the stage listing — verify in the Corezoid UI.", cur.ObjID)
		}
	}

	// Cache maintenance (non-fatal, mirrors create-variable).
	if ch.ShortName != nil {
		if err := v.removeVariableFromFile(cur.ShortName); err != nil {
			logger.Error("variables.json cache: %v", err)
		}
	}
	cacheTitle := cur.Title
	if ch.Title != nil {
		cacheTitle = *ch.Title
	}
	cacheValue := ""
	if cur.EnvVarType != "secret" {
		if ch.Value != nil {
			cacheValue = *ch.Value
		} else {
			cacheValue = cur.Value
		}
	}
	if err := v.updateVariablesFile(finalName, cacheTitle, cacheValue); err != nil {
		logger.Error("variables.json cache: %v", err)
	}

	out := fmt.Sprintf("✅ Variable '@%s' (obj_id %d) modified.%s\n\n%s", finalName, cur.ObjID, verified, strings.Join(summaryChanges, "\n"))
	if ch.ShortName != nil {
		matches, scanned := scanEnvVarRefs(cur.ShortName)
		out += "\n\n" + renderRefScan(cur.ShortName, matches, scanned,
			"update them to {{env_var[@"+finalName+"]}} and push-process each, or they fail at runtime")
	}
	return out, false
}

// ---- delete-variable --------------------------------------------------------

// renderDeletePreview builds the red warning block. It is intentionally loud:
// env-var deletion is the only permanently destructive delete in this plugin
// (no recycle bin), so the block is designed to be shown to the user verbatim.
func renderDeletePreview(stage int, ev EnvVar) string {
	matches, scanned := scanEnvVarRefs(ev.ShortName)
	refs := renderRefScan(ev.ShortName, matches, scanned, "these processes WILL FAIL at runtime after deletion")
	return fmt.Sprintf(
		"🔴🔴🔴 PERMANENT DELETION — NOT RECOVERABLE 🔴🔴🔴\n"+
			"\n"+
			"  Variable:   @%s   (obj_id %d)\n"+
			"  Stage:      %d\n"+
			"  Title:      %s\n"+
			"  Type:       %s / %s\n"+
			"  Value:      %s\n"+
			"  Created:    %s    Last changed: %s\n"+
			"\n"+
			"  ⛔ Environment variables are NOT moved to the Trash. Deletion is\n"+
			"  immediate and PERMANENT — the value (secrets included) cannot be\n"+
			"  recovered, and every process referencing {{env_var[@%s]}} will\n"+
			"  fail at runtime.\n"+
			"\n"+
			"%s\n"+
			"\n"+
			"🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴🔴",
		ev.ShortName, ev.ObjID, stage, ev.Title, ev.DataType, ev.EnvVarType,
		maskedEnvVarValue(ev), fmtUnix(ev.CreateTime), fmtUnix(ev.ChangeTime),
		ev.ShortName, refs)
}

func handleDeleteVariable(ctx context.Context, args map[string]interface{}) (string, bool) {
	v := NewValidator(ctx, 0)
	stage, cur, _, errMsg := resolveEnvVarTarget(v, args)
	if errMsg != "" {
		return errMsg, true
	}

	preview := renderDeletePreview(stage, cur)
	wantConfirm := fmt.Sprintf("%s#%d", cur.ShortName, cur.ObjID)
	apply := boolishArg(args, "apply")
	confirm, _ := args["confirm"].(string)

	if !apply {
		return fmt.Sprintf("%s\n\nDRY-RUN — nothing deleted. Show the block above to the USER VERBATIM, get their explicit agreement to permanently delete '@%s', then re-run with apply=true and confirm=%q.",
			preview, cur.ShortName, wantConfirm), false
	}
	if strings.TrimSpace(confirm) != wantConfirm {
		return fmt.Sprintf("⛔ Deletion NOT executed — confirmation required.\n\n%s\n\nShow the block above to the USER, get their explicit agreement, then re-run with apply=true and confirm=%q.",
			preview, wantConfirm), true
	}

	if err := v.DeleteEnvVar(stage, cur.ObjID); err != nil {
		return fmt.Sprintf("Error deleting variable: %v", err), true
	}

	// Post-verify: the variable must be gone from the server listing. A
	// verification that could not run must never be reported as passed.
	verifyNote := "Verified gone on the server."
	if after, lerr := v.ListEnvVars(stage); lerr != nil {
		verifyNote = fmt.Sprintf("⚠ Could not re-verify against the server (%v) — check list-variables.", lerr)
	} else {
		for _, ev := range after {
			if ev.ObjID == cur.ObjID {
				return fmt.Sprintf("⚠ Delete op reported ok, but '@%s' (obj_id %d) is STILL listed in stage %d — verify in the Corezoid UI before relying on the deletion.",
					cur.ShortName, cur.ObjID, stage), true
			}
		}
	}
	if err := v.removeVariableFromFile(cur.ShortName); err != nil {
		logger.Error("variables.json cache: %v", err)
	}
	return fmt.Sprintf("✅ Variable '@%s' (obj_id %d) permanently deleted from stage %d. %s\n(A stale _ENV_VARS_.json from an earlier pull-folder may still mention it.)",
		cur.ShortName, cur.ObjID, stage, verifyNote), false
}
