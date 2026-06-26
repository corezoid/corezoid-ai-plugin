---
name: corezoid-process-optimizer
description: >
  Optimizes a Corezoid process JSON — reduces tact consumption by merging nodes,
  cleans data flow, fills missing node titles, and adds resilience patterns.
  Activate when the user says "optimize", "improve", "reduce tacts", "merge nodes",
  "clean up process", "what can be improved", "show optimizations", or any phrase
  implying they want to make a process faster, cheaper, or more readable.
  Two modes: plan-only (analysis + report, no changes) and auto (plan + execute immediately).
---

# Corezoid Process Optimizer

## Mode and scope detection

Determine **mode** and **scope** from the user's phrasing before doing anything else.

### Mode

| User intent | Mode |
|-------------|------|
| "optimize", "apply", "fix", "improve" — action verb | **AUTO** — analyze, plan, execute |
| "show", "what can", "suggest", "check" — analysis verb | **PLAN** — analyze, report, wait |

In PLAN mode, after presenting the report ask:
> "Apply all? Apply by group? (1 — tacts, 2 — data, 3 — naming, 4 — resilience)"

### Scope

The user may request a specific optimization group. Detect from keywords:

| Keyword(s) in request | Scope — run only |
|-----------------------|------------------|
| "tacts", "tact", "state changes", "nodes", "merge" | Group 1 |
| "data", "payload", "cleanup", "garbage", "fields" | Group 2 |
| "names", "naming", "titles", "readability", "descriptions" | Group 3 |
| "resilience", "semaphors", "timeouts", "stability" | Group 4 |
| No group keyword — general request | All groups |

If scope is a single group — run Phase 1 analysis only for that group. Skip all others entirely.
Still run `lint-process` first (its findings feed Group 1 regardless).

Examples:
- "optimize by tacts" → AUTO + Group 1 only
- "show tact optimizations" → PLAN + Group 1 only
- "add missing semaphors" → AUTO + Group 4 only
- "optimize" → AUTO + all groups

---

## Step 0 — Resolve process

Resolve `PROCESS_PATH` before calling any tools:
1. Check if the user provided a path, name, or ID.
2. If not — ask: "Which process? Provide a file path, name, or ID."
3. If name or ID — search locally: `find . -name "*.conv.json"`.
4. Read and parse the file.
5. Call **`lint-process`** — record findings. They become Group 1 quick-wins.

---

## Step 1 — Analyze

Build a node map: `id → { title, obj_type, logics[], sems[], outgoing edges }`.

Trace the execution graph from the Start node following `go.to_node_id` and `err_node_id` edges.

Collect candidates for all four groups below.

---

## Group 1 — Tact Reduction

> Formula: SC = (N – 1) × T. Every node transition costs one state change. Fewer nodes = fewer tacts.

### 1.1 Merge consecutive set_param nodes

Detect chains A → B → C where all nodes have `type: "set_param"`, connected sequentially with no branching.

Merge condition: all nodes share the same `err_node_id` (or all have none).
If `err_node_id` values differ — flag as candidate, do not merge automatically.

Merge action: combine all `extra` and `extra_type` objects into the first node. Remove subsequent nodes. Reconnect routing to where the chain ended.

Tacts saved: (chain length − 1) per task.

---

### 1.2 Merge consecutive code nodes

Detect chains of `type: "api_code"` nodes connected sequentially with no branching.

Merge condition: all share the same `err_node_id`.
If different — flag only; note that error handling must be unified first.

Merge action: concatenate `src` fields in order, separated by `\n// ---\n`. Keep one `err_node_id`. Remove subsequent nodes. Reconnect routing.

Tacts saved: (chain length − 1) per task.

---

### 1.3 Merge consecutive condition nodes checking the same field

Detect chains of `type: "go_if_const"` nodes where all check the **same `arg` field**.

Merge action: combine all `conditions[]` arrays into the first node. Each original branch keeps its own `to_node_id`. Remove subsequent condition nodes.

Do NOT merge if conditions check different fields — different semantics, merging hurts readability.

After merging, add a note to the plan:
> "Merged conditions on field '{{field}}'. Review combined node for readability."

Tacts saved: (chain length − 1) per task.

---

### 1.4 Replace api_rpc with api_copy when reply is unused

Detect `api_rpc` nodes where no downstream node references any field that could only originate from the called process's reply.

Check: scan all downstream `extra`, condition `arg`/`val`, and `src` fields for parameter names not present in the task before the call. If none found — candidate for api_copy.

This change requires confirmation even in AUTO mode. Present:
> "Node '[title]' calls process but does not use the reply. Replace with api_copy (fire-and-forget)? [yes/no]"

If confirmed: change `type: "api_rpc"` → `type: "api_copy"`. Switch `extra`/`extra_type` to `data`/`data_type` per the api_copy schema (see `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md`).

---

### 1.5 Remove dead nodes

Apply lint findings:
- **Orphaned nodes** — remove from `scheme.nodes`.
- **No-op conditions** — re-route the incoming edge to the single destination; remove the condition node.
- **Unused set_params** — if the node sets only unused variables, remove the node. If mixed, remove only the unused keys from `extra`/`extra_type`.

---

## Group 2 — Data Cleanup

### 2.1 Inline payload cleanup after API calls

After each `type: "api"` node, identify response fields not referenced by any downstream node.

Do NOT add a new cleanup node — inline the cleanup into the nearest existing downstream node:
- **code node**: prepend `delete data.<field>;` at the top of `src`.
- **set_param node**: set_param cannot delete keys. Find the next code node and add the delete there. If no downstream code node exists — flag for the user, do not create a new node.

---

### 2.2 Remove dead code inside code nodes

Inside each `api_code` node's `src`, detect:
- `data.x = data.x` — self-assignment, remove.
- Variables declared but never read after declaration — flag for user review.
- Large commented-out blocks — flag for user review.

---

## Group 3 — Readability

Run this group when:
- User explicitly requested it, OR
- AUTO mode is active.

**Critical nodes always get titles filled** regardless of mode:

| Node type | Always fill `title` if empty |
|-----------|------------------------------|
| `api_code` | Yes |
| `api` | Yes |
| `api_rpc` | Yes |
| `api_copy` | Yes |
| `obj_type: 2` (End/Error) | Yes |

### Title inference rules

| Node type | Inference |
|-----------|-----------|
| `api` | `"[METHOD] [hostname][path]"` from the `url` field |
| `api_rpc` | `"Call @[alias]"` or `"Call [conv_id]"` |
| `api_copy` | `"Copy → @[alias]"` or `"Copy → [conv_id]"` |
| `api_code` | First meaningful line of `src` (strip `data.`, max 40 chars) |
| `set_param` | `"Set [key1], [key2], ..."` (first 3 keys) |
| `go_if_const` | `"Check [arg field]"` |
| `obj_type: 2`, icon `error` | `"Error"` |
| `obj_type: 2` | `"Final"` |

Never overwrite an existing non-empty `title`.

### 3.1 Fill process params array

If `params: []` and the Start node clearly receives input (inferred from downstream references to fields never set internally) — propose a `params` array.

Always ask for confirmation before applying — field types cannot be reliably inferred.

---

## Group 4 — Resilience

### 4.1 Add missing time semaphors

| Node type | Severity | Default timeout |
|-----------|----------|-----------------|
| `api_callback` (Waiting for Callback) | 🔴 Critical — always add | 3600 sec |
| `api` (API Call) | 🟡 Important — add without asking | 30 sec |
| `api_rpc` (Call a Process) | 🟢 Recommended — add without asking | 60 sec |

Semaphor `to_node_id` must point to a valid error node.
If no suitable error node exists — create one at `x + 300`, same `y` as the parent node.
Use `obj_type: 2` with `title: "Timeout"` and connect the semaphor to it.

Semaphor JSON:
```json
{
  "type": "time",
  "value": 30,
  "dimension": "sec",
  "to_node_id": "<error_node_id>"
}
```

---

## Step 2 — Plan report

Present findings in this format before executing anything:

```
## Optimization Plan: <Process Title> (<ID>)

### Group 1 — Tact Reduction
| # | Type               | Nodes                                 | Tacts saved |
|---|--------------------|---------------------------------------|-------------|
| 1 | Merge set_param    | "Set ref" → "Set amount" → "Set cur"  | 2/task      |
| 2 | Merge code         | "Parse" → "Validate"                  | 1/task      |
| 3 | Remove orphaned    | "Old handler" (abc123)                | 1/task      |
| 4 | rpc→copy ⚠️ confirm | "Send notification"                   | wait saved  |

Total nodes removed: N | Tacts saved: X/task

### Group 2 — Data Cleanup
| # | After node          | Fields to remove          | Inline into          |
|---|---------------------|---------------------------|----------------------|
| 1 | "Call Stripe API"   | payment_method_details... | "Parse response"     |

### Group 3 — Readability
| # | Node (id)      | Suggested title                        |
|---|----------------|----------------------------------------|
| 1 | api (abc123)   | "POST api.stripe.com/v1/charges"       |
| 2 | api_rpc (def)  | "Call @payment-process"                |

### Group 4 — Resilience
| # | Node                    | Issue                         | Action             |
|---|-------------------------|-------------------------------|--------------------|
| 1 | "Call SMS API"          | Missing timeout semaphor      | Add 30sec          |
| 2 | "Wait callback" 🔴      | Missing timeout semaphor      | Add 3600sec        |

### Requires action outside this process
- Hardcoded URL in "Call Stripe API" → use /corezoid-variable-manager
- Numeric conv_id 1307813 (×3 nodes)  → use /corezoid-alias-manager
```

---

## Step 3 — Execute

### Execution order

Always apply in this sequence:
1. Group 1 — tact reduction (graph changes first)
2. Group 4 — resilience (semaphors reference the now-clean graph)
3. Group 2 — data cleanup (inline into final node set)
4. Group 3 — naming (operates on final nodes)

### Confirmation rules

| Change | Confirm in AUTO | Confirm in PLAN |
|--------|----------------|-----------------|
| Merge set_param / code / condition | No | Yes per group |
| api_rpc → api_copy | **Always** | **Always** |
| Add semaphors | No | Yes per group |
| Fill titles (critical nodes) | No | No |
| Fill titles (other nodes) | No | Yes per group |
| Fill `params` array | **Always** | **Always** |
| Create variable for hardcoded value | **Always** | **Always** |

### After all changes

1. Write updated JSON to `PROCESS_PATH`.
2. Call **`lint-process`** — fix any errors before proceeding.
3. Call **`push-process`**.
4. Notify the user: "Deployed. Please **refresh the page** in Corezoid to see the updated process."

---

## Boundaries — what the optimizer does not do

| Finding | Action |
|---------|--------|
| Hardcoded URLs / tokens | Flag + point to `/corezoid-variable-manager` |
| Numeric conv_id without alias | Flag + point to `/corezoid-alias-manager` |
| Full Markdown documentation | Point to `/corezoid-process-tech-writer` |
| Cross-process audit | Point to `/corezoid-project-review` |
| Extract subprocess (architecture) | Discuss with user + point to `/corezoid-create` |

---

## Reference Documents

| Path | When to read |
|------|-------------|
| `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md` | JSON schemas for all node types |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-node.md` | set_param merge rules |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/code-node.md` | Code node structure |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | API Call semaphor configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | api_rpc vs api_copy decision |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/copy-task-node.md` | api_copy structure |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/waiting-for-callback-node.md` | api_callback critical semaphor |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error node patterns |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/node-positioning-best-practices.md` | Positioning new nodes |
