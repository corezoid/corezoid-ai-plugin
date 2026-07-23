package main

import (
	"fmt"
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
	return p, nil
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
	rel, err := filepath.Rel(cwdReal, filepath.Join(dirReal, filepath.Base(p)))
	if err != nil {
		return "", fmt.Errorf("cannot relativize path: %v", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path resolves outside the working directory")
	}
	return rel, nil
}

// resolveFolderIDFromDir looks for a file matching <id>_<name>.folder.json or
// <id>_<name>.stage.json in the given directory and returns the numeric id.
func resolveFolderIDFromDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("failed to read directory '%s': %v", dir, err)
	}
	reFolderFile := regexp.MustCompile(`^(\d+)_.*\.(folder|stage)\.json$`)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := reFolderFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		id, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		return id, nil
	}
	return 0, fmt.Errorf("no <id>_<name>.folder.json file found in '%s'; cannot determine folder ID", dir)
}

// intArg extracts an integer argument from args map.
func intArg(args map[string]interface{}, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing required argument: %s", key)
	}
	switch val := v.(type) {
	case float64:
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
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple .conv.json files found in current directory — pass process_path explicitly: %v", matches)
	}
	return "", fmt.Errorf("no .conv.json file found in current directory and process_path was not provided")
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
