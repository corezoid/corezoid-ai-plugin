#!/bin/sh
# install-kiro.sh — set up or package the corezoid plugin for AWS Kiro.
#
# Usage:
#   plugins/corezoid/scripts/install-kiro.sh [workspace-dir]
#     Install into an existing Kiro workspace (default mode). Creates the
#     following under <workspace>/.kiro/:
#       settings/mcp.json        ← generated from .mcp.kiro.json with PLUGIN_ROOT
#                                  resolved to an absolute path
#       steering/<name>.md       ← symlinked from this plugin's steering/
#       skills/<name>/SKILL.md   ← HARD-COPIED with $CLAUDE_PLUGIN_ROOT resolved
#                                  to the absolute plugin path
#     workspace-dir defaults to $KIRO_WORKSPACE_DIR or $PWD.
#
#   plugins/corezoid/scripts/install-kiro.sh --power [output-dir]
#     Build a portable, importable Kiro Power bundle instead of installing
#     into a workspace (Powers panel → Add Custom Power → Import from
#     folder). Generates:
#       POWER.md            (metadata + onboarding + steering routing table)
#       mcp.json             (copy of .mcp.kiro.json, left UNresolved — its
#                             ${KIRO_PLUGIN_ROOT:-$PWD} probe-then-append
#                             fallback resolves PLUGIN_ROOT at runtime
#                             instead, since this bundle can land at any
#                             path on any machine)
#       steering/<name>.md   (one per skill, frontmatter stripped,
#                             $CLAUDE_PLUGIN_ROOT resolved to a relative
#                             path since docs/ ships alongside), plus
#                             steering/corezoid-guardrails.md (the plugin's
#                             own always-on rules file, renamed on copy to
#                             avoid colliding with the "corezoid" skill's
#                             own steering/corezoid.md)
#       docs/                (copied for reference resolution)
#     Also syncs VERSION across the other plugin manifests + repo-root
#     POWER.md. output-dir defaults to <repo-root>/power-corezoid/.
#
# Why hard-copy and resolve the token, instead of symlinking the source files
# the way Claude Code / Codex consume them?
#   - The token `$CLAUDE_PLUGIN_ROOT` is a host-side text substitution Claude
#     Code performs at skill-load time (anthropics/claude-code#48230 etc.).
#     Kiro does no such substitution — so the literal `$CLAUDE_PLUGIN_ROOT`
#     would survive into the model context, leaving every reference-doc path
#     as a dead string. Resolving the token at build/install time fixes that.
#   - Workspace-install mode resolves to an *absolute* path, since it's tied
#     to one clone on one machine. Power-build mode resolves to a *relative*
#     path, since the bundle must work wherever it's imported.
#   - Symlinked skills would re-introduce the unresolved token on every read,
#     which is why workspace-install mode hard-copies skills but can still
#     symlink steering/ (small, stable, no token to resolve).
#
# `sed -i` portability is handled with the `-i.bak`+`find -delete` two-step
# (works under both GNU sed on Linux and BSD sed on macOS without branching).
#
# Idempotent — safe to run repeatedly; each mode regenerates its output from
# scratch.

set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$PLUGIN_ROOT/../.." && pwd)"
SKILLS_DIR="$PLUGIN_ROOT/skills"

# ─── Mode: install into an existing Kiro workspace ──────────────────────────

run_workspace_install() {
  WORKSPACE="${1:-${KIRO_WORKSPACE_DIR:-$PWD}}"
  KIRO_DIR="$WORKSPACE/.kiro"

  if [ ! -d "$WORKSPACE" ]; then
    echo "ERROR: workspace dir not found: $WORKSPACE" >&2
    exit 1
  fi

  mkdir -p "$KIRO_DIR/settings" "$KIRO_DIR/steering" "$KIRO_DIR/skills"

  # 1) MCP entry — sed-substitute the ${KIRO_PLUGIN_ROOT:-...} placeholder in
  #    .mcp.kiro.json with the resolved absolute PLUGIN_ROOT. This keeps
  #    settings/mcp.json and the checked-in template in lock-step: the MCP
  #    command/args live in one place, and the server starts without needing
  #    KIRO_PLUGIN_ROOT in the environment.
  sed "s#\${KIRO_PLUGIN_ROOT:-\$PWD}#$PLUGIN_ROOT#" \
    "$PLUGIN_ROOT/.mcp.kiro.json" > "$KIRO_DIR/settings/mcp.json"

  # 2) Steering — small, stable, no token substitution needed. Symlink on
  #    POSIX, hard-copy on Windows shells.
  case "$(uname -s 2>/dev/null || echo Unknown)" in
    MINGW*|CYGWIN*|MSYS*) STEERING_LINK="cp -R" ;;
    *)                    STEERING_LINK="ln -sfn" ;;
  esac
  for f in "$PLUGIN_ROOT"/steering/*.md; do
    [ -f "$f" ] || continue
    $STEERING_LINK "$f" "$KIRO_DIR/steering/$(basename "$f")"
  done

  # 3) Skills — HARD-COPY then sed-substitute $CLAUDE_PLUGIN_ROOT in every .md
  #    so reference-doc paths resolve to the absolute plugin dir under Kiro.
  #    Handles both `${CLAUDE_PLUGIN_ROOT}` (braced) and `$CLAUDE_PLUGIN_ROOT`
  #    (unbraced) forms in a single sed invocation.
  for d in "$SKILLS_DIR"/*/; do
    [ -d "$d" ] || continue
    name="$(basename "$d")"
    dst="$KIRO_DIR/skills/$name"
    rm -rf "$dst"
    cp -R "$d" "$dst"
    # `#` delimiter avoids escaping the `/` inside $PLUGIN_ROOT. Backup suffix
    # is the portable two-step for GNU and BSD sed.
    find "$dst" -name '*.md' -type f -exec \
      sed -i.bak \
        -e "s#\\\${CLAUDE_PLUGIN_ROOT}#$PLUGIN_ROOT#g" \
        -e "s#\\\$CLAUDE_PLUGIN_ROOT#$PLUGIN_ROOT#g" {} +
    find "$dst" -name '*.md.bak' -type f -delete
  done

  echo "Installed corezoid plugin into: $KIRO_DIR"
  echo "Open this workspace in Kiro and the corezoid MCP server, skills, and steering will be picked up."
  echo "Reference-doc paths in skills were resolved to: $PLUGIN_ROOT"
}

# ─── Mode: build a portable Kiro Power bundle ───────────────────────────────

run_power_build() {
  OUTPUT_DIR="${1:-$REPO_ROOT/power-corezoid}"

  VERSION=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$PLUGIN_ROOT/.claude-plugin/plugin.json" | head -1)
  if [ -z "$VERSION" ]; then
    echo "ERROR: could not read version from .claude-plugin/plugin.json" >&2
    exit 1
  fi

  rm -rf "$OUTPUT_DIR"
  mkdir -p "$OUTPUT_DIR/steering"

  # MCP entry — reuse .mcp.kiro.json verbatim. Unlike workspace-install mode,
  # this bundle is portable (imported into any Kiro workspace via Powers
  # panel → Add Custom Power → Import from folder), so the
  # ${KIRO_PLUGIN_ROOT:-$PWD} probe-then-append fallback is left unresolved —
  # it resolves PLUGIN_ROOT at runtime, wherever the bundle actually lands.
  cp "$PLUGIN_ROOT/.mcp.kiro.json" "$OUTPUT_DIR/mcp.json"

  # Plugin's own always-on guardrails file — renamed on copy since the
  # "corezoid" skill below would otherwise also produce steering/corezoid.md.
  if [ -f "$PLUGIN_ROOT/steering/corezoid.md" ]; then
    awk '
      BEGIN { count=0 }
      /^---$/ { count++; next }
      count >= 2 { print }
    ' "$PLUGIN_ROOT/steering/corezoid.md" > "$OUTPUT_DIR/steering/corezoid-guardrails.md"
  fi

  # Convert each skill to a steering file
  skill_count=0
  for d in "$SKILLS_DIR"/*/; do
    [ -d "$d" ] || continue
    [ -f "$d/SKILL.md" ] || continue
    skill_count=$((skill_count + 1))

    name=$(basename "$d")
    steering_file="$OUTPUT_DIR/steering/${name}.md"

    # Extract body (everything after closing ---) and write as steering
    # file. Replace $CLAUDE_PLUGIN_ROOT with relative docs path — handles
    # both ${CLAUDE_PLUGIN_ROOT} (braced) and $CLAUDE_PLUGIN_ROOT (unbraced)
    # forms; corezoid skills use the braced form throughout.
    awk '
      BEGIN { count=0 }
      /^---$/ { count++; next }
      count >= 2 { print }
    ' "$d/SKILL.md" | sed \
      -e "s#\\\${CLAUDE_PLUGIN_ROOT}/docs#docs#g" \
      -e "s#\\\$CLAUDE_PLUGIN_ROOT/docs#docs#g" \
      -e "s#\\\${CLAUDE_PLUGIN_ROOT}#.#g" \
      -e "s#\\\$CLAUDE_PLUGIN_ROOT#.#g" > "$steering_file"

    # Append reference files inline if they exist
    if [ -d "$d/references" ]; then
      for ref in "$d/references"/*.md; do
        [ -f "$ref" ] || continue
        ref_name=$(basename "$ref")
        printf '\n---\n\n## Reference: %s\n\n' "$ref_name" >> "$steering_file"
        sed \
          -e "s#\\\${CLAUDE_PLUGIN_ROOT}/docs#docs#g" \
          -e "s#\\\$CLAUDE_PLUGIN_ROOT/docs#docs#g" \
          -e "s#\\\${CLAUDE_PLUGIN_ROOT}#.#g" \
          -e "s#\\\$CLAUDE_PLUGIN_ROOT#.#g" "$ref" >> "$steering_file"
      done
    fi

    # Append template files inline if they exist
    for tmpl in "$d"/template.*; do
      [ -f "$tmpl" ] || continue
      tmpl_name=$(basename "$tmpl")
      ext="${tmpl_name##*.}"
      printf '\n---\n\n## Template: %s\n\n```%s\n' "$tmpl_name" "$ext" >> "$steering_file"
      cat "$tmpl" >> "$steering_file"
      printf '\n```\n' >> "$steering_file"
    done
  done

  # Copy docs/ for reference resolution
  if [ -d "$PLUGIN_ROOT/docs" ]; then
    cp -R "$PLUGIN_ROOT/docs" "$OUTPUT_DIR/docs"
  fi

  # Generate POWER.md
  cat > "$OUTPUT_DIR/POWER.md" << FRONTMATTER
---
name: "corezoid"
displayName: "Corezoid"
version: "$VERSION"
description: "Corezoid BPM platform assistant. Exposes the Corezoid REST API as MCP tools (\`convctl\`) plus $skill_count steering files covering process creation, editing, review, validation, dashboards, state diagrams, variables, access, and stage-export scanning. Ships JSON schemas and per-node-type documentation for all 24 Corezoid node types."
keywords: ["corezoid", "process", "bpm", "workflow", "automation", "mcp"]
author: "Corezoid"
---

# Corezoid Power

## Onboarding

### Step 1: Authenticate

Call the MCP tool \`login\` with no arguments. It walks through, in sequence:
1. Corezoid account URL
2. OAuth2 browser login (token saved to \`~/.corezoid/credentials\`)
3. Workspace selection (saved to \`.env\` as \`WORKSPACE_ID\`)
4. Project / stage selection (saved to \`.env\` as \`COREZOID_STAGE_ID\`)

If the client doesn't support MCP elicitation, drive the same sequence
manually with \`list-workspaces\` → \`list-projects\` → stage selection —
always let the user choose; never pick on their behalf.

### Step 2: Verify

Call \`list-workspaces\` — if it returns your workspace list, you're connected.

FRONTMATTER

  cat >> "$OUTPUT_DIR/POWER.md" << 'ROUTING'
## When to Load Steering Files

- Mandatory process-JSON validation rules, language policy, bug-report
  triggers — applies to every interaction, load first → `corezoid-guardrails.md`
- General Corezoid questions, MCP tool routing, platform overview → `corezoid.md`
- First-time setup, login, workspace/project/stage selection → `corezoid-init.md`
- Creating a new process from scratch → `corezoid-create.md`
- Modifying an existing process, adding/removing nodes, fixing logic → `corezoid-edit.md`
- Creating a new state diagram (`conv_type: "state"`) → `corezoid-state-diagram-create.md`
- Modifying an existing state diagram → `corezoid-state-diagram-edit.md`
- Auditing or reviewing a single process → `corezoid-review.md`
- Auditing a whole project / multiple processes → `corezoid-project-review.md`
- Tact reduction, node merging, resilience patterns → `corezoid-process-optimizer.md`
- Generating human-readable technical documentation for a process → `corezoid-process-tech-writer.md`
- Creating or editing dashboards, charts, process metrics → `corezoid-dashboard-manager.md`
- Sharing processes/folders, managing groups or API keys → `corezoid-access.md`
- Managing environment variables / `{{env_var[@name]}}` references → `corezoid-variable-manager.md`
- Creating, listing, or linking process aliases (`short_name`) → `corezoid-alias-manager.md`
- Building a Corezoid-API-calling connector process → `corezoid-api-connector.md`
- Writing custom code in a Git Call node (Python, Go, PHP, ...) → `corezoid-gitcall.md`
- Updating a process/folder/project description only → `corezoid-describe.md`
- Reporting a bug or improvement to the Corezoid team → `corezoid-feedback.md`
- Validating an exported stage `.zip` for merge errors → `corezoid-stage-scan.md`
- Capturing session learnings into CLAUDE.md / feedback / memory → `corezoid-retro.md`
- Pre-publish checklist before listing on the marketplace → `marketplace-publish-validation.md`

ROUTING

  cat >> "$OUTPUT_DIR/POWER.md" << 'FOOTER'
## MCP Tools (highlights)

| Tool | What it does |
|---|---|
| `login` | OAuth2 browser login; saves credentials to `~/.corezoid/`. |
| `pull-process` / `pull-folder` | Export a process / folder tree locally. |
| `push-process` | Validate and deploy a `.conv.json`. |
| `lint-process` | Static checks: orphaned nodes, noop conditions, unused params. |
| `run-task` | Execute a task on a deployed process. |
| `create-process` / `create-folder` / `create-variable` | Bootstrap resources. |
| `create-dashboard` / `add-chart` | Visualise node metrics. |
| `list-workspaces` / `list-projects` / `list-stages` | Workspace navigation. |
| `modify-task` / `delete-task` | Per-task ops on deployed processes. |
| `send-feedback` | Submit feedback/bug reports to the Corezoid team. |

FOOTER

  # Sync version across manifests
  sync_version() {
    if [ -f "$1" ]; then
      sed -i.bak "s/\"version\": *\"[^\"]*\"/\"version\": \"$VERSION\"/" "$1"
      rm -f "$1.bak"
    fi
  }

  sync_version "$PLUGIN_ROOT/.kiro-plugin/plugin.json"
  sync_version "$PLUGIN_ROOT/.codex-plugin/plugin.json"
  sync_version "$REPO_ROOT/.claude-plugin/marketplace.json"
  sync_version "$REPO_ROOT/.agents/plugins/marketplace.json"

  # Also update the repo-root POWER.md version + skill count if it exists
  if [ -f "$REPO_ROOT/POWER.md" ]; then
    sed -i.bak "s/^version: .*/version: $VERSION/" "$REPO_ROOT/POWER.md"
    sed -i.bak "s/plus [0-9]* skills/plus $skill_count skills/" "$REPO_ROOT/POWER.md"
    rm -f "$REPO_ROOT/POWER.md.bak"
  fi

  echo ""
  echo "✓ Kiro Power built"
  echo "  Output:   $OUTPUT_DIR"
  echo "  Version:  $VERSION"
  echo "  Skills → steering files: $skill_count"
  echo ""
  echo "  Structure:"
  echo "    $(basename "$OUTPUT_DIR")/"
  echo "    ├── POWER.md"
  echo "    ├── mcp.json"
  echo "    ├── docs/          (reference documents)"
  echo "    └── steering/      ($skill_count files)"
  ls "$OUTPUT_DIR/steering/" | sed 's/^/        /'
  echo ""
  echo "  Install in Kiro: Powers panel → Add Custom Power → Import from folder"
}

# ─── Dispatch ────────────────────────────────────────────────────────────────

if [ "${1:-}" = "--power" ]; then
  shift
  run_power_build "$@"
else
  run_workspace_install "$@"
fi
