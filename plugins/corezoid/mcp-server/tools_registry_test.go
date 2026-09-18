package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestToolRegistryNoDuplicates verifies there are no duplicate tool names.
func TestToolRegistryNoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, tool := range toolRegistry {
		if seen[tool.Name] {
			t.Errorf("duplicate tool name in registry: %q", tool.Name)
		}
		seen[tool.Name] = true
	}
}

// TestToolRegistryMatchesREADME verifies every callable name — advertised
// tool, router, and router action — appears in the root README.md MCP tools
// section. Collapsing a tool took it out of tools/list, not out of the
// product: an action nobody documents is one nobody can discover.
func TestToolRegistryMatchesREADME(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine source file path")
	}
	// mcp-server/ → plugins/corezoid/ → plugins/ → repo root
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	readmePath := filepath.Join(repoRoot, "README.md")

	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("could not read README.md: %v", err)
	}
	content := string(data)

	documented := func(kind, name string) {
		// README uses backtick-quoted names in the tables
		if !strings.Contains(content, "`"+name+"`") {
			t.Errorf("%s %q is callable but missing from the README.md MCP tools section", kind, name)
		}
	}
	for _, tool := range toolRegistry {
		documented("tool", tool.Name)
	}
	for _, r := range toolRouters {
		for _, a := range r.Actions {
			documented("action", a.Action)
		}
	}
}

// TestSkillPathsExist verifies that every ${CLAUDE_PLUGIN_ROOT}/... path
// referenced in skills SKILL.md files actually exists on disk.
func TestSkillPathsExist(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine source file path")
	}
	// mcp-server/ is inside plugins/corezoid/
	pluginRoot := filepath.Join(filepath.Dir(thisFile), "..")

	skillsDir := filepath.Join(pluginRoot, "skills")
	re := regexp.MustCompile(`\$\{CLAUDE_PLUGIN_ROOT\}/([^\s'")\]` + "`" + `]+)`)

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("could not read skills dir: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue // skip skills without SKILL.md
		}

		matches := re.FindAllSubmatch(data, -1)
		for _, m := range matches {
			relPath := string(m[1])
			// Strip URL fragment (#anchor) — the test checks file existence,
			// not whether a specific heading exists inside the file.
			if idx := strings.Index(relPath, "#"); idx >= 0 {
				relPath = relPath[:idx]
			}
			if relPath == "" {
				continue
			}
			absPath := filepath.Join(pluginRoot, relPath)
			if _, err := os.Stat(absPath); os.IsNotExist(err) {
				t.Errorf("%s/SKILL.md: references non-existent path ${CLAUDE_PLUGIN_ROOT}/%s (resolved: %s)",
					entry.Name(), relPath, absPath)
			}
		}
	}
}
