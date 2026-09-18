package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// confineToWorkdir is a guard against path-traversal in LLM-supplied path
// arguments. Relative paths are checked lexically (the cleaned form must not
// start with ".."). Absolute paths are accepted only when they resolve inside
// the working directory, in which case they are rewritten to the equivalent
// relative path.
//
// Threat model: a prompt-injected LLM might call lint-process with
// process_path="../../etc/passwd" or pull-folder writing to ~/.ssh/. Handlers
// run with the user's full file permissions, so anything the path argument
// says, the OS will do. This check is defence-in-depth: it blocks the
// path-traversal escape without restricting legitimate paths inside the
// workspace.
//
// Design note: the absolute-path comparison resolves symlinks on both sides
// (filepath.EvalSymlinks) before filepath.Rel. A purely lexical comparison is
// unsound — on macOS /var/folders/... and /private/var/folders/... are the
// same directory via symlink — which is why earlier versions rejected
// absolute paths outright. Resolving both sides removes that failure mode
// while keeping the guarantee: a path that escapes cwd is still rejected.
// Callers (IDE agents in particular) routinely produce absolute paths for
// files they just read or wrote inside the project; forcing them to guess the
// project-relative spelling caused a needless error/retry loop.
func confineToWorkdir(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	if filepath.IsAbs(p) {
		rel, err := relativeToCwd(p)
		if err != nil {
			return "", fmt.Errorf("absolute path %q is not inside the working directory; pass a path relative to the project root (%v)", p, err)
		}
		return rel, nil
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes working directory", p)
	}
	// The lexical check above only rejects paths that SAY they leave the
	// workspace. A symlink says nothing: "exports/x.conv.json" is a clean
	// relative path whether "exports" is a real directory or a link to /etc,
	// and every handler here writes with the user's full permissions. The
	// absolute branch has resolved both sides since it was written, so the
	// relative spelling — the one callers actually use — gets the same
	// treatment instead of a weaker one.
	//
	// ensureInsideRoot is the guard unzipFile already uses for the same
	// question: it resolves the deepest ancestor that exists, so a write
	// target whose parent directory has yet to be created (create-process into
	// a new folder) is still allowed, while an existing symlinked component —
	// or an existing symlinked leaf — is resolved and compared.
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %v", err)
	}
	cwdReal, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("cannot resolve working directory: %v", err)
	}
	if err := ensureInsideRoot(filepath.Join(cwdReal, clean), cwdReal); err != nil {
		return "", fmt.Errorf("path %q resolves outside the working directory: %v", p, err)
	}
	// Return the CLEANED path, not p. The check above runs on clean, and the
	// two disagree the moment a symlink meets "..": filepath.Clean("link/../x")
	// is "x" — lexically inside the workspace, and that is what gets validated
	// — while the OS resolves the original spelling left to right, walking
	// through link to wherever it points and only then applying "..". Handing
	// the caller back p meant every handler re-derived the escaping path the
	// guard had just cleared under a different name.
	return clean, nil
}

// relativeToCwd converts an absolute path to its cwd-relative form, resolving
// symlinks on both sides so the comparison holds on macOS (/var → /private/var)
// and similar layouts. The path's parent directory must exist — EvalSymlinks
// needs a real directory — but the file itself may not (write targets).
// Returns an error when the path lies outside the working directory.
func relativeToCwd(p string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %v", err)
	}
	cwdReal, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("cannot resolve working directory: %v", err)
	}
	dirReal, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %v", err)
	}
	leaf := filepath.Join(dirReal, filepath.Base(p))
	// The leaf itself may be a symlink, and a write follows it. Resolving the
	// parent alone would accept "workspace/x.conv.json" whose file is a link
	// to ~/.ssh/authorized_keys — the name is inside the workspace, the write
	// is not. A leaf that does not exist yet is a legitimate write target, so
	// only an existing one is resolved.
	if _, statErr := os.Lstat(leaf); statErr == nil {
		leafReal, resolveErr := filepath.EvalSymlinks(leaf)
		if resolveErr != nil {
			return "", fmt.Errorf("cannot resolve path: %v", resolveErr)
		}
		leaf = leafReal
	}
	rel, err := filepath.Rel(cwdReal, leaf)
	if err != nil {
		return "", fmt.Errorf("cannot relativize path: %v", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path resolves outside the working directory")
	}
	return rel, nil
}

// folderMarkerFileRe matches the marker file that makes a directory resolvable
// to a Corezoid folder: <id>_<name>.folder.json or <id>_<name>.stage.json.
var folderMarkerFileRe = regexp.MustCompile(`^(\d+)_.*\.(folder|stage)\.json$`)

// dirHasFolderMarker reports whether dir already carries a folder/stage marker,
// so callers that materialize mirrored directories don't overwrite a marker
// pulled from the server with a synthesized one.
func dirHasFolderMarker(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && folderMarkerFileRe.MatchString(e.Name()) {
			return true
		}
	}
	return false
}

// resolveFolderIDFromDir looks for a file matching <id>_<name>.folder.json or
// <id>_<name>.stage.json in the given directory and returns the numeric id
// together with the marker file name it was resolved from.
//
// When the directory contains MORE than one marker, the target is ambiguous
// and we refuse to guess: silently picking the first match once sent creates
// into a production stage (the marker files sorted production before develop)
// where the only symptom was a misleading "Stage is immutable" error.
func resolveFolderIDFromDir(dir string) (int, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, "", fmt.Errorf("failed to read directory '%s': %v", dir, err)
	}
	type match struct {
		id   int
		name string
	}
	var found []match
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := folderMarkerFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		id, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		found = append(found, match{id: id, name: e.Name()})
	}
	switch len(found) {
	case 0:
		return 0, "", fmt.Errorf("no <id>_<name>.folder.json file found in '%s'; cannot determine folder ID — pass folder_id explicitly", dir)
	case 1:
		return found[0].id, found[0].name, nil
	default:
		names := make([]string, len(found))
		for i, f := range found {
			names[i] = f.name
		}
		return 0, "", fmt.Errorf("ambiguous target: directory '%s' contains %d folder/stage markers (%s) — pass folder_id explicitly or run from the specific folder's directory", dir, len(found), strings.Join(names, ", "))
	}
}

// findStageRootFromCWD returns the absolute path of the local stage root —
// i.e. Folder.RootPath, since pull-folder writes the stage's contents
// directly into RootPath (no <id>_<name>.stage wrapper). Returns "" when
// no Folder matches the current cwd, or when expectedStageID is non-zero
// and disagrees with the Folder's StageID (guards pull-process against
// writing into the wrong workspace after a stage change).
//
// Used by pull-process so a re-pull from a subfolder still writes the file
// to the location it lives at inside Corezoid, not to the CWD.
func findStageRootFromCWD(expectedStageID int) string {
	f := Current()
	if f == nil || f.RootPath == "" {
		return ""
	}
	if expectedStageID != 0 && f.StageID != 0 && f.StageID != expectedStageID {
		return ""
	}
	return f.RootPath
}

// resolvePullDest returns the destination directory for pull-folder /
// workspace-root pulls. When a Folder matches the current cwd, the pull is
// anchored at that Folder's RootPath so a re-pull invoked from any subfolder
// still overwrites the stage at its original location — never nesting a
// second copy under the caller's cwd. Falls back to "." for the first-ever
// pull (no matching Folder yet), where cwd == the RootPath about to be
// registered by UpdateCurrent.
func resolvePullDest() string {
	if root := findStageRootFromCWD(0); root != "" {
		return root
	}
	return "."
}

// intArg extracts an integer argument from args map.
func intArg(args map[string]interface{}, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing required argument: %s", key)
	}
	switch val := v.(type) {
	case float64:
		// Truncating here is how a mistyped id becomes a correct-looking
		// operation on a DIFFERENT object: delete-process{process_id: 123.9}
		// used to delete process 123 and report success. Declaring the
		// property as "integer" in the tool's InputSchema does not prevent it
		// — unknownArgsError checks argument NAMES, nothing validates values,
		// and JSON has one number type, so every id arrives here as float64.
		// Individual handlers grew their own whole-number guards for this
		// (commsFieldInt, commsCheckTargetID, resolveProcessID); the shared
		// reader every other tool uses did not have one.
		if val != math.Trunc(val) {
			return 0, fmt.Errorf("argument %s must be a whole number, got %v", key, val)
		}
		return int(val), nil
	case int:
		return val, nil
	case string:
		n, err := strconv.Atoi(val)
		if err != nil {
			return 0, fmt.Errorf("argument %s must be an integer, got: %s", key, val)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("argument %s has unexpected type %T", key, v)
	}
}

// strArg extracts a string argument from args map.
func strArg(args map[string]interface{}, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string, got %T", key, v)
	}
	return s, nil
}

// optStrArg returns the string value of an optional argument, or "" if absent.
func optStrArg(args map[string]interface{}, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// resolveProcessPath resolves an optional process_path argument.
// If the argument is empty, it searches the current directory for a single
// .conv.json file and returns its path. The supplied path is confined to the
// workspace tree (see confineToWorkdir) so a prompt-injected tool call can't
// reach files outside the project root.
func resolveProcessPath(args map[string]interface{}, key string) (string, error) {
	p := optStrArg(args, key)
	if p != "" {
		safe, err := confineToWorkdir(p)
		if err != nil {
			return "", err
		}
		return safe, nil
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		return "", fmt.Errorf("cannot read current directory: %v", err)
	}
	var matches []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".conv.json") {
			matches = append(matches, e.Name())
		}
	}
	if len(matches) == 1 {
		// Auto-discovery used to hand this back unchecked, which made the
		// zero-argument call the weakest way in: os.ReadDir reports a symlink
		// as a regular entry, so "999_x.conv.json" linked at somebody else's
		// file matched, and the handler wrote through it. An explicit
		// process_path naming that same link is rejected, so the implicit
		// route has no business being more permissive.
		safe, err := confineToWorkdir(matches[0])
		if err != nil {
			return "", err
		}
		return safe, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple .conv.json files found in current directory — pass process_path explicitly: %v", matches)
	}
	return "", fmt.Errorf("no .conv.json file found in current directory and process_path was not provided")
}

// resolveProcessID resolves the target process ID from either an explicit
// convIDKey argument or a pathKey argument (a local .conv.json file whose
// filename encodes the ID, resolved the same way as resolveProcessPath).
//
// convIDKey is the only route that needs no local file at all: hosts with no
// local process repository (e.g. an AI console with no filesystem, or a
// process that was never pulled) can still call the tool by passing the
// numeric Corezoid process ID directly, with no preceding pull-process.
//
// Supplying both is rejected rather than silently preferring one: several of
// these tools are destructive (e.g. delete-snapshot), so a caller pointing at
// two different processes by mistake must be told, not have one argument
// quietly ignored.
//
// filePath is "" when convIDKey was used — callers must not stat or read it
// in that case. On failure, errMsg names both accepted arguments so a caller
// that supplied neither gets a self-explanatory error instead of one that
// only mentions the file-based path.
func resolveProcessID(args map[string]interface{}, pathKey, convIDKey string) (procID int, filePath string, errMsg string) {
	raw, hasID := args[convIDKey]
	// A client that always serializes declared-but-unset optional fields as
	// "" or null (some MCP hosts do) must not trip the both-given conflict
	// below — optStrArg already treats "" and null as absent everywhere else
	// pathKey is read, so the presence check has to agree with that.
	hasID = hasID && raw != nil
	hasPath := optStrArg(args, pathKey) != ""
	if hasID && hasPath {
		return 0, "", fmt.Sprintf(
			"Error: pass either %s or %s, not both — got both arguments and cannot tell which one to trust.",
			pathKey, convIDKey)
	}

	if hasID {
		if f, isFloat := raw.(float64); isFloat && f != math.Trunc(f) {
			return 0, "", fmt.Sprintf("Error: %s must be a whole number, got %v", convIDKey, f)
		}
		id, err := intArg(args, convIDKey)
		if err != nil {
			return 0, "", "Error: " + err.Error()
		}
		if id <= 0 {
			return 0, "", fmt.Sprintf("Error: %s must be greater than zero", convIDKey)
		}
		return id, "", ""
	}

	fp, err := resolveProcessPath(args, pathKey)
	if err != nil {
		return 0, "", fmt.Sprintf(
			"Error: %v. Pass either %s (a local .conv.json file) or %s (the numeric Corezoid process ID — use this when there is no local process repository).",
			err, pathKey, convIDKey)
	}
	id, msg := extractProcessIDFromPath(fp)
	if msg != "" {
		return 0, "", msg
	}
	return id, fp, ""
}

// resolveDirPath returns the path argument confined to the workspace tree,
// or "." if the argument is absent. Returns "." and logs a warning if the
// supplied path escapes the workspace — directory handlers (create-process,
// create-folder) can't surface a typed error through resolveDirPath's
// signature, so the caller's subsequent resolveFolderIDFromDir(".") will
// fail naturally if cwd has no folder marker, which is the same error
// the user would see for any unrecognised directory.
func resolveDirPath(args map[string]interface{}, key string) string {
	p := optStrArg(args, key)
	if p == "" {
		return "."
	}
	safe, err := confineToWorkdir(p)
	if err != nil {
		logger.Warn("resolveDirPath: rejected path %q: %v — falling back to cwd", p, err)
		return "."
	}
	return safe
}

// boolishArg reads a boolean argument the way callers actually send it.
//
// The schema says boolean and JSON has the type, but the CLI passes every
// argument as a string, and hosts and models do send "true" for a declared
// boolean. A bare args[key].(bool) asserts those to FALSE and runs the call
// with the flag off — which is how modify-task{deep_merge:"true"} used to
// shallow-replace a task and drop every key it did not send, silently.
//
// Only the unambiguous affirmatives ("true", "1") count as true: anything
// else, including a value nobody can read as a boolean, stays false. That
// keeps the failure pointing at the safe side for the waiver flags (force,
// allow_no_snapshot, apply), where a wrong guess in the other direction would
// bypass a gate rather than leave it standing.
//
// Every handler reads boolean arguments through this (or through
// strictBoolArg / requiredBoolArg, which share parseBoolish so the three
// cannot drift apart), and TestNoDirectBooleanArgAssertions keeps it that way.
func boolishArg(args map[string]interface{}, key string) bool {
	v, _ := parseBoolish(args[key])
	return v
}

// parseBoolish is the single reading of a boolean value; the three callers
// differ only in what they do when ok is false.
//
// It exists because they had drifted: boolishArg read " true" as false while
// requiredBoolArg read it as true, and a host that serialises booleans as the
// JSON numbers 0/1 got false from both — for deep_merge that silently meant a
// shallow write. The accepted spellings are exactly the ones the CHANGELOG
// promises, in every form the value can arrive in: the bool itself, the string
// the CLI and some hosts send, and the number.
//
// ok=false means "present but unreadable as a boolean". Nothing beyond the
// listed spellings is guessed at — "yes" and 2 are not affirmatives — so a
// caller that treats !ok as false lands on the safe side of a waiver flag.
func parseBoolish(raw interface{}) (value, ok bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	case float64: // JSON numbers decode as float64
		switch v {
		case 1:
			return true, true
		case 0:
			return false, true
		}
	case int:
		switch v {
		case 1:
			return true, true
		case 0:
			return false, true
		}
	}
	return false, false
}

// strictBoolArg reads an optional flag whose FALSE direction is destructive.
//
// boolishArg answers false for anything it cannot read, which is right when
// false just leaves a gate standing. It is wrong for modify-task's deep_merge:
// there false is a shallow write that drops every nested key the caller did
// not send. Absent still means the documented default (false, shallow), but a
// value that is present and unreadable — deep_merge:"yes" — is refused rather
// than silently taken as the destructive answer.
func strictBoolArg(args map[string]interface{}, key string) (bool, string) {
	raw, given := args[key]
	if !given || raw == nil {
		return false, ""
	}
	if v, ok := parseBoolish(raw); ok {
		return v, ""
	}
	return false, fmt.Sprintf("Error: %q must be a boolean (true or false); got %#v.", key, raw)
}

// requiredBoolArg reads a boolean argument that has no safe default.
//
// boolishArg answers false for anything it cannot read, which is right for an
// optional flag — the call proceeds with the flag off. It is wrong where false
// is itself an instruction: set-stage-immutable{immutable:"maybe"} would make
// a stage editable on the strength of a typo. So here an unreadable value is
// refused, and the message says what the two accepted spellings are.
func requiredBoolArg(args map[string]interface{}, key string) (bool, string) {
	raw, given := args[key]
	if !given || raw == nil {
		return false, fmt.Sprintf("Error: %q (boolean) is required.", key)
	}
	if v, ok := parseBoolish(raw); ok {
		return v, ""
	}
	return false, fmt.Sprintf("Error: %q must be a boolean (true or false); got %T.", key, raw)
}

// docIntField reads an integer field out of a DECODED JSON document, applying
// the same two rules intArg applies to a tool argument: a quoted "834936" is
// the id it spells, and a non-integral 1234.9 is refused instead of being
// truncated to 1234 — a different, existing process.
//
// Absent (missing, null, or an empty string) is not an error: the caller
// decides whether the field is required. Present-but-unreadable IS, because
// the alternative is what materializeProcessFile used to do — read only
// float64, leave everything else at 0, and treat that 0 as "no id given",
// which skipped the very guard that stops one process's body being written
// over another's file.
func docIntField(doc map[string]interface{}, key string) (int, error) {
	raw, present := doc[key]
	if !present || raw == nil {
		return 0, nil
	}
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("%s must be a whole number, got %v", key, v)
		}
		return int(v), nil
	case int:
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer, got %q", key, v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s has unexpected type %T", key, raw)
	}
}
