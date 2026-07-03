---
name: corezoid-index
description: >
  Build or refresh the persistent project index (`.corezoid/project-map.json`,
  `QUERIES.md`, CLAUDE.md auto-block) for a pulled Corezoid project. Activate
  when the user says "build project index", "regenerate index", "index
  project", "update project map", "corezoid index", or asks a question that
  needs cross-process knowledge — "does a process for X already exist", "who
  calls process Y", "where is variable @Z used", "which processes hit this
  API", "any cycles in the project" — and no index has been built yet. Also
  activate when the user reports that `.corezoid/project-map.json` is out of
  date or missing.
---

# Build / Refresh the Corezoid Project Index

You are a specialist in building the persistent, on-disk index that lets
Claude answer cross-process questions about a Corezoid project without
grep-walking every `.conv.json` file each session.

## What the index is

Three files, all written into the **currently pulled project's** directory
(never into the plugin itself):

| File | Purpose | Regenerable |
|---|---|---|
| `.corezoid/project-map.json` | Structured graph: processes, edges (`api_rpc`/`api_copy`/`api_get_task`), aliases, env-var usage, external HTTP endpoints, DB instances, security hotspots, graph statistics. | Yes (rebuild anytime) |
| `.corezoid/QUERIES.md` | `jq` command recipes for the 9 common questions an agent asks about a project. | Yes |
| `CLAUDE.md` (auto-block) | Short summary between `<!-- corezoid-index:start -->` markers, loaded automatically into every Claude Code session in this project. | Yes (block only; content outside markers preserved) |
| `.corezoid/index-config.json` | Per-project heuristic keyword lists (entry_point / suspicious_name) **and** the `config_references` allow-list (which config ref names to track in the diagram scan). Created with defaults on first run, **never overwritten** — hand-edit to override defaults for non-English project naming or to add/remove config ref names. | Created once, not on rebuild |

## When this skill runs vs when automation runs

- `corezoid-init` invokes `build-project-index` automatically at the end of a
  fresh `pull-folder`.
- `corezoid-edit` and `corezoid-create` rebuild the index automatically after
  a successful `push-process`.
- **This skill** is for everything else: a project pulled before automation
  landed, a project edited outside the plugin, a user who explicitly asks to
  refresh the map, or a question that needs the index and no index yet exists.

## Step 1 — Verify environment

Read `.env` in the current working directory (or up to the project root). If
`COREZOID_STAGE_ID` is missing or empty, do **not** try to build the index —
invoke the `corezoid-init` skill first. Same pattern as `corezoid-project-review`
Step 0.1. The index needs a real pulled project on disk; without one, there
is nothing meaningful to index.

## Step 2 — Build (or refresh) the index

Call MCP tool **`build-project-index`** with no arguments (or with
`project_path` if the pulled project lives in a subdirectory). The tool:

- Walks the project tree
- Parses every `.conv.json`, `_ALIASES_.json`, `_ENV_VARS_.json`, and
  `*.instance.json` if present (missing optional files are not an error)
- Writes `.corezoid/project-map.json` and `.corezoid/QUERIES.md`
- Updates the auto-generated block in `CLAUDE.md`
- Creates `.corezoid/index-config.json` with defaults on first run only

**Never** run `build-project-index` inside the plugin's own repository — it
will find no `.conv.json` files and produce an empty, meaningless index.
Always run it in the directory of a pulled Corezoid project.

## Step 3 — Report a short summary

The tool returns a concise multi-line summary. Show it to the user verbatim
if the counts are interesting; otherwise a one-liner is enough. Aim for **≤ 10
lines** of your own output. Do not restate the full contents of
`project-map.json` — that defeats the whole point of having a queryable file.

If any of these appear in the summary, call them out explicitly:

- **Config references (> 0)** — mention how many configured refs are actively used and which local state-stores back them (via `local_conv_id`). The index stores structure (who reads which refs, which `.ref[X]` fields), **not values**. If you need the actual runtime config values before working with a process, call `list-node-tasks` on the state-store process identified by `local_conv_id` — see "Reading live config values" below.
- **Security hotspots (> 0)** — mention that these list field NAMES and
  locations only; no values ever leak. Suggest reviewing `.security_hotspots`.
- **Cycles (> 0)** — flag as "risk to edit"; direct the user to
  `.graph_stats.cycles`.
- **Orphaned candidates (> 0)** — phrase as "no internal caller in this
  project, verify before deletion"; never as "dead code".
- **First-time CLAUDE.md creation** — the tool prints a one-line notice; do
  not suppress it. This is intentional: the plugin is mutating a user file
  for the first time in this project, and the user must know it happened.

## Step 4 — Point at `QUERIES.md`

Close by mentioning `.corezoid/QUERIES.md`. It documents the 9 most common
questions (who calls X, what does X call, resolve alias, find env var usage,
fuzzy title search, process card, risk overview, external APIs + secrets,
manual rebuild) with the exact `jq` command each. Direct the user there for
follow-up questions instead of re-loading the whole `project-map.json` into
context.

## Reading live config values

The index records **structure** — which processes read which config, via which fields — but never stores the actual runtime values. Values are intentionally excluded: they change frequently, may contain secrets, and an index built days ago would serve stale data.

To read current config values before working on a project:

1. Look up the config's `local_conv_id` in `.config_references`:
   ```
   jq '.config_references["config"].local_conv_id' .corezoid/project-map.json
   ```
2. Find the api_callback node of that state-diagram:
   ```
   jq '.scheme.nodes[] | select(.condition.logics[]?.type == "api_callback") | .id' \
     <path_to_state_diagram.conv.json>
   ```
3. Call `list-node-tasks(process_id=<local_conv_id>, node_id=<callback_node_id>)` to see the current task payload.

This keeps live secrets out of any file that could be committed or cached.

## Checking freshness without a full rebuild

If the user just wants to know whether the existing index is up to date (for
example after `git pull` on a shared project), call the tool with
`mode="check"`. It runs a fast `size`/`mtime`/`hash` comparison — no rebuild
— and returns the list of files that diverged, if any. Use this before
recommending a full rebuild: a report of "up-to-date" saves the user a wait.

## Key facts about the schema

- `edges[]` is the single source of truth for the graph; `calls_out`,
  `calls_in`, and `state_stores[].written_by` are derived from it.
- Cross-process edges are only `api_rpc`, `api_copy`, `api_get_task`.
  `api_callback` marks a receiver node but is not an edge.
- Env var values are **never** stored. `env_vars[name].used_by` lists which
  processes reference `{{env_var[@name]}}`; the value stays in Corezoid.
- Secret detection reports `security_hotspots[]` with field names and
  locations only, never values — the pattern matches both `.instance.json`
  fields and hardcoded-looking values in diagram `api` nodes' `url`,
  `extra`, and `extra_headers`.
- State processes (`conv_type == "state"`) are excluded from
  orphaned/entry_points classification — a state store with no incoming
  writes is a legitimate read-only lookup, not a dead-code signal.

## Reference: what the tool detects

| Signal | Where it comes from |
|---|---|
| Process inventory + hashes | Every `<id>_<name>.conv.json` |
| `api_rpc` / `api_copy` / `api_get_task` edges | `scheme.nodes[].condition.logics[]` |
| External APIs | `url` field of `api`-type logics |
| DB instance calls | `instance_id`/`instance_name` of `db_call` logics |
| Env var usage | `{{env_var[@NAME]}}` regex over all string values in logics |
| Alias resolution | `_ALIASES_.json` at project root |
| Env var declarations | `_ENV_VARS_.json` at project root (values NOT read) |
| Instance secrets | `.instance.json` `data.*` field names matching `pass\|password\|secret\|token\|apikey\|api_key\|private_key` |
| Diagram-hardcoded secrets | Same regex against `extra`/`extra_headers` field names in `api` logics, with a heuristic filter on value shape to reduce noise |
| Folder breadcrumbs | Walk up parents, read `<id>_<name>.folder.json` markers |
| Config references (`config_references`) | Purely local scan for `{{conv[@ref]...}}` patterns matching the allow-list in `.corezoid/index-config.json` → `config_references.tasks`. For each configured ref that IS referenced by some diagram: records `used_by` (caller conv_ids), `read_fields` (the `.ref[X]` field names extracted from the diagram), and `local_conv_id` if the ref resolves to a local state-store via `_ALIASES_.json`. When the ref does resolve to a local state store, the reader info is also mirrored into the corresponding `state_stores[cid]` entry (`read_by`/`read_fields`) so writers and readers live side by side. Provider-agnostic: the actual config values may be stored anywhere (external state store, Simulator, another Corezoid stage) — the index only shows the local usage picture. |
