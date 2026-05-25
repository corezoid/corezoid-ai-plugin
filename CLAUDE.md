# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Purpose

This is a Claude Code plugin (`@corezoid/corezoid-ai-plugin`) that gives Claude the knowledge and tools to create, edit, and review [Corezoid](https://corezoid.com) BPM processes directly from the IDE. It is primarily a **documentation and workflow plugin** — there is no build step or test suite. The repo ships static `.md` files, JSON samples, and plugin manifests.

## Plugin Development Commands

```bash
# Publish a new version (triggers GitHub Actions on tag push)
git tag v1.x.x && git push origin v1.x.x

# Install the plugin locally for testing
npm install -g .
claude plugin install @corezoid/corezoid-ai-plugin
```

## convctl MCP server (bundled in this plugin)

The MCP server is bundled as Go source at `plugins/corezoid/mcp-server/`. All operations are exposed as MCP tools — no separate installation required, only Go must be available. The server starts automatically via `.mcp.json`.

To test the MCP server without Claude:

```bash
cd plugins/corezoid/mcp-server && npx @modelcontextprotocol/inspector go run . mcp-server
```

<!-- AUTO:ARCHITECTURE:START -->
## Architecture

```
.claude-plugin/plugin.json        — Plugin manifest (name, version, description)
plugins/corezoid/
  mcp-server/                     — Go MCP server source (starts automatically via .mcp.json)
  skills/
    corezoid/                     — Main skill: platform overview, MCP tools, routing
      SKILL.md
      references/                 — Lookup documents (variables guide, env setup)
    corezoid-init/                — Sub-skill: environment setup and workspace pull
      SKILL.md
    corezoid-create/              — Sub-skill: create a new process from scratch
      SKILL.md
    corezoid-edit/                — Sub-skill: modify an existing process
      SKILL.md
    corezoid-review/              — Sub-skill: audit and analyze a process
      SKILL.md
  docs/
    nodes/                        — Per-node-type documentation (24 node types)
    process/                      — Process structure, validation rules, error handling
    tasks/                        — Task metadata and examples
    node-structures.md            — JSON schemas for all node types (canonical reference)
  samples/                        — Example .conv.json processes
```

### How skills work

Each skill has a frontmatter `description` with trigger phrases — Claude Code routes to the right sub-skill automatically based on user intent. Sub-skills are also directly invocable.

The main `corezoid/SKILL.md` is the universal entry point for general Corezoid questions and routes to the specialized sub-skills.

Commands use the path variable `${CLAUDE_PLUGIN_ROOT}` to reference files relative to the installed plugin root.
<!-- AUTO:ARCHITECTURE:END -->

## Key Corezoid Process Rules

Processes are stored as `.conv.json` files named `<ID>_<name>.conv.json`.

**Critical validation rules** (violations cause `push-process` to fail):
- Node IDs must be 24-character hexadecimal strings
- Every `extra` key must have a matching `extra_type` key with the correct type, and vice versa
- Object values in `extra` must be stringified JSON strings (`"{\"key\":\"val\"}"` not `{"key":"val"}`)
- Nodes that can fail (`set_param`, `api_rpc`, `api_code`, `api_copy`, `db_call`, `git_call`, `api_sum`, `api_reply`) require `err_node_id`
- Call Process nodes use `type: "api_rpc"` with `extra`/`extra_type` (not `data`/`data_type`)
- All constants (URLs, tokens, IDs) must be Corezoid variables: `{{env_var[@variable-name]}}` — never hardcoded

**obj_type values for process-level objects:** 1=process, 0=folder  
**obj_type values for nodes:** 0=code/api, 1=start, 2=end, 3=condition

## Simulator Plugin

A second plugin lives at `plugins/simulator/` with its own manifest, MCP server, skills, and docs.

```
plugins/simulator/
  .mcp.json                       — MCP server config (separate from corezoid)
  mcp-server/                     — Go MCP server source
  skills/
    simulator-graph/              — Skill for managing actor graphs (template.yaml = YAML format)
  docs/
    entities/                     — Per-entity docs (actors, forms, layers, accounts, etc.)
    user-flows/                   — User-facing workflow guides
```

## Adding Documentation

When adding a new node type, follow the template at `plugins/corezoid/docs/nodes/node-documentation-template.md` and add a corresponding JSON schema example to `plugins/corezoid/docs/node-structures.md`.
