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

Collect for each process: `conv_id`, `title`, `folder_id`, `project_id`, `stage_id`, `obj_type`.
Store as `process_inventory[]`. Skip objects where `obj_type != conveyor` unless explicitly requested.

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

1. Pull the process with MCP tool **`pull-process`** using `conv_id`
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

Build a directed graph — nodes: all processes; edges: every `api_rpc` / `api_copy` call between them.

Flag:
- ⚠️ **Circular dependencies** — A calls B which calls A
- ⚠️ **Orphaned processes** — never called and no direct external input
- ⚠️ **High fan-in** — called by > 5 other processes (single point of failure)
- ⚠️ **High fan-out** — calls > 7 other processes (coupling risk)
- ℹ️ **External calls** — `conv_id` values pointing outside the project inventory

### Step 2.2: Duplicate Logic Across Processes

- Two processes with identical or >80% similar `api_code` nodes → candidate for shared subprocess
- Two processes calling the same external URL with same parameters → candidate for shared wrapper process

### Step 2.3: Shared Hardcoded Values

Aggregate only normalized `hardcode.*` findings from per-process reports. Do **not** aggregate dynamic Corezoid expressions, `dependency.state_store_ref`, or values fully wrapped in `{{...}}` as shared hardcodes.

- Same URL in > 2 processes → recommend shared `env_var`
- Same numeric `conv_id` in > 1 process → one alias fix resolves all
- Same token/key fragment in > 1 process → security risk, centralize immediately
- Same status string in > 2 processes → recommend shared constant or `env_var`
- Same error text in > 1 process → recommend shared text constant

False-positive guard (same as per-process review):
- Ignore values that are fully dynamic expressions, e.g. `{{conv[@storage].ref[{{key}}].field}}`
- If a shared value is a state-store alias, report it under dependency analysis instead of `cross_process.shared_hardcode_value`
- Recompute aggregate summary counts after removing false positives

### Step 2.4: Alias Consistency

Flag:
- Same alias used with both `create` and `modify` in different processes → race condition risk
- Alias defined in one process but used with numeric `conv_id` in another → inconsistency
- Aliases referenced in processes but absent from project inventory → undocumented external dependency

### Step 2.5: Naming Consistency

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
      "cross_process": 0
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
| 0.1 List processes | `list folder filter:"conveyor" obj_id:<COREZOID_STAGE_ID>` |
| 1 Pull process | `pull-process conv_id:<conv_id>` |
| 1 Lint process | `lint-process process_path:<path>` |
| 2.1 Resolve alias | `show conv` with alias |

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
