# Changelog

## [2.7.0]

- Feat: AWS Kiro support — the same plugin payload now installs on Kiro alongside Claude Code and Codex via a symmetric overlay (`plugins/corezoid/.kiro-plugin/plugin.json`, `plugins/corezoid/.mcp.kiro.json`, `plugins/corezoid/steering/corezoid.md`, and a root-level `POWER.md` distribution manifest for kiro.dev/powers).
- Feat: `plugins/corezoid/scripts/install-kiro.sh` sets up an existing Kiro workspace from a cloned repo. Copies the MCP entry, symlinks steering files, and hard-copies each skill into `.kiro/skills/<name>/` while sed-substituting every `$CLAUDE_PLUGIN_ROOT` (and braced `${CLAUDE_PLUGIN_ROOT}`) token with the absolute plugin path so reference-doc paths resolve under Kiro. Idempotent.
- Feat: `corezoid-stage-scan` skill — offline pre-merge/pre-deploy static validator for exported stage `.zip`s (or extracted dirs). Detects non-active processes, empty/battered processes, broken intra-process node links (`to_node_id`/`err_node_id`), and broken/inactive cross-process `conv_id` references. Maps findings to the platform's merge "Errors list" messages. Ships a stdlib-only Python scanner with CI-friendly exit codes (`scripts/scan_stage.py`); each finding carries a `folder` field with the human-readable folder path in the stage tree.
- Feat: `delete-process` MCP tool — move a process to Trash without leaving the IDE.
- Docs: `$CLAUDE_PLUGIN_ROOT` inside SKILL.md is a host-side text substitution Claude Code performs at skill-load time (anthropics/claude-code#48230). Codex resolves the same token by the same name; there is currently no mechanism to register a host-neutral alias, so the token name stays as `$CLAUDE_PLUGIN_ROOT` across all skills and `install-kiro.sh` resolves it at install time for Kiro.
- CI: package and attach the `.kiro` overlay and `POWER.md` to GitHub Releases; ignore `${VAR}` placeholder paths in the markdown link check.

## [2.6.0]

- Feat: `send-feedback` MCP tool — submits user feedback to a dedicated Corezoid process (`conv_id 1871779`) and returns a ticket id. Does not require authentication so users can report auth-related issues too.
- Feat: `corezoid-feedback` skill — UX layer for the feedback flow: detects when a result was unexpected, collects problem/expected/solution, shows the full payload for confirmation, then calls `send-feedback`.
- Feat: opt-in email telemetry — after first successful login, users are asked (via elicitation) if they want to share their email with the Corezoid team; stored in `~/.corezoid/preferences.json`, included as `user_email` in analytics events.
- Refactor: all telemetry values (analytics + feedback endpoint and conv_id) centralised in `telemetry_config.go`; individually overridable via `COREZOID_ANALYTICS_ENDPOINT`, `COREZOID_ANALYTICS_CONV_ID`, `COREZOID_FEEDBACK_ENDPOINT`, `COREZOID_FEEDBACK_CONV_ID`. Existing default behavior unchanged.
- Security: secret redaction applied to all feedback fields before transmission (Bearer tokens, JWTs, `api_key`/`token`/`password`/`secret` values, hex strings ≥ 32 chars). Feedback disabled by `COREZOID_FEEDBACK_DISABLED=1`.
- Fix: allow templated/dynamic `conv_id` in `api_copy` nodes (align schema with `api_rpc`).
- Fix: detect and disallow passthrough escalation nodes during lint.
- Docs: api-call — require the full canonical api logic shape; a "light" node fails the deploy.
- Docs: api-call — correct `customize_response=false` behavior; document response-body placement and silent mapping failure.
- Docs: params — document the exact valid params element shape and that params are optional for receiving data.
- Docs: set-param — document nested env_var keys and expand `conv[].ref[]` rules.
- Docs: delay-node — clarify the 30s limit is static-literal only; document dynamic absolute-timestamp timers.
- Docs: delay-node — make timestamp source explicitly irrelevant (set_param is one example).
- Docs: node-ids — document server reassignment and stability of node IDs on push.
- Docs: updated SECURITY.md telemetry section to disclose optional email opt-in and how to remove it.
- Chore: MCP server log file moved from `/tmp/corezoid.log` to `~/.corezoid/mcp.log` for easier discoverability.

## [2.5.0]

- Feat: Project CRUD MCP tools — create-project, modify-project, delete-project, show-project — for managing Corezoid projects without leaving the IDE.
- Feat: Folder CRUD MCP tools — show-folder, list-folders, modify-folder, delete-folder — for working with the folder hierarchy.
- Feat: corezoid-api-connector skill with a sample API-node-list process for wiring external API integrations.
- Refactor: API-key Principal uses login obj_id (int) instead of the login string; drops the extra show_api_key round-trip. Note: changes the on-disk format under ~/.corezoid/api-keys/ — `login` is now a JSON number.
- Fix: bump OAuth PKCE token-exchange timeout from 30s to 60s to avoid silent failures on slow networks.

## [2.4.0]

- Feat: corezoid-access skill and MCP tools for user groups, API keys, and object/folder sharing.
- Feat: corezoid-state-diagram-create and corezoid-state-diagram-edit skills with a create-state-diagram MCP tool for building and modifying state-diagram processes.
- Feat: corezoid-process-optimizer skill for auditing existing processes for performance and structural issues.
- Feat: corezoid-alias-manager and corezoid-variable-manager skills for working with aliases and environment variables.
- Feat: get-node-stat MCP tool exposing node archive statistics.
- Feat: AI discovery files — llms.txt and .well-known/skills/index.json — with a generator script under scripts/.
- Feat: context7 integration for live documentation lookups.
- Docs: full state-diagram documentation set under plugins/corezoid/docs/state-diagrams/ (overview, node structures, process interaction).
- Docs: clarifications in call-process, copy-task, set-state, set-parameters dynamic values, and variables-guide nodes.
- Docs: bundle docs/corezoid-swagger.json as a canonical Corezoid REST API reference.
- Chore: unit tests for mcp-server analytics, access, config, and helpers.
- CI: minor tweak to release.yml.

## [2.3.9]

- Docs: clarify in SECURITY.md that Go is not required on supported prebuilt platforms; only needed for developer/fallback scenarios.
- Docs: expand "older tags" support policy — security fixes are released as new versions only.
- Chore: add comment to .gitignore explaining why `/.mcp.json` is root-level only (prevents accidental `**/.mcp.json` breakage).

## [2.3.8]

- Docs: remove Go requirement from README — prebuilt binary is the only supported install path; Go fallback remains silent for developers.
- Docs: add telemetry disclosure block in the Installation section with opt-out example (`COREZOID_ANALYTICS_DISABLED=1`).
- Feat: run.sh — add `COREZOID_MCP_DEV=1` override and prefer local `./convctl` binary for developer workflows.
- Fix: gitignore `.mcp.json` to prevent local MCP config from being committed.

## [2.3.7]

- Feat: `--version` flag injected at build time via ldflags; defaults to `"dev"` for local builds.
- CI: validate `run.sh` syntax with `sh -n` on every push/PR; run `go run . --version` as a smoke test after build.
- Security: GitHub Artifact Attestations (`actions/attest-build-provenance`) for all release binaries, providing verifiable supply-chain provenance beyond SHA256 checksums.

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
