---
name: corezoid-edit
description: >
  Corezoid process editing specialist. Use when the user wants to modify, update,
  or fix an existing Corezoid process, add or remove nodes, change node behavior,
  add an API call, fix an error, or update process logic. Activate when the user
  says "edit a process", "modify", "update", "fix", "add a node", "change
  behavior", "add a call", "remove a node", or "update the logic".
---

# Edit an Existing Corezoid Process

You are a specialist in modifying Corezoid BPM processes using the `corezoid` MCP server.

## Identify the Process (MANDATORY FIRST STEP)

**Before doing anything else**, resolve `PROCESS_PATH`:

1. Check whether the user already provided a process identifier — a file path, process name, or process ID — in the current message or conversation history.
2. If no identifier is provided, ask:

   > "Please specify the process — you can provide a file path (e.g. `123_payment.conv.json`), a process name, or a process ID."

   Do **not** call any MCP tools until the user provides an identifier.
3. If the user gave a **name, alias, or ID** (not a file path), call MCP tool **`describe-process`** with `identifier: "<user-value>"`. The tool resolves it against `.corezoid/project-map.json` and returns a JSON payload with:

   - `path` — the file to open
   - `calls_in_count` and `high_fan_in` (bool, true if > 5)
   - `stale` (bool, true if the on-disk file's hash differs from the indexed hash)
   - `candidates[]` if the identifier matches multiple processes

   If `candidates[]` is non-empty, show the list to the user and ask which one — do not guess. If `index_missing: true`, fall back to `find`/`grep` in the working directory and note that blast-radius/staleness data is unavailable until the index is built (suggest running the `corezoid-index` skill).

4. **Blast-radius / staleness — read from the tool's response, not from memory:**
   - If `high_fan_in: true`, explicitly tell the user before making any changes: "This process is called by `<calls_in_count>` other processes in the project — edits will affect all of them." Then continue.
   - If `stale: true`, tell the user: "The project index is out of date for this file — it may have been edited outside the plugin. I'll continue, but consider running the `corezoid-index` skill afterwards to refresh." Then continue.

   These two flags come from `describe-process`, not from a separate MANDATORY check step you have to remember. If they aren't present in the response, they are simply false — no action needed.

5. If the user gave a file path directly (e.g. `123_payment.conv.json`), you may skip `describe-process` — the path is enough for the edit itself. But if `.corezoid/project-map.json` exists, calling `describe-process` with `identifier: "123"` (from the filename prefix) is still recommended so you see the blast-radius / staleness data.

6. Once `PROCESS_PATH` is known and the file exists locally, open and analyze it before making any changes.

---

## Step 1: Analyze the Process

Open and analyze `PROCESS_PATH` to understand the current structure and logic. Pay attention to:

- Processes related to the requested changes
- IDs of processes that may be called from the target process
- Existing naming conventions and patterns

---

## Step 2: Implement the Changes

Apply changes to `PROCESS_PATH`.

### Contract-compatibility rules (MANDATORY when `calls_in_count > 0`)

The `describe-process` response from the identify step already carries `calls_in_count`. If it is **> 0**, existing processes in this project depend on the current input/output contract of this one, and only additive changes are allowed here:

| Change | Additive? |
|---|---|
| Add a new **optional** param to `params[]` (not required, sensible default) | ✅ |
| Add a new field to a reply-node's `extra`/`extra_type` (don't remove existing ones) | ✅ |
| Add a new logic branch that doesn't remove any existing route | ✅ |
| Change the internal implementation while preserving the input/output contract | ✅ |
| Rename a param, remove a param, change a param's type | ❌ breaking |
| Change a param from optional to required | ❌ breaking |
| Remove or rename a field in a reply-node's `extra` | ❌ breaking |
| Change reply-node `throw_exception` behaviour (from silent to throwing, or vice versa) | ❌ breaking |
| Change the error-path node so a previously-successful branch now goes to Error / End-Error | ❌ breaking |

If the requested change is **breaking** and `calls_in_count > 0`, do **not** apply it here. Instead:

1. Route the change through `corezoid-create` — its Step 1a decision tree will fork a new process (last row of that table: breaking-diff + callers > 0 → FORK), leaving this one untouched for the existing callers.
2. Alternatively, if the user explicitly wants the breaking change to land in place, log the request and ask for confirmation **in interactive mode** with the explicit list of affected `calls_in[]` conv_ids. In autonomous mode, refuse: emit a clear error line to the log — `breaking change refused: conv_id=<id> has <n> callers, route through corezoid-create for a fork instead` — and exit without touching the file. Silent breakage of N callers is worse than not making the edit.

If `calls_in_count == 0`, breaking changes are safe — proceed to Core rules as usual. Still record the intent in the audit line (see below): a caller-less process today may not be caller-less tomorrow, and the audit trail is what tells the next agent whether a past change was safe by luck or by design.

### Audit line in `description`

Regardless of the change (additive or in-place breaking on a caller-less process), prepend a one-line audit entry to `root.description` using the same format as `corezoid-create` Step 1a-D. Example:

```
Decision (2026-07-02): extended conv_id=1877387 with optional param "incomeType" (additive, callers=3)
```

Keep the existing description content after the new audit line (older audit lines are history — do not delete them). This makes the process's history readable in-place and gives the next autonomous run a decision breadcrumb without needing to reconstruct anything from git.

### Core rules

- Connect nodes only through the `go` field
- Every node that can fail must have `err_node_id` — point it **directly at a Final Error node** (`obj_type: 2`) unless the error path needs logic (reply to caller, retry routing). Never create an Escalation node (`obj_type: 3`) that only contains a bare `go` — that is a passthrough anti-pattern flagged by `lint-process`
- Node IDs must be unique 24-character hex strings: `^[0-9a-f]{24}$`. **Always `pull-process` before editing** and reference only canonical, server-assigned IDs — IDs you invented in a previous push were reassigned by the server and no longer exist. New nodes added now get placeholder IDs that the server will likewise reassign on push. Existing nodes' IDs are preserved.
- Use descriptive node `title` values (e.g., "Call Payment Process", not "RPC")
- Place new nodes below existing ones, incrementing `y` by 200–250px
- Position error nodes to the right of their parent (`x + 300`)

### Variables for constants

All constants (URLs, tokens, endpoints, hosts) must be stored as variables — never hardcoded:

1. Check `_ENV_VARS_.json` (from `pull-folder`) or `.processes/variables.json` (from this session) for existing variables
2. Create a new variable if needed: call MCP tool **`create-variable`** with `name`, `description`, `value`
3. Reference in logic using `{{env_var[@variable-name]}}`

See `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` for details.

### Node type quick reference

| Node | obj_type | Logic type |
|------|----------|------------|
| Start | 1 | `go` |
| Code Node | 0 | `api_code` |
| Call a Process | 0 | `api_rpc` |
| API Call | 0 | `api` |
| Reply to Process | 0 | `api_rpc_reply` |
| End / Error | 2 | _(no logics)_ |

For complete JSON structures see `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md`.

### Common pitfalls

- Using `"type": "call_process"` instead of `"type": "api_rpc"` — will fail validation
- Missing `extra`/`extra_type` in Call a Process node — both required even if empty (`{}`)
- Raw JSON objects as values in `extra` — must be stringified: `"{\"key\":\"val\"}"`
- Keys in `extra` and `extra_type` must match exactly

---

## Step 3: Deploy the Changes

**MANDATORY: Always run this step whenever any changes were made to the process file — even if there are open questions or the work is not fully complete. Without deploying, all changes are lost.**

Deploy the modified process by calling MCP tool **`push-process`** with `process_path: "<PROCESS_PATH>"`.

If deployment fails, fix the reported errors and re-run `push-process` until it succeeds. Do not skip this step or postpone it — changes exist only in memory until pushed.

After a successful deploy, notify the user:

> "Changes have been deployed. Please **refresh the page** in Corezoid to see the updated process."

The project index refreshes automatically as part of `push-process` — the MCP server's `handlePushProcess` appends a "Project index refreshed at ... — processes: N, edges: M" line to the deploy output. Do **not** call `build-project-index` separately after a push; that would be a redundant second rebuild. Just surface the appended line to the user so they see the fresh counts.

If the appended line reads "Warning: project index build failed ...", tell the user but do not treat it as a rollback — the deploy itself succeeded, and the user can retry the rebuild via the `corezoid-index` skill.

---

## Reference Documents

Use the `Read` tool to load these files when specific node or validation details are needed:

| Path | When to read |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md` | JSON schemas for all node types + full Logics fields reference (canonical) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-built-in-functions.md` | Built-in functions: `$.math`, `$.date`, `$.random`, `$.sha1_hex`, `$.md5_hex`, `$.base64_encode`, `$.unixtime`, `$.map`, `$.filter` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-dynamic-values.md` | Dynamic values: `{{var}}`, `{{node[id].count}}`, `{{node[id].SumID}}`, `{{conv[@alias].ref[...]}}`, `{{env_var[@name].key[1]}}` |
| `${CLAUDE_PLUGIN_ROOT}/docs/tasks/task-metadata.md` | Global `root.*` fields: `root.task_id`, `root.ref`, `root.conv_id`, `root.node_id`, `root.prev_node_id`, `root.user_id`, `root.change_time`, `root.create_time` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/code-node.md` | Code node details and available JS libraries |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | Call a Process node, semaphores, cross-folder calls |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/reply-to-process-node.md` | Reply formats, object stringification |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | HTTP API call configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/end-node.md` | End node success/error configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/condition-node.md` | Condition node (branching logic) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/delay-node.md` | Delay node (timers and waiting); 30s limit is static-literal only — dynamic absolute-timestamp `value` for scheduled or sub-30s delays |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/copy-task-node.md` | Copy Task node (task duplication) |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-json-validation.md` | Validation rules and common errors |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error handling patterns (hardware vs software errors) |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/node-positioning-best-practices.md` | Coordinate system and layout guidelines |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variable naming rules, creation workflow, usage examples |

## Example Processes

| Path | Description |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/samples/stripe-checkout.json` | Stripe payment checkout flow |
| `${CLAUDE_PLUGIN_ROOT}/samples/create-actors.json` | Creating actors/users |
| `${CLAUDE_PLUGIN_ROOT}/samples/create-user.json` | User creation process |
| `${CLAUDE_PLUGIN_ROOT}/samples/gpt-calculator.json` | GPT integration example |
| `${CLAUDE_PLUGIN_ROOT}/samples/api-post.json` | HTTP POST API call example |
