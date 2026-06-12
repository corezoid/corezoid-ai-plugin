# State Diagram Overview

A **State Diagram** (`conv_type: "state"`) is a special type of Corezoid object that **stores data as long-lived tasks** representing the current state of an entity (user, order, device, account, etc.). Other processes read, create, and modify these state tasks through a small set of dedicated logics.

Think of a State Diagram as a **persistent key-value store with workflow semantics**:
- The **key** is the task's `ref` (passed when the state task is created)
- The **value** is the task's data (any parameters you store on it)
- The **workflow** is the set of states (nodes) the task transitions through over its lifetime

## When to use a State Diagram

Use a State Diagram when you need to:

- Track the lifecycle of an entity (e.g. `pending → active → blocked → closed`)
- Persist the latest known data for an entity across many processes
- Trigger automatic transitions when an entity's data changes
- Share state between independent processes through a `ref`

Do **not** use a State Diagram for transient processing — use a regular process for that.

## How it differs from a regular Process

| Aspect | Regular Process (`conv_type: "process"`) | State Diagram (`conv_type: "state"`) |
|---|---|---|
| Purpose | Run a workflow then complete | Store data and react to external changes |
| Task lifetime | Short-lived — flows from Start to End | Long-lived — parks in a state node, waits for callbacks |
| Allowed logic nodes | All 24 node types | 10 node types only (see below) |
| Triggered by | A new task created on Start | A new task on Start, **or** a `modify` callback from another process |
| State pattern | Linear flow | State machine — each state node waits for a callback, then conditionally transitions |
| Reads from outside | Returns a reply via `api_rpc_reply` | Read directly via `{{conv[<id>].ref[<ref>].<field>}}` |

## Allowed nodes inside a State Diagram

Only the following node logics may appear in a State Diagram. Adding any other node type will fail validation on push.

| Node | Logic `type` | Purpose inside a state diagram |
|---|---|---|
| Start | `go` (Start, `obj_type: 1`) | Entry point — creates the state task |
| Condition | `go_if_const` | Branch between states based on data |
| Code | `api_code` | Transform data before it enters a state |
| Set Parameters | `set_param` | Set / compute parameters on the task |
| Copy Task | `api_copy` with `mode: "create"` | Fork data to another process (e.g. notification) |
| Modify Task | `api_copy` with `mode: "modify"` | Modify a target task by `ref` in some process (often this same state diagram) |
| Set State | `api_callback` + `go_if_const`s + self-`go` (see below) | The "park here" node — waits for callbacks |
| Delay | semaphor only (`semaphors: [{ "type": "time", ... }]`) | Park the task for a fixed time, then auto-transition |
| Queue | `api_queue` / `api_get_task` | Hold tasks in a queue until processed |
| End: Error | (terminal, `obj_type: 2`, icon `error`) | Final error sink |
| End: Success | (terminal, `obj_type: 2`, icon `success`) | Final success sink |

> ⚠️ **Forbidden in state diagrams:** API Call (`api`), Call a Process (`api_rpc`), Reply to Process (`api_rpc_reply`), DB Call (`db_call`), Git Call (`git_call`), Sum (`api_sum`), API Form (`api_form`).
>
> If you need to call out to an external system from a state, do it from the **driver process** that triggered the state change — never from inside the state diagram.

## The "state" node pattern

A **state node** is the structural heart of a State Diagram. Each named state (e.g. `Active`, `Inactive`, `Blocked`) is one node with this exact shape:

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      { "type": "api_callback" },
      {
        "type": "go_if_const",
        "to_node_id": "<other-state-id>",
        "conditions": [
          { "param": "status", "const": "active", "fun": "eq", "cast": "string" }
        ]
      },
      { "type": "go", "to_node_id": "<self-id>" }
    ],
    "semaphors": []
  },
  "title": "Inactive users",
  "x": 1108,
  "y": 400,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"state\"}",
  "options": null
}
```

How it works at runtime:

1. **`api_callback`** — the task **parks** here. The state is "stored". The task does nothing until an external `api_copy` with `mode: "modify"` updates its data.
2. When data is updated, the logics below `api_callback` re-evaluate top-to-bottom.
3. Each `go_if_const` checks updated data and routes the task to a different state if the condition matches.
4. The final `go` points back to the **same node's own id** — the "stay in this state" fallback.

> ✏️ `extra` must include `"icon":"state"` so the Corezoid UI renders it as a state pill rather than a regular logic node.

## Root document structure

```json
{
  "obj_type": 1,
  "obj_id": null,
  "parent_id": <folder_id>,
  "title": "User Status",
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

The only difference from a regular process is `"conv_type": "state"`. Everything else uses the same envelope.

## How processes interact with a state diagram

A regular process can do four things to a state diagram task:

### 1. Read a parameter (`set_param`)

```json
{
  "type": "set_param",
  "extra": { "user": "{{conv[1863140].ref[{{userId}}].status}}" },
  "extra_type": { "user": "string" },
  "err_node_id": "<error_id>"
}
```

Templates:
- `{{conv[<state_diagram_id>].ref[<ref>]}}` — the whole task object
- `{{conv[<state_diagram_id>].ref[<ref>].<field>}}` — a single field
- `{{conv[@user-states].ref[{{userId}}].status}}` — via alias and a dynamic ref

### 2. Create a new state task (`api_copy` with `mode: "create"`)

```json
{
  "type": "api_copy",
  "conv_id": 1863140,
  "ref": "{{userId}}",
  "mode": "create",
  "is_sync": true,
  "group": "",
  "data": { "status": "active" },
  "data_type": { "status": "string" },
  "err_node_id": "<error_id>",
  "user_id": <user_id>
}
```

- **`ref` is mandatory.** It is the lookup key for this state task. Choose a stable, unique identifier (user id, order id, etc.).
- A new task starts at the State Diagram's Start node and is routed to its initial state.
- Possible errors: `not_unical_ref` (the ref already exists — use `modify` instead), `access_denied`, `copy_task_timeout`, `crash_api`.

### 3. Modify an existing state task (`api_copy` with `mode: "modify"`)

```json
{
  "type": "api_copy",
  "conv_id": 1863140,
  "ref": "{{userId}}",
  "mode": "modify",
  "is_sync": true,
  "group": "",
  "data": { "status": "inactive" },
  "data_type": { "status": "string" },
  "err_node_id": "<error_id>",
  "user_id": <user_id>
}
```

- Updates the task data and **wakes up** the `api_callback` inside the state node so the transitions re-evaluate.
- Possible errors: `not_found_task` (no task with that `ref` exists yet — create it first), `access_denied`, `crash_api`.

### 4. Delete / advance the state task

State diagrams do not expose a direct "delete" — instead, transition the state into a terminal **End** node. A task that lands on `obj_type: 2` is removed from the state machine and can be archived via `options: "{\"save_task\":true}"`.

## Reference workflow

A typical "user lifecycle" example uses two artifacts:

1. **State Diagram** (`User Status`, `conv_type: "state"`) with states `Active`, `Inactive`, `Blocked`.
2. **Driver Process** (`conv_type: "process"`) that:
   - Reads the current status with `set_param` + `{{conv[<sd>].ref[{{userId}}].status}}`
   - Branches with a `go_if_const` (Condition node)
   - Calls `api_copy mode: "modify"` to flip the status, which triggers a transition inside the state diagram.

See `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-state-diagram.conv.json` (the state diagram) and `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-driver-process.conv.json` (the driver process) for a complete working pair.
