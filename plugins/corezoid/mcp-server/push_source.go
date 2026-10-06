package main

// The parts of a push that differ between a local .conv.json and a hosted
// request. pushProcessCore runs the same gates for both.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type pushSource interface {
	// saveFixed persists JSON that fixStruct repaired before validation.
	saveFixed(content string) error
	validateSchema(content string) error
	lint(content string) (*LintResult, error)
	// conflicts is where the concurrency gate reads what the caller last saw.
	conflicts() conflictStore
	// name labels the pre-push snapshot.
	name(content string) string
	deploy(v *Executor, content string) error
	// afterDeploy refreshes the caller's view of the deployed version. It
	// returns failures to report as stale state, and a note for the result.
	afterDeploy(ctx context.Context, v *Executor) (staleStateNotes []string, deployedNote string)
}

// filePushSource is the local push: the process lives in filePath, with the
// baseline and merge ancestor as sidecars next to it.
type filePushSource struct{ filePath string }

func (f filePushSource) saveFixed(content string) error {
	return os.WriteFile(f.filePath, []byte(content), 0644)
}
func (f filePushSource) validateSchema(string) error      { return ValidateJSONSchema(f.filePath, debug) }
func (f filePushSource) lint(string) (*LintResult, error) { return lintProcess(f.filePath) }
func (f filePushSource) conflicts() conflictStore {
	return fileConflictStore{dir: filepath.Dir(f.filePath), filePath: f.filePath}
}
func (f filePushSource) name(string) string { return extractProcessNameFromPath(f.filePath) }
func (f filePushSource) deploy(v *Executor, content string) error {
	_, err := v.ProcessJSON(f.filePath, content)
	return err
}
func (f filePushSource) afterDeploy(ctx context.Context, v *Executor) ([]string, string) {
	// In local mode: regenerate CLAUDE.md so the process index stays current.
	regenerateLocalCLAUDEMDIfNeeded(ctx)

	// Refresh the pull baseline AND the merge ancestor to the version we just
	// committed, so the next push starts current instead of re-flagging our own
	// change, and a later concurrent-edit conflict still has a 3-way ancestor
	// (without this, a push→edit→push flow degrades to the delete-only report).
	//
	// A failure here cannot be undone (the deploy already happened) but it must
	// not be silent: the local sidecars are what lost-update protection reads,
	// so leaving them stale while reporting a clean deploy makes the next push
	// either re-flag our own change as someone else's or lose the 3-way
	// ancestor. The user has to know the local state is no longer trustworthy,
	// so every failure is collected and reported alongside the success.
	var staleStateNotes []string
	if v.ProcessID != 0 {
		dir := filepath.Dir(f.filePath)
		if proc, gerr := v.GetProcessByID(v.ProcessID); gerr != nil {
			logger.Warn("push: could not read back process %d to refresh baseline: %v", v.ProcessID, gerr)
			staleStateNotes = append(staleStateNotes, fmt.Sprintf("the deployed version of process #%d could not be read back (%v), so the concurrency baseline still points at the pre-push version", v.ProcessID, gerr))
		} else if berr := writeBaseline(dir, v.ProcessID, baselineFromServer(proc)); berr != nil {
			logger.Warn("push: could not refresh baseline for %d: %v", v.ProcessID, berr)
			staleStateNotes = append(staleStateNotes, fmt.Sprintf("the concurrency baseline could not be written (%v)", berr))
		}
		if theirsConv, ok := exportConv(v); !ok {
			logger.Warn("push: could not export process %d to refresh the merge ancestor", v.ProcessID)
			staleStateNotes = append(staleStateNotes, "the deployed scheme could not be exported, so the 3-way merge ancestor is stale")
		} else if aerr := writeAncestorScheme(dir, v.ProcessID, theirsConv); aerr != nil {
			logger.Warn("push: could not refresh ancestor for %d: %v", v.ProcessID, aerr)
			staleStateNotes = append(staleStateNotes, fmt.Sprintf("the merge ancestor could not be written (%v)", aerr))
		}
	}

	return staleStateNotes, ""
}

// hostedPushSource is a push from a hosted request: content arrives in the
// request, the caller's view of the server version arrives as a base token
// (see pull-process), and the deployed scheme goes back in the result.
type hostedPushSource struct {
	base    *baselineEntry // nil when the caller sent no base token
	content string
}

func (h hostedPushSource) saveFixed(string) error { return nil }
func (h hostedPushSource) validateSchema(content string) error {
	return ValidateJSONSchemaData([]byte(content), debug)
}
func (h hostedPushSource) lint(content string) (*LintResult, error) {
	return lintProcessData([]byte(content))
}
func (h hostedPushSource) conflicts() conflictStore { return tokenConflictStore{base: h.base} }
func (h hostedPushSource) name(content string) string {
	if t := extractTitleFromJSON(content); t != "" {
		return t
	}
	return "process"
}
func (h hostedPushSource) deploy(v *Executor, content string) error {
	_, err := v.ProcessJSON("", content)
	return err
}

// afterDeploy returns the deployed scheme with the server's node IDs and a
// fresh base token, so the caller's next edit starts from what is live.
func (h hostedPushSource) afterDeploy(_ context.Context, v *Executor) ([]string, string) {
	if v.ProcessID == 0 {
		return nil, ""
	}
	var b strings.Builder
	if proc, err := v.GetProcessByID(v.ProcessID); err == nil {
		fmt.Fprintf(&b, "base: %s\n", hostedBaseToken(v.ProcessID, baselineFromServer(proc)))
	} else {
		fmt.Fprintf(&b, "base: unavailable (%v) — pull-process again before the next edit.\n", err)
	}
	if data, _, err := exportProcessJSON(v); err == nil {
		b.WriteString("\nDeployed scheme (with server node IDs — edit this, not your earlier copy):\n")
		b.Write(data)
	} else {
		fmt.Fprintf(&b, "The deployed scheme could not be exported (%v) — pull-process before the next edit.", err)
	}
	return nil, b.String()
}

// tokenConflictStore answers the concurrency gate from a hosted base token.
// The token always comes from a process-detail read, so the equal-timestamp
// content check (list-sourced or legacy baselines only) never runs, and there
// is no merge ancestor: a concurrent change blocks with a report.
type tokenConflictStore struct{ base *baselineEntry }

func (t tokenConflictStore) lookupBaseline(int) (baselineEntry, bool, error) {
	if t.base == nil {
		return baselineEntry{}, false, nil
	}
	return *t.base, true, nil
}
func (t tokenConflictStore) contentChangedSince(*Executor, int) contentCheckResult {
	return contentCheckExportFailed
}
func (t tokenConflictStore) healMissingAncestor(*Executor, int) string { return "" }
func (t tokenConflictStore) mergePlan(*Executor, int, string) (mergePlan, []map[string]any, string, bool) {
	return mergePlan{}, nil, "", false
}
func (t tokenConflictStore) applyMerge(procID int, _ string, _ baselineEntry, _ string, _ mergePlan, _ []map[string]any, _ string, _ int64) conflictResult {
	return conflictResult{action: conflictBlock, message: fmt.Sprintf("Push blocked: process #%d changed on the server since your pull, and merging is not available on the hosted server. pull-process again, re-apply your edits, then push.", procID)}
}

// extractTitleFromJSON returns the process title, or "" if there is none.
func extractTitleFromJSON(content string) string {
	var m struct {
		Title string `json:"title"`
	}
	if json.Unmarshal([]byte(content), &m) != nil {
		return ""
	}
	return m.Title
}
