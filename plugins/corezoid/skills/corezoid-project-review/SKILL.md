---
name: corezoid-project-review
description: >
  Corezoid project review and audit specialist. Use when the user wants to
  review or audit an entire Corezoid project or folder — multiple processes at
  once. Activate when the user says "review project", "audit project",
  "review all processes", "audit all processes", "review folder",
  "review all processes in", "project-wide review", "cross-process analysis",
  "find issues across processes", "review the whole project", or "audit folder".
---

# Review a Corezoid Project

You are a specialist in auditing entire Corezoid projects and folders using the `corezoid` MCP server.

Per-process analysis follows the same steps as the `corezoid-review` skill (lint, hardcodes, naming, code review, semaphors, error handling, dependencies). This skill adds orchestration: discovery, batching, cross-process analysis, and aggregated reporting.

---

## Phase 0 — Project Discovery

### Step 0.1: Verify Environment

Read `.env` from the current working directory and check for `COREZOID_STAGE_ID`.

- If `COREZOID_STAGE_ID` is **missing or empty** → stop and invoke the `corezoid-init` skill. Do not proceed until init completes.
- If `COREZOID_STAGE_ID` is present → use it as the root `folder_id` for the review scope.

### Step 0.2: Build Process Inventory

**Prefer the index over full re-pull.** If `.corezoid/project-map.json` exists, use it as the process inventory — one file read gives you every `conv_id`, `title`, folder breadcrumb, node count, and status without any `pull-process` calls. This is the reason the index exists.

- Call MCP tool **`build-project-index`** with `mode: "check"` first. If the report says "up-to-date", the index is safe to use. If any files diverged (`changed`/`added`/`removed`), call `build-project-index` with no arguments to refresh, then continue. If either tool errors, or the index doesn't exist, fall back to the legacy path: `pull-process` per known process to reconstruct the inventory.
- Read `.corezoid/project-map.json`. `processes{}` is `process_inventory[]` in map form (conv_id keyed). Skip entries where `conv_type == "state"` unless explicitly requested — state stores are audited under Phase 2 alias/cross-process analysis instead of per-process audit.

Store as `process_inventory[]`. Only fall back to `pull-process`-based inventory building when the index is unavailable or the fallback was requested explicitly.

### Step 0.3: Announce Scope

Before starting, report:

```
Found N processes in project "<project_name>":
  - Process A (conv_id: 12345)
  - Process B (conv_id: 67890)
  ...
Proceeding with full review.
```

Process all sizes automatically without confirmation. Stream progress in batches of 10.

---

## Phase 1 — Per-Process Audit

For each process in `process_inventory[]`:

1. Pull the process with MCP tool **`pull-process`** using `process_id`
2. Run the full per-process audit (same steps as `corezoid-review` skill):
   - **Step 1** Structural lint (`lint-process`)
   - **Step 2** Load and parse nodes
   - **Step 3** Hardcode check
   - **Step 4** Repeated logic
   - **Step 5** Cycle verification
   - **Step 6** Node naming
   - **Step 7** Code node analysis
   - **Step 8** Semaphor coverage
   - **Step 9** Error handling review
   - **Step 10** External dependencies inventory
3. Store result as `process_reports[conv_id]`
4. Log progress: `Reviewed N/total: "<title>" — X findings`

Reference: `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-review/SKILL.md`

---

## Phase 2 — Cross-Process Analysis

Requires all `process_reports[]` from Phase 1.

### Step 2.1: Dependency Graph

**Prefer the index over reconstruction.** If `.corezoid/project-map.json` is available and fresh (from Step 0.2), take the dependency graph directly from it — no pass over `.conv.json` files is needed:

- Edges → `.edges` (each element has `from`, `to`, `type`, optional `mode` for `api_copy` and `via_alias`; type is one of `api_rpc`, `api_copy`, `api_get_task`)
- Fan-in / fan-out → `.graph_stats.fan_in` / `.graph_stats.fan_out` per conv_id
- High fan-in / fan-out — `.graph_stats.high_fan_in` / `.graph_stats.high_fan_out` (thresholds > 5 / > 7)
- Cycles → `.graph_stats.cycles` (each is a normalised list of conv_ids)
- Orphaned candidates → `.graph_stats.orphaned` (each with `suspicious_name: bool`)
- Entry points → `.graph_stats.entry_points`
- External `conv_id` references → `.unresolved_targets` (conv_ids referenced but not in this project inventory)

**Do not interpret an empty `state_stores[cid].written_by` as `orphaned`.** State stores (`conv_type == "state"`) are excluded from the orphaned heuristic by construction — a state store with no writers is a legitimate read-only lookup, not dead code. This is a deliberate design choice in the index; treat `state_stores` and `orphaned` as unrelated signals.

Flag:
- ⚠️ **Circular dependencies** — from `.graph_stats.cycles`
- ⚠️ **Orphaned candidates** — from `.graph_stats.orphaned`; phrase as "no internal caller in this project, verify before deletion", never as "dead code"
- ⚠️ **High fan-in** — from `.graph_stats.high_fan_in`; single point of failure
- ⚠️ **High fan-out** — from `.graph_stats.high_fan_out`; coupling risk
- ℹ️ **External calls** — from `.unresolved_targets`

Fallback: if the index is unavailable (Step 0.2 already surfaced that), reconstruct the graph the legacy way — one `pull-process` per inventory entry, walk `api_rpc`/`api_copy`/`api_get_task` logics manually. Note that this is much slower than the indexed path; suggest running the `corezoid-index` skill afterwards so the next review is fast.

### Step 2.2: Duplicate Logic Across Processes

- Two processes with identical or >80% similar `api_code` nodes → candidate for shared subprocess
- Two processes calling the same external URL with same parameters → candidate for shared wrapper process

### Step 2.3: Shared Hardcoded Values

Aggregate only normalized `hardcode.*` findings from per-process reports. Do **not** aggregate dynamic Corezoid expressions, `dependency.state_store_ref`, or values fully wrapped in `{{...}}` as shared hardcodes.

- Same URL in > 2 processes → recommend shared `env_var` (use `/corezoid-variable-manager` to create)
- Same numeric `conv_id` in > 1 process → one alias fix resolves all
- Same token/key fragment in > 1 process → security risk, centralize immediately via `/corezoid-variable-manager` as `secret` variable
- Same status string in > 2 processes → recommend shared constant or `env_var`
- Same error text in > 1 process → recommend shared text constant

False-positive guard (same as per-process review):
- Ignore values that are fully dynamic expressions, e.g. `{{conv[@storage].ref[{{key}}].field}}`
- If a shared value is a state-store alias, report it under dependency analysis instead of `cross_process.shared_hardcode_value`
- Recompute aggregate summary counts after removing false positives

### Step 2.4: Alias Consistency

Use `.by_alias` from `.corezoid/project-map.json` as the resolved alias↔conv_id map instead of walking `_ALIASES_.json` and diagram references manually. Then cross-reference against `edges[]` and per-process `aliases[]`.

Flag:
- Same alias used with both `create` and `modify` in different processes → race condition risk (scan `edges` for `api_copy` with the same `via_alias` and differing `mode`)
- Alias defined in one process but used with numeric `conv_id` in another → inconsistency (compare `.by_alias` targets with numeric `conv_id` references in `edges`)
- Aliases referenced in processes but absent from `.by_alias` → falls out naturally as entries in `.unresolved_targets`

To fix alias issues found here (create missing aliases, rename, repoint, delete conflicts),
use the `/corezoid-alias-manager` skill.

### Step 2.5: Environment variable usage

Read `.env_vars` from `.corezoid/project-map.json` — a map of `var_name -> { description, used_by }`. Cross-check against `_ENV_VARS_.json` (if present).

Flag:
- Variables in `_ENV_VARS_.json` with empty `used_by` → potentially dead variable declaration (`env_var_unused`)
- References in `used_by` for variables not declared in `_ENV_VARS_.json` → undeclared variable used, often a typo or a variable defined at workspace level but not tracked in this project's export (`env_var_undeclared`)

### Step 2.6: Naming Consistency

Flag:
- Same vague name used widely (e.g. 10+ nodes named `"error"` across project) → recommend naming standard
- Mixed conventions across processes (`Create_X` vs `createX`)

---

## Phase 3 — Aggregate Report

Produce two output files: `project-review-<date>.json` and `project-review-<date>.md`.

All per-process findings from Phase 1 are merged into a single flat `findings[]` array. Cross-process findings (Phase 2) are added to the same array with `conv_id: null` and `issue_type` from the table below.

Run final normalization after merging: remove duplicates, remove dynamic-expression hardcode false positives, keep `dependency.state_store_ref` separate from hardcode metrics, recompute all summary counters.

### Cross-Process Issue Types

| issue_type | issue_subtype | severity |
|------------|---------------|----------|
| `cross_process` | `circular_dependency` | high |
| `cross_process` | `orphaned_process` | warning |
| `cross_process` | `high_fan_in` | warning |
| `cross_process` | `high_fan_out` | warning |
| `cross_process` | `external_call` | low |
| `cross_process` | `shared_hardcode_url` | high |
| `cross_process` | `shared_hardcode_token` | high |
| `cross_process` | `shared_hardcode_value` | medium |
| `cross_process` | `duplicate_logic` | low |
| `cross_process` | `alias_conflict` | warning |
| `cross_process` | `env_var_unused` | low |
| `cross_process` | `env_var_undeclared` | warning |
| `cross_process` | `naming_convention` | low |

Cross-process finding example:

```json
{
  "conv_id": null,
  "process_title": null,
  "node_id": null,
  "node_title": null,
  "issue_type": "cross_process",
  "issue_subtype": "shared_hardcode_url",
  "severity": "high",
  "value": "https://api.openai.com",
  "location": "found_in: [1779750, 1779754, 1782365]",
  "recommendation": "extract to shared env_var OPENAI_API_URL"
}
```

### Step 3.1: Per-Process Summary Table (Markdown)

| Process | Findings | High | Medium | Warning | Low |
|---------|----------|------|--------|---------|-----|
| Process A (12345) | 12 | 2 | 3 | 4 | 3 |
| Process B (67890) | 5 | 0 | 1 | 2 | 2 |
| Cross-process | 4 | 1 | 1 | 2 | 0 |
| **Total** | **N** | | | | |

### Step 3.2: Top Issues List (Markdown)

```
🔴 CRITICAL (fix before release)
  1. [Process A / Node X / semaphor / api_callback_missing] — tasks will hang
  2. [Cross-process / shared_hardcode_token] — token "sk-xxx" in 3 processes — revoke & move to env_var
  3. [Process B / Node Y / hardcode / url] — external URL hardcoded — extract to env_var

🟡 IMPORTANT (fix in next sprint)
  4. [Cross-process / circular_dependency] — Process B → Process A → Process B
  5. [Cross-process / shared_hardcode_url] — https://api.example.com in 4 processes
  6. [Process D / Node Z / dependency / missing_alias] — numeric conv_id 44444 — replace with @alias

⚠️ WARNINGS (technical debt)
  7. [Cross-process / orphaned_process] — Process C never called — possible dead code
  8. [Cross-process / duplicate_logic] — Process A, Process D — identical code nodes
```

### Step 3.3: JSON Output Schema

```json
{
  "project_name": "<name>",
  "project_id": "<id>",
  "review_date": "YYYY-MM-DD",
  "process_count": 0,
  "summary": {
    "total_findings": 0,
    "per_process_findings": 0,
    "cross_process_findings": 0,
    "by_severity": { "high": 0, "medium": 0, "warning": 0, "low": 0 },
    "by_type": {
      "hardcode": 0,
      "semaphor": 0,
      "structural": 0,
      "naming": 0,
      "error_handling": 0,
      "code_quality": 0,
      "response_mapping": 0,
      "dependency": 0,
      "cycle": 0,
      "repeated_logic": 0,
      "cross_process": 0,
      "env_var": 0
    }
  },
  "dependency_graph": {
    "edges": [
      { "from_conv_id": 11111, "from_title": "Process A", "to_conv_id": 22222, "to_title": "Process B", "call_type": "api_rpc", "count": 3 }
    ],
    "circular_dependencies": [],
    "orphaned_processes": [],
    "high_fan_in": [],
    "high_fan_out": [],
    "external_calls": []
  },
  "findings": []
}
```

---

## Scope Modifiers

| Request | Behavior |
|---------|----------|
| `"review all processes in folder X"` | Phase 0 scoped to folder_id |
| `"review only stage prod"` | Filter `process_inventory` by `stage_id` |
| `"skip cross-process analysis"` | Phase 1 only, skip Phase 2 |
| `"quick review"` | Skip code node analysis (Step 7) and duplicate logic (Step 2.2) |
| `"review only hardcodes"` | Hardcode check per process + Step 2.3 only |
| `"review only N processes"` | Prioritize by last modified date |

---

## MCP Tool Map

| Step | MCP call |
|------|----------|
| 0.2 Freshness check (preferred first call) | `build-project-index mode:check` |
| 0.2 Full rebuild if stale | `build-project-index` (no args) |
| 0.2 Read process inventory (indexed path) | Read `.corezoid/project-map.json` → `.processes{}` |
| 0.1 List processes (legacy fallback) | `list folder filter:"conveyor" obj_id:<COREZOID_STAGE_ID>` |
| 1 Pull process (legacy fallback) | `pull-process process_id:<conv_id>` |
| 1 Lint process | `lint-process process_path:<path>` |
| 2.1 Dependency graph (indexed path) | Read `.corezoid/project-map.json` → `.edges` / `.graph_stats` / `.unresolved_targets` |
| 2.4 Alias consistency (indexed path) | Read `.corezoid/project-map.json` → `.by_alias`; then `/corezoid-alias-manager` for fixes |
| 2.5 Env var usage (indexed path) | Read `.corezoid/project-map.json` → `.env_vars`; cross-check against `_ENV_VARS_.json` |

---

## Reference Documents

| Path | When to read |
|------|-------------|
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-review/SKILL.md` | Per-process audit steps (Steps 1–14) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/code-node.md` | Code node details and available JS libraries |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | Call a Process node, semaphores |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | HTTP API call configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error handling patterns |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variable naming rules and usage examples |
