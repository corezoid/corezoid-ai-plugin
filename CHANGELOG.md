# Changelog

## [3.2.1]

- Fix(mcp-server): `show-task` now declares the same contract in its MCP JSON Schema as at runtime: `process_id` plus a non-empty `task_id` or `ref` are required.
- Fix(mcp-server): mirrored folder markers are validated before reuse. A directory whose existing marker names a different folder, or that holds several markers, now stops local-tree materialisation instead of silently routing a later operation to a different Corezoid folder. A correctly named marker is still kept as-is — only its name decides which folder the directory resolves to, so an unfamiliar body (as a server export may produce) is logged rather than treated as fatal. New markers are written atomically.
- Fix(mcp-server): `pull-process` reports failure when it cannot prepare the required local mirror; `create-process` and `create-folder` surface an explicit warning when their already-created server object could not be fully mirrored locally.
- Build: `convctl --version` now includes the release version and short source commit SHA.
- CI: Release waits for the complete CI workflow, including documentation validation, build/vet/test and `govulncheck`; publishing an existing release tag is rejected.

## [3.2.0]

- Fix(mcp-server): mirrored directories now receive their `<id>_<name>.folder.json` markers when created by `create-process`, `create-folder`, or `pull-process`. A process or folder created in such a directory can therefore be resolved and used by subsequent create, pull, and push operations.
- Feat(mcp-server): new read-only `show-task` tool — look up one task by `ref` and/or `task_id` in a single op and get its current `data`, `obj_id` (task_id), `node_id` and `status`. It commits nothing, so it works on immutable stages and with view-only access; use it instead of paging `list-node-tasks` for a known ref, and to resolve the `task_id` that `list-task-history` requires.
- Feat(mcp-server): snapshot support is detected per environment. Installations whose API has no snapshot object no longer have every push blocked by the failed pre-push auto-snapshot: support is probed read-only, confirmed against control requests, cached per project/stage with a TTL on negative answers, and the push result states the snapshot was skipped. Only a refusal that is evidently about the snapshot object counts — network faults, per-process complaints and "Unsupported stage"/"Invalid obj_id" answers keep snapshots enabled. Keep `.conv.json` under version control on such environments: the platform holds no rollback point.
- Feat(corezoid-review): Step 5 now covers cross-process cycles. Every automatic inter-process call (`api_copy`, `api_rpc`, `api`) is checked for a route back to the originating process without a break point, the trace runs after Step 11 once dependency schemes are pulled, and dispatcher-shaped processes are called out because the 1-level dependency pull will not surface 3+-hop cycles.
- Fix(mcp-server): `create-process` → `push-process` works again. The pre-push snapshot gate treated "this process was never deployed" as "cannot protect the previous version" and refused the push, though a fresh process has no version to lose. `create-process`/`create-folder` also placed objects in the CWD while a later pull mirrored them into the folder tree, producing two copies and split baseline sidecars — both now resolve the same placement.
- Fix(mcp-server): `processNeverDeployed` fails closed. A missing `commits` or `list` in a partial or reshaped API response used to read as "never deployed" and waive the snapshot protection of a process that already held state; both facts must now be positively confirmed.
- Fix(mcp-server): lint counts a Code-node read as a use of a `set_param` variable. `api_code` reads `data.name` / `data["name"]`, never `{{name}}`, so every `set_param` feeding a Code node was reported dead — pushing authors to rewrite a correct `set_param` as an `api_code`.
- Fix(mcp-server): `chunkify` is declared on `api_code`. Corezoid adds the flag to deployed Code nodes, so `pull-process` carried it back and the plugin's own pull → edit → lint → push loop failed on its own output.
- Fix(mcp-server): the equal-timestamp conflict check no longer has two fail-open gaps. Duplicate node titles could hide a server edit from the classifier (the whole scheme is now multiset-compared on any ambiguous key), and an export or parse failure now blocks the push unless `force=true` instead of warning and proceeding. Legacy sidecars with no recorded ancestor keep the warn-and-proceed path so pre-v3.1.3 pulls do not regress.
- Fix(mcp-server): `pull-folder`/`pull-project` JSON format and unmarshal failures name the offending file or directory instead of reporting a bare error.
- Docs: `show-task` documented across README, `POWER.md`, Troubleshooting, the Corezoid API integration reference and the task examples — including why not to page `list-node-tasks` for a known `ref`, and why `modify-task` with `deep_merge` is the wrong way to peek at task data.
- Chore(deps): bump `github.com/santhosh-tekuri/jsonschema/v6`.
- CI: bump GitHub Actions — `checkout@v7`, `setup-go@v7`, `upload-artifact@v7`, `dependency-review-action@v5`.

## [3.1.3]

- Fix(mcp-server): `serverMovedSince` is source-aware again. v3.1.2 removed the version tiebreak globally so a `ListFolder`-derived baseline would stop producing false positives on `pull-folder`, but the same change silently disabled same-second lost-update detection on the `pull-process` path where both sides come from `GetProcessByID` and versions ARE comparable. Baselines now carry a `source` tag (`detail` vs `list`); the tiebreak applies only when both sides are `detail`. Fixes #152.
- Fix(mcp-server): `push --merge` no longer claims "proceed cleanly" when the baseline sidecar could not be updated. The materialised merge is still preserved on disk, but the message now reports the baseline/ancestor write failure and warns that the next push will re-report the conflict. Fixes #151.
- Docs: README telemetry blurb aligned with `SECURITY.md` — both references now list the same fields (tool name, duration, error type, API hostname).

## [3.1.2]

- Revert(push-process): remove the developer approval gate introduced in 3.1.0. The extra confirmation on every push disrupted the normal dev iteration cycle in Corezoid workspaces without adding a meaningful safety guarantee — `push-process` is already an explicit tool invocation.
- Fix(mcp-server): merge no longer emits a graph with dangling links. A server-side node delete whose target is still referenced by a retained node is promoted to a delete-edit conflict, and a post-materialize graph-closure invariant runs as a hard safety net so an unforeseen classifier miss cannot silently write a broken merge.
- Fix(push-process): `force=true` no longer bypasses structural lint findings (broken links, old-format nodes, self-referencing `api_copy`/`api_rpc`). Those describe an invalid graph the server itself rejects — "override" is not a valid resolution.
- Fix(push-process): the concurrency gate now fails closed when the server-state fetch returns any error other than a genuine "not found". Silently proceeding when the Corezoid API is degraded disabled lost-update detection exactly when it was needed.
- Fix(push-process): the pre-push server snapshot is required for existing processes. A snapshot API failure now blocks the push instead of writing a warning and overwriting anyway — without a git safety net for `.conv.json` files, a failed snapshot means the previous server version is unrecoverable.
- Perf(mcp-server): `pull-folder` baseline capture reads `change_time` directly from the `list-folder` response instead of calling `GetProcessByID` per process, dropping baseline capture from O(N processes) API calls to O(folders).

## [3.1.1]

- Fix(mcp-server): `pull-folder` no longer clobbers the merge ancestor of locally-edited processes that live outside the pulled folder. The walk now skips files absent from the pre-export snapshot, preventing a later 3-way merge from seeing `base == mine` and silently dropping unpushed local edits.

## [3.1.0]

- Feat: **concurrent-change detection with 3-way merge** on `push-process`. `pull-process`/`pull-folder` record a pre-export server version and merge ancestor. A later push blocks when the server changed, reports local/server/overlap buckets across nodes and process-level fields, and supports re-pull, `merge=true` for a local reviewable merge with a `.pre-merge` backup, or an explicitly forced overwrite after attempting a server snapshot. Nodes match by title, or by `obj_type` plus ordinal when untitled. Baseline writes are locked and atomic; corrupt sidecars fail closed. Corezoid has no atomic compare-and-swap for process deploys, so this is a client-side read/compare/write guard and does not claim transactional isolation.
- Feat: new `corezoid-lifecycle` skill plus `pause-process`, `resume-process`, `move-process` and `move-folder` MCP tools. All four require explicit user intent — a review or refactor in progress is not authorization to mutate lifecycle or location; unsupported folder-container errors are named clearly instead of surfacing as opaque server rejections.
- Feat: **developer approval gate on every push**. `push-process` now requires explicit confirmation before deploying, so an automated or accidental tool call cannot silently redeploy a live process.
- Feat: MCP HTTP transport now **requires a bearer token** on every request and **caps request body size**, closing the previous unauthenticated-loopback attack surface. The stdio transport is unaffected.
- Fix(push-process): self-referencing `api_copy` (a Copy node whose target is itself) is detected offline and reported with the exact node name instead of surfacing as a generic server error.
- Fix(executor_nodes): `condition.logics` now enforces the trailing default `go` at execution time, matching the lint rule — a missing default `go` no longer slips through when the process is edited via MCP.
- Fix(run-task): a task is now created even when the deployed node list is unreadable (issue #143). Previously a transient read failure aborted the run instead of degrading gracefully.
- Fix(skills): Kiro skill inventory kept in sync with Claude and Codex; skill-sync CI check tightened.
- Docs: MCP debug log path corrected to `~/.corezoid/mcp.log` across docs and troubleshooting.
- CI: `govulncheck`, GitHub dependency-review and Dependabot enabled — Go vulnerabilities and supply-chain regressions are caught in PR CI.
- Security: Claude PR-reviewer workflow hardened — checkout split from the model step, `git`/`gh` dropped from model tools, credential-exfil vectors closed, and code-execution primitives removed from the reviewer job.
- Build: Go directive bumped to `1.26.6` to clear `govulncheck`.

## [3.0.0]

- **Breaking / auth**: MCP-server persisted state has moved from per-project `.env` files plus `~/.corezoid/credentials` into a single `~/.corezoid/config.json` (mode `0600`) keyed by working directory. Each entry holds `account_url`, `corezoid_url`, `apigw_url`, `workspace_id`, `access_token`, `api_login`, `api_secret`, `git_url`, `git_stage_path`; `stage_id` and `project_id` are read from the on-disk `<id>_<name>.stage.json` marker and never persisted in config. Cross-process safety uses `gofrs/flock` plus an in-process mutex; writes go through a serialised read-modify-write. Codex/Kiro subprocesses that do not inherit `cwd` fall back to `$COREZOID_WORK_DIR`. **All users must re-authenticate after upgrade** (run `/corezoid-init` or the login flow again); existing `.env`/`credentials` files are no longer read.
- Feat: new `corezoid-logout` skill — removes saved Corezoid credentials for the current workspace.
- Feat: `server/discover` implemented on both stdio and HTTP MCP transports, so hosts can enumerate this plugin's tools without a full handshake.
- Feat: safety annotations on every MCP tool (destructive / read-only / open-world hints); `set-stage-immutable` is marked destructive.
- Feat: **"No Project" workspace-root pull mode.** `pull-folder` can now anchor at `Folder.RootPath` and pull without a project id, so a workspace that hasn't been split into projects still syncs cleanly. `pull-folder` is now always anchored at `Folder.RootPath` instead of the caller's cwd.
- Feat: stage is resolved from the workspace marker (`<id>_<name>.stage.json`) instead of a `stage_id` argument; `stage_id` is persisted in config and flattened into `RootPath`.
- Feat: atomic login persistence with abandoned-workspace pruning — half-written login state cannot leak, and orphaned workspace entries are cleaned up on next login.
- Feat: `Origin` header is now set on Corezoid-bound HTTP requests, matching browser behaviour and unblocking gateways that require it.
- Feat: `api_code` `lang` enum accepts `erl` (Erlang).
- Feat: **`git_call` selection guardrails.** `lint-process` emits an advisory `GIT_CALL USAGE` finding for every `git_call`/`api_git` node (never blocks a push), and the `corezoid-create` and `corezoid-gitcall` skills plus `docs/nodes/git-call-node.md` define a concrete selection rule: default to native nodes → a Code (`api_code`) node → `git_call` only when a step needs file parsing, an external library, cryptography, or a custom runtime the former cannot provide. Hosted sandbox measurements show an approximately 60s execution deadline (handlers kept <50s), default 50MB RAM / 0.1 CPU from a shared pool, and ephemeral local storage; long-running loops/polling and external waits should be modelled as process state/callbacks instead of holding a handler open.
- Fix: `run-task` no longer triggers an implicit process redeploy — a task run used to silently re-push the process, which could clobber concurrent edits.
- Fix(mcp): WebSocket reader goroutine is released on early return; previously a failed handshake could leak a reader per attempt.
- Fix(mcp): `create-process` writes `.conv.json` (not `.json`), matching the naming `pull-folder` already used.
- Fix(mcp): `modify-task` documents its shallow-merge semantics and adds a `deep_merge` option for callers that need recursive merging.
- Fix(mcp): `serverInfo.version` is derived from the ldflags-injected `main.Version` instead of a hard-coded string, so `server/info` reports the real build.
- Fix(telemetry): analytics event schema aligned with `simulator-ai-plugin`; email-lookup race that could send events with an empty user id is fixed.
- Docs: `__queue_task_data__` contract and the correct `moment` require pattern documented.
- Perf: node box sizes are cached across `resolveOverlaps` and readability passes; large processes lay out noticeably faster.
- Docs: HTTP transport is clarified as local-only in `runHTTPServer` — the server binds loopback and is not intended for network exposure.
- Docs: Kiro manifest, `POWER.md`, `README.md`, `SECURITY.md`, `RELEASE_CHECKLIST.md`, `docs/Troubleshooting.md` and multiple skill/node docs updated for the new config layout (`corezoid-init`, `corezoid-alias-manager`, `corezoid-variable-manager`, `corezoid-git-context`, `corezoid-gitcall`, `corezoid-create`, `corezoid-project-review`; `git-call-node`, `get-from-queue-node`, `code-node-libraries`, `corezoid-api-integration`).
- CI: version-sync check now covers the Kiro manifest and `POWER.md` in addition to the Claude/Codex/marketplace manifests.
- CI: Claude Code review pipeline gains fork-PR support behind a maintainer `ai` label (via `pull_request_target`), the `github-actions` bot can dispatch the PR reviewer, the issue-worker skips only when an open PR exists, the auto-merge severity threshold is raised, and the AI worker no longer bumps versions on its own.

## [2.11.0]

- Feat: new `corezoid-git-context` skill + four MCP tools — `git-pull-context`, `git-push-context`, `read-context-file`, `update-context-file` — that sync a project's `.git-context/` with the Corezoid Gitea mirror (with a transparent local-only fallback when Gitea is unreachable), merge Developer Notes and a process index into `CLAUDE.md` via a marker block that leaves hand-authored content intact, and let Claude read/write `_ext/` documentation without touching the process JSON tree. `pull-folder` and `push-process` sync context and regenerate `CLAUDE.md` as part of their normal flow; per-invocation Basic auth goes via `GIT_CONFIG_*` env vars and is never persisted to `.git/config`.
- Feat: **layout engine driven by measured node geometry and readability signals.** Placement now uses real rendered sizes read from the live SVG DOM (see `docs/process/node-size-reference.md`) instead of conservative estimates that over-reserved ~74–77px on every Condition and ~29px per output row — the biggest source of "too much air between nodes". Strategy routing is now by error *ownership* (shared vs dedicated roots) rather than raw error-node fraction, so a long spine with one small dedicated cluster per action stays a readable waterfall. Sugiyama gains a Fenwick-based crossing counter, weighted median and adjacent transpose (attribution added in `THIRD_PARTY_NOTICES.md`); a new DIAMOND region handles the do-work-vs-skip fork. `layoutReport` now carries readability numbers (crossings, edge spans, upward edges) and a CI quality ratchet runs over every fixture.
- Feat: layout no longer changes `extra.modeForm`. Collapse/expand is the author's state; the engine only moves nodes. A sentinel blocks strategy-local mode changes and every `extra` is restored before the finishing geometry runs, so boxes are separated at their real rendered size.
- Feat: `push-process` and `pull-folder` now regenerate the marker-based `corezoid-mirror` block in the project's `CLAUDE.md` and — when Gitea is reachable — pull/push the git-context mirror as part of their normal flow, so Claude sees the freshest project documentation without extra tool calls.
- Feat: `.mcp.json` now includes an `mcpServers` wrapper for hosts that expect the wrapped shape, keeping compatibility with hosts that read the flat form.
- Fix: **call-process stub mode** is preserved and gated correctly on push — new `stub.json` schema, executor gating, lint rules and tests, and a full `stubbed_api_rpc.json` sample. Previously stubs could be silently stripped or bypassed.
- Fix: diamond orientation search was exponential in a way node count did not bound — 8 diamonds meant 256 full layouts (~23s inside the push path). Capped at 16 candidates (23s → 1.15s).
- Fix: `compact()` could CREATE an overlap because `cluster1D` chains transitively, so a "row" can span more than its tallest member and the next row was pulled into it. Masked in the main path by a later `resolveOverlaps`, but not in strategies that end with `compact`.
- Fix: a loop body was placed one row below the condition entering it, so the return edge climbed an extra row and cut across the spine; the polish pass then shoved the condition off the axis. Bodies now align to their entry row.
- Fix: `resolveNodeEdgeOverlaps` used to break column alignment to dodge link lines. Nodes sharing a column with a primary-chain neighbour are now left in place: a line under a box stays traceable, a jogging column does not.
- Fix: duplicate node ids were accepted silently, dropping nodes while reporting `overlaps=0`. `loadLayoutDoc` now refuses the file and names the offending ids.
- Fix(mcp): `create-alias` now derives the stage from the process being aliased instead of a frozen env-var value, so an alias created after a stage switch targets the right stage (issue #26).
- Fix(mcp): `create-folder` and `create-process` now send `project_id` in the create call, so the object lands in the intended project instead of the workspace default (issue #36).
- Fix(auth): `cachedProjectID`, `gitURL` and `gitStagePath` are invalidated on stage switch, host switch, and logout (previously only on workspace switch); a failed `COREZOID_API_URL` discovery no longer silently falls back to the wrong host.
- Docs: `code-node-libraries` — CryptoJS side-effect `require` examples corrected; the require pattern is `require('crypto-js/hmac-sha256')`, not the previous form which silently loaded the wrong module.
- Docs: `README.md` License section links to the `LICENSE` file.
- Docs: `SKILL.md` for `corezoid-node-layout` no longer claims a hard ±10000 clamp — `clampCoords` re-centres and cannot shrink a layout past the row-step floor, and the node schema allows ±100000 because real processes reach ~25500.
- Docs: `README.md` — Architecture ASCII tree now includes the Snapshots and Git-context tool groups, and the Project-structure tree lists all 22 skill directories instead of the original 8.
- CI: GitHub-native Claude Code automation — a set of workflows that run issue-worker → self-review → second-line PR review with severity-based auto-merge for low-risk PRs, retry when a previous PR was closed unmerged, and permission grants that let the issue-worker dispatch the PR reviewer and the `github-actions` bot invoke it.
- CI: release workflow now builds Windows binaries for `convctl` alongside the existing platforms.

## [2.10.0]

- Feat: a **from-scratch process now gets the full layout engine** on push (the same waterfall / layered+error-rail / regions strategies as the `layout-process` tool), instead of the lean grid — so a new process comes out cleanly arranged by default rather than cramped.
- Feat: **smooth expansion** when inserting a node. Adding a node directly above its own placed down-child now slides that child and its downstream subtree down one row to open an in-style gap, instead of nudging the new node far below; unrelated/parallel nodes stay put, and incidental overlaps still nudge. Already-placed nodes are otherwise never moved.
- Feat: create tools (`create-process` / `create-state-diagram` / `create-folder`) accept an explicit `folder_id` and report the resolved target ("created in Corezoid folder #N (explicit folder_id / resolved from marker X)") in the result.
- Feat: `run-task` accepts an optional `ref` — pass a caller-supplied task ref (e.g. one a downstream process keys off) instead of always generating a random `unix_ts_rand`. Backward compatible: omit `ref` and the auto-generated one is used.
- Feat: `lint-process` blocks two more server-hang classes before push — an `api_rpc_reply` whose `res_data`/`res_data_type` disagree, and an `api_*` logic node missing the canonical extras (`extra`, `extra_type`, `format`, `send_sys`, `debug_info`, `customize_response`, `rfc_format`, `cert_pem`, `version`). Both used to make `push-process` hang ~15–20s and fail with the opaque "no response from server"; they now surface immediately with the exact node and field named.
- Feat: `lint-process`'s reply-mismatch check now names the offending key. A `res_data` entry without its `res_data_type` (or vice versa) — including template, literal, `mode:"key_value"` and `mode:""` variants — is reported as `res_data key "status" has no matching res_data_type entry` instead of the server's vague "invalid value res_data or res_data_type, or both".
- **Breaking / behaviour**: tool calls now REJECT undeclared arguments with an error naming the unknown keys and the accepted list (previously unknown keys were silently dropped — a call could quietly act on the wrong object). Integrations passing stray keys must remove them.
- **Breaking / behaviour**: `create-process` / `create-state-diagram` / `create-folder` refuse a working directory that contains MORE than one `<id>_<name>.folder/stage.json` marker instead of silently picking the first one (which could target a production stage). Pass the new explicit `folder_id` argument or run from the specific folder's directory.
- Fix: `push-process` no longer destroys a hand-arranged layout. On push the auto-layout re-lays-out the whole process only when **every** node is unplaced (`x==0 && y==0`); an edit that dropped node coordinates therefore used to wipe a nice arrangement. Push now **re-hydrates** any lost coordinate from the process's current server version first (matched by node title, or `obj_type`+ordinal for untitled nodes), so an existing layout is preserved — whether the edit dropped every coordinate or just some — and only genuinely-new nodes are placed; it reports `Restored N node coordinate(s)`. The server is consulted only when some node arrives unplaced.
- Fix(login): add `prompt=login` to the OAuth `/authorize` request and validate `exp` on the returned token — the browser used to reuse a stale SSO session and hand back a token whose `exp` was already in the past, and the plugin silently accepted it and reported "Setup complete". `logout` now also reminds the user that the browser SSO session at `account.corezoid.com` must be cleared for a clean re-login.
- Fix(codex): MCP server startup now works under Codex. The `.mcp.json` command chain preserves the caller's `PWD` as `COREZOID_WORK_DIR` and resolves the installed plugin root separately, since Codex does not expose `CLAUDE_PLUGIN_ROOT` inside MCP subprocesses.
- Fix(pull-process): sanitize `/ \ : * ? " < > |` in titles when computing on-disk filenames (previously only spaces). A process titled `/chat_v2` now writes as `_chat_v2.conv.json`, matching the naming `pull-folder` already used. Also applied to `create-process`, `create-folder`, and to parent folder titles used as directory segments.
- Fix(run-sh): actionable error when a prebuilt `convctl` asset can't be downloaded — the script prints the exact version and a direct link to the GitHub releases page instead of silently falling through to `go run .`. If Go is also missing, it now exits with a clear message listing the two recovery paths (update the plugin, or install Go) instead of `exec: go: not found`.
- Fix: widen node `x`/`y` schema bounds from ±10000 to ±100000. Large real-world processes (a 760-node process was observed with nodes at ~25500 / -10920) legitimately lay out beyond ±10000, and schema validation happens *before* the lint force override, so `force=true` couldn't rescue them; push now accepts them.
- CI: `scripts/check-skills-sync.py` verifies that every directory under `plugins/corezoid/skills/` is referenced in both `CLAUDE.md` (Architecture section) and `README.md` (skills table), and vice versa — so new skills can't silently drift out of the docs.
- Docs: `CLAUDE.md` and `README.md` skill lists brought back in sync with the current skill set (state-diagram-create/edit, node-layout, describe, alias-manager, variable-manager, api-connector, gitcall, retro, feedback, process-optimizer, process-tech-writer); `CLAUDE.md` gains the MCP-server build/test/lint commands and a summary of repo-level CI checks.
## [2.9.0]

- Feat: `push-process` now runs `lint-process` before deploying and blocks on issues that would break the deploy or its callers (broken node links, old-format nodes, RPC paths without reply, nodes missing a default `go`, sub-30s timers, literal reply values); advisory findings are shown but do not block, and `force=true` overrides. Advisory findings from `lint-process` are also surfaced back on a successful push instead of silently swallowed.
- Feat: `lint-process` detects **broken node links** — a `to_node_id`/`err_node_id`/`go_to` (or a semaphor `to_node_id`/`esc_node_id`) pointing at a static node id absent from the process; the server rejects such a deploy ("referenced node does not exist"), now caught offline. Dynamic `{{...}}`/`@alias` targets resolve at deploy and are left alone.
- Feat: `lint-process` catches four more classes of deploy/UX hazard before push: **old-format nodes** (an `err_node_id`/`esc_node_id` target left at `obj_type: 0`, or an action logic mixed with `go_if_const` in one node — both make the UI show "Convert process to new format" and rewrite the scheme), **RPC paths without reply** (a final reachable from Start without any `api_rpc_reply` in a process that replies elsewhere — an `api_rpc` caller would hang until timeout), **nodes without a trailing default `go`**, and **time semaphors under the 30-second server minimum**. Graph walks now also follow count-semaphor `esc_node_id` edges, so escalation clusters are no longer misreported as orphaned or missed by the shared-cluster check, and the passthrough-escalation check no longer misfires on retry Condition/Delay escalations.
- Feat: `lint-process` detects **shared error clusters** — an error node fed by the error paths of several different failing nodes (direct `err_node_id` fan-in or converging escalation tails). The house rule is one dedicated Reply/Error cluster per failing node; a single node's error fanning through its own Condition into one Error terminal stays allowed.
- Feat: `lint-process` flags literal non-string values in `api_rpc_reply` `res_data` — the platform's reply nodes expect strings; numeric/boolean/object literals silently break downstream callers.
- Feat: `lint-process` requires `extra_headers` and `max_threads` on `api_call` node schemas — the platform rejects the "light" node shape at deploy, now caught offline.
- Feat: `layout-process` MCP tool — deterministic auto-layout engine (waterfall for simple trees, sugiyama-lite + error rail for meshes, aligned TABLE/STAR region grids), fully in Go inside convctl. It rewrites only `x`/`y` and the `extra.modeForm` collapse flag, preserves the source file's indentation and trailing newline, and always reports the chosen strategy, canvas size and overlap count; `dry=true` previews without writing, `density=compact|medium|roomy` controls spacing.
- Feat: `layout-process` places every error cluster next to the node it protects. Waterfall and region strategies pin each Reply/Error cluster beside its owner in a compact staircase distilled from hand-tuned production layouts; the layered strategy's error rail got a monotone cursor so clusters of same-row owners no longer pile onto one point; count-semaphor `esc_node_id` targets are treated as error edges instead of drifting to the orphan grid.
- Feat: code-enforced node placement on `push-process` — new nodes added with placeholder coordinates (`x: 0, y: 0`) are auto-placed by the MCP server. Preserve mode is the default: already-placed nodes are never moved, only the new `(0,0)` nodes are slotted near their graph neighbours without overlap; a fully-new process gets a clean layered layout. Disable with `COREZOID_AUTOLAYOUT=off`.
- Feat: new `corezoid-node-layout` skill — auto-arranges a process's node x/y into a clean, readable layout and rewrites the `.conv.json` in place (positions + IF/Delay/error collapse only; edges, logic, `conv_id`, aliases and node types stay byte-for-byte intact). Simple tree-like processes use a "waterfall" (branches fanned around a central column); large mesh processes use a layered algorithm (dummy nodes for long edges + median crossing-minimisation + priority coordinate straightening). The 12 layout invariants run as ordinary go tests plus golden coordinate files. Run it as the last step before `push-process`.
- Feat: full env-var lifecycle from the IDE — `list-variables`, `modify-variable` and `delete-variable` MCP tools. Both write tools are dry-run-by-default and confirm-gated (`confirm="<short_name>#<obj_id>"`): modify shows a current → new diff (rename additionally scans local `.conv.json` files for `{{env_var[@old-name]}}` references), delete shows a red permanent-deletion warning block that the AI must present to the user verbatim — env vars have NO recycle bin. Secrets are always masked in every output.
- Feat: API-key (login+secret) auth as a Simulator token fallback — sign requests with double-salted SHA1 when no OAuth session is available, so users on API-key-only stages can still drive the plugin.
- Feat: `install-kiro.sh --power [output-dir]` — build a portable, importable Kiro Power bundle (`POWER.md`, `mcp.json`, `steering/*.md`, `docs/`) alongside the workspace-install mode.
- Feat: `install-kiro.sh --install-power` — build the Power bundle and install it directly into this machine's local Kiro (`~/.kiro/powers/installed/power-corezoid/`, registered in `~/.kiro/powers/installed.json` via a safe `python3` JSON merge), bypassing the Powers panel's "Import from folder" UI. Plain `install-kiro.sh [workspace-dir]` now always also runs `--install-power`, so the plugin stays registered as a Kiro Power globally, not just installed into one workspace.
- Feat: `.mcp.kiro.json` ships with `"disabled": true`; `install-kiro.sh --install-power` writes the Power's own MCP entry into Kiro's global `~/.kiro/settings/mcp.json` under `powers.mcpServers.power-power-corezoid-corezoid` and force-enables it there, since that's the entry Kiro actually runs for an installed Power.
- Change: the standalone stage-export scanner is removed — the `corezoid-stage-scan` skill and its `scan_stage.py` are gone. Its per-process check moved into `lint-process`; cross-process `conv_id`/`api_get_task` reference validation is left to the server, which already rejects bad references on deploy, rather than duplicated offline.
- Fix: `deploy-stage` no longer refuses a deploy with a false "unexpected/conflicting status" when a process was deleted on the source stage. `/api/2/compare` reports such objects as `"deleted"`; the UI merge propagates the deletion without complaint, and the tool now does the same.
- Fix: `deploy-stage` failures are now diagnosable. A failed compare carries a nested `errors` tree naming the exact stage → process → node and the reason (empty scheme, orphan node, a reference into another project, …); the tool previously swallowed it and printed only the bare description. A genuinely unrecognized compare status now also lists each offending object with its id, title and the literal status value instead of an anonymous count.
- Fix: `deploy-stage` gives a definitive good/bad verdict when the progress WebSocket fails. Compare is re-run with retries — an empty diff reports a verified success, a leftover diff reports UNCONFIRMED as an error. Previously every fast merge ended with a scary "completion could not be confirmed over the WebSocket" warning on a successful deploy.
- Fix: AWS Kiro MCP server failed to start after `install-kiro.sh` — the `.mcp.kiro.json` fallback path pointed two directory levels above the actual `mcp-server/run.sh` location. The installer now resolves `PLUGIN_ROOT` to an absolute path and bakes it into the generated `.kiro/settings/mcp.json` at install time.
- Fix: `.mcp.kiro.json`'s `PLUGIN_ROOT` resolution now probes for `mcp-server/run.sh` and appends `/plugins/corezoid` only if the direct path doesn't exist, instead of assuming one fixed layout; fails with a clear error if neither layout matches.
- Fix: `install-kiro.sh` sed-substitutes `settings/mcp.json` from `.mcp.kiro.json` instead of duplicating the MCP command/args inline, keeping the two in lock-step.
- Fix: `--power` bundle mode resolves `$CLAUDE_PLUGIN_ROOT` doc references to this repo clone's absolute `docs/` path — Kiro's power-install step drops everything except `POWER.md`, `mcp.json`, and `steering/`, so a relative path was always going to be a dead link.
- Fix: sync version drift that had accumulated across `.agents/plugins/marketplace.json`, `.codex-plugin/plugin.json`, `.kiro-plugin/plugin.json`, and the repo-root `POWER.md`.
- Fix: `pull-process` falls back to the list API for undeployed processes so node IDs and structure still land on disk instead of an empty scheme.
- Fix: `push-process` writes server-assigned node IDs to the local file only after the commit succeeds, so a failed push no longer leaves the on-disk process desynced from the server.
- Fix: `push-process` surfaces the actual server commit rejection instead of a bland "no response" — the underlying error is threaded up from the Executor and shown to the user.
- Fix: wire `COREZOID_DEBUG` all the way through the Executor trace and redact secrets in the debug log; API-key URLs are masked before logging, and the folder probe order is corrected so private/shared folders resolve on the first try.
- Fix: `modify-variable`/`delete-variable` and other variable tools surface the auth cause when project resolution fails instead of a generic "project not found".
- Fix: JSON schema accepts `null` for node-level `extra` — the platform emits it on nodes that don't carry extras, and the schema was previously rejecting the pulled shape.
- Fix: bundled api samples conform to the required api schema (`extra_headers`, `max_threads`); the retry/error-handling sample gained the required fields.
- Docs: AWS Kiro install/update instructions in `README.md`; note the global-Kiro-Power registration side effect.
- Docs: expanded `docs/nodes/` for `api_call`, `api_rpc_reply`, and `call-process` / `reply-to-process` — reference examples now carry the required `extra_headers`/`max_threads` and document the exact reply-value contract.
- Docs: process-level `callback_hash` + rotate/disable flow in `corezoid-alias-manager`.
- Docs: correct signature label from HMAC-SHA1 to double-salted SHA1 in `main.go` and auth docs.
- Docs: state that the standard error-path Condition and retry Delay are authored collapsed (business Conditions stay expanded); wire `layout-process` into the create and edit skill flows.
- Docs: reword `steering/corezoid.md`'s tool-routing note to be accurate for both the workspace-install skill layout and the Kiro Power steering layout; the always-on guardrails file also ships in the Power bundle as `steering/corezoid-guardrails.md`.
- Chore: gitignore the `power-corezoid/` build output.

## [2.8.0]

- Feat: process snapshots — new MCP handlers (`create-snapshot`, `list-snapshots`, `restore-snapshot`) and an auto-snapshot taken before every `push-process`; snapshot titles include a timestamp and the `.env` write notice is surfaced back to the user.
- Feat: `deploy-stage` and `set-stage-immutable` MCP tools — deploy from one stage to another (with a source-stage-deployed precheck) and mark a stage immutable without leaving the IDE.
- Feat: `git_call` node support in `push-process` — schema validation for `api_git`/`git_call` (including `code_error`), multi-language build-log integration tests across all runtimes, and the build log is surfaced in the push result on failure.
- Feat: `run-task` polls for the final node and accepts a `wait_sec` parameter for long-running tasks.
- Feat: capture MCP client identity (`clientInfo.name`/`version` from the `initialize` handshake) and attach it as `client_name`/`client_version` to every analytics event; both stdio and HTTP transports parse it via one shared `parseInitializeParams()`.
- Feat: flush buffered analytics events on shutdown — SIGINT/SIGTERM and deferred exit paths drain the sender queue synchronously instead of losing anything short of the 20-event/5s batch threshold.
- Feat: new skills — `corezoid-gitcall` (build/publish git_call nodes), `corezoid-describe` (safe process-description updates), and `corezoid-retro` (retrospective analysis).
- Fix: return HTTP 404 when a request carries an `Mcp-Session-Id` the server doesn't recognize, per the Streamable HTTP spec. Previously it silently degraded to the process-global client identity with no signal to the client that its session was gone. `initialize`, notifications, and unsessioned requests keep the existing graceful-fallback behaviour.
- Fix: track MCP client identity per HTTP session (keyed by `Mcp-Session-Id`, threaded through `context.Context` into `handleToolCall`) instead of a single process-global. In HTTP mode one server process serves many concurrent clients, and the previous global let the most recent `initialize` silently overwrite every other client's analytics attribution. Adds a 1h idle-session sweep. Covered by a 20-client concurrency test through `httptest.Server`.
- Fix: guard the remaining MCP client-identity globals with a mutex (`clientSupportsElicitation`, `clientName`, `clientVersion`); reads go through `clientElicitationSupported()`/`clientIdentitySnapshot()`, mirroring the existing `authStateMu` pattern. Caught by `-race` and reproduced with a torn-pair concurrency test.
- Fix: guard `stopAnalytics()` with a `sync.Once` — three call sites (deferred, signal handler, HTTP-error path) previously blocked on `analyticsFlushCh` for up to 2s after the sender goroutine had already exited.
- Fix: `api_copy` compare/merge operations now route to their own `/api/2` endpoints.
- Fix: allow object cast in `go_if_const` conditions.
- Fix: `pull-folder` skips hidden directories and handles permission errors instead of aborting the walk.
- Fix: accept absolute paths that resolve inside the project root.
- Docs: expand `corezoid-api-integration.md` to a full pattern reference.
- Docs: dedicated per-node error-cluster pattern in `error-handling.md`.
- Docs: node-positioning best-practices note.
- Docs: `README.md` lists the new `corezoid-gitcall` skill.

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
