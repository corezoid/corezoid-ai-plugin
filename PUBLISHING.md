# Publishing

This document describes how to publish a new version of the Corezoid AI plugin.

## 1. Validate Manifests Locally

Check that all JSON manifests are well-formed:

```bash
python3 -m json.tool .claude-plugin/marketplace.json >/dev/null
python3 -m json.tool .agents/plugins/marketplace.json >/dev/null
python3 -m json.tool plugins/corezoid/.claude-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.codex-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.mcp.json >/dev/null
```

Verify version sync between manifests:

```bash
grep '"version"' plugins/corezoid/.claude-plugin/plugin.json \
                 plugins/corezoid/.codex-plugin/plugin.json \
                 .claude-plugin/marketplace.json
```

All three should show the same version number.

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
codex plugin marketplace upgrade corezoid
```

Restart Codex, open Plugin Directory, select **Corezoid**, and confirm the plugin installs and the skills are available.

## 4. Update Files

1. Bump the version in all three manifest files:
   - `plugins/corezoid/.claude-plugin/plugin.json`
   - `plugins/corezoid/.codex-plugin/plugin.json`
   - `.claude-plugin/marketplace.json` (the `plugins[0].version` field)
2. Add a section to `CHANGELOG.md` for the new version.
3. Commit the changes.

## 5. Push to GitHub and Tag

```bash
git push origin main
git tag v1.x.x
git push origin v1.x.x
```

The `release.yml` workflow fires automatically on any `v*` tag and creates a GitHub Release with the corresponding `CHANGELOG.md` section as release notes.

## 6. Install from GitHub

**Claude Code:**

```bash
claude plugin marketplace add corezoid/corezoid-ai-plugin
claude plugin install corezoid@corezoid
```

**Codex (stable):**

```bash
codex plugin marketplace add corezoid/corezoid-ai-plugin --ref v1.x.x
codex plugin marketplace upgrade corezoid
```

**Codex (development tracking):**

```bash
codex plugin marketplace add corezoid/corezoid-ai-plugin --ref main
codex plugin marketplace upgrade corezoid
```

## 7. Notify Users

After tagging, ask users to upgrade their local marketplace and plugin:

- **Claude Code:** `claude plugin marketplace update && claude plugin upgrade corezoid@corezoid`
- **Codex:** `codex plugin marketplace upgrade corezoid`
