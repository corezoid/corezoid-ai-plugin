package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// reProcessIDFromFilename extracts the leading numeric process ID from a
// filename like "12345_my_process.conv.json". Compiled once and shared by the
// handlers that resolve a process ID from a file path.
//
// Both spellings convFileName can emit are accepted: the usual
// "<ID>_<name>.conv.json" and the untitled "<ID>.conv.json" fallback, which a
// pull of a process with no server-side title produces. The second alternative
// is written out in full instead of a bare "<ID>." so that an unrelated
// "<ID>.<something>.json" is not silently read as a process.
var reProcessIDFromFilename = regexp.MustCompile(`^(\d+)(?:_|\.conv\.json)`)

// sanitizeFileSegment converts a raw Corezoid title into a safe filename or
// directory-name segment. It replaces spaces AND every character that is either
// a path separator on any supported OS or illegal in Windows filenames with an
// underscore. This means a title like "/chat_v2" becomes "_chat_v2", which
// matches the server-side naming that pull-folder already produces.
//
// Characters replaced: space  /  \  :  *  ?  "  <  >  |
func sanitizeFileSegment(title string) string {
	const illegal = ` /\:*?"<>|`
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		if strings.ContainsRune(illegal, r) {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// convFileName builds the canonical local filename for a process or state
// diagram: "<ID>_<name>.conv.json", falling back to "<ID>.conv.json" when the
// title is empty. The ".conv.json" suffix is not cosmetic — resolveProcessPath,
// the MCP resource listing, the git-sync process index and the env-var
// reference scan all discover files by that exact suffix, so a file written
// with a plain ".json" extension is invisible to every one of them.
func convFileName(processID int, title string) string {
	safeName := sanitizeFileSegment(title)
	if safeName == "" {
		return fmt.Sprintf("%d.conv.json", processID)
	}
	return fmt.Sprintf("%d_%s.conv.json", processID, safeName)
}

// extractProcessIDFromPath returns the numeric process ID encoded in the
// filename, or an error message describing the expected format.
func extractProcessIDFromPath(filePath string) (int, string) {
	baseName := filepath.Base(filePath)
	matches := reProcessIDFromFilename.FindStringSubmatch(baseName)
	if matches == nil {
		return 0, fmt.Sprintf("Error: cannot extract process ID from filename '%s': expected format '<ID>_<name>.conv.json'", baseName)
	}
	id, _ := strconv.Atoi(matches[1])
	return id, ""
}

type stubModeStagePolicy struct {
	requiresConfirmation bool
	reason               string
}

func extractParentIDFromJSON(jsonContent string) int {
	var processData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonContent), &processData); err != nil {
		return 0
	}
	if f, ok := processData["parent_id"].(float64); ok {
		return int(f)
	}
	if i, ok := processData["parent_id"].(int); ok {
		return i
	}
	return 0
}

func resolveStageAndProjectFromFolder(v *Executor, folderID int) (stage, project int, err error) {
	if folderID == 0 {
		return 0, 0, fmt.Errorf("process parent_id is missing")
	}
	const maxDepth = 20
	currentID := folderID
	for i := 0; i < maxDepth; i++ {
		info, showErr := v.ShowFolder(currentID)
		if showErr != nil {
			return 0, 0, fmt.Errorf("show folder %d: %w", currentID, showErr)
		}
		if info.ParentObjType == "project" {
			return info.ObjID, info.ParentObjID, nil
		}
		if info.ParentObjID == 0 || info.ParentObjID == currentID {
			break
		}
		currentID = info.ParentObjID
	}
	return 0, 0, fmt.Errorf("could not find stage root while walking parents from folder %d", folderID)
}

func stageNameLooksProduction(title, shortName string) bool {
	for _, value := range []string{title, shortName} {
		normalized := strings.ToLower(strings.TrimSpace(value))
		for _, token := range strings.FieldsFunc(normalized, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
		}) {
			switch token {
			case "prod", "production", "preprod":
				return true
			}
		}
	}
	return false
}

func stubModeStagePolicyForPush(v *Executor, jsonContent string) stubModeStagePolicy {
	if parentID := extractParentIDFromJSON(jsonContent); parentID != 0 && v.APIUrl != "" {
		stageID, projectID, err := resolveStageAndProjectFromFolder(v, parentID)
		if err != nil {
			return stubModeStagePolicy{
				requiresConfirmation: true,
				reason:               fmt.Sprintf("target stage could not be resolved from process parent_id %d: %v", parentID, err),
			}
		}
		return stubModePolicyForStage(v, stageID, projectID)
	}

	if v.StageID == 0 {
		return stubModeStagePolicy{
			requiresConfirmation: true,
			reason:               "target stage is unknown because process parent_id could not be resolved and stage_id is not configured for this folder",
		}
	}

	projectID := v.GetProjectIDByStageID(v.StageID)
	if projectID == 0 {
		return stubModeStagePolicy{
			requiresConfirmation: true,
			reason:               fmt.Sprintf("target stage %d could not be resolved to a project", v.StageID),
		}
	}

	return stubModePolicyForStage(v, v.StageID, projectID)
}

func stubModePolicyForStage(v *Executor, stageID, projectID int) stubModeStagePolicy {
	immutable, _, title, shortName, err := v.stageInfo(stageID, projectID)
	if err != nil {
		return stubModeStagePolicy{
			requiresConfirmation: true,
			reason:               fmt.Sprintf("target stage %d metadata could not be read: %v", stageID, err),
		}
	}

	stageLabel := fmt.Sprintf("stage %d", stageID)
	if title != "" {
		stageLabel += fmt.Sprintf(" (%q)", title)
	}
	if immutable {
		return stubModeStagePolicy{
			requiresConfirmation: true,
			reason:               fmt.Sprintf("%s is immutable/read-only", stageLabel),
		}
	}
	if stageNameLooksProduction(title, shortName) {
		return stubModeStagePolicy{
			requiresConfirmation: true,
			reason:               fmt.Sprintf("%s looks production-like by title or short_name", stageLabel),
		}
	}

	return stubModeStagePolicy{
		requiresConfirmation: false,
		reason:               fmt.Sprintf("%s is mutable and does not look production-like", stageLabel),
	}
}

// handlePullProcess downloads a process by ID and writes its JSON to disk in
// the folder that mirrors its parent_id chain, so re-pulling places the file
// back where it lived.
func handlePullProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	v := NewValidator(ctx, processID)
	// Capture the version before exporting. If another user commits while the
	// export is in flight, this older baseline makes the next push detect the
	// race instead of pairing old file content with newer metadata.
	pullBaselineProc, pullBaselineErr := v.GetProcessByID(processID)
	procInfo1, err := v.ExportProcess()
	if err != nil {
		return fmt.Sprintf("Error fetching process: %v", err), true
	}
	var procInfo interface{}
	if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
		procInfo = arr[0]
	} else {
		procInfo = procInfo1
	}
	data, err := json.MarshalIndent(procInfo, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling process: %v", err), true
	}

	// Derive filename from process title if available
	title := ""
	if m, ok := procInfo.(map[string]interface{}); ok {
		title, _ = m["title"].(string)
	}
	fileName := convFileName(processID, title)

	// Resolve save directory from parent_id so the file lands in the correct folder tree.
	var dir string
	if m, ok := procInfo.(map[string]interface{}); ok {
		parentID := 0
		if pid, ok := m["parent_id"].(float64); ok {
			parentID = int(pid)
		}
		if parentID != 0 && v.StageID != 0 {
			segments, resolveErr := v.resolveFolderChainFromAPI(parentID)
			resolved := ""
			for _, seg := range segments {
				resolved = filepath.Join(resolved, seg.DirName())
			}
			if resolveErr != nil {
				logger.Warn("pull-process: could not resolve folder path for parent_id %d: %v", parentID, resolveErr)
			} else {
				// resolved is relative to the stage root. Anchor it at the
				// local stage-root directory (found by walking up from CWD
				// to a *.stage.json marker) so a re-pull from a subfolder
				// still writes the file to the same location it lives at
				// inside Corezoid — not into whatever the current CWD is.
				// When the process sits at the stage root (resolved == ""),
				// filepath.Join(stageRoot, "") == stageRoot, which is what
				// we want. If no stage marker is found (user is outside a
				// workspace), fall back to the old CWD-relative behaviour.
				if stageRoot := findStageRootFromCWD(v.StageID); stageRoot != "" {
					dir = filepath.Join(stageRoot, resolved)
					// Give every directory we are about to create its folder
					// marker, so the pulled tree can be used as a create /
					// push target instead of being a dead end.
					if merr := ensureFolderMarkers(mirroredPlacement{
						Dir: dir, StageRoot: stageRoot, Segments: segments,
					}); merr != nil {
						logger.Warn("pull-process: could not write folder markers under %s: %v", stageRoot, merr)
						return fmt.Sprintf("Error preparing local mirror under %s: %v", stageRoot, merr), true
					}
				} else {
					dir = resolved
				}
			}
		}
	}

	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Sprintf("Error creating directory: %v", err), true
		}
	}

	filePath := filepath.Join(dir, fileName)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Sprintf("Error writing file: %v", err), true
	}
	// Record the server version this file was pulled at, so a later push can
	// detect that someone else changed the process in the meantime (see
	// baseline.go and the push-process conflict gate). Best-effort: a failure
	// here only means the push can't verify freshness, never a pull failure.
	if pullBaselineErr == nil {
		if berr := writePulledBaseline(dir, processID, baselineFromServer(pullBaselineProc)); berr != nil {
			logger.Warn("pull-process: could not record baseline for %d: %v", processID, berr)
		}
	} else {
		logger.Warn("pull-process: could not fetch pre-export baseline for %d: %v", processID, pullBaselineErr)
	}
	// Store the pulled scheme as the 3-way merge ancestor (see baseline.go).
	if aerr := writeAncestorScheme(dir, processID, string(data)); aerr != nil {
		logger.Warn("pull-process: could not record ancestor for %d: %v", processID, aerr)
	}

	result := fmt.Sprintf("Process %d saved to %s", processID, filePath)

	// Warn if the downloaded process contains self-referencing api_copy/api_rpc
	// nodes — the Corezoid UI supports this pattern but push-process cannot deploy
	// it and force=true does NOT bypass the pre-deployment check.
	if m, ok := procInfo.(map[string]interface{}); ok {
		if rawNodes, getErr := getNodes(m); getErr == nil {
			typed := parseProcessNodes(rawNodes)
			if selfRefs := findSelfReferenceCopies(typed, processID); len(selfRefs) > 0 {
				var parts []string
				for _, sr := range selfRefs {
					parts = append(parts, fmt.Sprintf("%q (id=%s, type=%s)", sr.NodeTitle, sr.NodeID, sr.NodeType))
				}
				result += fmt.Sprintf(
					"\n\nWarning: %d self-referencing node(s) detected — this process cannot be re-deployed with push-process without fixing them first (force=true does not bypass this check):\n  • %s\nFix: replace each self-copy node with a time-semaphore delay node (≥30s) followed by a bare `go` back to the loop entry. The task cycles in-place without spawning a new one.",
					len(selfRefs), strings.Join(parts, "\n  • "))
			}
		}
	}

	return result, false
}

// handlePullFolder recursively downloads a folder (stage) and all its
// processes/subfolders into the current working directory.
// Before downloading processes, it attempts to sync the git mirror context
// into .git-context/ (non-blocking — a git failure does not abort the pull).
func handlePullFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	// Sync git mirror context before downloading processes.
	// ensureGitContext handles derivation of git_url if missing,
	// and silently skips on any error so the main pull always proceeds.
	ensureGitContext(ctx)

	v := NewValidator(ctx, 0)

	// pull-folder always writes into the matched Folder's RootPath — the stage
	// root registered in ~/.corezoid/config.json — so a re-pull triggered from
	// any subfolder overwrites the workspace in place instead of nesting a
	// second copy under the caller's cwd. Falls back to "." for the first-ever
	// pull (before any Folder exists), which matches cwd.
	dest := resolvePullDest()

	// folder_id=0 is "No Project" mode: no stage root, mirror the workspace
	// root view instead. project_id/stage_id are pinned to 0 in the config so
	// downstream tools resolve context off individual process files.
	if folderID == 0 {
		baselineSnapshot := captureWorkspaceBaselineSnapshot(v)
		if err := downloadWorkspaceRootRecursively(v, dest); err != nil {
			return fmt.Sprintf("Error fetching workspace root: %v", err), true
		}
		if err := writeWorkspaceProvisionedMarkerIfEmpty(dest); err != nil {
			logger.Warn("pull-folder: could not write %s marker: %v", workspaceProvisionedMarker, err)
		}
		captured := recordPulledBaselines(dest, baselineSnapshot)
		regenerateLocalCLAUDEMDIfNeeded(ctx)
		return fmt.Sprintf("Workspace root saved to %s (%d baseline(s) recorded)", dest, captured), false
	}

	// Pre-warm project_id cache so push-process never needs an extra API call.
	// folderID is the stage; its parent is the project.
	_, _ = resolveAndCacheProjectID(v)

	baselineSnapshot := captureFolderBaselineSnapshot(v, folderID)
	if err := downloadStageRecursively(v, folderID, dest); err != nil {
		return fmt.Sprintf("Error fetching folder: %v", err), true
	}
	if err := writeWorkspaceProvisionedMarkerIfEmpty(dest); err != nil {
		logger.Warn("pull-folder: could not write %s marker: %v", workspaceProvisionedMarker, err)
	}

	// In local/offline mode: regenerate CLAUDE.md from the freshly-downloaded
	// .conv.json files (online mode copies it from the Gitea mirror instead).
	regenerateLocalCLAUDEMDIfNeeded(ctx)

	// Record a pull baseline for every process so push-process can detect a
	// concurrent server-side change before overwriting it.
	captured := recordPulledBaselines(dest, baselineSnapshot)
	return fmt.Sprintf("Folder %d saved to %s (%d baseline(s) recorded)", folderID, dest, captured), false
}

// handleCreateVariable creates a Corezoid env variable in the current stage.
// The stage is resolved from the workspace marker file — never accepted as an
// argument. The LLM never needs to look up stage_id.
func handleCreateVariable(ctx context.Context, args map[string]interface{}) (string, bool) {
	name, err := strArg(args, "name")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	description, err := strArg(args, "description")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	value, err := strArg(args, "value")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	if v.StageID == 0 {
		return "Error: cannot resolve stage_id — no <id>_<name>.stage.json marker on disk. Run the 'login' tool to pull one.", true
	}
	rootFolderID := strconv.Itoa(v.StageID)
	if err := v.CreateVariable(rootFolderID, name, description, value); err != nil {
		return fmt.Sprintf("Error creating variable: %v", err), true
	}
	return fmt.Sprintf("Environment variable '%s' created successfully in stage %d", name, v.StageID), false
}

// categorizeLintForPush splits push-gate lint findings into three buckets:
//   - structural: an invalid graph the server itself rejects (BrokenLinks,
//     OldFormatNodes, SelfReferenceCopies). force=true does NOT bypass these
//     because "override" is not a possible resolution — the file must be fixed.
//   - overridable: contract/warning-level issues that block by default and
//     force=true can waive (MissingDefaultGo, ShortTimers, RpcReplyMismatches,
//     LiteralReplyValues, UnrepliedTerminals).
//   - advisory: never block; surfaced in the response so the user sees them.
func categorizeLintForPush(lintRes *LintResult) (structural, overridable, advisory int) {
	structural = len(lintRes.BrokenLinks) + len(lintRes.OldFormatNodes) + len(lintRes.SelfReferenceCopies)
	overridable = len(lintRes.MissingDefaultGo) + len(lintRes.ShortTimers) +
		len(lintRes.RpcReplyMismatches) + len(lintRes.LiteralReplyValues) +
		len(lintRes.UnrepliedTerminals)
	advisory = len(lintRes.NoopConditions) + len(lintRes.UnusedSetParams) +
		len(lintRes.OrphanedNodes) + len(lintRes.PassthroughEscalations) +
		len(lintRes.SharedErrorClusters) + len(lintRes.GitCallUsages)
	return structural, overridable, advisory
}

// handlePushProcess validates a local .conv.json and deploys it to Corezoid.
func handlePushProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	v := NewValidator(ctx, procID)

	jsonContent, err := LoadBinFromFile(filePath)
	if err != nil {
		return fmt.Sprintf("Error loading JSON file: %v", err), true
	}

	// Coordinate re-hydration (see coords.go): if the process exists on the
	// server and the local file lost any node coordinate (an edit dropped x/y,
	// fully or partially), applyLayout below would move those nodes — and, if
	// ALL are unplaced, re-lay-out the whole process. Refill lost coordinates
	// from the server first so push preserves the arrangement; only genuinely-
	// new nodes (absent on the server) stay unplaced and get placed normally.
	var rehydrateNote string
	if objID := extractObjIDFromJSON(jsonContent); objID != 0 && anyNodeUnplaced(jsonContent) {
		if refilled, n := rehydrateCoordsFromServer(v, jsonContent); n > 0 {
			// In-memory only: the refilled coordinates flow to the server via
			// ProcessJSON below and are persisted to the file on success. We do
			// NOT write the file here, so a later validation failure leaves the
			// user's file untouched rather than silently baked with server coords.
			jsonContent = refilled
			rehydrateNote = fmt.Sprintf("Restored %d node coordinate(s) from the server — layout preserved.", n)
		}
	}

	jsonContent1, messages := fixStruct(jsonContent, procID)
	if len(messages) > 0 {
		for _, msg := range messages {
			fmt.Fprintln(os.Stderr, msg)
		}
	}
	if jsonContent1 != jsonContent {
		jsonContent = jsonContent1
		if err := os.WriteFile(filePath, []byte(jsonContent), 0644); err != nil {
			return fmt.Sprintf("Error writing fixed JSON: %v", err), true
		}
	}

	if err := ValidateJSONSchema(filePath, debug); err != nil {
		return fmt.Sprintf("JSON schema validation failed: %v", err), true
	}

	if err := v.BeforeValidation(jsonContent, nil); err != nil {
		return fmt.Sprintf("Validation failed: %v", err), true
	}

	// Structural lint gate: catch deploy-breaking / caller-breaking shapes
	// offline before mutating the live process.
	//   • structural findings (BrokenLinks, OldFormatNodes, SelfReferenceCopies)
	//     describe an invalid graph the server itself rejects — force=true does
	//     NOT bypass them, because "override" is not a possible resolution.
	//   • overridable hard findings (contract/warning-level) block by default
	//     but force=true can waive them for known-good pushes.
	//   • advisory findings (noop, unused set_param, orphans, passthrough,
	//     shared clusters) never block; they are surfaced so the user sees them.
	// Active Stub Mode has its own stage-aware gate because it bypasses the real
	// called process at runtime.
	// force waives generic blocking LINT findings and nothing else. The
	// concurrency gate has its own waiver, overwrite_server_change: one boolean
	// covering both meant a force passed for a lint finding also pre-authorised
	// overwriting a concurrent server change that had not happened yet and was
	// therefore never shown to anyone (see resolveConflict).
	force, _ := args["force"].(bool)
	overwriteServerChange, _ := args["overwrite_server_change"].(bool)
	allowStubMode, _ := args["allow_active_stub_mode"].(bool)
	allowNoSnapshot, _ := args["allow_no_snapshot"].(bool)
	var lintNote, lintNoteHeader string // findings surfaced on a proceeding push (see below)
	// Every gate this push waived, reported in the tool result. A waiver visible
	// only on stderr is not an audit trail: an MCP host is free to surface just
	// the returned content, leaving "deployed successfully" as the whole record
	// of an overridden safety check.
	var waiverNotes []string
	if lintRes, lintErr := lintProcess(filePath); lintErr == nil {
		stubMode := len(lintRes.StubModeNodes)
		if stubMode > 0 {
			policy := stubModeStagePolicyForPush(v, jsonContent)
			if policy.requiresConfirmation && !allowStubMode {
				return fmt.Sprintf("Push blocked: active Stub Mode found in %d node(s). Stub replies bypass the real called process and are intended as temporary development/integration placeholders. Target policy: %s. Disable Stub Mode, or re-run with allow_active_stub_mode=true after explicit confirmation.\n\n%s",
					stubMode, policy.reason, FormatLintResult(lintRes)), true
			}
			if allowStubMode {
				waiverNotes = append(waiverNotes, fmt.Sprintf(
					"WARNING: allow_active_stub_mode=true was used — %d node(s) were deployed with active Stub Mode, which bypasses the real called process at runtime (%s).", stubMode, policy.reason))
			} else {
				fmt.Fprintf(os.Stderr, "[lint] %d active Stub Mode node(s) are warning-only for this push (%s)\n", stubMode, policy.reason)
			}
		}
		structural, overridable, advisory := categorizeLintForPush(lintRes)
		if structural > 0 {
			return fmt.Sprintf("Push blocked: lint found %d structural issue(s) — the resulting graph is invalid and the server rejects it. force=true does NOT bypass these; the graph itself must be fixed (broken links, old-format nodes, self-referencing api_copy/api_rpc).\n\n%s",
				structural, FormatLintResult(lintRes)), true
		}
		if overridable > 0 && !force {
			return fmt.Sprintf("Push blocked: lint found %d issue(s) that would break the deploy or its callers. Fix them, or re-run with force=true to override.\n\n%s",
				overridable, FormatLintResult(lintRes)), true
		}
		if overridable > 0 && force {
			waiverNotes = append(waiverNotes, fmt.Sprintf(
				"WARNING: force=true was used — %d lint finding(s) that block a push by default were overridden and deployed (listed below).", overridable))
			// Reporting these under the old "non-blocking" heading was worse than
			// not reporting them: the findings were there but the audit trail said
			// they had never blocked anything.
			lintNoteHeader = fmt.Sprintf("Lint — %d BLOCKING finding(s) overridden with force=true, deployed anyway:", overridable)
		}
		// The push proceeds. Surface any findings so the promise "advisory
		// findings are shown but do not block" is actually kept — otherwise
		// advisory-only issues would deploy silently and never be seen.
		if overridable+advisory+stubMode > 0 {
			lintNote = FormatLintResult(lintRes)
			if lintNoteHeader == "" {
				lintNoteHeader = "Lint (non-blocking, deployed anyway):"
			}
		}
	}

	// Concurrency gate: if this process was pulled and someone else changed it
	// on the server since, a plain push would silently overwrite their edits
	// (DeleteNotUsedNodes drops server nodes absent from the local scheme).
	// Block with an impact report unless overwrite_server_change=true, which is
	// meant to be passed in reply to that report — not ahead of it. New/never-
	// pulled processes have no baseline and are unaffected.
	merge, _ := args["merge"].(bool)
	adoptExisting, _ := args["adopt_existing"].(bool)
	// Set when the gate authorised writing over a live server version whose
	// content was never reconciled. Paired with the snapshot outcome below.
	overwroteLiveState, overwriteWaiver := false, ""
	if objID := extractObjIDFromJSON(jsonContent); objID != 0 {
		res := resolveConflict(v, filePath, objID, jsonContent, overwriteServerChange, merge, adoptExisting)
		switch res.action {
		case conflictBlock:
			return res.message, true
		case conflictMerged:
			return res.message, false // merged file written for review — do not push now
		case conflictProceed:
			if res.message != "" {
				waiverNotes = append(waiverNotes, res.message)
			}
			overwroteLiveState, overwriteWaiver = res.overwroteLiveState, res.waiver
		}
	}

	// Auto-snapshot: if the process already exists on the server (obj_id != 0),
	// capture the current server state before overwriting. There are four
	// outcomes:
	//   • snapshot succeeded → note it, push proceeds.
	//   • the environment has no snapshot feature at all → note it, push
	//     proceeds; CreateSnapshot is never called (see snapshot_support.go).
	//   • snapshot skipped because project/stage aren't resolved → BLOCK, unless
	//     the process has never been deployed or the caller passes
	//     allow_no_snapshot=true on a resolved mutable stage. An unresolved
	//     target is not evidence that this environment needs no rollback point;
	//     it is an unknown safety configuration, and proceeding overwrites a
	//     live process with no way back. See applySnapshotWaiverPolicy.
	//   • snapshot was attempted and the API returned an error → BLOCK, unless the
	//     process has never been deployed, or the caller passes
	//     allow_no_snapshot=true on a resolved mutable stage (see
	//     applySnapshotWaiverPolicy). Without git for .conv.json files the
	//     previous server version is unrecoverable once ProcessJSON overwrites it,
	//     and the same Corezoid API that just failed here is the one ProcessJSON is
	//     about to call anyway. A never-deployed process is the exception: it has no
	//     committed version and no nodes, CreateSnapshot rejects it, and there is no
	//     previous state that could be lost — blocking there would make the
	//     create-process → push-process flow impossible for every new process.
	var snapshotNote string
	snapshotTaken := false
	// Kept in scope for the irreversibility gate below: when the target did
	// resolve, the waiver there must be judged against the stage the snapshot
	// would have gone to, not re-derived from parent_id.
	snapshotProjectID := 0
	// The target policy, if one of the branches below already read it. Carried
	// rather than recomputed so the gate cannot reach a different verdict about
	// the same push, and does not pay a second `show stage` round trip.
	var waiverPolicy *stubModeStagePolicy
	existingObjID := extractObjIDFromJSON(jsonContent)
	if existingObjID != 0 {
		projectID, envNotice := resolveAndCacheProjectID(v)
		snapshotProjectID = projectID
		// "Does this installation have snapshots at all" is asked BEFORE "could
		// we resolve the target", because the answers mean different things and
		// only one of them is worth blocking over. An unresolved target on an
		// installation that HAS snapshots means a rollback point existed and we
		// failed to take it. On an installation that has none, there was never
		// a rollback point to take, and blocking would leave those environments
		// unable to push existing processes at all while telling the user to
		// "configure snapshots" that do not exist. The probe tolerates a zero
		// project/stage: it keys on the conv, and its positive control is `list
		// commits` for that same conv (see snapshot_support.go). An inconclusive
		// probe answers "supported", so an unproven environment still blocks.
		switch supported := snapshotsSupported(v, existingObjID, projectID, v.StageID); {
		case !supported:
			// No snapshot object in this installation: there is nothing to
			// capture and nothing to block on, so the push just proceeds.
			snapshotNote = "Auto-snapshot skipped: this Corezoid environment does not support snapshots. The platform holds no rollback point — keep the .conv.json under version control if you need one."

		case projectID != 0 && v.StageID != 0:
			name := extractProcessNameFromPath(filePath)
			title := fmt.Sprintf("pre-push %s %s", name, time.Now().UTC().Format("2006-01-02 15:04"))
			if snapObjID, snapVer, snapErr := v.CreateSnapshot(existingObjID, projectID, v.StageID, title); snapErr != nil {
				logger.Warn("[snapshot] auto-snapshot failed: %v", snapErr)
				switch {
				case processNeverDeployed(v, existingObjID):
					logger.Info("[snapshot] auto-snapshot skipped: process %d has never been deployed", existingObjID)
					snapshotNote = fmt.Sprintf("Auto-snapshot skipped: process #%d has no deployed version yet, so there is no previous state to restore.", existingObjID)
				default:
					// Resolution succeeded — we know exactly which stage this
					// would have landed on — so the waiver is checked against
					// that stage directly rather than re-derived from
					// parent_id (contrast the unresolved-target case below,
					// which has to re-derive it).
					policy := stubModePolicyForStage(v, v.StageID, projectID)
					waiverPolicy = &policy
					if allowed, why := applySnapshotWaiverPolicy(policy, allowNoSnapshot); allowed {
						snapshotNote = fmt.Sprintf("Warning: auto-snapshot could not be taken for process #%d — the CreateSnapshot API call failed (%v) — and allow_no_snapshot=true was passed. NO ROLLBACK POINT EXISTS for the version this push overwrites (%s).", existingObjID, snapErr, why)
					} else {
						return fmt.Sprintf(
							"Push blocked: the pre-push snapshot of process #%d failed (%v). Without a snapshot the previous server version cannot be restored after this push. %s\n\nRetry once the Corezoid API is reachable, or re-run with allow_no_snapshot=true to accept an irreversible push once you accept the risk. allow_no_snapshot is separate from force on purpose: force overrides a *known* conflict, this waives the ability to undo.",
							existingObjID, snapErr, why), true
					}
				}
			} else {
				logger.Info("[snapshot] created version %d (obj_id=%d) for process %d", snapVer, snapObjID, existingObjID)
				snapshotNote = fmt.Sprintf("Snapshot created before push (version %d, obj_id=%d).", snapVer, snapObjID)
				snapshotTaken = true
			}

		case processNeverDeployed(v, existingObjID):
			// Nothing deployed yet, so there is no previous state a snapshot
			// could preserve. This is the create-process → push-process flow.
			snapshotNote = fmt.Sprintf("Auto-snapshot skipped: process #%d has no deployed version yet, so there is no previous state to restore.", existingObjID)

		default:
			// Snapshots exist here, the process has state to lose, and we could
			// not resolve where to put the snapshot.
			policy := stubModeStagePolicyForPush(v, jsonContent)
			waiverPolicy = &policy
			allowed, why := applySnapshotWaiverPolicy(policy, allowNoSnapshot)
			if !allowed {
				return fmt.Sprintf(
					"Push blocked: process #%d already exists on the server and this environment does support snapshots, but no pre-push snapshot could be taken because project_id/stage_id could not be resolved%s — so there is no rollback point for the version this push would overwrite. %s\n\nFix the workspace configuration (re-run corezoid-init, or push from the folder whose stage marker names the target stage) so snapshots work, or re-run with allow_no_snapshot=true to accept an irreversible push. allow_no_snapshot is separate from force on purpose: force overrides a *known* conflict, this waives the ability to undo.",
					existingObjID, envNoticeSuffix(envNotice), why), true
			}
			snapshotNote = fmt.Sprintf("Warning: auto-snapshot skipped for process #%d (project_id/stage_id not resolved) and allow_no_snapshot=true was passed. NO ROLLBACK POINT EXISTS for the version this push overwrote (%s).", existingObjID, why)
		}
		if envNotice != "" && snapshotNote != "" {
			snapshotNote += " " + envNotice
		}
	}

	// An unreconciled overwrite of live server state with no rollback point is
	// the one combination in this handler that nothing can undo. Each waiver on
	// its own is a defensible judgement call — one says "their change loses",
	// the other says "no undo is available here". Together they mean the previous
	// version is neither reported nor recoverable, and that is not something a
	// single flag set for an unrelated reason should be able to reach. It is also
	// the only path a misclassified snapshot capability can turn into data loss,
	// so the check keys on the snapshot OUTCOME, not on why it was missing.
	//
	// A never-deployed process is the same exception it is for the snapshot gate
	// itself: there is no committed version, so there is nothing this push can
	// make unrecoverable and nothing a snapshot could have preserved. The
	// concurrency path can still get here for one — a pulled-but-never-deployed
	// process whose change_time moved — and refusing that would demand a second
	// waiver to protect a version that does not exist. The check costs one read
	// and only on the path that is about to block or warn anyway.
	//
	// What is honoured here is the POLICY-GATED waiver, not the raw flag.
	// allow_no_snapshot promises in its own contract that it applies only on a
	// stage that resolves and is mutable, and is refused on immutable,
	// production-like or unresolvable targets — but the branch above asks that
	// question only where it takes (or fails to take) a snapshot. On an
	// installation whose API has no snapshot object it deliberately asks
	// nothing, because blocking there would leave those environments unable to
	// push at all. That left this gate as the only thing standing between the
	// two waivers and an irreversible overwrite, reading the flag directly — so
	// the stage policy did not apply in the one environment where the rollback
	// point is missing permanently rather than transiently. On the other two
	// paths the same policy already ran and returned early, so asking it again
	// yields the same answer; it is not a second, stricter gate.
	if overwroteLiveState && !snapshotTaken && !processNeverDeployed(v, existingObjID) {
		if waiverPolicy == nil {
			// Only the snapshotless branch gets here without a policy: it asks
			// no question of its own, so the lookup is paid here, on the
			// destructive path, rather than on every push to such an install.
			policy := snapshotWaiverPolicyForTarget(v, jsonContent, v.StageID, snapshotProjectID)
			waiverPolicy = &policy
		}
		allowed, why := applySnapshotWaiverPolicy(*waiverPolicy, allowNoSnapshot)
		switch {
		case waiverPolicy.requiresConfirmation:
			// The target refuses the waiver whatever the caller passed. Keyed on
			// the policy rather than on the flag: telling someone to pass
			// allow_no_snapshot here would send them into a second, identical
			// refusal — and telling someone who already passed it to pass it
			// reads as a broken flag.
			return fmt.Sprintf(
				"Push blocked: %s waived the comparison against the live server version and no pre-push snapshot exists (%s), so this push would be irreversible — and allow_no_snapshot is not honoured on this target. %s\n\nAn irreversible overwrite is refused exactly where it is least recoverable. Re-pull and reconcile instead (pull-process, then push with merge=true), or push to a stage the waiver applies to.",
				overwriteWaiver, strings.TrimSuffix(snapshotNote, "."), why), true
		case !allowed:
			return fmt.Sprintf(
				"Push blocked: %s waived the comparison against the live server version, and no pre-push snapshot exists (%s). Together those two make this push irreversible — the version it overwrites is neither reported nor recoverable.\n\nEither restore one of the guarantees (re-pull and reconcile: pull-process, then push with merge=true; or fix the workspace configuration so a snapshot can be taken), or pass allow_no_snapshot=true in addition to accept an irreversible overwrite deliberately.",
				overwriteWaiver, strings.TrimSuffix(snapshotNote, ".")), true
		}
		// Deliberate and allowed — but it is the most destructive thing this tool
		// can do, so it is stated outright rather than left to be inferred from
		// two flags and a snapshot line.
		waiverNotes = append(waiverNotes, fmt.Sprintf(
			"WARNING: allow_no_snapshot=true was combined with %s — this push overwrote live server state that was never compared to anything, with no rollback point. It CANNOT be undone. (%s)", overwriteWaiver, why))
	}

	if _, err := v.ProcessJSON(filePath, jsonContent); err != nil {
		return fmt.Sprintf("Error deploying process: %v", err), true
	}

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
		dir := filepath.Dir(filePath)
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

	result := fmt.Sprintf("Process deployed successfully, ProcessID: %d", procID)
	if len(staleStateNotes) > 0 {
		result += fmt.Sprintf(
			"\n\nWARNING: the deploy succeeded, but the local concurrency state was NOT updated — %s. Lost-update protection now compares against stale data: re-pull this process before editing it again, otherwise the next push may report your own change as someone else's conflict or fall back to a delete-only impact report.",
			strings.Join(staleStateNotes, "; "))
	}
	if rehydrateNote != "" {
		result += "\n" + rehydrateNote
	}
	if snapshotNote != "" {
		result += "\n" + snapshotNote
	}
	if len(waiverNotes) > 0 {
		result += "\n\n" + strings.Join(waiverNotes, "\n\n")
	}
	if lintNote != "" {
		result += "\n\n" + lintNoteHeader + "\n" + lintNote
	}
	// Surface the git_call container build log so the user sees what the build
	// service reported (progress + result), not just silence on success.
	if len(v.gitCallBuildLog) > 0 {
		result += "\n\ngit_call build:\n" + strings.Join(v.gitCallBuildLog, "\n")
	}
	return result, false
}

// handleLintProcess validates a local .conv.json without touching the server.
func handleLintProcess(_ context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	result, err := lintProcess(filePath)
	if err != nil {
		return fmt.Sprintf("Error: lint failed: %v", err), true
	}
	return FormatLintResult(result), false
}

// runTaskDefaultWaitSec is how long run-task waits for the task to reach a
// final node before reporting it as still in progress. Long enough to ride out
// typical async hops (api / api_rpc / db_call), short enough not to stall an
// interactive session.
const runTaskDefaultWaitSec = 30

// runTaskMaxWaitSec caps the user-supplied wait_sec so a single tool call
// cannot pin the session for more than 10 minutes.
const runTaskMaxWaitSec = 600

// runTaskNoNodeMetaWaitSec caps the wait when deployed node metadata could not
// be read: without it there is no way to tell a final node from a logic node,
// so waiting out the full budget would only stall the caller.
const runTaskNoNodeMetaWaitSec = 5

// runTaskPollEvery is the interval between show_task polls while waiting.
var runTaskPollEvery = 2 * time.Second

// runTaskFirstPollAfter is the delay before the first show_task poll — short,
// so a synchronous process that finishes in milliseconds returns immediately
// instead of waiting out a full poll interval.
var runTaskFirstPollAfter = 300 * time.Millisecond

// handleRunTask fires a task at the already-deployed process and polls until it
// reaches a final node or wait_sec elapses. It deliberately reads runtime node
// metadata from the server and never deploys the local file: all deployments
// must pass through push-process and its safety gates first.
func handleRunTask(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	dataStr, err := strArg(args, "data")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	waitSec := runTaskDefaultWaitSec
	if _, ok := args["wait_sec"]; ok {
		n, err := intArg(args, "wait_sec")
		if err != nil {
			return "Error: " + err.Error(), true
		}
		waitSec = n
	}
	if waitSec < 1 {
		waitSec = 1
	}
	if waitSec > runTaskMaxWaitSec {
		waitSec = runTaskMaxWaitSec
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}
	if info, statErr := os.Stat(filePath); statErr != nil {
		return fmt.Sprintf("Error reading process file: %v", statErr), true
	} else if info.IsDir() {
		return fmt.Sprintf("Error reading process file: %s is a directory", filePath), true
	}

	v := NewValidator(ctx, procID)

	taskData := make(map[string]interface{})
	if err := json.Unmarshal([]byte(dataStr), &taskData); err != nil {
		return fmt.Sprintf("Error parsing task data: %v", err), true
	}
	// Node metadata is only needed to name the node the task settles on. A
	// caller with run-only rights can be denied both read paths, and refusing
	// to create the task in that case is the regression this tool used to have
	// with its implicit deploy: creating the task is what was actually asked
	// for, so degrade the reporting instead of failing.
	nodeMetaErr := loadRuntimeNodeMap(v)
	nodeMetaOK := nodeMetaErr == nil
	if !nodeMetaOK {
		logger.Info("run-task: deployed node metadata unavailable for process %d, reporting without node names: %v", v.ProcessID, nodeMetaErr)
		if waitSec > runTaskNoNodeMetaWaitSec {
			waitSec = runTaskNoNodeMetaWaitSec
		}
	}

	ref := optStrArg(args, "ref")
	if ref == "" {
		ref = fmt.Sprintf("%d_%d", time.Now().Unix(), rand.Intn(1000000))
	}
	if err := v.createTask(ref, taskData); err != nil {
		return fmt.Sprintf("Error creating task: %v", err), true
	}

	// Poll until the task settles on a final node or the wait budget runs out.
	// The first poll fires after a short beat so a synchronous process that
	// completes in milliseconds returns without paying the full poll interval;
	// subsequent polls run on the regular cadence.
	deadline := time.Now().Add(time.Duration(waitSec) * time.Second)
	var rspTask map[string]interface{}
	sawTask := false
	nextPoll := runTaskFirstPollAfter
	for {
		select {
		case <-ctx.Done():
			return fmt.Sprintf("Error: cancelled while waiting for task (ref %s): %v", ref, ctx.Err()), true
		case <-time.After(nextPoll):
		}
		nextPoll = runTaskPollEvery

		rsp, err := v.showTask(ref)
		if err != nil {
			// A task that reached a final node without save_task is dropped by
			// the platform, so show_task stops finding it. Once we have seen
			// the task at least once, report that instead of failing with a
			// confusing lookup error.
			if sawTask {
				return fmt.Sprintf(
					"Task finished, but its final data is not available: the task left the process and show_task no longer finds it (ref %s).\n"+
						"Most likely the final node has no save_task option. Enable {\"save_task\":true} on the final node or add an api_rpc_reply before it to inspect results.\n"+
						"Last error: %v", ref, err), false
			}
			if time.Now().After(deadline) {
				if !nodeMetaOK {
					// The task itself was created; only the follow-up read failed.
					return runTaskNoNodeMetaSummary(v, ref, "", "", "{}", nodeMetaErr,
						fmt.Sprintf("its result could not be read (%v)", err)), false
				}
				return fmt.Sprintf("Error getting task result (ref %s): %v", ref, err), true
			}
			continue
		}
		sawTask = true
		rspTask = rsp

		if !nodeMetaOK {
			break // no node types to compare against — report the first observation
		}
		serverNodeID, _ := rsp["node_id"].(string)
		if ni, ok := lookupNode(v, serverNodeID); ok && ni.Type == 2 {
			break // final node reached
		}
		if time.Now().After(deadline) {
			break // still in flight — report the parked node below
		}
	}

	logger.Info("Task response: %+v", rspTask)
	rspTaskData, _ := rspTask["data"].(map[string]interface{})
	rspTaskDataBin, _ := json.Marshal(rspTaskData)
	serverNodeID, _ := rspTask["node_id"].(string)
	taskID := ""
	if idv, ok := rspTask["obj_id"]; ok && idv != nil {
		taskID = fmt.Sprintf("%v", idv)
	}

	if !nodeMetaOK {
		return runTaskNoNodeMetaSummary(v, ref, taskID, serverNodeID, string(rspTaskDataBin), nodeMetaErr,
			"the node it settled on cannot be classified"), false
	}

	if v.Debug {
		for k, ni := range v.NodeIDMap {
			logger.Debug("NodeIDMap entry: key=%s type=%d serverID=%s name=%s", k, ni.Type, ni.ServerID, ni.Name)
		}
	}

	nodeInfo, found := lookupNode(v, serverNodeID)
	logger.Info("Node info (found=%v): %+v", found, nodeInfo)
	nodeType := "logic (not final)"
	msg := fmt.Sprintf("Task is still in progress after %ds: it is parked at a non-final node (an async node keeps it there). "+
		"Re-check later with show-task (a single read-only lookup by ref or task_id) or list-task-history, "+
		"or re-run with a larger wait_sec", waitSec)
	isErr := true
	if nodeInfo.Type == 1 {
		nodeType = "start"
	} else if nodeInfo.Type == 2 {
		isErr = false
		nodeType = "end (Success)"
		msg = "Task completed"
		if nodeInfo.Icon == "error" {
			isErr = true
			nodeType = "error node"
			msg = "Task failed: stopped at error node"
		}
	}

	summary := fmt.Sprintf("%s\nNodeID: %s\nNodeName: %s\nNodeType: %s\nProcessID: %d\nTaskRef: %s\nTaskID: %s\nData: %s",
		msg, serverNodeID, nodeInfo.Name, nodeType, v.ProcessID, ref, taskID, string(rspTaskDataBin))
	return summary, isErr
}

// runTaskNoNodeMetaSummary reports a task that was created while the deployed
// node list was unreadable — typically run-only access, where the caller may
// send tasks but not inspect the scheme. The task creation is the part that
// succeeded, so this is not an error; the summary just says which half of the
// usual answer is missing.
func runTaskNoNodeMetaSummary(v *Executor, ref, taskID, serverNodeID, data string, metaErr error, consequence string) string {
	if data == "" {
		data = "{}"
	}
	return fmt.Sprintf(
		"Task created, but %s: the deployed node list could not be read (%v). "+
			"This usually means run-only access to the process — the task was still sent. "+
			"Use show-task (pass the ref you sent) or list-task-history to follow it.\n"+
			"NodeID: %s\nNodeName: (unknown)\nNodeType: (unknown)\nProcessID: %d\nTaskRef: %s\nTaskID: %s\nData: %s",
		consequence, metaErr, serverNodeID, v.ProcessID, ref, taskID, data)
}

// loadRuntimeNodeMap indexes the server's currently deployed nodes without
// modifying the process. run-task must never call ProcessJSON: doing so turns a
// smoke test into an implicit deploy and bypasses push-process safety gates.
func loadRuntimeNodeMap(v *Executor) error {
	nodes, listErr := v.GetProcessNodes()
	if len(nodes) == 0 {
		exported, exportErr := v.ExportProcess()
		if exportErr != nil {
			if listErr != nil {
				return fmt.Errorf("process node list failed (%v) and export failed: %w", listErr, exportErr)
			}
			return fmt.Errorf("process node list is empty and export failed: %w", exportErr)
		}
		doc := exported
		if list, ok := exported.([]interface{}); ok && len(list) > 0 {
			doc = list[0]
		}
		if process, ok := doc.(map[string]interface{}); ok {
			for _, node := range schemeNodesFromDoc(process) {
				nodes = append(nodes, node)
			}
		}
	}
	if len(nodes) == 0 {
		if listErr != nil {
			return fmt.Errorf("process node list failed (%v) and export returned no nodes", listErr)
		}
		return fmt.Errorf("process %d has no deployed nodes", v.ProcessID)
	}
	for _, raw := range nodes {
		node, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := node["id"].(string)
		if id == "" {
			continue
		}
		title, _ := node["title"].(string)
		objType := nodeObjType(node)
		icon := ""
		if extra, _ := node["extra"].(string); extra != "" {
			var decoded map[string]interface{}
			if json.Unmarshal([]byte(extra), &decoded) == nil {
				icon, _ = decoded["icon"].(string)
			}
		}
		v.NodeIDMap[id] = NodeInfo{
			Type: objType, ObjType: objType, ServerID: id, Name: title, Icon: icon,
		}
	}
	if len(v.NodeIDMap) == 0 {
		return fmt.Errorf("process %d returned no valid deployed nodes", v.ProcessID)
	}
	return nil
}

// lookupNode resolves a server node ID against the validator's NodeIDMap,
// falling back to a scan over ServerID values (the map is keyed by local IDs
// after a push, but show_task returns server-side IDs).
func lookupNode(v *Executor, serverNodeID string) (NodeInfo, bool) {
	if ni, ok := v.NodeIDMap[serverNodeID]; ok {
		return ni, true
	}
	for _, ni := range v.NodeIDMap {
		if serverNodeID == ni.ServerID {
			return ni, true
		}
	}
	return NodeInfo{}, false
}

// handleCreateProcess creates an empty process in the given local folder and
// writes its skeleton JSON to disk for the user to flesh out.
func handleCreateProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	return createConv(ctx, args, "process")
}

// handleCreateStateDiagram creates an empty state diagram (conv_type "state")
// in the given local folder and writes its skeleton JSON to disk.
func handleCreateStateDiagram(ctx context.Context, args map[string]interface{}) (string, bool) {
	return createConv(ctx, args, "state")
}

// createConv is the shared implementation for create-process and
// create-state-diagram. It accepts a conv_type ("process" or "state") and
// produces a .conv.json skeleton on disk inside the requested folder.
func createConv(ctx context.Context, args map[string]interface{}, convType string) (string, bool) {
	folderPath := resolveDirPath(args, "folder_path")
	processName, err := strArg(args, "process_name")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	// An explicit folder_id always wins; otherwise the target is resolved
	// from the local directory's <id>_<name>.folder.json marker. Either way
	// the resolved target is reported back so a wrong destination is visible
	// immediately instead of surfacing as a confusing server error.
	folderID, resolvedFrom, err := resolveCreateTarget(args, folderPath)
	if err != nil {
		return fmt.Sprintf("Error resolving folder ID: %v", err), true
	}

	v := NewValidator(ctx, 0)
	markerWarning := ""
	processID, cerr := v.CreateEmptyConv(folderID, processName, "", convType)
	if processID == 0 {
		// Pass the server's reason through: "Stage is immutable" in the tool
		// result is actionable, "failed to create" alone is not.
		return fmt.Sprintf("Error: failed to create %s '%s' in folder #%d (%s): %v", convType, processName, folderID, resolvedFrom, cerr), true
	}

	procInfo1, err := v.ExportProcess()
	if err != nil {
		return fmt.Sprintf("Error exporting process: %v", err), true
	}
	var procInfo interface{}
	if arr, ok := procInfo1.([]interface{}); ok && len(arr) > 0 {
		procInfo = arr[0]
	} else {
		procInfo = procInfo1
	}
	data, err := json.MarshalIndent(procInfo, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling process: %v", err), true
	}

	// Mirror pull-process's placement when the caller pinned the Corezoid folder
	// but not the local one. Without this the two tools disagree about where a
	// process lives on disk: create writes into the CWD while a later
	// pull-process writes the same object into the folder tree that mirrors its
	// parent_id — leaving two copies and split baseline sidecars.
	if optStrArg(args, "folder_path") == "" {
		if mirrored := mirroredDirForFolder(v, folderID); mirrored.Dir != "" {
			folderPath = mirrored.Dir
			// Materialize the folder markers together with the directories.
			// Without them the tree we just created is a dead end: the next
			// create-process run from it can't resolve its folder ID (see
			// resolveFolderIDFromDir) and fails with "no <id>_<name>.folder.json".
			if err := ensureFolderMarkers(mirrored); err != nil {
				logger.Warn("create: could not write folder markers under %s: %v", mirrored.StageRoot, err)
				markerWarning = fmt.Sprintf("Local mirror warning: could not prepare folder markers under %s: %v", mirrored.StageRoot, err)
			}
		}
	}

	fileName := convFileName(processID, processName)
	filePath := filepath.Join(folderPath, fileName)
	if err := os.MkdirAll(folderPath, 0o755); err != nil {
		return fmt.Sprintf("Error creating folder %s: %v", folderPath, err), true
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Sprintf("Error writing file: %v", err), true
	}

	label := "Process"
	if convType == "state" {
		label = "State diagram"
	}
	result := fmt.Sprintf("%s '%s' created in Corezoid folder #%d (%s) and saved to %s",
		label, processName, folderID, resolvedFrom, filePath)
	if markerWarning != "" {
		result += "\nWarning: " + markerWarning
	}
	return result, false
}

// mirroredPlacement describes where a Corezoid folder is mirrored on disk:
// the target directory, the local stage root it is anchored at, and the chain
// of folders in between (stage root → target). Segments are what makes the
// directories usable — each one needs its own <id>_<name>.folder.json marker.
type mirroredPlacement struct {
	Dir       string
	StageRoot string
	Segments  []folderPathSegment
}

// mirroredDirForFolder returns the local directory that mirrors folderID inside
// the Corezoid tree — the same placement pull-process uses — with an empty Dir
// when it cannot be determined (no stage marker, unresolvable folder, API
// error). Callers keep their previous behaviour on an empty Dir, so this can
// only improve placement.
func mirroredDirForFolder(v *Executor, folderID int) mirroredPlacement {
	if v == nil || folderID == 0 || v.StageID == 0 {
		return mirroredPlacement{}
	}
	stageRoot := findStageRootFromCWD(v.StageID)
	if stageRoot == "" {
		return mirroredPlacement{}
	}
	segments, err := v.resolveFolderChainFromAPI(folderID)
	if err != nil {
		logger.Warn("create: could not resolve folder path for %d: %v", folderID, err)
		return mirroredPlacement{}
	}
	dir := stageRoot
	for _, seg := range segments {
		dir = filepath.Join(dir, seg.DirName())
	}
	return mirroredPlacement{Dir: dir, StageRoot: stageRoot, Segments: segments}
}

// folderMarkerContent is the on-disk shape of a <id>_<name>.folder.json marker,
// matching what a folder export writes.
type folderMarkerContent struct {
	Description string `json:"description"`
	ObjID       int    `json:"obj_id"`
	ObjType     int    `json:"obj_type"`
	ParentID    int    `json:"parent_id"`
	Title       string `json:"title"`
}

// writeFolderMarker writes dir's <id>_<name>.folder.json marker, creating dir
// if needed. A server-pulled marker is preserved only after proving its *name*
// identifies the folder this directory is meant to mirror: silently accepting
// any marker here can direct the next create/push operation at a different
// Corezoid folder after a copied or stale local directory is encountered.
//
// Only the file name is load-bearing — resolveFolderIDFromDir reads the ID from
// it and never parses the body. An unexpected body is therefore logged and the
// marker kept, not treated as fatal: markers inside a pulled workspace come out
// of a server ZIP export whose exact shape is the server's to choose, and
// refusing to mirror on an unfamiliar one would break healthy workspaces.
func writeFolderMarker(dir string, seg folderPathSegment) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory '%s': %w", dir, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading directory '%s': %w", dir, err)
	}
	var markers []string
	for _, entry := range entries {
		if !entry.IsDir() && folderMarkerFileRe.MatchString(entry.Name()) {
			markers = append(markers, entry.Name())
		}
	}
	if len(markers) > 1 {
		return fmt.Errorf("directory '%s' contains %d folder/stage markers (%s)", dir, len(markers), strings.Join(markers, ", "))
	}
	if len(markers) == 1 {
		match := folderMarkerFileRe.FindStringSubmatch(markers[0])
		markerID, _ := strconv.Atoi(match[1])
		if markerID != seg.ID {
			return fmt.Errorf("existing marker '%s' identifies folder %d, expected folder %d", markers[0], markerID, seg.ID)
		}
		if raw, err := os.ReadFile(filepath.Join(dir, markers[0])); err != nil {
			logger.Warn("folder marker '%s' in '%s' could not be read: %v", markers[0], dir, err)
		} else {
			var marker folderMarkerContent
			if err := json.Unmarshal(raw, &marker); err != nil {
				logger.Warn("folder marker '%s' in '%s' is not valid JSON: %v", markers[0], dir, err)
			} else if marker.ObjID != 0 && marker.ObjID != seg.ID {
				logger.Warn("folder marker '%s' in '%s' has obj_id=%d, expected folder %d", markers[0], dir, marker.ObjID, seg.ID)
			}
		}
		return nil
	}

	data, err := json.MarshalIndent(folderMarkerContent{
		ObjID:    seg.ID,
		ObjType:  0,
		ParentID: seg.ParentID,
		Title:    seg.Title,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling folder marker for %d: %w", seg.ID, err)
	}
	name := fmt.Sprintf("%d_%s.folder.json", seg.ID, seg.SafeName)
	tmp, err := os.CreateTemp(dir, ".folder-marker-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary marker in '%s': %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("setting permissions on temporary marker: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temporary marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary marker: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("installing folder marker '%s': %w", filepath.Join(dir, name), err)
	}
	return nil
}

// ensureFolderMarkers creates every directory level of a mirrored placement and
// gives each one a folder marker, so a directory this tool materializes can be
// used as a target by the next create-process / create-folder / push-process.
func ensureFolderMarkers(p mirroredPlacement) error {
	if p.StageRoot == "" {
		return nil
	}
	dir := p.StageRoot
	for _, seg := range p.Segments {
		dir = filepath.Join(dir, seg.DirName())
		if err := writeFolderMarker(dir, seg); err != nil {
			return err
		}
	}
	return nil
}

// resolveCreateTarget picks the Corezoid folder a create lands in: an explicit
// integer folder_id argument if given, else the local directory's marker file.
// The second return value describes HOW the target was chosen, for the tool's
// result message.
func resolveCreateTarget(args map[string]interface{}, dir string) (int, string, error) {
	if raw, ok := args["folder_id"]; ok {
		id, err := intArg(args, "folder_id")
		if err != nil {
			return 0, "", fmt.Errorf("invalid folder_id %v: %v", raw, err)
		}
		return id, "explicit folder_id", nil
	}
	id, marker, err := resolveFolderIDFromDir(dir)
	if err != nil {
		return 0, "", err
	}
	return id, fmt.Sprintf("resolved from local marker %s in '%s'", marker, dir), nil
}

// handleCreateFolder creates a new folder under the given parent, mirrors it
// on disk, and writes a placeholder *.folder.json so the directory is
// recognizable as a Corezoid folder.
func handleCreateFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	parentPath := resolveDirPath(args, "parent_path")
	folderName, err := strArg(args, "folder_name")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	parentFolderID, parentResolvedFrom, err := resolveCreateTarget(args, parentPath)
	if err != nil {
		return fmt.Sprintf("Error resolving parent folder ID: %v", err), true
	}

	v := NewValidator(ctx, 0)
	markerWarning := ""
	newFolderID, err := v.CreateFolder(parentFolderID, folderName, "")
	if err != nil {
		return fmt.Sprintf("Error creating folder '%s': %v", folderName, err), true
	}

	// Same divergence as createConv: an explicit parent folder_id with no
	// parent_path used to mirror the new folder into the CWD, so the on-disk
	// tree stopped matching the Corezoid tree that pull-folder reproduces.
	if optStrArg(args, "parent_path") == "" {
		if mirrored := mirroredDirForFolder(v, parentFolderID); mirrored.Dir != "" {
			parentPath = mirrored.Dir
			// The parent chain we are about to create needs markers too —
			// otherwise those intermediate directories can't be used as
			// targets for anything (see ensureFolderMarkers).
			if err := ensureFolderMarkers(mirrored); err != nil {
				logger.Warn("create-folder: could not write folder markers under %s: %v", mirrored.StageRoot, err)
				markerWarning = fmt.Sprintf("Local mirror warning: could not prepare folder markers under %s: %v", mirrored.StageRoot, err)
			}
		}
	}

	safeName := sanitizeFileSegment(folderName)
	dirName := fmt.Sprintf("%d_%s", newFolderID, safeName)
	dirPath := filepath.Join(parentPath, dirName)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Sprintf("Error creating directory '%s': %v", dirPath, err), true
	}

	fileContent := folderMarkerContent{
		Description: "",
		ObjID:       newFolderID,
		ObjType:     0,
		ParentID:    parentFolderID,
		Title:       folderName,
	}
	fileData, err := json.MarshalIndent(fileContent, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling folder file: %v", err), true
	}
	fileName := fmt.Sprintf("%d_%s.folder.json", newFolderID, safeName)
	filePath := filepath.Join(dirPath, fileName)
	if err := os.WriteFile(filePath, fileData, 0644); err != nil {
		return fmt.Sprintf("Error writing folder file: %v", err), true
	}

	result := fmt.Sprintf("Folder '%s' created in Corezoid folder #%d (%s) and saved to %s",
		folderName, parentFolderID, parentResolvedFrom, filePath)
	if markerWarning != "" {
		result += "\nWarning: " + markerWarning
	}
	return result, false
}

// handleShowFolder returns metadata for a single folder (title, obj_type,
// parent). Used to introspect folders without writing anything to disk.
func handleShowFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	info, err := v.ShowFolder(folderID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	kind := "folder"
	switch {
	case info.ObjType == 1:
		kind = "root"
	case isFolderProjectObjType(info.ObjType):
		kind = "project"
	case isFolderStageObjType(info.ObjType):
		kind = "stage"
	}
	return fmt.Sprintf("Folder #%d %q (kind=%s, parent=%s#%d)",
		info.ObjID, info.Title, kind, info.ParentObjType, info.ParentObjID), false
}

// handleListFolders prints the immediate children of a folder in a tabular
// form. Subfolders come first, then convs (processes + state diagrams).
func handleListFolders(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	children, err := v.ListFolder(folderID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	if len(children) == 0 {
		return fmt.Sprintf("Folder #%d is empty.", folderID), false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Folder #%d children (%d total):\n\n", folderID, len(children)))
	sb.WriteString(fmt.Sprintf("  %-10s  %-12s  %s\n", "ID", "Kind", "Title"))
	sb.WriteString("  " + strings.Repeat("-", 50) + "\n")
	for _, c := range children {
		kind := c.Obj
		if c.Obj == "conv" && c.ConvType != "" {
			kind = c.ConvType
		}
		sb.WriteString(fmt.Sprintf("  %-10d  %-12s  %s\n", c.ObjID, kind, c.Title))
	}
	return sb.String(), false
}

// handleModifyFolder renames a folder and/or updates its description. At
// least one of title / description must be provided — the API silently
// accepts an empty modify so we guard client-side.
func handleModifyFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	title := optStrArg(args, "title")
	description := optStrArg(args, "description")
	if title == "" && description == "" {
		return "Error: at least one of title or description must be provided", true
	}

	v := NewValidator(ctx, 0)
	if err := v.ModifyFolder(folderID, title, description); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}

	parts := []string{}
	if title != "" {
		parts = append(parts, fmt.Sprintf("title=%q", title))
	}
	if description != "" {
		parts = append(parts, fmt.Sprintf("description=%q", description))
	}
	return fmt.Sprintf("Folder #%d updated (%s)", folderID, strings.Join(parts, ", ")), false
}

// handleDeleteProcess moves a process (conv) to the Corezoid recycle bin
// (Trash). The operation is reversible from the UI; permanent destruction is
// intentionally not exposed via this tool.
func handleDeleteProcess(ctx context.Context, args map[string]interface{}) (string, bool) {
	processID, err := intArg(args, "process_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	if err := v.DeleteProcess(processID); err != nil {
		msg := fmt.Sprintf("Error: %v", err)
		// "object not found" means the server has no such process: it was
		// already deleted, or the id came from a local file pulled against a
		// different stage. Either way the caller may be acting on a stale local
		// copy — the exact trap behind mass "object not found" deletes. Nudge
		// them to reconcile with the server before trusting local files further.
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			msg += fmt.Sprintf("\nHint: process #%d is not on the server (already deleted, or its local .conv.json was pulled from a different stage). Any local file for it is now STALE — re-pull the folder and reconcile before deleting or running reachability analysis on local files, so you don't act on outdated state.", processID)
		}
		return msg, true
	}
	return fmt.Sprintf("Process #%d moved to Trash.", processID), false
}

// handleDeleteFolder moves a folder to the recycle bin. The Corezoid UI's
// Trash view restores it; permanent destruction is intentionally not exposed.
func handleDeleteFolder(ctx context.Context, args map[string]interface{}) (string, bool) {
	folderID, err := intArg(args, "folder_id")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	v := NewValidator(ctx, 0)
	if err := v.DeleteFolder(folderID); err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	return fmt.Sprintf("Folder #%d moved to Trash.", folderID), false
}

// handleCreateAlias creates a Corezoid alias (short_name → conv) pointing at
// the process whose ID is encoded in the file path.
//
// Aliases are stage-scoped, so we need to know which stage the target process
// lives in. Resolution priority:
//  1. explicit stage_id argument (belt-and-braces override for scripts);
//  2. stage derived from the process file's parent_id (walk up folders until
//     obj_type==3) — this is the correct answer for the target process and
//     avoids the frozen-config failure mode where a stale stage_id pointed at
//     a different project's stage and the server rejected with the cryptic
//     "Object is not in stage";
//  3. stage_id from the current Folder in ~/.corezoid/config.json (legacy
//     fallback for pre-parent_id files).
func handleCreateAlias(ctx context.Context, args map[string]interface{}) (string, bool) {
	filePath, err := resolveProcessPath(args, "process_path")
	if err != nil {
		return "Error: " + err.Error(), true
	}
	shortName, err := strArg(args, "short_name")
	if err != nil {
		return "Error: " + err.Error(), true
	}

	procID, errMsg := extractProcessIDFromPath(filePath)
	if errMsg != "" {
		return errMsg, true
	}

	v := NewValidator(ctx, 0)

	stageID, stageSrc, sErr := resolveAliasStageID(v, args, filePath)
	if sErr != nil {
		return "Error: " + sErr.Error(), true
	}

	aliasID, err := v.CreateAlias(shortName, procID, stageID)
	if err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "object is not in stage") {
			hint := fmt.Sprintf(" — the process (obj_id %d) does not live in stage %d (%s).", procID, stageID, stageSrc)
			if v.StageID != 0 && v.StageID != stageID {
				hint += fmt.Sprintf(" The workspace marker's stage_id is %d; that value was NOT used here.", v.StageID)
			}
			hint += " Pull-process this file again so its parent_id points at the current stage."
			return fmt.Sprintf("Error creating alias: %s%s", msg, hint), true
		}
		return fmt.Sprintf("Error creating alias: %v", err), true
	}

	return fmt.Sprintf("Alias '%s' created successfully, AliasID: %d (stage %d, %s)", shortName, aliasID, stageID, stageSrc), false
}

// resolveAliasStageID picks the stage_id for a create-alias call. The LLM
// never supplies stage_id — the resolver walks the process file's parent_id
// chain first (so a re-pulled file always lands in the right stage), then
// falls back to the workspace marker's stage_id.
func resolveAliasStageID(v *Executor, args map[string]interface{}, filePath string) (int, string, error) {
	_ = args // signature kept for callers; stage_id is no longer a valid arg

	if parentID, ok := readParentIDFromFile(filePath); ok && parentID != 0 {
		stage, err := v.ResolveStageIDByFolder(parentID)
		if err == nil && stage != 0 {
			label := "derived from process parent_id"
			if v.StageID != 0 && v.StageID != stage {
				label = fmt.Sprintf("derived from process parent_id — overrides workspace marker stage_id=%d", v.StageID)
			}
			return stage, label, nil
		}
		if err != nil {
			logger.Warn("create-alias: could not derive stage from parent_id %d: %v", parentID, err)
		}
	}

	if v.StageID != 0 {
		return v.StageID, "from workspace marker stage_id (fallback — process file had no parent_id)", nil
	}
	return 0, "", fmt.Errorf("could not resolve stage_id: process file has no parent_id and no stage marker is on disk. Re-pull the process (so parent_id is written) or run 'login' to materialize the marker.")
}

// readParentIDFromFile reads a .conv.json file just far enough to extract its
// top-level parent_id. Returns (0, false) on any error — the caller is
// expected to fall back gracefully; we never fail the tool because a file was
// unparsable, that's the fallback's job.
func readParentIDFromFile(filePath string) (int, bool) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, false
	}
	var head struct {
		ParentID interface{} `json:"parent_id"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return 0, false
	}
	switch p := head.ParentID.(type) {
	case float64:
		return int(p), p != 0
	case string:
		if n, err := strconv.Atoi(p); err == nil {
			return n, n != 0
		}
	}
	return 0, false
}

// processNeverDeployed reports whether a process that exists on the server has
// never been deployed: no committed version and no nodes. Such a process holds no
// state a snapshot could capture, and CreateSnapshot rejects it outright — so the
// pre-push snapshot gate must not treat that rejection as a reason to block.
//
// The answer is fail-closed: true is returned only when the API response
// positively confirms BOTH facts. A failed lookup, a missing field, an
// unexpected type or a partial response all return false, keeping the
// conservative "block the push" behaviour — this function is what disarms the
// snapshot protection of an already-deployed process, so an ambiguous response
// must never be read as "nothing to lose".
func processNeverDeployed(v *Executor, objID int) bool {
	if v == nil || objID == 0 {
		return false
	}
	data, err := v.GetProcessByID(objID)
	if err != nil || data == nil {
		return false
	}
	return commitsConfirmedEmpty(data) && nodeListConfirmedEmpty(data)
}

// snapshotWaiverPolicyForTarget reads the target policy that decides whether
// allow_no_snapshot may be honoured, from whichever description of the target is
// the more trustworthy: a stage that resolved is asked about directly, an
// unresolved one is re-derived from the process parent_id.
//
// Walking parent_id can fail where stage_id/project_id are perfectly well known,
// so preferring the resolved pair is what keeps the irreversibility gate from
// contradicting the snapshot branch that ran a few lines earlier.
func snapshotWaiverPolicyForTarget(v *Executor, jsonContent string, stageID, projectID int) stubModeStagePolicy {
	if stageID != 0 && projectID != 0 {
		return stubModePolicyForStage(v, stageID, projectID)
	}
	return stubModeStagePolicyForPush(v, jsonContent)
}

// applySnapshotWaiverPolicy turns a target policy plus the flag into a verdict
// on allow_no_snapshot, and always returns the reason so the block, the refusal
// and the waiver notice can all state it.
//
// Waiving the rollback point is only ever acceptable somewhere a lost version is
// cheap to recreate, so the waiver is gated on the same stage policy the Stub
// Mode gate uses: it is honoured only on a stage that resolved and is mutable.
// Anywhere that policy wants confirmation — an immutable stage, a
// production-looking name, or a stage that could not be resolved or read at all
// — the waiver is refused, because that is exactly where an irreversible
// overwrite is least recoverable and where "I could not determine the target"
// must not be allowed to mean "so anything goes". That holds for a transient
// CreateSnapshot error as much as for an unresolved target, and — since the
// irreversibility gate reuses this — for an installation that has no snapshot
// object at all.
//
// The policy is passed in rather than read here so callers that already hold it
// can reuse it: reading it again costs a `show stage` round trip and, worse,
// lets two independently derived verdicts about the same push disagree.
func applySnapshotWaiverPolicy(policy stubModeStagePolicy, allowNoSnapshot bool) (bool, string) {
	switch {
	case policy.requiresConfirmation:
		return false, fmt.Sprintf("Target policy: %s — a rollback waiver is not accepted here even with allow_no_snapshot=true.", policy.reason)
	case !allowNoSnapshot:
		return false, fmt.Sprintf("Target policy: %s.", policy.reason)
	default:
		return true, policy.reason
	}
}

// envNoticeSuffix renders resolveAndCacheProjectID's notice as a parenthetical,
// so a block message can carry the reason the lookup came back empty.
func envNoticeSuffix(envNotice string) string {
	if envNotice == "" {
		return ""
	}
	return " (" + envNotice + ")"
}

// commitsConfirmedEmpty reports whether the response states that the process
// carries no committed version. It requires commits.version to be present and
// numerically 0; last_confirmed_version, which baselineFromServer prefers when
// present, vetoes the answer whenever it is anything but a confirmed 0.
func commitsConfirmedEmpty(data map[string]interface{}) bool {
	if lcv, present := data["last_confirmed_version"]; present {
		n, ok := jsonNumberValue(lcv)
		if !ok || n != 0 {
			return false
		}
	}
	commits, ok := data["commits"].(map[string]interface{})
	if !ok {
		return false
	}
	ver, present := commits["version"]
	if !present {
		return false
	}
	n, ok := jsonNumberValue(ver)
	return ok && n == 0
}

// nodeListConfirmedEmpty reports whether the response states that the process
// has no nodes. The list key must be present and an actual empty array — a
// missing list, or a list of some other shape, means the response did not
// answer the question.
func nodeListConfirmedEmpty(data map[string]interface{}) bool {
	list, present := data["list"]
	if !present {
		return false
	}
	nodes, ok := list.([]interface{})
	return ok && len(nodes) == 0
}

// jsonNumberValue coerces the numeric spellings a Corezoid response can carry
// (float64 from a plain decode, json.Number under UseNumber, an int from a
// hand-built map, or a numeric string) to a float64. Anything else — nil,
// bool, object, non-numeric string — reports false so the caller can treat the
// field as unanswered rather than as zero.
func jsonNumberValue(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}
