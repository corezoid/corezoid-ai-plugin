# Interacting with a State Diagram from a Process

A state diagram (`conv_type: "state"`) is a passive store — it does not start anything. Driver **processes** (`conv_type: "process"`) are responsible for creating, reading, and modifying its tasks. This document is the canonical reference for those four interactions.

Every example below assumes a state diagram with process id `1863140` (or alias `@user-states`) and a `ref` taken from the driver task's `userId` parameter.

---

## 1. Read a parameter — `set_param` with the `conv[]` template

Use when the driver process needs to know the current value stored in the state diagram.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "set_param",
        "extra": {
          "user": "{{conv[1863140].ref[{{userId}}].status}}"
        },
        "extra_type": { "user": "string" },
        "err_node_id": "<error_node_id>"
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Read user status from state diagram",
  "x": 780, "y": 196,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

Template forms (any of these may appear inside `extra`):

| Template | What it returns |
|---|---|
| `{{conv[1863140].ref[{{userId}}]}}` | The entire task data object (type: `object`) |
| `{{conv[1863140].ref[{{userId}}].status}}` | A single field |
| `{{conv[@user-states].ref[{{userId}}].status}}` | Same, via alias |
| `{{conv[{{sd_id}}].ref[{{userId}}].status}}` | Both id and ref are dynamic |

If the `ref` does not exist, the template resolves to an empty string. Add a Condition node afterwards to handle the "no state yet" case (typically by branching to a Copy Task that creates the state with `mode: "create"`).

---

## 2. Create a new state task — `api_copy` with `mode: "create"`

Use when the entity does not yet have a state task. The new task starts on the state diagram's Start node.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_copy",
        "conv_id": 1863140,
        "ref": "{{userId}}",
        "mode": "create",
        "is_sync": true,
        "group": "",
        "send_parent_data": false,
        "data": {
          "status": "active",
          "created_at": "$.date()"
        },
        "data_type": {
          "status": "string",
          "created_at": "number"
        },
        "err_node_id": "<error_condition_node_id>",
        "user_id": <user_id>
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": [
      { "type": "time", "value": 30, "dimension": "sec", "to_node_id": "<retry_or_timeout_node_id>" }
    ]
  },
  "title": "Create user state",
  "x": 480, "y": 476,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

Rules:
- `ref` is **mandatory** and is what every subsequent read / modify will use. Choose a stable, unique identifier (e.g. `userId`, `orderId`).
- `mode: "create"` raises `not_unical_ref` if the ref already exists — handle it in the error condition node and fall through to `mode: "modify"` if needed.
- `is_sync: true` waits for the create to succeed before continuing.
- `group: ""` is required when `data` is non-empty in `mode: "create"`. (Different from "Copy Task to a regular process" — there it's `"all"`.)

### Default error escalation pattern

`api_copy` exposes the standard `__conveyor_copy_task_return_type_tag__` error tags. The minimum escalation is a Condition node that branches on:

| Tag | Meaning | Suggested route |
|---|---|---|
| `not_unical_ref` | Ref already exists | Fall through to a Modify Task node |
| `not_found_task` | (Only in `modify`) the ref doesn't exist | Fall back to Create |
| `crash_api`, `copy_task_timeout`, `copy_task_fatal_error`, `duplicate_callback` | Transient failure | Delay → retry |
| `access_denied` | Permission missing | Error end (cannot be recovered) |

See `${CLAUDE_PLUGIN_ROOT}/docs/nodes/copy-task-node.md` for the full error catalogue.

---

## 3. Modify an existing state task — `api_copy` with `mode: "modify"`

This is the interaction that **triggers a transition** inside the state diagram. The `api_callback` inside the current state node re-evaluates the data and routes the task to the next state.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_copy",
        "conv_id": 1863140,
        "ref": "{{userId}}",
        "mode": "modify",
        "is_sync": true,
        "group": "",
        "send_parent_data": false,
        "data": { "status": "inactive" },
        "data_type": { "status": "string" },
        "err_node_id": "<error_condition_node_id>",
        "user_id": <user_id>
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Mark user inactive",
  "x": 480, "y": 476,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

Behaviour notes:
- Only the fields in `data` are merged onto the task. Existing fields are kept unless explicitly overwritten.
- After `modify`, the `api_callback` in the **current** state node re-fires its outgoing logics; if a `go_if_const` matches the new data the task moves to that next state.
- If no `go_if_const` matches, the task stays in the current state (the trailing `go` self-loop).

---

## 4. Move a state task to End — let the state diagram do it

There is no "delete by ref" API. To end a state task, design a transition inside the state diagram that routes to an End node:

```
[Active]  --api_callback + go_if_const status=='closed'-->  [Closed (End: Success)]
```

Then from the driver process, call `api_copy mode: "modify"` with `data: { "status": "closed" }`. The state node's outgoing `go_if_const` matches, the task hops to the End node, and it is removed from the live state machine.

If you need to keep the closed task around for audits, set the End node's `options: "{\"save_task\":true}"`.

---

## End-to-end example

Driver process: read current status, branch on it, modify the state.

```
Start
  └── set_param ({{conv[1863140].ref[{{userId}}].status}} → user)
        └── go_if_const user == "active" → api_copy(mode:modify, data:{status:"inactive"}) → Final
            go_if_const user == "inactive" → api_copy(mode:modify, data:{status:"active"}) → Final
            go (default) → Final
```

This is exactly the pattern in `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-driver-process.conv.json`, paired with `${CLAUDE_PLUGIN_ROOT}/samples/state-diagrams/user-status-state-diagram.conv.json`.

---

## Common pitfalls

| Pitfall | Symptom | Fix |
|---|---|---|
| Hardcoding the state diagram id in many processes | Hard to repoint when the id changes | Create an alias (`@user-states`) and reference it everywhere |
| Forgetting `ref` on `api_copy` | `mode: "create"` fails or silently creates one nameless task | `ref` is mandatory for any state-diagram interaction |
| Using `mode: "create"` for an existing ref | `not_unical_ref` error | Pre-read the ref or handle `not_unical_ref` and fall through to `modify` |
| Reading without handling "no state yet" | `{{conv[...]}}` resolves to empty string and subsequent logic silently misbehaves | Add a Condition right after `set_param` for the empty case |
| Modifying with `is_sync: false` then immediately reading | Race: the read returns the old value | Use `is_sync: true` for write-then-read flows |
| Calling APIs from inside the state diagram | Push fails validation, or worse, blocks the state machine | Move the API call into the driver process |
