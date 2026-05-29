# Changelog

## [2.3.6]

- Feat: prebuilt MCP server binaries (darwin/linux × amd64/arm64) distributed via GitHub Releases; run.sh downloads and caches the binary on first start, falls back to `go run .` when unavailable.
- Security: SHA256 checksum verification against release checksums.txt before executing a downloaded binary.
- Security: remove workspace_id and stage_id from anonymous telemetry events.
- Fix: logout confirmation message now shows `~/.corezoid/credentials` instead of project `.env`.
- Fix: mid-session environment switching — login/logout now correctly reload and persist changed account URL, workspace ID, and stage ID.
- Docs: add Telemetry section to README with opt-out instructions (`COREZOID_ANALYTICS_DISABLED=1`).
- Docs: clarify Go 1.24+ is required only as a fallback, not when a prebuilt binary is available.
- CI: attach per-platform SHA256 `checksums.txt` to every GitHub Release.

## [2.3.5]

- Feat: store ACCESS_TOKEN in ~/.corezoid/credentials instead of project .env to prevent accidental git leaks.
- Feat: add anonymous tool-call analytics (opt-out via COREZOID_ANALYTICS_DISABLED=1).
- Fix: sync version and license across all four manifests (.agents/plugins/marketplace.json was missing both fields).
- Fix: replace conv_id with process_id in pull-process examples across four skill files.
- Docs: update SECURITY.md with two-layer credential model, network activity, and analytics disclosure.
- Docs: update corezoid-init/SKILL.md and README to reflect new credential file location.

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
