# Publishing

This document describes how to publish a new version of the Corezoid AI plugin.

## 1. Validate Manifests Locally

Check that all JSON manifests are well-formed:

```bash
python3 -m json.tool .claude-plugin/marketplace.json >/dev/null
python3 -m json.tool .agents/plugins/marketplace.json >/dev/null
python3 -m json.tool plugins/corezoid/.claude-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.codex-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.kiro-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.mcp.json >/dev/null
python3 -m json.tool plugins/corezoid/.mcp.kiro.json >/dev/null
```

Verify version sync between manifests:

```bash
grep -E '"version"|^version:' \
  plugins/corezoid/.claude-plugin/plugin.json \
  plugins/corezoid/.codex-plugin/plugin.json \
  plugins/corezoid/.kiro-plugin/plugin.json \
  .claude-plugin/marketplace.json \
  .agents/plugins/marketplace.json \
  POWER.md
```

All six should show the same version number.

## 2. Test in Claude Code

Install the plugin from the local clone:

```bash
claude plugin marketplace add ./
claude plugin install corezoid@corezoid
```

Verify that the Corezoid skills load and the MCP server starts. Run a quick smoke test:

```
log in to Corezoid
```

## 3. Test in Codex

```bash
codex plugin marketplace add ./
codex plugin install corezoid@corezoid
```

Restart Codex, open Plugin Directory, select **Corezoid**, and confirm the plugin installs and the skills are available.

## 3b. Test in AWS Kiro

```bash
plugins/corezoid/scripts/install-kiro.sh "$YOUR_KIRO_WORKSPACE"
```

Open the workspace in Kiro. Confirm:

- `.kiro/settings/mcp.json` loads and the `corezoid` MCP server appears in Kiro's tool panel.
- `.kiro/steering/corezoid.md` is recognised as steering.
- A prompt like "use corezoid" / "pull a process" routes through `.kiro/skills/corezoid/SKILL.md`.
- A live tool call (e.g. `login`) succeeds end-to-end.

The script is idempotent and supports re-runs to refresh the workspace overlay.

## 4. Update Files

1. Bump the version in **six** files (every host manifest plus the Power manifest):
   - `plugins/corezoid/.claude-plugin/plugin.json`
   - `plugins/corezoid/.codex-plugin/plugin.json`
   - `plugins/corezoid/.kiro-plugin/plugin.json`
   - `.claude-plugin/marketplace.json` (the `plugins[0].version` field)
   - `.agents/plugins/marketplace.json` (the `plugins[0].version` field)
   - `POWER.md` (frontmatter `version:` field)
2. Add a section to `CHANGELOG.md` for the new version.
3. Commit the changes.

## 5. Push to GitHub and Tag

```bash
git push origin main
git tag vX.Y.Z
git push origin vX.Y.Z
```

The `release.yml` workflow fires automatically on any `v*` tag. It cross-compiles the MCP server, generates SHA-256 checksums, regenerates `public/` via `python3 scripts/generate-discovery.py`, and creates a GitHub Release whose body is the matching `CHANGELOG.md` section. `POWER.md` is attached so kiro.dev/powers can resolve the Power manifest from the tag.

## 6. Install from GitHub

**Claude Code:**

```bash
claude plugin marketplace add corezoid/corezoid-ai-plugin
claude plugin install corezoid@corezoid
```

**Codex (stable):**

```bash
codex plugin marketplace add corezoid/corezoid-ai-plugin --ref vX.Y.Z
codex plugin install corezoid@corezoid
```

**Codex (development tracking):**

```bash
codex plugin marketplace add corezoid/corezoid-ai-plugin --ref main
codex plugin install corezoid@corezoid
```

**AWS Kiro:**

```bash
git clone https://github.com/corezoid/corezoid-ai-plugin
plugins/corezoid/scripts/install-kiro.sh "$YOUR_KIRO_WORKSPACE"
```

`install-kiro.sh` hard-copies each skill into the workspace and resolves the
`$CLAUDE_PLUGIN_ROOT` token (used in reference-doc paths) to the absolute
plugin path at install time. Kiro does no host-side token substitution of
its own, so a pre-built overlay zip would still need this post-extract
step on every machine — clone + `install-kiro.sh` is therefore the single
supported install path.

To list this Power on **kiro.dev/powers**, submit the release tag URL to the
Kiro Power registry. `POWER.md` is attached to every Release so the registry
can resolve metadata from the tag.

## 7. Notify Users

After tagging, ask users to upgrade their local marketplace and plugin:

- **Claude Code:** `claude plugin marketplace update && claude plugin update corezoid@corezoid`
- **Codex:** `codex plugin update corezoid@corezoid`
