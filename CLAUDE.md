# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Purpose

This is a Claude Code / Codex / Kiro plugin (`@corezoid/corezoid-ai-plugin`) that gives the AI the knowledge and tools to create, edit, and review [Corezoid](https://corezoid.com) BPM processes directly from the IDE. The repo ships:

- Static skills (`plugins/corezoid/skills/*/SKILL.md` + reference docs, JSON samples).
- Plugin manifests for Claude Code, Codex, and the agents marketplace.
- A Go-based MCP server (`convctl`) in `plugins/corezoid/mcp-server/` that exposes Corezoid operations as MCP tools. It has real tests (`go test -race`), golden tests for layout and lint, integration tests, and a release pipeline that builds signed multi-platform binaries.

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

### MCP server: build, test, lint

Run these from `plugins/corezoid/mcp-server/`:

```bash
go build ./...                                    # compile
go vet ./...                                      # static analysis
go test -race -coverprofile=coverage.out ./...    # tests with race detector
go tool cover -func=coverage.out                  # coverage summary
```

Golden tests (layout coordinates in `testdata/golden/layout_*.json`, lint output in `testdata/golden/lint_*.txt`) are regenerated with the `-update` flag after an intentional algorithm change:

```bash
go test -run TestLayoutGolden -update ./...
```

### Repo-level checks

CI (`.github/workflows/ci.yml`) also runs:

- JSON validation for all four plugin manifests + `.mcp.json`.
- Version sync across `plugins/corezoid/.claude-plugin/plugin.json`, `plugins/corezoid/.codex-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `.agents/plugins/marketplace.json`.
- License consistency (must be MIT everywhere).
- Markdown link check.
- Skills-list sync (`scripts/check-skills-sync.py`): the skill directories under `plugins/corezoid/skills/` must match the lists in `CLAUDE.md` and `README.md`.

To regenerate the machine-readable discovery files (`public/llms.txt`, `public/.well-known/skills/index.json`) locally — normally the release workflow does this automatically:

```bash
python3 scripts/generate-discovery.py
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
    corezoid-init/                  — Environment setup, OAuth login, workspace pull
    corezoid-create/                — Create a new process from scratch
    corezoid-edit/                  — Modify an existing process
    corezoid-state-diagram-create/  — Create a new state diagram (conv_type "state") from scratch
    corezoid-state-diagram-edit/    — Modify an existing state diagram
    corezoid-review/                — Audit and analyze a single process
    corezoid-project-review/        — Audit a whole project / multiple processes
    corezoid-process-optimizer/     — Reduce tacts, merge nodes, clean data flow, add resilience
    corezoid-node-layout/           — Auto-arrange node x/y (positions only) before push
    corezoid-process-tech-writer/   — Generate Markdown docs + enriched JSON with node descriptions
    corezoid-describe/              — Set/refresh description on a process, folder, or project
    corezoid-dashboard-manager/     — Create and edit Corezoid dashboards
    corezoid-alias-manager/         — Create, list, modify, delete process aliases
    corezoid-variable-manager/      — Create, list, modify, delete environment variables (env_var)
    corezoid-api-connector/         — Build processes that call the Corezoid public API (/api/2/json)
    corezoid-gitcall/               — Custom code (Python/Go/Java/PHP/JS/…) as a git_call step
    corezoid-access/                — Share processes/folders, manage groups/API keys
    corezoid-retro/                 — End-of-session retrospective: route learnings to CLAUDE.md, feedback, settings, or memory
    corezoid-feedback/              — Collect and submit bug reports / improvement requests to the Corezoid team
    marketplace-publish-validation/ — Pre-publication checklist for the Corezoid marketplace
  docs/
    nodes/                        — Per-node-type documentation (24 node types)
    process/                      — Process structure, validation rules, error handling
    state-diagrams/               — State diagram concepts, node structures, process interaction
    tasks/                        — Task metadata and examples
    node-structures.md            — JSON schemas for all node types (canonical reference)
  samples/                        — Example .conv.json processes (state-diagrams/ holds state-diagram samples)
scripts/
  generate-discovery.py           — Generate public/llms.txt and public/.well-known/skills/index.json (runs at release)
  check-skills-sync.py            — CI check: skills directories vs CLAUDE.md vs README.md skill lists
public/
  llms.txt                        — Machine-readable skill + MCP-tool index (autogenerated at release)
  .well-known/skills/index.json   — JSON skill index (autogenerated at release)
```

### How skills work

Each skill has a frontmatter `description` with trigger phrases — Claude Code routes to the right sub-skill automatically based on user intent. Sub-skills are also directly invocable.

The main `corezoid/SKILL.md` is the universal entry point for general Corezoid questions and routes to the specialized sub-skills.

Skills and commands use `${CLAUDE_PLUGIN_ROOT}` to reference files relative to the installed plugin root. This token is a host-side text substitution that Claude Code performs at skill-load time (see anthropics/claude-code#48230). Codex resolves the same token by the same name for skill/reference paths. **Do not rename it** — there is currently no mechanism to register a host-neutral alias, and the rename silently breaks reference-doc loading because Bash-tool invocations don't see `${CLAUDE_PLUGIN_ROOT}` in their environment and the substitution is the only thing that resolves it. MCP subprocesses are different: Codex does not expose `CLAUDE_PLUGIN_ROOT` as an environment variable there, so `.mcp.json` must preserve the user's `PWD` as `COREZOID_WORK_DIR` and resolve the installed plugin root separately. For AWS Kiro, the install script (`plugins/corezoid/scripts/install-kiro.sh`) hard-copies skills and sed-substitutes the token to the absolute plugin path at install time.
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

Changes to node structure or lint rules may require refreshing MCP-server golden files:

- `plugins/corezoid/mcp-server/testdata/golden/layout_*.json` — layout coordinates. Regenerate with `go test -run TestLayoutGolden -update ./...`.
- `plugins/corezoid/mcp-server/testdata/golden/lint_*.txt` — lint output. Regenerate with `go test -run TestFormatLintResult_Golden -update ./...`.

When adding a new skill, also update this file's Architecture section AND the skills table in `README.md`. CI (`scripts/check-skills-sync.py`) fails the build if the two lists diverge from the actual directory.

## Bug/improvement reporting rule (Corezoid plugin)

When responding to a user message that signals a **platform-level mistake** — wrong node type, wrong API choice (Corezoid vs Simulator), wrong process structure, wrong MCP tool, missing required platform field — add one line to your response, adapted to the context:

- Bug / broken behavior → "Хотите сообщить о баге команде Corezoid?"
- Unexpected plugin choice → "Хотите сообщить об этом команде Corezoid?"
- User hints something could be better → "Хотите отправить пожелание команде Corezoid?"

This is an additional line, independent of what the rest of the response does (explain, redo, acknowledge). It appears once per problem context; do not repeat if the user declines.

**Do not add this line** when the user is iterating on business logic: changing values, adding fields, renaming things, adjusting conditions — these are normal user-driven changes, not platform issues.
