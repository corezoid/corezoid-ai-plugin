#!/usr/bin/env bash
# Build the OpenAI plugin-directory submission ZIP for the HOSTED Corezoid MCP
# server (https://mcp.corezoid.com/mcp). The repository's own .mcp.json keeps the
# local (stdio) server for existing installs; this script only produces a
# separate artifact:
#   - .mcp.json points at the hosted URL (Codex format: {"url": ...});
#   - the Go server sources, install scripts and Kiro files are left out;
#   - skills that only make sense locally (login, git mirror, local layout,
#     retro/feedback, marketplace publishing) are left out;
#   - the main skill gains a "Hosted connector" section.
# Usage: scripts/build-hosted-submission.sh [output-dir]   (default: dist/)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/plugins/corezoid"
OUT="${1:-$ROOT/dist}"
URL="https://mcp.corezoid.com/mcp"
EXCLUDE_SKILLS=(corezoid-init corezoid-logout corezoid-git-context corezoid-node-layout corezoid-retro corezoid-feedback marketplace-publish-validation)

VERSION="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$SRC/.codex-plugin/plugin.json")"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
PKG="$WORK/corezoid"
mkdir -p "$PKG" "$OUT"

cp -R "$SRC/.codex-plugin" "$SRC/assets" "$SRC/docs" "$SRC/skills" "$PKG/"
[ -d "$SRC/samples" ] && cp -R "$SRC/samples" "$PKG/"
[ -f "$SRC/THIRD_PARTY_NOTICES.md" ] && cp "$SRC/THIRD_PARTY_NOTICES.md" "$PKG/"
for s in "${EXCLUDE_SKILLS[@]}"; do rm -rf "$PKG/skills/$s"; done

cat > "$PKG/.mcp.json" <<EOF
{
  "mcpServers": {
    "corezoid": {
      "url": "$URL"
    }
  }
}
EOF

python3 - "$PKG/skills/corezoid/SKILL.md" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
note = """
## Hosted connector (read this first)

This plugin talks to the hosted Corezoid MCP server. Differences from the local plugin:

- **No login tool.** The connection itself is authenticated (OAuth, account.corezoid.com).
- **Scope per call.** Tools that act inside a workspace take `scope: {company_id, stage_id}`. Find the values with `cz-structure` → `list-workspaces`, `list-projects`, `list-stages`.
- **No local files.** `pull-process` returns the process JSON and a `base` token in the tool result. Edit that JSON and deploy it with `push-process` (`content` + `base`). The result contains the deployed scheme and a new `base` for the next edit. `lint-process` takes `content`. `create-process` takes `folder_id` and returns the new process JSON.
- **Concurrent changes.** If the process changed on the server since your `base`, the push is blocked with a report. Pull again and re-apply your edits; merging is not available here.
- **Not available:** git mirror, local layout, snapshot management, and anything below that mentions files, `.conv.json` paths, `login`, `pull-folder` or `run.sh`. Use the hosted equivalents above.
"""
# insert after the YAML frontmatter
if s.startswith("---"):
    end = s.index("\n---", 3) + 4
    s = s[:end] + "\n" + note + s[end:]
else:
    s = note + s
open(p, "w").write(s)
PY

ZIP="$OUT/corezoid-plugin-hosted-v$VERSION.zip"
rm -f "$ZIP"
(cd "$PKG" && zip -qr "$ZIP" . -x '.DS_Store')
echo "$ZIP"
