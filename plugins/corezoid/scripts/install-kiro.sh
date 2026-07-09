#!/bin/sh
# install-kiro.sh — set up the corezoid plugin inside an AWS Kiro workspace.
#
# Usage:
#   plugins/corezoid/scripts/install-kiro.sh [workspace-dir]
#
# If workspace-dir is omitted, defaults to $KIRO_WORKSPACE_DIR or $PWD.
# Creates the following under <workspace>/.kiro/:
#   settings/mcp.json        ← copy of .mcp.kiro.json
#   steering/<name>.md       ← symlinked from this plugin's steering/
#   skills/<name>/SKILL.md   ← HARD-COPIED with $CLAUDE_PLUGIN_ROOT resolved
#                              to the absolute plugin path
#
# Why hard-copy and resolve the token, instead of symlinking the source files
# the way Claude Code / Codex consume them?
#   - The token `$CLAUDE_PLUGIN_ROOT` is a host-side text substitution Claude
#     Code performs at skill-load time (anthropics/claude-code#48230 etc.).
#     Kiro does no such substitution — so the literal `$CLAUDE_PLUGIN_ROOT`
#     would survive into the model context, leaving every reference-doc path
#     as a dead string. Resolving the token at install time fixes that.
#   - Symlinked skills would re-introduce the unresolved token on every read.
#
# `sed -i` portability is handled with the `-i.bak`+`find -delete` two-step
# (works under both GNU sed on Linux and BSD sed on macOS without branching).

set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WORKSPACE="${1:-${KIRO_WORKSPACE_DIR:-$PWD}}"
KIRO_DIR="$WORKSPACE/.kiro"

if [ ! -d "$WORKSPACE" ]; then
  echo "ERROR: workspace dir not found: $WORKSPACE" >&2
  exit 1
fi

mkdir -p "$KIRO_DIR/settings" "$KIRO_DIR/steering" "$KIRO_DIR/skills"

# 1) MCP entry — always plain copy (workspace-local edits must not leak back).
cp "$PLUGIN_ROOT/.mcp.kiro.json" "$KIRO_DIR/settings/mcp.json"

# 2) Steering — small, stable, no token substitution needed. Symlink on POSIX,
#    hard-copy on Windows shells.
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
for d in "$PLUGIN_ROOT"/skills/*/; do
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
echo
echo "If your shell does not already set KIRO_PLUGIN_ROOT, add:"
echo "  export KIRO_PLUGIN_ROOT=\"$PLUGIN_ROOT\""
