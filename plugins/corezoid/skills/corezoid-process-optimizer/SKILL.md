---
name: corezoid-process-optimizer
description: >
  Optimizes a Corezoid process JSON — reduces tact consumption by merging nodes,
  cleans data flow, fills missing node titles, adds resilience patterns, and replaces
  logic-only Code nodes with Condition + Set Parameters.
  Activate when the user says "optimize", "improve", "reduce tacts", "merge nodes",
  "clean up process", "what can be improved", "show optimizations", "replace Code node
  with Set Parameters / Condition", "simplify code nodes", or any phrase
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
> "Apply all? Apply by group? (1 — tacts, 2 — data, 3 — naming, 4 — resilience, 5 — code nodes)"

### Scope

The user may request a specific optimization group. Detect from keywords:

| Keyword(s) in request | Scope — run only |
|-----------------------|------------------|
| "tacts", "tact", "state changes", "nodes", "merge" | Group 1 |
| "data", "payload", "cleanup", "garbage", "fields" | Group 2 |
| "names", "naming", "titles", "readability", "descriptions" | Group 3 |
| "resilience", "semaphors", "timeouts", "stability" | Group 4 |
| "code node", "api_code", "replace code", "simplify code" | Group 5 |
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

Collect candidates for all five groups below.

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

## Group 5 — Code Node Reduction

> Prefer a light Condition + Set Parameters combination over an `api_code` node wherever the logic is only branching on values and assigning them. The logic becomes visible on the canvas, and there is no JavaScript left to fail at runtime. Default: replace wherever possible — "impossible" means only the cases in 5.3.

### 5.1 Audit all code nodes

List every `type: "api_code"` node: title, which task keys it reads, which keys it writes (per outcome), where `go` and `err_node_id` lead. Put each into one category:

| Category | Meaning |
|----------|---------|
| **Replace** | Whole node → Condition + Set Parameters (5.2) |
| **Simplify** | Part of the logic moves out, a smaller Code node stays (5.4) |
| **Merge** | Join with an adjacent code node (1.2) — decided after replacements |
| **Keep** | Cite the 5.3 item, or "already optimal" (rename short names only) |

Present the table "node → category → result → reason" in the plan. Always confirm before changing any code node, even in AUTO mode. A node whose `src` carries a comment that it must stay Code is never proposed for replacement.

---

### 5.2 Replace with Condition + Set Parameters

```
            ┌─ Set Param (outcome 1) ─┐
Condition ──┼─ Set Param (outcome 2) ─┼──► node the old code node's go led to
            └─ Set Param (outcome N) ─┘
```

- One Set Param per **logical outcome**, not always two: `if/else` → 2 branches; `switch` / `if … else if …` → one per distinct result (identical results share a branch).
- No branching in the code (only assignments) → a single Set Param, no Condition.
- Several `go_if_const` in one Condition only when they test values of the **same** source. Outcomes that depend on different sources → a chain of single-check Conditions (one `go_if_const` each, default `go` to the next check).
- `&&` → several `conditions` inside one `go_if_const` (they are ANDed). `||` → several `go_if_const` leading to the same branch.
- The Condition's default `go` is the final `else`.
- All Set Params converge on the node the old code node's `go` pointed to.

Translating JS:

| In Code | In Condition / Set Parameters |
|---------|-------------------------------|
| `if (data.status === 'paid')` | `"param": "status"`, `"fun": "eq"`, `"const": "paid"`, `"cast": "string"` |
| `if (data.is_active === true)` | `"param": "is_active"`, `"const": "true"`, `"cast": "string"` |
| `if (!data.status)` / `=== ''` | `"param": "status"`, `"fun": "eq"`, `"const": ""` |
| `if (data.order.status === 'paid')` | `"param": "{{order.status}}"` — a nested path must be wrapped in `{{}}`; a bare `order.status` is not resolved (only bare top-level names are) |
| `if (!data.items \|\| !data.items.length)` | `"param": "{{items[0]}}"`, `"fun": "eq"`, `"const": ""` — a missing key or element compares as `""` |
| `if (data.a === data.b)` | `"param": "{{a}}"`, `"fun": "eq"`, `"const": "{{b}}"` — `const` accepts a template |
| `if (data.total > 5)` | `"fun": "more"`, `"cast": "number"` |
| `data.x = 'const'` | `"x": "const"` |
| `data.x = data.y` | `"x": "{{y}}"` |
| `data.x = data.a + '_' + data.b` | `"x": "{{a}}_{{b}}"` |
| `data.x = data.a + data.b` (numbers) | `"x": "$.math({{a}}+{{b}})"` — `$.math` evaluates only two operands (`a+b+c` silently gives `a+b`): one operation per key, intermediate results in separate keys |
| `data.x = []` / `{…}` | JSON string value, `extra_type` `array` / `object` |
| `delete data.tmp` | Set Param cannot delete keys → `"tmp": ""` |

---

### 5.3 When NOT to replace (keep Code)

1. **Adding fields to an existing object.** `data.order = data.order || {}; data.order.status = …` — Set Param keys `{{order.status}}`, `{{order.total}}` in one node do **not** merge: only one of them (the last alphabetically) survives, the rest are silently dropped, and the node still exits via `go`. Treat a single `{{obj.x}}` key over an existing object as unsafe too.
   - Replaceable only if this node **creates** the object from scratch: one key `order`, `extra_type: object`, value = JSON of all fields. Anything previously in the object is lost — confirm nothing earlier in the flow wrote it.
2. **Fallback to the current value** — `data.x = data.x || 'default'`. Possible as Condition (`x` eq `""`) + Set Param on the empty branch only; if `x` is a nested field, see item 1 → keep Code.
3. **Operations Set Param lacks**: `.length`, regex, `JSON.parse`/`stringify`, `.map`/`.filter`/`.reduce`, iterating object keys, loops, date libraries, building arrays or tables from lists.
4. **Too many outcomes** — more than ~5–6 outcomes from different sources would make the diagram unreadable; discuss with the user.

---

### 5.4 Simplify the code that stays

- **Move the replaceable part out.** Constant/template assignments and value branches that do not depend on the irreplaceable part → Set Param / Condition before or after the Code node; only 5.3 logic stays in `src`. Do it only if the remaining Code really shrinks — do not add a node for a single key.
- **Dead code** (extends 2.2): unreachable branches, unused helpers, task keys the node writes that nothing downstream reads (search the whole `.conv.json` for `{{key}}` and `data.key`; keys returned in a Reply count as used).
- **Simplify JS** without changing behaviour: drop duplicate checks and needless copies of `data`, flatten nested `if` into early returns.
- **Readable names** across the whole node, not just edited lines: `orderTotal`, `customerQuery`, `padTwoDigits` — not `d`, `q`, `t`, `op`. Exceptions: `i`/`j` loop counters, `e` in `catch (e)`.

Check behaviour locally: run the old and new `src` (Node.js) on inputs covering every branch and compare the resulting `data` key by key.

---

### 5.5 Wiring the new nodes

- **Changing node kind = a new node.** When a Code (or other action) node becomes a Condition, do not reuse its `id`: delete it, create the Condition with a new `id`, and re-point all incoming edges. Reusing the id breaks merges between stages ("referenced node … does not exist"). Code → Set Param (action → action) is safe.
- If the old code node had its own error cluster used only by it, remove the cluster with it.
- Position new nodes per `${CLAUDE_PLUGIN_ROOT}/docs/process/node-positioning-best-practices.md` — do not re-layout the rest of the process.

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

### Group 5 — Code Node Reduction ⚠️ confirm
| # | Code node            | Category | Result                                        | Reason           |
|---|----------------------|----------|-----------------------------------------------|------------------|
| 1 | "Set order flags"    | Replace  | Condition "Paid?" → 2 Set Params              | if/else only     |
| 2 | "Fill order fields"  | Keep     | —                                             | 5.3 item 1       |
| 3 | "Build items table"  | Simplify | constants → Set Param, loop stays in Code     | 5.3 item 3       |

### Requires action outside this process
- Hardcoded URL in "Call Stripe API" → use /corezoid-variable-manager
- Numeric conv_id 1307813 (×3 nodes)  → use /corezoid-alias-manager
```

---

## Step 3 — Execute

### Execution order

Always apply in this sequence:
1. Group 5 — code node replacements, then simplifications (decides what remains Code)
2. Group 1 — tact reduction (graph changes; 1.2 merges only the code nodes that remain)
3. Group 4 — resilience (semaphors reference the now-clean graph)
4. Group 2 — data cleanup (inline into final node set)
5. Group 3 — naming (operates on final nodes)

### Confirmation rules

| Change | Confirm in AUTO | Confirm in PLAN |
|--------|----------------|-----------------|
| Merge set_param / code / condition | No | Yes per group |
| api_rpc → api_copy | **Always** | **Always** |
| Replace / simplify code node (Group 5) | **Always** | **Always** |
| Add semaphors | No | Yes per group |
| Fill titles (critical nodes) | No | No |
| Fill titles (other nodes) | No | Yes per group |
| Fill `params` array | **Always** | **Always** |
| Create variable for hardcoded value | **Always** | **Always** |

### After all changes

1. Write updated JSON to `PROCESS_PATH`.
2. Call **`lint-process`** — fix any errors before proceeding.
3. Call **`push-process`**.
4. If any code node was replaced or simplified — verify before reporting done:
   - before editing, list **every** key the old code node wrote, per outcome;
   - `run-task` once per Condition branch, with input that reaches it;
   - check each listed key is present with the right value (nested fields especially). Branches that cannot be reached without a server-side failure — list them to the user as unverified.
5. Notify the user: "Deployed. Please **refresh the page** in Corezoid to see the updated process."

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
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/condition-node.md` | Condition structure for Group 5 replacements |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-built-in-functions.md` | `$.math` and other value functions |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | API Call semaphor configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | api_rpc vs api_copy decision |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/copy-task-node.md` | api_copy structure |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/waiting-for-callback-node.md` | api_callback critical semaphor |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error node patterns |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/node-positioning-best-practices.md` | Positioning new nodes |
