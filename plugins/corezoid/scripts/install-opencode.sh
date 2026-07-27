#!/bin/sh
# install-opencode.sh — set up the Corezoid plugin for OpenCode (opencode.ai).
#
# What this script does, and does NOT do:
#   - It configures the Corezoid MCP server in the target opencode.json.
#     `.mcp.opencode.json` is a template with `${OPENCODE_PLUGIN_ROOT:-$PWD}`
#     resolved to this checkout's absolute plugin path, then merged into the
#     target opencode.json via a Python JSON merge (idempotent — safe to
#     re-run).
#   - It installs the sibling TypeScript plugin (../opencode-plugin/) via
#     `opencode plugin install file://<absolute-path>`. That plugin reads
#     ../skills/*/SKILL.md at load time and registers each as a tool named
#     `corezoid_<snake_skill_name>`. Skills are NOT copied anywhere — the
#     TS plugin loads them straight from the plugin dir with
#     `${CLAUDE_PLUGIN_ROOT}` substituted in memory.
#
# Usage:
#   plugins/corezoid/scripts/install-opencode.sh
#     workspace-install mode. Patches ./opencode.json in $PWD, installs the
#     TS plugin. Also runs `opencode plugin install ...` if the `opencode`
#     CLI is on PATH.
#
#   plugins/corezoid/scripts/install-opencode.sh --user-install
#     Patches ~/.config/opencode/opencode.json (creating it and its parent
#     dirs if needed). Same TS-plugin install step.
#
#   plugins/corezoid/scripts/install-opencode.sh --print-mcp
#     Emits the resolved MCP JSON block to stdout. Nothing is written. Used
#     by CI to validate the template and by the docs to show what gets
#     merged into opencode.json.
#
# Idempotent — safe to run repeatedly; the JSON merge overwrites the
# `mcp.corezoid` entry but preserves all sibling entries and top-level keys.

set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATE="$PLUGIN_ROOT/.mcp.opencode.json"

if [ ! -f "$TEMPLATE" ]; then
  echo "ERROR: MCP template not found: $TEMPLATE" >&2
  exit 1
fi

# ─── Resolve template ───────────────────────────────────────────────────────
# Substitute ${OPENCODE_PLUGIN_ROOT:-$PWD} with the absolute PLUGIN_ROOT.
# `#` delimiter avoids escaping `/` in paths; two-step `sed -i.bak` isn't
# needed here since we're piping to a variable, not editing in place.

resolve_template() {
  sed "s#\${OPENCODE_PLUGIN_ROOT:-\$PWD}#$PLUGIN_ROOT#g" "$TEMPLATE"
}

# ─── Mode: --print-mcp ──────────────────────────────────────────────────────

run_print_mcp() {
  resolve_template
}

# ─── JSON merge helper ──────────────────────────────────────────────────────
# python3 is required (same dependency as install-kiro.sh's power-install
# mode). Merges the resolved template's `mcp.corezoid` entry into the target
# opencode.json, creating the file if it doesn't exist. Preserves all other
# keys and MCP entries. Uses 2-space indent to match OpenCode's own writes.

merge_into_opencode_json() {
  target="$1"
  mkdir -p "$(dirname "$target")"

  resolved=$(resolve_template)

  python3 - "$target" "$resolved" << 'PYEOF'
import json
import os
import sys

target_path = sys.argv[1]
resolved_json = sys.argv[2]

resolved = json.loads(resolved_json)
new_entry = resolved.get("mcp", {}).get("corezoid")
if not new_entry:
    print("ERROR: resolved template lacks mcp.corezoid entry", file=sys.stderr)
    sys.exit(1)

if os.path.exists(target_path):
    with open(target_path) as f:
        try:
            data = json.load(f)
        except json.JSONDecodeError as e:
            print(f"ERROR: {target_path} is not valid JSON: {e}", file=sys.stderr)
            sys.exit(1)
else:
    data = {}

data.setdefault("mcp", {})["corezoid"] = new_entry

with open(target_path, "w") as f:
    json.dump(data, f, indent=2)
    f.write("\n")

print(f"✓ Merged mcp.corezoid into {target_path}")
PYEOF
}

# ─── Install the sibling TS plugin ──────────────────────────────────────────
# OpenCode loads local plugins from ~/.config/opencode/plugins/*.ts (docs:
# https://opencode.ai/docs/plugins → "Use a plugin > From local files"). The
# `opencode plugin install` CLI is only for npm modules, not local paths, so
# for a from-source install we bundle skills-loader.ts + index.ts into a
# single file at that location. Bundling (not symlinking) is required because
# OpenCode scans that dir for top-level .ts entry points, not subdirectories.

install_ts_plugin() {
  ts_plugin_src="$PLUGIN_ROOT/opencode-plugin"
  if [ ! -d "$ts_plugin_src" ]; then
    echo "⚠ TS plugin dir not found: $ts_plugin_src" >&2
    return 0
  fi

  cfg_plugins_dir="${XDG_CONFIG_HOME:-$HOME/.config}/opencode/plugins"
  mkdir -p "$cfg_plugins_dir"
  dest="$cfg_plugins_dir/corezoid.ts"

  python3 - "$ts_plugin_src/index.ts" "$ts_plugin_src/skills-loader.ts" "$dest" "$PLUGIN_ROOT" << 'PYEOF'
import re
import sys

index_path, loader_path, dest, plugin_root = sys.argv[1:]

with open(loader_path) as f:
    loader = f.read()
with open(index_path) as f:
    index = f.read()

# Strip the `import { ... } from "./skills-loader.ts"` line from index — the
# names are declared inline below via the loader body.
index = re.sub(
    r'^import\s*\{[^}]+\}\s+from\s+"\./skills-loader\.ts"\s*\n',
    "",
    index,
    flags=re.MULTILINE,
)

# Replace `resolvePluginRoot(import.meta.url)` with the hardcoded absolute
# path — the bundle lives in ~/.config/opencode/plugins/corezoid.ts, so
# import.meta.url points to that location, not the source plugin dir.
index = index.replace(
    "resolvePluginRoot(import.meta.url)",
    f'"{plugin_root}"',
)

header = (
    "// AUTOGENERATED by plugins/corezoid/scripts/install-opencode.sh —\n"
    "// bundle of opencode-plugin/index.ts + skills-loader.ts.\n"
    f"// Plugin root hardcoded to: {plugin_root}\n"
    "// Edits here are lost on the next install. Change the source files.\n\n"
)
bundle = header + "// ─── skills-loader.ts ───\n" + loader + "\n\n// ─── index.ts ───\n" + index

with open(dest, "w") as f:
    f.write(bundle)

print(f"✓ Bundled OpenCode plugin: {dest}")
PYEOF
}

# ─── Mode: workspace-install (default) ──────────────────────────────────────

run_workspace_install() {
  target="${1:-$PWD/opencode.json}"
  merge_into_opencode_json "$target"
  install_ts_plugin

  echo ""
  echo "✓ Corezoid OpenCode adapter installed"
  echo "  opencode.json:  $target"
  echo "  Plugin root:    $PLUGIN_ROOT"
  echo "  MCP command resolved with OPENCODE_PLUGIN_ROOT=$PLUGIN_ROOT"
  echo ""
  echo "  Start OpenCode in this workspace to load the Corezoid MCP server"
  echo "  and skills (registered as tools with the corezoid_ prefix)."
}

# ─── Mode: --user-install ───────────────────────────────────────────────────

run_user_install() {
  case "$(uname -s 2>/dev/null || echo Unknown)" in
    Darwin) CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/opencode" ;;
    Linux)  CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/opencode" ;;
    *)      CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/opencode" ;;
  esac
  target="$CFG_DIR/opencode.json"
  merge_into_opencode_json "$target"
  install_ts_plugin

  echo ""
  echo "✓ Corezoid OpenCode adapter installed (user scope)"
  echo "  opencode.json:  $target"
  echo "  Plugin root:    $PLUGIN_ROOT"
}

# ─── Dispatch ───────────────────────────────────────────────────────────────

case "${1:-}" in
  --print-mcp)     run_print_mcp ;;
  --user-install)  shift; run_user_install "$@" ;;
  *)               run_workspace_install "$@" ;;
esac
