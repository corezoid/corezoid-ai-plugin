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

// TestToolRegistryMatchesREADME verifies every tool name in toolRegistry
// appears in the root README.md MCP tools table.
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

	for _, tool := range toolRegistry {
		// README uses backtick-quoted tool names in the table
		if !strings.Contains(content, "`"+tool.Name+"`") {
			t.Errorf("tool %q is in toolRegistry but missing from README.md MCP tools table", tool.Name)
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
