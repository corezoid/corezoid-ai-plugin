# Release Checklist

Use this before tagging a public release.

## Manifests

- [ ] `plugins/corezoid/.claude-plugin/plugin.json` version is updated.
- [ ] `plugins/corezoid/.codex-plugin/plugin.json` version matches Claude manifest.
- [ ] `plugins/corezoid/.opencode-plugin/plugin.json` version matches Claude manifest.
- [ ] `plugins/corezoid/opencode-plugin/package.json` version matches Claude manifest.
- [ ] `.claude-plugin/marketplace.json` `plugins[0].version` matches all manifests.
- [ ] `.agents/plugins/marketplace.json` `plugins[0].version` matches all manifests.
- [ ] `.agents/plugins/marketplace.json` `plugins[0].license` is `"MIT"`.
- [ ] No TODO or placeholder values remain in any manifest.
- [ ] Manifest asset and skill paths resolve under `plugins/corezoid/`.
- [ ] All manifests have `"license": "MIT"` (not ISC).
- [ ] All plugin `source` paths listed in marketplace manifests exist on disk.

## MCP Server

- [ ] `plugins/corezoid/.mcp.json` contains no credentials or private URLs.
- [ ] Go source in `plugins/corezoid/mcp-server/` compiles without errors (`go build ./...`).

## Content

- [ ] `CHANGELOG.md` has an entry for the new version.
- [ ] `README.md` install commands reference `corezoid/corezoid-ai-plugin`.
- [ ] No local test processes (`*.conv.json`) or `.env` files are tracked in git.

## JSON Validation

All manifests parse cleanly:

```bash
python3 -m json.tool .claude-plugin/marketplace.json >/dev/null
python3 -m json.tool .agents/plugins/marketplace.json >/dev/null
python3 -m json.tool plugins/corezoid/.claude-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.codex-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/.opencode-plugin/plugin.json >/dev/null
python3 -m json.tool plugins/corezoid/opencode-plugin/package.json >/dev/null
python3 -m json.tool plugins/corezoid/.mcp.json >/dev/null
python3 -m json.tool plugins/corezoid/.mcp.opencode.json >/dev/null
```

## Testing

- [ ] Claude Code can install the plugin from the local clone.
- [ ] Codex can install the plugin from the local clone.
- [ ] OpenCode: `sh plugins/corezoid/scripts/install-opencode.sh` merges MCP + bundles TS plugin, restart OpenCode, `corezoid_*` tools visible.
- [ ] MCP server starts and `login` tool responds.
- [ ] `NPM_TOKEN` secret is set in GitHub if the OpenCode npm package should auto-publish on tag push.

## Git

- [ ] All changes are committed on `main` (or merged from a feature branch).
- [ ] Release tag matches the manifest version, e.g. `vX.Y.Z`.
- [ ] Tag is pushed to `origin`.
