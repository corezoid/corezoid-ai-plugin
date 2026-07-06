# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Purpose

This is a Claude Code plugin (`@corezoid/corezoid-ai-plugin`) that gives Claude the knowledge and tools to create, edit, and review [Corezoid](https://corezoid.com) BPM processes directly from the IDE.

The repo has three layers:
- **Go MCP server** (`plugins/corezoid/mcp-server/`) — 44 tools, compiled from Go source, started automatically via `.mcp.json`. Has a test suite (`go test ./...`) and CI.
- **Skills** (`plugins/corezoid/skills/`) — 18 SKILL.md files that teach Claude platform conventions.
- **Documentation corpus** (`plugins/corezoid/docs/`) — 24 node types, process guides, JSON schemas, samples.

## Plugin Development Commands

```bash
# Build and test the MCP server
cd plugins/corezoid/mcp-server && go build ./... && go test ./...

# Run tests with race detector
cd plugins/corezoid/mcp-server && go test -race ./...

# Publish a new version (triggers GitHub Actions on tag push)
git tag vX.Y.Z && git push origin vX.Y.Z

# Install the plugin locally for testing
claude plugin marketplace add .
claude plugin install corezoid@corezoid
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
    corezoid/                       — Main skill: platform overview, MCP tools, routing
      SKILL.md
      references/                   — Lookup documents (variables guide, env setup)
    corezoid-init/                  — Sub-skill: environment setup and workspace pull
    corezoid-create/                — Sub-skill: create a new process from scratch
    corezoid-edit/                  — Sub-skill: modify an existing process
    corezoid-state-diagram-create/  — Sub-skill: create a new state diagram (conv_type "state") from scratch
    corezoid-state-diagram-edit/    — Sub-skill: modify an existing state diagram
    corezoid-review/                — Sub-skill: audit and analyze a single process
    corezoid-project-review/        — Sub-skill: audit a whole project / multiple processes
    corezoid-dashboard-manager/     — Sub-skill: create and edit Corezoid dashboards
    corezoid-process-tech-writer/   — Sub-skill: generate technical documentation for processes
    corezoid-access/                — Sub-skill: share processes/folders, manage groups/API keys
    marketplace-publish-validation/ — Sub-skill: validation checklist for marketplace publishing
  docs/
    nodes/                        — Per-node-type documentation (24 node types)
    process/                      — Process structure, validation rules, error handling
    state-diagrams/               — State diagram concepts, node structures, process interaction
    tasks/                        — Task metadata and examples
    node-structures.md            — JSON schemas for all node types (canonical reference)
  samples/                        — Example .conv.json processes (state-diagrams/ holds state-diagram samples)
```

### How skills work

Each skill has a frontmatter `description` with trigger phrases — Claude Code routes to the right sub-skill automatically based on user intent. Sub-skills are also directly invocable.

The main `corezoid/SKILL.md` is the universal entry point for general Corezoid questions and routes to the specialized sub-skills.

Skills and commands use `${CLAUDE_PLUGIN_ROOT}` to reference files relative to the installed plugin root. This token is a host-side text substitution that Claude Code performs at skill-load time (see anthropics/claude-code#48230). Codex resolves the same token by the same name. **Do not rename it** — there is currently no mechanism to register a host-neutral alias, and the rename silently breaks reference-doc loading because Bash-tool invocations don't see `${CLAUDE_PLUGIN_ROOT}` in their environment and the substitution is the only thing that resolves it. For AWS Kiro, the install script (`plugins/corezoid/scripts/install-kiro.sh`) hard-copies skills and sed-substitutes the token to the absolute plugin path at install time.
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

## Adding Documentation

When adding a new node type, follow the template at `plugins/corezoid/docs/nodes/node-documentation-template.md` and add a corresponding JSON schema example to `plugins/corezoid/docs/node-structures.md`.

## Bug/improvement reporting rule (Corezoid plugin)

When responding to a user message that signals a **platform-level mistake** — wrong node type, wrong API choice (Corezoid vs Simulator), wrong process structure, wrong MCP tool, missing required platform field — add one line to your response, adapted to the context:

- Bug / broken behavior → "Хотите сообщить о баге команде Corezoid?"
- Unexpected plugin choice → "Хотите сообщить об этом команде Corezoid?"
- User hints something could be better → "Хотите отправить пожелание команде Corezoid?"

This is an additional line, independent of what the rest of the response does (explain, redo, acknowledge). It appears once per problem context; do not repeat if the user declines.

**Do not add this line** when the user is iterating on business logic: changing values, adding fields, renaming things, adjusting conditions — these are normal user-driven changes, not platform issues.
