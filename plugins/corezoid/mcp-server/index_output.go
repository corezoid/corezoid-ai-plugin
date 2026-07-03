package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeAtomically writes data to path atomically via os.CreateTemp + Rename
// in the same directory. Using the same directory is required so that Rename
// is a filesystem-level move, not a cross-device copy. A fixed .tmp name
// was previously used — os.CreateTemp avoids races when two builds run
// concurrently (e.g. parallel tool calls in the same session).
func writeAtomically(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".idx-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // clean up if Rename was never reached
	}()
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// WriteProjectMap serialises the index to .corezoid/project-map.json in the
// given project root. Uses an atomic write-to-temp-then-rename so a crash
// mid-write cannot leave a truncated file that breaks the next build.
func WriteProjectMap(projectRoot string, pm *ProjectMap) error {
	dir := filepath.Join(projectRoot, IndexOutputDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pm, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomically(filepath.Join(dir, IndexMapFile), data, 0644)
}

// WriteQueriesMD emits the query cookbook next to project-map.json. The file
// is a deterministic template (no LLM), documenting every common lookup an
// agent will need. Kept short — the point is to be a command reference, not
// a project overview.
func WriteQueriesMD(projectRoot string) error {
	dir := filepath.Join(projectRoot, IndexOutputDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, IndexQueriesFile), []byte(queriesMDTemplate), 0644)
}

const queriesMDTemplate = "# Project Index Queries\n" +
	"\n" +
	"`.corezoid/project-map.json` is the source of truth for who calls what, which\n" +
	"env variables are used where, and which processes look risky to touch. Use\n" +
	"`jq` (or Python) to answer specific questions instead of loading the whole\n" +
	"file into context.\n" +
	"\n" +
	"Replace `<id>` with a numeric conv_id (as a string), `@name` with an alias.\n" +
	"\n" +
	"## 1. Who calls process `<id>`\n" +
	"\n" +
	"```\n" +
	"jq --arg id \"<id>\" '.calls_in[$id] // []' .corezoid/project-map.json\n" +
	"```\n" +
	"Returns the list of conv_ids that reference this process via `api_rpc`,\n" +
	"`api_copy`, or `api_get_task`. Empty list means either an entry point or an\n" +
	"orphaned candidate — cross-check `.graph_stats.entry_points` /\n" +
	"`.graph_stats.orphaned`.\n" +
	"\n" +
	"## 2. What does process `<id>` call\n" +
	"\n" +
	"```\n" +
	"jq --arg id \"<id>\" '[.edges[] | select(.from == $id)]' .corezoid/project-map.json\n" +
	"```\n" +
	"Each element carries `to`, `type`, `mode` (for `api_copy`), and `via_alias`.\n" +
	"Title of the target: `jq --arg id \"<to_id>\" '.processes[$id].title'`.\n" +
	"\n" +
	"## 3. Resolve an alias\n" +
	"\n" +
	"```\n" +
	"jq --arg a \"@name\" '.by_alias[$a]' .corezoid/project-map.json\n" +
	"```\n" +
	"Works with or without the leading `@`. `null` means the alias does not\n" +
	"resolve inside this project (possibly external or dynamic).\n" +
	"\n" +
	"## 4. Where is env variable `@name` used\n" +
	"\n" +
	"```\n" +
	"jq --arg n \"name\" '.env_vars[$n].used_by' .corezoid/project-map.json\n" +
	"```\n" +
	"The variable name is passed WITHOUT the leading `@`. Returns the list of\n" +
	"conv_ids that reference `{{env_var[@name]}}` anywhere in their logics. If the\n" +
	"variable is not listed at all, either it isn't in `_ENV_VARS_.json` and no\n" +
	"process references it, or the file wasn't part of the export.\n" +
	"\n" +
	"## 5. Does a process for X already exist (fuzzy title search)\n" +
	"\n" +
	"```\n" +
	"jq '.processes | to_entries | map(select(.value.title | test(\"payment\"; \"i\"))) |\n" +
	"    map({conv_id: .key, title: .value.title, path: .value.path})' \\\n" +
	"    .corezoid/project-map.json\n" +
	"```\n" +
	"Regexp is case-insensitive. Use before creating a new process — a hit here\n" +
	"often means the flow already exists somewhere in the tree.\n" +
	"\n" +
	"## 6. Full card for process `<id>`\n" +
	"\n" +
	"```\n" +
	"jq --arg id \"<id>\" '.processes[$id]' .corezoid/project-map.json\n" +
	"```\n" +
	"Includes title, location breadcrumb, aliases, external URLs called,\n" +
	"env-var references, node count, and hash (compare with actual file hash\n" +
	"to see if the index has drifted since last rebuild).\n" +
	"\n" +
	"## 7. What is risky to touch right now\n" +
	"\n" +
	"```\n" +
	"jq '.graph_stats | {cycles, high_fan_in, high_fan_out, orphaned}' \\\n" +
	"    .corezoid/project-map.json\n" +
	"```\n" +
	"`orphaned` is a heuristic — \"no internal caller in this project\", not a\n" +
	"diagnosis. Verify before deleting anything.\n" +
	"\n" +
	"## 8. External systems and detected secrets\n" +
	"\n" +
	"```\n" +
	"jq '{external_apis, instances, security_hotspots}' .corezoid/project-map.json\n" +
	"```\n" +
	"`security_hotspots` reports field NAMES and locations only — values are\n" +
	"never included, even partially. `source: instance` means a `.instance.json`\n" +
	"connector definition; `source: diagram` means a hardcoded-looking field in\n" +
	"an `api` node's `extra`/`extra_headers`/`url`.\n" +
	"\n" +
	"## 9. Rebuilding the index\n" +
	"\n" +
	"The index rebuilds automatically after `pull-folder` (via `corezoid-init`)\n" +
	"and after each successful `push-process` (via `corezoid-edit` /\n" +
	"`corezoid-create`). To rebuild manually — for example after editing\n" +
	"`.conv.json` files outside the plugin — run the `build-project-index` MCP\n" +
	"tool or invoke the `corezoid-index` skill.\n"

// UpdateClaudeMD writes/refreshes the auto-generated block in CLAUDE.md at
// the project root. Preserves user content outside the marker pair and does
// nothing if project-map.json is missing (index-driven artefact only makes
// sense when there is an index to summarise). Returns firstCreation=true when
// a fresh CLAUDE.md was created or when a pre-existing CLAUDE.md gained
// markers for the first time — callers use this to decide whether to notify
// the user (see TZ §6.2).
func UpdateClaudeMD(projectRoot string, pm *ProjectMap) (firstCreation bool, err error) {
	path := filepath.Join(projectRoot, "CLAUDE.md")
	block := renderClaudeBlock(pm)

	existing, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return false, readErr
	}
	var content string
	if errors.Is(readErr, os.ErrNotExist) {
		content = claudeMDScaffold + block + "\n"
		firstCreation = true
	} else {
		var hadMarkers bool
		content, hadMarkers = replaceAutoBlock(string(existing), block)
		firstCreation = !hadMarkers
	}
	return firstCreation, writeAtomically(path, []byte(content), 0644)
}

const claudeMDScaffold = "# Project Notes\n" +
	"\n" +
	"This file is loaded by Claude Code at the start of every session in this\n" +
	"project. Add your team's workflow rules, naming conventions, and pointers\n" +
	"to `.env` / credentials here — anything outside the auto-generated block\n" +
	"below is preserved across rebuilds.\n" +
	"\n"

// renderClaudeBlock produces the marker-delimited summary that gets injected
// into CLAUDE.md. Must stay short (~30–50 lines total for the whole file,
// per TZ §6.2) — CLAUDE.md is loaded whole into every session and cannot
// grow linearly with project size.
func renderClaudeBlock(pm *ProjectMap) string {
	var sb strings.Builder
	sb.WriteString("<!-- ")
	sb.WriteString(IndexClaudeMdMarker)
	sb.WriteString(":start -->\n")
	sb.WriteString("## Corezoid project index (auto-generated)\n")
	sb.WriteString(fmt.Sprintf("- **Processes:** %d (state stores: %d, instances: %d, env variables: %d)\n",
		pm.ProcessCount, pm.StateStoreCount, pm.InstanceCount, len(pm.EnvVars)))
	if pm.StageID != 0 {
		sb.WriteString(fmt.Sprintf("- **Stage ID:** %d\n", pm.StageID))
	}
	sb.WriteString("- **Full data:** `.corezoid/project-map.json` — query with `jq`, recipes in `.corezoid/QUERIES.md`.\n")

	if pm.GraphStats != nil {
		if len(pm.GraphStats.HighFanIn) > 0 {
			shown := pm.GraphStats.HighFanIn
			if len(shown) > 2 {
				shown = shown[:2]
			}
			var pairs []string
			for _, cid := range shown {
				if p, ok := pm.Processes[cid]; ok {
					pairs = append(pairs, fmt.Sprintf("%s (%s)", cid, p.Title))
				} else {
					pairs = append(pairs, cid)
				}
			}
			extra := ""
			if len(pm.GraphStats.HighFanIn) > 2 {
				extra = fmt.Sprintf(" (+%d more)", len(pm.GraphStats.HighFanIn)-2)
			}
			sb.WriteString(fmt.Sprintf("- **High fan-in (touch carefully):** %s%s\n",
				strings.Join(pairs, ", "), extra))
		}
		if n := len(pm.GraphStats.Cycles); n > 0 {
			sb.WriteString(fmt.Sprintf("- **Cycles detected:** %d — see `.graph_stats.cycles` in project-map.json.\n", n))
		}
		if n := len(pm.GraphStats.Orphaned); n > 0 {
			sb.WriteString(fmt.Sprintf("- **Orphaned candidates:** %d — verify before deletion (heuristic, not diagnosis).\n", n))
		}
	}
	if n := len(pm.SecurityHotspots); n > 0 {
		sb.WriteString(fmt.Sprintf("- **Security hotspots:** %d — see `.security_hotspots` (field names + locations only, no values).\n", n))
	}
	sb.WriteString(fmt.Sprintf("- **Generated:** %s\n", pm.GeneratedAt))
	sb.WriteString("<!-- ")
	sb.WriteString(IndexClaudeMdMarker)
	sb.WriteString(":end -->")
	return sb.String()
}

// replaceAutoBlock swaps or appends the marker-delimited auto-block. Returns
// (updatedContent, markersFoundAndReplaced). The behaviour matrix (TZ §12):
//   - both markers present in order  → replace
//   - both markers absent            → append at end, hadMarkers=false
//   - only start marker present      → treat as no markers (append fresh)
//   - only end marker present        → treat as no markers (append fresh)
//   - markers reversed               → treat as no markers (append fresh)
//
// Rationale: attempting to "fix" broken markers is guessing — either the file
// was hand-edited (in which case a fresh block is the safer choice) or the
// previous run crashed mid-write (same conclusion). Never modify content
// outside a well-formed marker pair.
func replaceAutoBlock(content, block string) (string, bool) {
	startTag := "<!-- " + IndexClaudeMdMarker + ":start -->"
	endTag := "<!-- " + IndexClaudeMdMarker + ":end -->"
	startIdx := strings.Index(content, startTag)
	endIdx := strings.Index(content, endTag)
	if startIdx >= 0 && endIdx > startIdx {
		before := content[:startIdx]
		after := content[endIdx+len(endTag):]
		// Trim exactly one leading newline in `after` if present so we don't
		// accumulate blank lines on repeated rebuilds.
		if strings.HasPrefix(after, "\n") {
			after = after[1:]
		}
		return before + block + "\n" + after, true
	}
	// Malformed or absent markers — append fresh block after ensuring
	// exactly one blank line of separation.
	sep := ""
	if content != "" && !strings.HasSuffix(content, "\n\n") {
		if strings.HasSuffix(content, "\n") {
			sep = "\n"
		} else {
			sep = "\n\n"
		}
	}
	return content + sep + block + "\n", false
}
