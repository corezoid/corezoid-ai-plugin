---
name: corezoid-review
description: >
  Corezoid process review and audit specialist. Use when the user wants to
  analyze, review, audit, or improve an existing Corezoid process. Activate
  when the user says "review a process", "analyze", "check", "audit", "find
  issues", "explain this process", "what's wrong with", "optimize", or
  "check for hardcoded values".
---

# Review a Corezoid Process

You are a specialist in auditing and analyzing Corezoid BPM processes using the `corezoid` MCP server.

## Identify the Process (MANDATORY FIRST STEP)

**Before doing anything else**, resolve `PROCESS_PATH`:

1. Check whether the user already provided a process identifier — a file path, process name, or process ID — in the current message or conversation history.
2. If no identifier is provided, ask:

   > "Please specify the process — you can provide a file path (e.g. `1278273_Business.folder/2778176_payment.conv.json`), a process name, or a process ID."

   Do **not** call any MCP tools until the user provides an identifier.
3. If the user gave a **name or ID** (not a file path), search the local working directory for the matching `.conv.json` file using the `find` or `grep` Bash tools (the project is already pulled locally).
4. Once `PROCESS_PATH` is known, begin the audit below.

---

## Step 1: Structural Lint

Run the linter to detect structural issues automatically:

Call MCP tool **`lint-process`** with `process_path: "<PROCESS_PATH>"`.

This checks for:
- **Orphaned nodes** — unreachable nodes not connected from Start
- **No-op conditions** — all branches of a condition leading to the same node
- **Unused set_param** — variables set but never referenced downstream

Record all findings. They will be included in the final report.

---

## Step 2: Load and Parse the Process

Read the `.conv.json` file and extract nodes:

- `ops[0]['scheme']` is a **list** — always index `[0]`
- `node['condition']` is a dict with keys `logics` (list) and `semaphors` (list)
- `node['extra']` is a **string** (escaped JSON) — not a dict
- Conditions in `go_if_const` logics live in `lg['conditions']`, NOT in `lg['extra']`

Collect node groups for analysis:

```python
code_nodes  = [n for n in nodes for lg in n['condition']['logics'] if lg['type'] == 'code']
api_nodes   = [n for n in nodes for lg in n['condition']['logics'] if lg['type'] == 'api']
rpc_nodes   = [n for n in nodes for lg in n['condition']['logics'] if lg['type'] == 'api_rpc']
copy_nodes  = [n for n in nodes for lg in n['condition']['logics'] if lg['type'] == 'api_copy']
cond_nodes  = [n for n in nodes for lg in n['condition']['logics'] if lg['type'] == 'go_if_const']
```

---

## Step 3: Hardcode Check

- **`code` nodes** — look for hardcoded IDs, URLs, tokens
- **`api` nodes** — check URLs; must use `{{env_var[@name]}}`, not literals
- **`api_rpc` / `api_copy`** — check `conv_id` values; numeric IDs instead of `@alias` are a flag
- **`api_rpc` extra fields** — check for hardcoded values that should be variables

Flag each hardcoded value for extraction to env_var (see `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md`).

### Root-level process metadata — do NOT flag as hardcoded

The `.conv.json` file has top-level fields that are process metadata assigned by the platform. Do **not** report them as hardcoded values:

| Field | Description |
|-------|-------------|
| `conv_id` | The ID of this process itself |
| `user_id` | Owner/author user ID |
| `company_id` | Company/tenant identifier |
| `folder_id` | Folder identifier |
| `project_id` | Project identifier |
| `stage_id` | Stage/environment identifier |

These are read-only platform metadata, not configuration that should be extracted to env_vars.

---

## Step 4: Repeated Logic

- Compare similarly named nodes (e.g. multiple `CREATE ACTOR`, `MANAGE ACCESS RULES`)
- If structure is identical → mark as duplicated logic, recommend extracting into a subprocess

---

## Step 5: Cycle Verification

- Detect nodes with `semaphors` of type `time` or `go_if_const` that create loops
- Verify exit conditions exist and iteration limits are enforced

---

## Step 6: Node Naming

- Identify nodes with empty `title`
- Check for duplicate or vague names
- Recommended format: `Action_Object_Context` (e.g. `Create_Stream_Active`)

---

## Step 7: Code Node Analysis

### JavaScript nodes — check for:

- `try/catch` wrapping all external calls
- No hardcoded values (IDs, tokens, URLs)
- Safe type conversions (`parseInt`, `Number`)
- Input validation (`if (!data.var) { ... }`)
- No `eval` usage

### Erlang nodes — check for:

- Pattern matching covers all cases
- `catch` or `case` for invalid data
- No recursion without termination conditions

### set_param optimization

Code nodes that only do simple assignments should be replaced with `set_param`. Flag these patterns:

| Pattern in code node                    | Replace with set_param                      |
|-----------------------------------------|---------------------------------------------|
| `data.x = data.y;`                      | `"x": "{{y}}"`                              |
| `data.x = data.a + "_" + data.b;`       | `"x": "{{a}}_{{b}}"`                        |
| `data.x = data.a + data.b;` (numeric)   | `"x": "$.math({{a}}+{{b}})"`                |
| `data.x = data.a * data.b;`             | `"x": "$.math({{a}}*{{b}})"`                |
| `data.x = "constant";`                  | `"x": "constant"`                           |
| `data.x = data.x;`                      | remove entirely (self-assignment, no-op)     |

`$.math()` takes exactly **two operands**. For 3+, nest: `$.math($.math({{a}}+{{b}})+{{c}})`. Supported operators: `+`, `-`, `*`, `/`. Use `extra_type: "number"` when the result should be numeric.

Operations that genuinely require a code node: `str.length`, regex, `JSON.parse/stringify`, array `.map/.filter`, complex `if/else`, object key iteration.

---

## Step 8: Semaphor Coverage

Check for missing semaphors by severity:

- 🔴 **`api_callback`** — MUST have a `time` semaphor. Without one tasks hang forever if the user abandons the session.
- 🟡 **`api`** (outbound HTTP) — Should have a `time` semaphor as safety net against unresponsive endpoints.
- 🟢 **`api_rpc`** — Lower severity; target process handles its own timeouts. Still recommended.
- 🟢 **`api_copy` with `is_sync: true`** — Informational; target process manages its own lifecycle.

---

## Step 9: Error Handling Review

- Every error node (obj_type 3) must transition to a final error node (obj_type 2)
- Every error reply node must have `throw_exception: true`
- Every success reply node must have `throw_exception: false`
- Each error node should have a meaningful `errorText`
- Detect duplicated error nodes with identical titles/messages

---

## Step 10: External Dependencies Inventory

Scan all nodes and collect every outbound reference:

1. **api_rpc** — unique `conv_id` values
2. **api_copy** — unique `conv_id` values
3. **State reads** — `conv[@alias]` references inside `set_param` extra values or condition parameters

Flag:
- ⚠️ Numeric `conv_id` without `@alias` — flag in the report; suggest a `short_name` derived from the process title (lowercase, hyphens). Do **not** call `create-alias` automatically — only create aliases when the user explicitly requests it.
- ⚠️ Same alias called with both create and modify modes
- ⚠️ More than 5 unique dependencies — note coupling risk
- ⚠️ `conv[@alias]` state reads — implicit dependencies that break if the referenced process changes schema

To manually verify unused set_param findings, search for each variable name across the `.conv.json` file — check all logics, extras, conditions, and semaphors.

---

## Step 11: Dependency Process Reviews

Perform a **1-level deep** review of all unique outbound dependencies. Review each direct dependency but do NOT recurse into their sub-dependencies — only list them.

For each dependency:

1. Collect all unique `conv_id` values from the main process
2. Pull the dependency process using MCP tool **`pull-process`** with `process_id` set to the `conv_id` value, then read the resulting `.conv.json`
3. Run a lightweight review covering:
   - Node count and type distribution
   - Untitled node count
   - JS/Erlang code nodes: `try/catch`, hardcoded values
   - API nodes missing semaphors
   - Hardcoded values in RPC extra fields / URLs
   - Sub-dependencies (list but do NOT recurse)
   - Flag processes with 200+ nodes as needing their own dedicated review

Report format:

```markdown
## Dependency Process Reviews

### @alias-name (conv_id=XXXXX) — "Process Title"

NN nodes. MM/NN untitled.

- [ ] ⚠️ X API nodes missing semaphors
- [ ] ⚠️ JS code without try/catch in node "Y"
- [ ] Sub-dependencies: @a, @b, 12345
- [ ] **Needs own dedicated review** (200+ nodes)

## Dependency Health Summary

| Dependency | Nodes | Untitled | Missing Semaphors | Hardcoded conv_ids | JS no try/catch | Needs Own Review |
|-----------|-------|----------|-------------------|-------------------|-----------------|--------------------|
| @alias    | 154   | 74       | 6                 | 0                 | 10              | —                  |
```

---

## Step 12: Dependency Graph

Based on the dependency data collected in Steps 10–11, produce a Mermaid diagram of direct process-to-process dependencies:

````markdown
```mermaid
graph TD
    MainProcess["Process Name"] --> Dep1["@alias-name (conv_id=XXXXX)"]
    MainProcess --> Dep2["@alias2 (conv_id=YYYYY)"]
    Dep1 --> SubDep1["@sub-alias"]
```
````

Include in the report:

```markdown
## 12. Dependency Graph

\`\`\`mermaid
graph TD
    ...
\`\`\`

N direct dependencies, N total processes mapped.
```

---

## Step 13: Generate Report

Produce a Markdown report:

```markdown
# Process Review: <process name>

## 1. Structural Issues (lint-process)

- [ ] 🔴 N orphaned nodes — list each: (id, title, type)
- [ ] ⚠️ No-op condition in node "X" (id) — all branches route to same node "Y"
- [ ] ⚠️ Unused set_param in node "Z" (id) — variable `{{var}}` not referenced downstream

## 2. Hardcode

- [ ] Node X: API key hardcoded → move to env_var

## 3. Repeated Logic

- [ ] Nodes Y, Z: identical structure → extract into subprocess

## 4. Cycles

- [ ] Node W: no exit condition → add iteration limit

## 5. Naming

- [ ] Node without title → rename to "Validate Token"
- [ ] Duplicate titles "error manage access rules" → make unique

## 6. Code Review

- [ ] JS: Node "Code_123" has no try/catch → add error handling
- [ ] Erlang: Node "Code_456" has recursion without termination condition

## 7. Code Node Optimization (set_param migration)

- [ ] ⚠️ Node "X": `data.a = data.b + "__" + data.c` → set_param: `"a": "{{b}}__{{c}}"`
- [ ] ⚠️ Node "Y": `data.total = data.x + data.y` → set_param: `"total": "$.math({{x}}+{{y}})"`
- [ ] ⚠️ Node "Z": `data.x = data.x` → remove (self-assignment, no-op)

## 8. Semaphor Coverage

- [ ] 🔴 api_callback node "X" — missing time semaphor (tasks will hang)
- [ ] 🟡 api node "Y" — missing time semaphor (risk on unresponsive endpoint)

## 9. Error Handling

- [ ] Missing err_node_id on set_param in node "X"
- [ ] Duplicated error messages across nodes "Y", "Z"
- [ ] Node "Z" reply node missing throw_exception: true

## 10. External Dependencies

| # | Alias / conv_id | Call Type | Count | Usage Summary | Notes |
|---|----------------|-----------|-------|---------------|-------|
| 1 | @send-message  | api_rpc   | 10    | OTP prompt, errors, success | — |
| 2 | 21123          | api_copy  | 2     | Send report   | ⚠️ hardcoded numeric |

### State Store References

- `conv[@user-profile]` — reads language, registration_ban

## 11. Dependency Process Reviews

### @alias-name (conv_id=XXXXX) — "Process Title"

NN nodes. MM/NN untitled.

- [ ] ⚠️ X API nodes missing semaphors
- [ ] Sub-dependencies: @a, @b

## Dependency Health Summary

| Dependency | Nodes | Untitled | Missing Semaphors | Hardcoded conv_ids | JS no try/catch | Needs Own Review |
|-----------|-------|----------|-------------------|-------------------|-----------------|--------------------|
| @alias    | 154   | 74       | 6                 | 0                 | 10              | —                  |

## 12. Dependency Graph

```mermaid
graph TD
    ...
```

N direct dependencies, N total processes mapped.
```

---

## Reference Documents

Use the `Read` tool to load these files when specific node or validation details are needed:

| Path | When to read |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/code-node.md` | Code node details and available JS libraries |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | Call a Process node, semaphores |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | HTTP API call configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error handling patterns |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-json-validation.md` | Validation rules and common errors |
