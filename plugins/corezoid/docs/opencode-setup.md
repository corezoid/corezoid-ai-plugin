# OpenCode setup

`corezoid-ai-plugin` works in [OpenCode](https://opencode.ai) via a small
TypeScript adapter that registers every Corezoid skill as an OpenCode tool,
plus the same Go MCP server used by Claude Code / Codex / Kiro.

## Install from a local clone

```bash
git clone https://github.com/corezoid/corezoid-ai-plugin
cd corezoid-ai-plugin
sh plugins/corezoid/scripts/install-opencode.sh
```

This does two things:

1. Merges an `mcp.corezoid` entry into `./opencode.json` (creating the file
   if needed), with the MCP command resolved to this checkout's absolute
   plugin path. Existing MCP entries and other keys are preserved.
2. Bundles `plugins/corezoid/opencode-plugin/` into a single file at
   `~/.config/opencode/plugins/corezoid.ts`. OpenCode auto-loads it at
   startup and registers every `SKILL.md` under `plugins/corezoid/skills/`
   as a tool named `corezoid_<skill_name>` (about 20 tools).

Restart OpenCode (or open a new session) and both the MCP server and the
skill tools become available.

### User-scope install

Patch `~/.config/opencode/opencode.json` instead of the project one:

```bash
sh plugins/corezoid/scripts/install-opencode.sh --user-install
```

### Inspect what will be merged

```bash
sh plugins/corezoid/scripts/install-opencode.sh --print-mcp
```

Emits the resolved MCP JSON block to stdout without writing anywhere.

## Update

Pull the latest source and re-run the installer:

```bash
cd corezoid-ai-plugin && git pull
sh plugins/corezoid/scripts/install-opencode.sh
```

The bundled plugin at `~/.config/opencode/plugins/corezoid.ts` is
regenerated. `opencode.json` is merged idempotently — sibling MCP entries
you added by hand stay intact.

## Uninstall

Remove the bundle and the MCP block:

```bash
rm ~/.config/opencode/plugins/corezoid.ts
# then delete "corezoid" from the "mcp" section of opencode.json
```

## How it fits together

```
opencode.json          ← MCP entry launches mcp-server/run.sh (Go binary)
~/.config/opencode/plugins/corezoid.ts
                       ← bundled skills-loader.ts + index.ts,
                         with ${CLAUDE_PLUGIN_ROOT} substituted to the
                         absolute plugin path at install time
```

`opencode plugin install @corezoid/opencode-plugin` (via npm, once
published) is an equivalent alternative to the local-clone install for the
plugin side; MCP still has to be added to `opencode.json` either way.
