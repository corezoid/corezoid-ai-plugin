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
3. If the user gave a **name or ID** (not a file path), search the local working directory for the matching `.conv.json` file using the `find` or `grep` Bash tools (the project is already pulled locally).
4. Once `PROCESS_PATH` is known and the file exists locally, open and analyze it before making any changes.

---

## Step 1: Analyze the Process

Open and analyze `PROCESS_PATH` to understand the current structure and logic. Pay attention to:

- Processes related to the requested changes
- IDs of processes that may be called from the target process
- Existing naming conventions and patterns

---

## Step 2: Implement the Changes

Apply changes to `PROCESS_PATH`.

### Core rules

- Connect nodes only through the `go` field
- Every node that can fail must have `err_node_id` — point it **directly at a Final Error node** (`obj_type: 2`) unless the error path needs logic (reply to caller, retry routing). Never create an Escalation node (`obj_type: 3`) that only contains a bare `go` — that is a passthrough anti-pattern flagged by `lint-process`
- Node IDs must be unique 24-character hex strings: `^[0-9a-f]{24}$`. **Always `pull-process` before editing** and reference only canonical, server-assigned IDs — IDs you invented in a previous push were reassigned by the server and no longer exist. New nodes added now get placeholder IDs that the server will likewise reassign on push. Existing nodes' IDs are preserved. See [Node ID Lifecycle](${CLAUDE_PLUGIN_ROOT}/docs/process/process-development-guide.md#node-id-lifecycle-server-assignment--stability-on-push).
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

## Step 3: Update the Description

Before deploying, update the root-level `description` field in `PROCESS_PATH` to reflect the process's current behaviour after your edits.

**Guard — preserve existing descriptions when appropriate:** if the process already has a non-empty description and your edits did not change the overall purpose (e.g. you fixed a bug, adjusted a timeout, or rewired an error path without altering what the process fundamentally does), preserve the existing description rather than replacing it.

Follow the **Description Update Rule** from the `corezoid` skill:
- 1–2 sentences, starting with a verb (*Calls*, *Creates*, *Validates*, *Routes*, *Aggregates*)
- Sentence 1: what the process does — verb + action + subject
- Sentence 2 (optional): key inputs/outputs or notable behaviour
- Under 200 characters, no *"This process…"* preamble

Write the updated `description` directly into the JSON file. This costs nothing extra — the description rides the same `push-process` call.

If the parent folder was structurally affected (process added, removed, or renamed), also call MCP tool **`modify-folder`** with a one-sentence `description` of what the folder contains.

---

## Step 4: Deploy the Changes

**MANDATORY: Always run this step whenever any changes were made to the process file — even if there are open questions or the work is not fully complete. Without deploying, all changes are lost.**

Deploy the modified process by calling MCP tool **`push-process`** with `process_path: "<PROCESS_PATH>"`.

If deployment fails, fix the reported errors and re-run `push-process` until it succeeds. Do not skip this step or postpone it — changes exist only in memory until pushed.

> **Auto-snapshot:** if the process already existed on the server (`obj_id` ≠ null), `push-process` automatically creates a snapshot of the current server state before deploying your changes. No action needed — this is transparent. The snapshot appears in the Corezoid UI and can be managed with `list-snapshots` / `get-snapshot` / `delete-snapshot`.

After a successful deploy, notify the user:

> "Changes have been deployed. Please **refresh the page** in Corezoid to see the updated process."

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
