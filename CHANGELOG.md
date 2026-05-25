# Changelog

## [2.3.4]

- Fix: always ask user to choose workspace/project/stage on `login` instead of auto-selecting.
- Codex plugin version bumped to 2.3.4.
- Add project-level commit skill with automatic version bump.

## [2.3.3]

- Remove redundant "Environment Context" section from skill documentation.

## [2.3.2]

- Fix: allow `list-workspaces`, `list-projects`, `list-stages` tools to work with token-only auth (no full `ensureAuth` required).

## [2.3.1]

- Fix: rewrite Mode B login flow to use explicit MCP tool calls instead of elicitation when client does not support it.

## [2.3.0]

- Feat: MCP server returns an actionable auth error message pointing to the `corezoid-init` skill when credentials are missing.
- Feat: support personal workspaces (accounts with no `WORKSPACE_ID`).
- Fix: skip OAuth browser flow when `ACCESS_TOKEN` is already present in `.env`.

## [2.2.0]

- Feat: add chat-based fallback login flow for Claude clients that do not support the elicitation protocol.
- Fix: update plugin update command to use `name@marketplace` format in README.

## [2.1.0]

- Feat: automatically pull the remote folder to disk after the user selects a stage during `login`.

## [2.0.0]

- Complete plugin restructure: Go MCP server replaces the old scripting approach.
- New skills: `corezoid`, `corezoid-init`, `corezoid-create`, `corezoid-edit`, `corezoid-review`, `corezoid-project-review`.
- MCP tools: `login`, `logout`, `pull-process`, `pull-folder`, `push-process`, `lint-process`, `run-task`, `create-process`, `create-folder`, `create-alias`, `create-variable`, `list-workspaces`, `list-projects`, `list-stages`, `list-task-history`, `list-node-tasks`, `modify-task`, `delete-task`, `create-dashboard`, `get-dashboard`, `add-chart`, `modify-chart`, `get-chart`, `set-dashboard-layout`.
- Rename marketplace identifier from `corezoid-ai-plugin` to `corezoid`.
- Simulator.Company was briefly bundled as a second plugin (removed in v2.3.3).

## [1.3.1]

- Initial public release of the Corezoid AI plugin for Claude Code and Codex.
- Go MCP server with tools for login, pull, push, lint, run-task, and process management.
- Skills: `corezoid`, `corezoid-init`, `corezoid-create`, `corezoid-edit`, `corezoid-review`, `corezoid-project-review`.
- Node documentation, JSON schemas, and sample `.conv.json` processes.
