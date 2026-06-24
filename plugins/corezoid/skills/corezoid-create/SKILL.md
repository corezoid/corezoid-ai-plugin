---
name: corezoid-create
description: >
  Corezoid process creation specialist. Use when the user wants to create a new
  Corezoid process from scratch, build a new automation flow, design a new BPM
  process, or implement a new API connector. Activate when the user says
  "create a process", "build a new flow", "new process", "design from scratch",
  "implement a connector", "create an automation", or "add a new process".
---

# Create a New Corezoid Process

You are a specialist in creating Corezoid BPM processes using the `corezoid` MCP server.

## Step 1: Gather Requirements

Ask the user for the following before proceeding:

- **Process purpose** — what should it do?
- **Input parameters** — what data does it receive?
- **Expected output** — what should it return on success?
- **Process type** — API connector (calls an external HTTP API) or business logic (orchestrates other Corezoid processes)?

For **API connector**, also require:
- `METHOD` — HTTP method (GET, POST, PUT, etc.)
- `URL` — endpoint URL (use a Corezoid variable, never hardcode)
- `AUTH` — authentication method and token variable name

> ⚠️ If the target API is the **Corezoid public API** (`/api/2/json/`), stop here and use `/corezoid-api-connector` instead — it follows a different pattern (`api_secret_outer`, `ops` array, no Code Node for signing).

If any required information is missing, ask the user before proceeding.

---

## Step 2: Create the Empty Process

Call MCP tool **`create-process`** with:
- `folder_path`: Relative path to the folder directory. Omit to use the current directory.
- `process_name`: the process name

This creates an empty process in Corezoid and saves the file as `<ID>_<Name>.conv.json` inside `folder_path`. The returned file path is `PROCESS_PATH` — all subsequent steps use it.

> ⚠️ Always verify `folder_path` points to the intended target folder. Omitting it places the process in the project root, which may not be the correct location.

> ⚠️ After `create-process`, Corezoid may create default template nodes (Start, a placeholder process node, Final) even with `create_mode: without_nodes`. Before generating the full JSON, check the current `scheme.nodes` array in the created file. If a Start node already exists, do **not** add another — doing so will cause a validation error.

If the process type is **business logic** and it needs to call existing processes, find their `conv_id` values by browsing the already-exported `.conv.json` files in the project folder.

---

## Step 3: Design the Process Structure

Every process follows this base structure:

| # | Node | obj_type | Purpose |
|---|------|----------|---------|
| 1 | Start | 1 | Entry point |
| 2 | Code Node _(optional)_ | 0 | Prepare / transform input data |
| 3 | **API Call** _or_ **Call a Process** | 0 | Core action (one or more) |
| 4 | Reply to Process (Success) | 0 | Return result to caller |
| 5 | Reply to Process (Error) | 0 | Return error to caller (one per failure point) |
| 6 | Error | 2 | Terminal error node |
| 7 | Final | 2 | Terminal success node |

**API connector** uses `type: "api"` in Step 3.
**Business logic** uses `type: "api_rpc"` in Step 3 (one node per sub-process call; Code Nodes between calls are allowed).

### Node type quick reference

| Node | obj_type | Logic type |
|------|----------|------------|
| Start | 1 | `go` |
| Code Node | 0 | `api_code` |
| Call a Process | 0 | `api_rpc` |
| API Call | 0 | `api` |
| Reply to Process | 0 | `api_rpc_reply` |
| End / Error | 2 | _(no logics)_ |

For complete JSON schemas see `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md`.

---

## Step 4: Generate the Process JSON

Produce a valid `.conv.json` file.

### Root object

```json
{
  "obj_type": 1,
  "obj_id": null,
  "parent_id": null,
  "title": "Process Name",
  "description": "",
  "status": "active",
  "params": [],
  "ref_mask": true,
  "conv_type": "process",
  "scheme": {
    "nodes": [],
    "web_settings": [[], []]
  }
}
```

`params` — declare all input parameters the caller must pass. See `${CLAUDE_PLUGIN_ROOT}/docs/process/process-with-parameters.md`.

### Core rules

- Node IDs must be unique 24-character hex strings: `^[0-9a-f]{24}$`. These are **temporary placeholders** for new nodes — on `push-process` Corezoid reassigns its own canonical IDs (and rewires references within the push). Run `pull-process` after pushing to get the canonical IDs before any further edits. See [Node ID Lifecycle](${CLAUDE_PLUGIN_ROOT}/docs/process/process-development-guide.md#node-id-lifecycle-server-assignment--stability-on-push).
- Connect nodes only through the `go` field
- Every node that can fail must have `err_node_id` — point it **directly at a Final Error node** (`obj_type: 2`) unless the error path needs logic (reply to caller, retry routing). Never create an Escalation node (`obj_type: 3`) that only contains a bare `go` — that is a passthrough anti-pattern flagged by `lint-process`
- All constants (URLs, tokens, IDs) must be Corezoid variables — never hardcoded:
  1. Check for existing variables: read `_ENV_VARS_.json` (from `pull-folder`) or `.processes/variables.json` (from this session)
  2. Create a new variable if needed: call MCP tool **`create-variable`** with `name`, `description`, `value`
  3. Reference in logic: `{{env_var[@variable-name]}}`
- Use descriptive `title` values (e.g., "Call Payment Process", not "RPC")
- Position nodes top-to-bottom, incrementing `y` by 200–250px; place error nodes to the right (`x + 300`)
- Place each Reply Error node at the **same `y`** as the Call/API node it handles — this creates a straight horizontal connector line for the error path

### Common pitfalls

- Using `"type": "call_process"` instead of `"type": "api_rpc"` — will fail validation
- Missing `extra`/`extra_type` in Call a Process node — both required even if empty (`{}`)
- Raw JSON objects as values in `extra` — must be stringified: `"{\"key\":\"val\"}"`
- Keys in `extra` and `extra_type` must match exactly
- Missing `rfc_format: true`, `customize_response: true`, or `version: 2` in API Call node

---

## Step 5: Validate with Lint

Call MCP tool **`lint-process`** with `process_path: "<PROCESS_PATH>"`.

Fix all reported errors and re-run until the output is clean. Do not proceed with lint errors.

---

## Step 6: Deploy and Test

Call MCP tool **`push-process`** with `process_path: "<PROCESS_PATH>"`.

After a successful deploy, run a test task to verify the process behaves as expected:

Call MCP tool **`run-task`** with:
- `process_path`: `<PROCESS_PATH>`
- `data`: `{"param1": "value1"}`

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
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/delay-node.md` | Delay/timer node; 30s limit is static-literal only — dynamic absolute-timestamp `value` for scheduled or sub-30s delays |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/end-node.md` | End node success/error configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-json-validation.md` | Validation rules and common errors |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error handling patterns |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/node-positioning-best-practices.md` | Coordinate system and layout guidelines |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variable naming rules, creation workflow, usage examples |

## Example Processes

| Path | Description |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/samples/api-post.json` | HTTP POST API call (connector pattern) |
| `${CLAUDE_PLUGIN_ROOT}/samples/corezoid-api-node-list.conv.json` | Corezoid API connector (Node List, `api_secret_outer` pattern) |
| `${CLAUDE_PLUGIN_ROOT}/samples/stripe-checkout.json` | Stripe payment checkout flow |
| `${CLAUDE_PLUGIN_ROOT}/samples/create-actors.json` | Business logic with multiple process calls |
| `${CLAUDE_PLUGIN_ROOT}/samples/gpt-calculator.json` | GPT integration example |
| `${CLAUDE_PLUGIN_ROOT}/samples/create-user.json` | User creation process |
