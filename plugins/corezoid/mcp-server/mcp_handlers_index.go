package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// handleBuildProjectIndex builds the .corezoid/ artefacts for the project
// rooted at the current working directory (or an explicit project_path).
// Local-file-only, no auth needed — see noAuthTools registration in
// mcp_handlers.go.
//
// Mode selection:
//   - "check"  → run CheckIndexFreshness only, no rebuild
//   - default  → full rebuild (writes project-map.json, QUERIES.md, updates
//     CLAUDE.md, creates index-config.json if missing)
func handleBuildProjectIndex(ctx context.Context, args map[string]interface{}) (string, bool) {
	projectPath := optStrArg(args, "project_path")
	if projectPath != "" {
		safe, err := confineToWorkdir(projectPath)
		if err != nil {
			return "Error: " + err.Error(), true
		}
		projectPath = safe
	} else {
		projectPath = "."
	}
	absRoot, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Sprintf("Error: cannot resolve absolute path: %v", err), true
	}

	mode := strings.ToLower(optStrArg(args, "mode"))
	if mode == "check" {
		rpt, err := CheckIndexFreshness(absRoot)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true
		}
		return FormatStaleReport(rpt), false
	}

	// Explicit build-project-index calls (via the corezoid-index skill or
	// a manual MCP call) are the natural place to refresh online task
	// contents — they're user-initiated, not part of a hot loop.
	pm, warnings, err := BuildProjectIndex(ctx, absRoot)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), true
	}
	if err := WriteProjectMap(absRoot, pm); err != nil {
		return fmt.Sprintf("Error writing project-map.json: %v", err), true
	}
	if err := WriteQueriesMD(absRoot); err != nil {
		return fmt.Sprintf("Error writing QUERIES.md: %v", err), true
	}
	firstCreation, err := UpdateClaudeMD(absRoot, pm)
	if err != nil {
		return fmt.Sprintf("Error updating CLAUDE.md: %v", err), true
	}

	return formatBuildResult(absRoot, pm, warnings, firstCreation), false
}

// formatBuildResult produces the human-readable summary the tool returns to
// the MCP client. Kept short (≤ ~15 lines) — details live in project-map.json.
// The "first creation" line is present only when the CLAUDE.md block was
// created for the first time in this project, per TZ §6.2 social-contract
// requirement (mutating a user file silently is a surprise even when the
// mutation is technically correct).
func formatBuildResult(root string, pm *ProjectMap, warnings []string, firstCreation bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Project index built at %s\n", filepath.Join(root, IndexOutputDir))
	fmt.Fprintf(&sb, "  Processes: %d (state stores: %d, instances: %d)\n",
		pm.ProcessCount, pm.StateStoreCount, pm.InstanceCount)
	fmt.Fprintf(&sb, "  Env variables: %d\n", len(pm.EnvVars))
	fmt.Fprintf(&sb, "  Edges: %d, external APIs: %d\n", len(pm.Edges), len(pm.ExternalAPIs))
	if pm.GraphStats != nil {
		if n := len(pm.GraphStats.HighFanIn); n > 0 {
			fmt.Fprintf(&sb, "  High fan-in: %d process(es)\n", n)
		}
		if n := len(pm.GraphStats.Cycles); n > 0 {
			fmt.Fprintf(&sb, "  Cycles: %d\n", n)
		}
		if n := len(pm.GraphStats.Orphaned); n > 0 {
			fmt.Fprintf(&sb, "  Orphaned candidates: %d (heuristic — verify before deletion)\n", n)
		}
	}
	if n := len(pm.SecurityHotspots); n > 0 {
		fmt.Fprintf(&sb, "  Security hotspots: %d (field names + locations only, no values)\n", n)
	}
	if n := len(pm.ConfigReferences); n > 0 {
		// Count refs that resolve to a local state-store (informative:
		// "these are backed by state we can see") vs. purely external
		// ones ("look elsewhere for the values"). Both are legitimate.
		local, external := 0, 0
		for _, e := range pm.ConfigReferences {
			if e == nil {
				continue
			}
			if e.LocalConvID != "" {
				local++
			} else {
				external++
			}
		}
		fmt.Fprintf(&sb, "  Config references: %d used (local state-store: %d, external: %d)\n",
			n, local, external)
	}
	if firstCreation {
		fmt.Fprintf(&sb, "\n"+
			"Added a short auto-generated section to CLAUDE.md between "+
			"<!-- %s:start --> / <!-- %s:end -->; content outside those markers is untouched.\n",
			IndexClaudeMdMarker, IndexClaudeMdMarker)
		// Also mention gitignore hygiene once, at first creation.
		if hint := gitignoreHint(root); hint != "" {
			sb.WriteString(hint)
		}
	}
	if len(warnings) > 0 {
		sb.WriteString("\nWarnings:\n")
		for _, w := range warnings {
			fmt.Fprintf(&sb, "  - %s\n", w)
		}
	}
	sb.WriteString("\nQuery recipes: .corezoid/QUERIES.md")
	return sb.String()
}

// autoRebuildIndex runs BuildProjectIndex + writers in a project root
// non-fatally: any error becomes a one-line warning appended to the caller's
// tool output, never a hard failure of the surrounding operation. Meant to
// be called at the end of pull-folder and push-process handlers so the index
// stays fresh even when the user bypasses corezoid-init / corezoid-edit /
// corezoid-create and calls those tools directly. This is the code-level
// counterpart of the SKILL.md "Step 3: Build the project index" — reliable
// where a prompt instruction is not.
//
// If projectRoot is "." or empty, resolves against the current working
// directory. If the directory contains no .conv.json files (e.g. the caller
// pulled a single process into an empty tree), the tool still runs and
// produces an index with process_count=0 — harmless.
//
// Returns a short status suffix to append to the caller's output. Empty
// string on success (silence). "\n\n" prefix keeps formatting clean.
//
func autoRebuildIndex(ctx context.Context, projectRoot string) string {
	if projectRoot == "" {
		projectRoot = "."
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		logger.Warn("auto-rebuild index: cannot resolve %q: %v", projectRoot, err)
		return "\n\nWarning: could not build project index — " + err.Error()
	}

	pm, warnings, err := BuildProjectIndex(ctx, absRoot)
	if err != nil {
		logger.Warn("auto-rebuild index: build failed: %v", err)
		return "\n\nWarning: project index build failed (" + err.Error() +
			"). Run the corezoid-index skill to retry."
	}
	if err := WriteProjectMap(absRoot, pm); err != nil {
		logger.Warn("auto-rebuild index: write project-map.json failed: %v", err)
		return "\n\nWarning: could not write project-map.json — " + err.Error()
	}
	if err := WriteQueriesMD(absRoot); err != nil {
		logger.Warn("auto-rebuild index: write QUERIES.md failed: %v", err)
		return "\n\nWarning: could not write QUERIES.md — " + err.Error()
	}
	firstCreation, err := UpdateClaudeMD(absRoot, pm)
	if err != nil {
		logger.Warn("auto-rebuild index: update CLAUDE.md failed: %v", err)
		return "\n\nWarning: could not update CLAUDE.md — " + err.Error()
	}

	var sb strings.Builder
	sb.WriteString("\n\nProject index refreshed at ")
	sb.WriteString(filepath.Join(absRoot, IndexOutputDir))
	sb.WriteString(fmt.Sprintf(" — processes: %d, edges: %d", pm.ProcessCount, len(pm.Edges)))
	if pm.GraphStats != nil {
		if n := len(pm.GraphStats.Cycles); n > 0 {
			sb.WriteString(fmt.Sprintf(", cycles: %d", n))
		}
		if n := len(pm.GraphStats.HighFanIn); n > 0 {
			sb.WriteString(fmt.Sprintf(", high fan-in: %d", n))
		}
	}
	if n := len(pm.SecurityHotspots); n > 0 {
		sb.WriteString(fmt.Sprintf(", security hotspots: %d", n))
	}
	sb.WriteString(".")
	if firstCreation {
		sb.WriteString("\n" +
			"Added an auto-generated section to CLAUDE.md between <!-- " + IndexClaudeMdMarker +
			":start --> / <!-- " + IndexClaudeMdMarker + ":end -->; content outside those markers is untouched.")
		if hint := gitignoreHint(absRoot); hint != "" {
			sb.WriteString(hint)
		}
	}
	if len(warnings) > 0 {
		sb.WriteString("\nIndex build warnings:")
		for _, w := range warnings {
			sb.WriteString("\n  - " + w)
		}
	}
	sb.WriteString("\nQuery recipes: " + filepath.Join(IndexOutputDir, IndexQueriesFile))
	return sb.String()
}

// gitignoreHint returns a one-off suggestion to gitignore the derived
// artefacts (project-map.json + QUERIES.md) — but explicitly NOT
// index-config.json (which embodies a team choice and should be committed).
// Only emits when the project has a .git directory (there's no point
// suggesting .gitignore if git isn't involved).
func gitignoreHint(root string) string {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return ""
	}
	return "\nTip: this project has a .git directory. Consider adding to `.gitignore`:\n" +
		"  " + IndexOutputDir + "/" + IndexMapFile + "\n" +
		"  " + IndexOutputDir + "/" + IndexQueriesFile + "\n" +
		"Do NOT gitignore " + IndexOutputDir + "/" + IndexConfigFile +
		" — it holds team-shared heuristic settings.\n"
}
