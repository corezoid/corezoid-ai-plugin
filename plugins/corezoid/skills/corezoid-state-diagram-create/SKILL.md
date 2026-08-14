---
name: corezoid-state-diagram-create
description: >
  Corezoid state diagram creation specialist. Use when the user wants to create
  a new Corezoid state diagram, build a state machine, design a status / lifecycle
  store, or set up a "state" object with conv_type "state". Activate when the
  user says "create a state diagram", "build a state machine", "new state diagram",
  "design states", "track status", "user lifecycle", "состояния", "стейт диаграмма",
  "создать state diagram", or mentions storing state-by-ref between processes.
---

# Create a New Corezoid State Diagram

You are a specialist in creating Corezoid **state diagrams** (`conv_type: "state"`) using the `corezoid` MCP server.

A state diagram is a long-lived data store: each task is one entity's state, referenced by a stable `ref`. Other processes read, create, and modify these state tasks. The state diagram itself only contains states (parked tasks), transitions between them, and a tiny subset of helper nodes.

Before you start, make sure you understand:
- A state diagram has `conv_type: "state"` at the root (not `"process"`).
- Only 10 node types are allowed: Start, Condition, Code, Set Parameters, Copy Task, Modify Task, Set State (= a state node), Delay, Queue, End: Success, End: Error.
- API Call, Call a Process, Reply to Process, DB Call, Git Call, Sum, API Form are **forbidden** inside a state diagram — they belong in the driver process.

Read `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-overview.md` if you need a refresher on the model.

---

## Step 1: Gather Requirements

Ask the user for the following before proceeding:

- **What entity is being tracked?** (user, order, device, subscription, account, …)
- **What is the `ref`?** — the stable identifier (e.g. `userId`, `orderId`). This is the lookup key every reader and writer will use.
- **List the states.** A name and a one-line description for each (e.g. `Pending`, `Active`, `Suspended`, `Closed`).
- **List the transitions.** For every state, what data change causes it to move to which other state? (e.g. `Active → Suspended when status == "suspended"`).
- **Initial state.** Which state does a newly created task enter first?
- **Terminal states.** Which states are "End: Success" / "End: Error", if any?
- **Side effects on entry / exit (optional).** Should entering a state trigger a notification, stamp a timestamp, etc.? (These are Copy Task / Modify Task nodes between states.)

If any of the above is missing, ask the user before continuing.

---

## Step 2: Create the Empty State Diagram

Call MCP tool **`create-state-diagram`** with:
- `folder_path`: Relative path to the folder directory. Omit to use the current directory.
- `process_name`: the state diagram name.

This creates an empty state diagram in Corezoid with `conv_type: "state"` and writes its skeleton JSON to `<ID>_<Name>.conv.json` inside `folder_path`. The returned file path is `PROCESS_PATH` — all subsequent steps use it.

> ⚠️ Always verify `folder_path` points to the intended target folder. Omitting it places the diagram in the project root, which may not be the correct location.

> ⚠️ Open the new file and confirm `"conv_type": "state"` at the root before doing anything else. The push pipeline now accepts both `"process"` and `"state"`, but if `conv_type` is accidentally `"process"`, the next push will redeploy it as a regular process.

**Already exists in Corezoid?** If the user pre-created the diagram in the Corezoid UI, pull it instead: call MCP tool **`pull-process`** with `process_id: <id>`. `pull-process` works for both processes and state diagrams — the resulting file preserves `conv_type: "state"`.

---

## Step 3: Design the State Diagram Structure

A state diagram is structured as:

| # | Node | obj_type | Purpose |
|---|------|----------|---------|
| 1 | Start | 1 | Entry — routes a newly-created task to its initial state |
| 2 | _(optional)_ Set Parameters / Code | 0 | Compute / normalise data on entry |
| 3 | One state node per state | 0 (logic begins with `api_callback`) | Park the task until externally modified |
| 4 | _(optional)_ Copy Task / Modify Task between states | 0 | Side effects on transition |
| 5 | _(optional)_ Delay node | 0 | Time-bounded states (e.g. trial expiry) |
| 6 | End: Success | 2 | Terminal state for "happy" closure |
| 7 | End: Error | 2 | Terminal state for failure closure |

### State node anatomy (memorise this shape)

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      { "type": "api_callback" },
      {
        "type": "go_if_const",
        "to_node_id": "<other_state_id>",
        "conditions": [
          { "param": "status", "const": "blocked", "fun": "eq", "cast": "string" }
        ]
      },
      { "type": "go", "to_node_id": "<self_id>" }
    ],
    "semaphors": []
  },
  "title": "Active",
  "x": 880, "y": 400,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"state\"}",
  "options": null
}
```

Key invariants for every state node:
- **First logic is `api_callback`** (with no other fields). This is what "parks" the task.
- One `go_if_const` per outbound transition. Order matters — first match wins.
- **Last logic is `go` pointing back to the node's own id** (the "stay here" fallback).
- `extra` must include `"icon":"state"` so the UI renders the state pill correctly.
- Do not add `err_node_id` — `api_callback` does not surface the regular error path.

---

## Step 4: Generate the State Diagram JSON

Produce a valid `.conv.json` file with the following root envelope:

```json
{
  "obj_type": 1,
  "obj_id": <id from step 2>,
  "parent_id": <folder_id>,
  "title": "<State Diagram Name>",
  "description": "",
  "status": "active",
  "params": [],
  "ref_mask": true,
  "conv_type": "state",
  "scheme": {
    "nodes": [],
    "web_settings": [[], []]
  }
}
```

### Core rules

- `conv_type` **must** be `"state"`.
- Node IDs are 24-character hex: `^[0-9a-f]{24}$`. Generate with `crypto.randomBytes(12).toString('hex')` or any equivalent.
- Connect nodes only through the `go` / `go_if_const` `to_node_id` fields.
- Every node that uses logic with `err_node_id` (Code, Set Parameters, Copy Task, Modify Task, Queue) must point at a dedicated End: Error node.
- Use descriptive node `title` values — they are the state names visible on the canvas and in dashboards.
- Layout: spread states **horizontally** (different `x` per state), keep the Start above them. State nodes sit around `y ≈ 400`, Start at `y = 100`. Increment `x` by ≈ 320–400 between adjacent states. Place End nodes at the bottom (`y ≈ 700–900`).

### Allowed logics inside a state diagram

| Node | Logic `type` | Notes |
|---|---|---|
| Start | `go` (`obj_type: 1`) | Exactly one per diagram |
| State (Set State) | `api_callback` + `go_if_const`s + self-`go` | The structural heart of the diagram |
| Condition | `go_if_const` | For pre-state routing |
| Code | `api_code` | Avoid unless necessary; prefer `set_param` |
| Set Parameters | `set_param` | Compute / stamp fields |
| Copy Task | `api_copy` with `mode: "create"` | Fan out to another process (notifications, audit) — **not** to write back to this same diagram |
| Modify Task | `api_copy` with `mode: "modify"` | Update a task by `ref` in some target process — note: in-place edits to the current task should use `set_param` instead |
| Delay | semaphor-only | Time-bounded states |
| Queue | `api_queue` | Ordered / throttled processing |
| End | (`obj_type: 2`) | Terminal node (success or error icon) |

### Variables for constants

If a node references an external id (e.g. another process to notify), store it as a Corezoid variable and reference it as `{{env_var[@variable-name]}}` — never hardcode. Use **`create-variable`** if the variable does not yet exist. See `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md`.

### Common pitfalls

- Forgetting `"icon":"state"` in `extra` for a state node — the node renders as a plain logic node.
- Missing the trailing self-loop `go` on a state node — the task escapes the state on every callback.
- Putting an API Call, Call a Process, or Reply to Process node in a state diagram — these are **forbidden**. Move them into the driver process.
- Using `api_copy mode: "modify"` from inside the state diagram targeting its own ref — that creates an infinite re-callback loop. Use `set_param` to update the current task in place instead.
- Raw JSON objects as `extra` / `data` values — must be stringified (`"{\"k\":\"v\"}"`).

---

## Step 5: Validate with Lint

Call MCP tool **`lint-process`** with `process_path: "<PROCESS_PATH>"`.

Fix every reported error and re-run until the output is clean. Do not proceed with lint errors.

> If the linter complains about a forbidden logic (`api`, `api_rpc`, `api_rpc_reply`, `db_call`, `git_call`, `api_sum`, `api_form`), remove the node and re-design the side effect to live in the driver process.

---

## Step 6: Deploy

Call MCP tool **`push-process`** with `process_path: "<PROCESS_PATH>"`.

If the push fails:
- Re-read the file and confirm `"conv_type": "state"` is present at the root.
- Confirm every state node ends in `go → self`.
- Confirm only allowed logic types are present.

After a successful push, notify the user:

> "State diagram deployed. Refresh the Corezoid page to see the new diagram. To start using it, create the driver process that calls `api_copy mode:create` with a `ref` to add entities, and `mode:modify` to drive transitions."

---

## Step 7 (optional): Build the Driver Process

A state diagram is useless without a driver process that creates and modifies its tasks. If the user has not already built one, offer to:

1. Hand off to `/corezoid-create` to scaffold the driver process.
2. Wire it with three node patterns:
   - **Read state:** `set_param` with `{{conv[<sd_id>].ref[{{ref}}].<field>}}`
   - **Create state task:** `api_copy` with `conv_id: <sd_id>`, `ref: {{<ref>}}`, `mode: "create"`, `data: {...}`
   - **Modify state task:** `api_copy` with `conv_id: <sd_id>`, `ref: {{<ref>}}`, `mode: "modify"`, `data: {...}`

Read `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-process-interaction.md` for full templates.

Recommend creating an alias (`/corezoid-alias-manager`) for the state diagram so the driver references `@user-states` instead of a numeric id.

---

## Reference Documents

| Path | When to read |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-overview.md` | Concepts, allowed nodes, root structure |
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-node-structures.md` | Canonical JSON for every allowed node type |
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-process-interaction.md` | How driver processes read / create / modify state tasks |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-state-node.md` | Background on Set State (legacy `obj_type:3` form) and the `{{conv[...]}}` template |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/copy-task-node.md` | Error catalogue for `api_copy` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/condition-node.md` | `go_if_const` reference |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variables (`{{env_var[@…]}}`) |

## Example Files

| Path | Description |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-state-diagram.conv.json` | Minimal two-state diagram (`Active` ⇄ `Inactive`) |
| `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-driver-process.conv.json` | Companion driver process that reads + modifies the state |
