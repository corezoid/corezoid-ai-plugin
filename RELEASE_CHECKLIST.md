# Release Checklist

Use this before tagging a public release.

## Manifests

- [ ] `plugins/corezoid/.claude-plugin/plugin.json` version is updated.
- [ ] `plugins/corezoid/.codex-plugin/plugin.json` version matches Claude manifest.
- [ ] `.claude-plugin/marketplace.json` `plugins[0].version` matches both manifests.
- [ ] `.agents/plugins/marketplace.json` is present and valid JSON.
- [ ] No TODO or placeholder values remain in any manifest.
- [ ] Manifest asset and skill paths resolve under `plugins/corezoid/`.
- [ ] Both `plugin.json` manifests have `"license": "MIT"` (not ISC).
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
python3 -m json.tool plugins/corezoid/.mcp.json >/dev/null
```

## Testing

- [ ] Claude Code can install the plugin from the local clone.
- [ ] Codex can install the plugin from the local clone.
- [ ] MCP server starts and `login` tool responds.

## Git

- [ ] All changes are committed on `main` (or merged from a feature branch).
- [ ] Release tag matches the manifest version, e.g. `v2.3.4`.
- [ ] Tag is pushed to `origin`.
