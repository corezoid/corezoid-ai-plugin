package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

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
// .conv.json file and returns its path.
func resolveProcessPath(args map[string]interface{}, key string) (string, error) {
	p := optStrArg(args, key)
	if p != "" {
		return p, nil
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

// resolveDirPath returns the path argument or "." if absent.
func resolveDirPath(args map[string]interface{}, key string) string {
	p := optStrArg(args, key)
	if p == "" {
		return "."
	}
	return p
}
