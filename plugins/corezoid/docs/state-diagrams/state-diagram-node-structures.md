# State Diagram Node Structures

Canonical JSON shapes for every node type that is **allowed inside a State Diagram** (`conv_type: "state"`).

If a node type is not listed here, it is **forbidden** in a state diagram and will fail validation. See [state-diagram-overview.md](./state-diagram-overview.md) for the full rationale.

For node types shared with regular processes (Condition, Code, Set Parameters, Copy Task, End), the JSON shape is identical to the one in `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md`; this file shows the **state-diagram-specific patterns** and the nodes that are unique to state diagrams.

---

## Start Node (`obj_type: 1`)

Identical to a regular process. Always one per state diagram.

```json
{
  "id": "<24-hex>",
  "obj_type": 1,
  "condition": {
    "logics": [
      { "type": "go", "to_node_id": "<initial_state_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Start",
  "description": "",
  "x": 880,
  "y": 100,
  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}",
  "options": null
}
```

The `go.to_node_id` of the Start node usually points to the **initial state node** (e.g. `Active`, `Pending`, `New`).

---

## State Node (`obj_type: 0`, logic begins with `api_callback`)

This is the **defining node of a state diagram**. Every named state is one of these. The task parks on `api_callback` and re-evaluates the transitions whenever another process calls `api_copy mode: "modify"` on its ref.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      { "type": "api_callback" },
      {
        "type": "go_if_const",
        "to_node_id": "<other_state_node_id>",
        "conditions": [
          { "param": "status", "const": "active", "fun": "eq", "cast": "string" }
        ]
      },
      {
        "type": "go_if_const",
        "to_node_id": "<another_state_node_id>",
        "conditions": [
          { "param": "status", "const": "blocked", "fun": "eq", "cast": "string" }
        ]
      },
      { "type": "go", "to_node_id": "<self_id>" }
    ],
    "semaphors": []
  },
  "title": "Inactive users",
  "description": "",
  "x": 1108,
  "y": 400,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"state\"}",
  "options": null
}
```

**Rules:**
- `obj_type: 0` (not `3` — older docs may show 3; current Corezoid uses 0 with the `api_callback`-first pattern).
- The **first logic must be `api_callback`** with no additional fields. This is what makes the node "park" the task.
- After `api_callback`, list each outbound transition as a `go_if_const`. Earliest match wins.
- The **last logic must be `go` pointing back to the same node's own `id`** — the "stay in this state" fallback.
- `extra` must include `"icon":"state"` for the UI to render the state pill.
- `title` is the **human-readable state name** (e.g. `Active`, `Pending`, `Blocked`). It is shown on the canvas and referenced in dashboards.
- Do not add `err_node_id` — `api_callback` does not raise the standard error path.

---

## Condition Node (`obj_type: 0`, `type: "go_if_const"`)

Same shape as in a regular process; commonly used to route a task **between states** before it parks on an `api_callback`. See `${CLAUDE_PLUGIN_ROOT}/docs/nodes/condition-node.md` for full details.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "go_if_const",
        "to_node_id": "<active_state_id>",
        "conditions": [
          { "param": "status", "const": "active", "fun": "eq", "cast": "string" }
        ]
      },
      { "type": "go", "to_node_id": "<inactive_state_id>" }
    ],
    "semaphors": []
  },
  "title": "Route by status",
  "x": 880,
  "y": 250,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

---

## Code Node (`obj_type: 0`, `type: "api_code"`)

Same shape as in a regular process. Use sparingly — most state diagrams should not run arbitrary code. Prefer `set_param` for simple transformations.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_code",
        "lang": "js",
        "src": "data.derived = (data.first || '') + '_' + (data.last || '');",
        "err_node_id": "<error_end_id>"
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Compose display name",
  "x": 880,
  "y": 250,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

`err_node_id` is **required** and must point to an End: Error node.

---

## Set Parameters Node (`obj_type: 0`, `type: "set_param"`)

Same shape as in a regular process. Use to compute / derive fields before parking on a state.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "set_param",
        "extra": {
          "entered_at": "$.date()",
          "full_name": "{{first_name}}_{{last_name}}"
        },
        "extra_type": {
          "entered_at": "number",
          "full_name": "string"
        },
        "err_node_id": "<error_end_id>"
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Stamp entered_at",
  "x": 880,
  "y": 250,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

`err_node_id` is **required**.

---

## Copy Task Node (`obj_type: 0`, `type: "api_copy"`)

Use to **fan out** data from a state diagram to another process (commonly: send a notification, record an audit event). The original task continues in the state diagram.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_copy",
        "conv_id": "@notify-user",
        "mode": "create",
        "ref": "",
        "group": "all",
        "is_sync": false,
        "send_parent_data": false,
        "data": { "user_id": "{{ref}}", "new_status": "{{status}}" },
        "data_type": { "user_id": "string", "new_status": "string" },
        "err_node_id": "<error_end_id>"
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Notify on state change",
  "x": 880,
  "y": 250,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

Notes:
- Do **not** use `api_copy` here to write back to the same state diagram — use Modify Task for that.
- `err_node_id` is **required**.

---

## Modify Task Node (`obj_type: 0`, `type: "api_copy"` with `mode: "modify"`)

"Modify Task" is **not a separate logic type** — it is `api_copy` with `mode: "modify"`. Used to update a task by `ref` in some target process (often the same state diagram).

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_copy",
        "conv_id": <target_process_id_or_alias>,
        "ref": "{{ref}}",
        "mode": "modify",
        "is_sync": true,
        "group": "",
        "send_parent_data": false,
        "data": {
          "previous_status": "{{status}}",
          "updated_at": "$.date()"
        },
        "data_type": {
          "previous_status": "string",
          "updated_at": "number"
        },
        "err_node_id": "<error_end_id>"
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Stamp previous status",
  "x": 880,
  "y": 250,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

Notes:
- To modify **fields on the current task in place** (without crossing a process boundary), prefer **Set Parameters** (`set_param`) — it edits the task's `data` object directly and has zero overhead.
- Use `api_copy mode: "modify"` only when you need to update a task in another process (or another state diagram) identified by `ref`.
- `err_node_id` is **required**.

---

## Delay Node (`obj_type: 0`, semaphor only)

Parks the task for a fixed time, then auto-routes via the semaphor target. Use for time-bounded states (e.g. "trial expires in 14 days").

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [],
    "semaphors": [
      {
        "type": "time",
        "value": 14,
        "dimension": "day",
        "to_node_id": "<expired_state_id>"
      }
    ]
  },
  "title": "Trial period",
  "x": 880,
  "y": 250,
  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}",
  "options": null
}
```

`dimension` values: `"sec"`, `"min"`, `"hour"`, `"day"`.

---

## Queue Node (`obj_type: 0`, `type: "api_queue"`)

Holds tasks in a named queue until a consumer drains them with `api_get_task`. Rarely needed inside state diagrams — only use when you need ordered, throttled processing.

```json
{
  "id": "<24-hex>",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_queue",
        "queue": "{{env_var[@user-status-queue]}}",
        "err_node_id": "<error_end_id>"
      },
      { "type": "go", "to_node_id": "<next_node_id>" }
    ],
    "semaphors": []
  },
  "title": "Enqueue for review",
  "x": 880,
  "y": 250,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

See `${CLAUDE_PLUGIN_ROOT}/docs/nodes/queue-node.md` and `${CLAUDE_PLUGIN_ROOT}/docs/nodes/get-from-queue-node.md` for the full pattern.

---

## End: Success / End: Error (`obj_type: 2`)

Terminal nodes. Identical to a regular process; only the `extra.icon` differs.

```json
{
  "id": "<24-hex>",
  "obj_type": 2,
  "condition": { "logics": [], "semaphors": [] },
  "title": "Closed",
  "description": "",
  "x": 880,
  "y": 900,
  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"success\"}",
  "options": "{\"save_task\":true}"
}
```

For an error sink, set `"icon":"error"`. Use `options: "{\"save_task\":true}"` if you want the closed task to be archived for later audit.

---

## Quick reference — full state diagram skeleton

A minimal viable state diagram with two states and a Start:

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
    "nodes": [
      {
        "id": "aaaaaaaaaaaaaaaaaaaaaaaa",
        "obj_type": 1,
        "condition": {
          "logics": [ { "type": "go", "to_node_id": "bbbbbbbbbbbbbbbbbbbbbbbb" } ],
          "semaphors": []
        },
        "title": "Start", "x": 880, "y": 100,
        "extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}",
        "options": null
      },
      {
        "id": "bbbbbbbbbbbbbbbbbbbbbbbb",
        "obj_type": 0,
        "condition": {
          "logics": [
            { "type": "api_callback" },
            {
              "type": "go_if_const",
              "to_node_id": "cccccccccccccccccccccccc",
              "conditions": [ { "param": "status", "const": "inactive", "fun": "eq", "cast": "string" } ]
            },
            { "type": "go", "to_node_id": "bbbbbbbbbbbbbbbbbbbbbbbb" }
          ],
          "semaphors": []
        },
        "title": "Active",
        "x": 740, "y": 400,
        "extra": "{\"modeForm\":\"expand\",\"icon\":\"state\"}",
        "options": null
      },
      {
        "id": "cccccccccccccccccccccccc",
        "obj_type": 0,
        "condition": {
          "logics": [
            { "type": "api_callback" },
            {
              "type": "go_if_const",
              "to_node_id": "bbbbbbbbbbbbbbbbbbbbbbbb",
              "conditions": [ { "param": "status", "const": "active", "fun": "eq", "cast": "string" } ]
            },
            { "type": "go", "to_node_id": "cccccccccccccccccccccccc" }
          ],
          "semaphors": []
        },
        "title": "Inactive",
        "x": 1108, "y": 400,
        "extra": "{\"modeForm\":\"expand\",\"icon\":\"state\"}",
        "options": null
      }
    ],
    "web_settings": [[], []]
  }
}
```

---

## Forbidden inside a state diagram

| Node | Logic `type` | Why forbidden / alternative |
|---|---|---|
| API Call | `api` | State diagrams must not make HTTP requests. Do the call in the driver process. |
| Call a Process | `api_rpc` | State diagrams must not block on RPC. Use Copy Task fan-out instead. |
| Reply to Process | `api_rpc_reply` | A state task is not invoked via RPC — there is nothing to reply to. |
| DB Call | `db_call` | Same as API Call — do it in the driver process. |
| Git Call | `git_call` | Same. |
| Sum | `api_sum` | Aggregation belongs in the driver process or in dashboards. |
| API Form | `api_form` | Forms are user-facing — do not place them inside a storage diagram. |

If the linter / `push-process` rejects a state diagram with `obj_type: 1, conv_type: "state"`, the most common cause is one of the forbidden logics above. Remove it and move the logic into the driver process.
