# Node JSON Structures Reference

Complete JSON structures for all node types used when modifying Corezoid processes.

---

## Node ID Rules

- All node IDs must be **24-character hexadecimal strings**: `^[0-9a-f]{24}$`
- Must be **unique** within the process
- Use descriptive names only in custom string IDs (when the process doesn't enforce hex format)

---

## Start Node (`obj_type: 1`)

```json
{
  "id": "507f1f77bcf86cd799439011",
  "obj_type": 1,
  "condition": {
    "logics": [ 
      {
        "type": "go",
        "to_node_id": "<next_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Start",
  "description": "",
  "x": 600,
  "y": 100,
  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}",
  "options": null
}
```

---

## Code Node (`obj_type: 0`, `type: "api_code"`)

```json
{
  "id": "507f1f77bcf86cd799439012",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_code",
        "lang": "js",
        "src": "data.result = data.input * 2;",
        "err_node_id": "<error_node_id>"
      },
      {
        "type": "go",
        "to_node_id": "<next_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Transform Data",
  "description": "Describe what this code does",
  "x": 500,
  "y": 300,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- `lang` must be `"js"` (JavaScript) or `"erl"` (Erlang)
- `err_node_id` is **required** — every Code node must have its own dedicated error node
- Modify the `data` object directly: `data.myParam = value;`
- Use `try/catch` inside `src` for internal error handling
- `console.log()` has no effect — use `data._ = []; data._.push("log")` for debugging

---

## API Call Node (`obj_type: 0`, `type: "api"`)

```json
{
  "id": "507f1f77bcf86cd799439018",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api",
        "is_migrate": true,
        "rfc_format": true,
        "format": "",
        "content_type": "application/json",
        "method": "POST",
        "url": "{{env_var[@api-url]}}/endpoint",
        "extra": {
          "param1": "{{input_value}}",
          "param2": "42"
        },
        "extra_type": {
          "param1": "string",
          "param2": "number"
        },
        "extra_headers": {},
        "cert_pem": "",
        "max_threads": 5,
        "send_sys": true,
        "debug_info": false,
        "err_node_id": "<error_node_id>",
        "customize_response": true,
        "response": {
          "header": "{{resp_header}}",
          "body": "{{resp_body}}"
        },
        "response_type": {
          "header": "object",
          "body": "object"
        },
        "version": 2
      },
      {
        "type": "go",
        "to_node_id": "<next_node_id>"
      }
    ],
    "semaphors": [
      {
        "type": "time",
        "value": 30,
        "dimension": "sec",
        "to_node_id": "<timeout_error_node_id>"
      }
    ]
  },
  "title": "Call External API",
  "description": "Sends POST request to external API with prepared parameters",
  "x": 500,
  "y": 300,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- All fields are required — do not omit any field shown above
- `err_node_id` is **required**
- The `semaphors` timeout is **mandatory** — without it, tasks hang forever if the API is unresponsive
- **Response variable gotcha:** the body is stored under the **second key** in `response`, not under `body`
  - With `"body": "{{resp_body}}"` → access as `{{resp_body.field}}` (not `{{body.field}}`)
  - With `"data": "{{resp_data}}"` → access as `{{resp_data.field}}`
- POST body fields go in `extra` / `extra_type`
- For GET requests: set `"method": "GET"` and use empty `"extra": {}, "extra_type": {}`
- Never hardcode URLs, tokens, or IDs — use `{{env_var[@name]}}`

---

## Set Parameters Node (`obj_type: 0`, `type: "set_param"`)

```json
{
  "id": "507f1f77bcf86cd799439019",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "set_param",
        "extra": {
          "output_var": "{{response.field}}",
          "full_name": "{{first_name}}_{{last_name}}",
          "total": "$.math({{price}}*{{qty}})",
          "timestamp": "$.date()"
        },
        "extra_type": {
          "output_var": "string",
          "full_name": "string",
          "total": "number",
          "timestamp": "string"
        },
        "err_node_id": "<error_node_id>"
      },
      {
        "type": "go",
        "to_node_id": "<next_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Prepare Parameters",
  "description": "Extracts and transforms fields from the API response",
  "x": 500,
  "y": 300,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- Prefer `set_param` over `api_code` (Code node) for simple transformations — it is native and has zero overhead
- `err_node_id` is **required**
- Built-in functions:
  - `$.math({{a}}+{{b}})` — arithmetic (exactly 2 operands; nest for more complex expressions)
  - `$.date()` — current Unix timestamp
  - `$.random()` — random value
  - `$.md5_hex()`, `$.sha256_hex()` — hashing
- String concatenation: `"full": "{{first}}_{{last}}"`
- Dynamic key access: `"value": "{{response.{{currency_code}}}}"`
- Use `api_code` (Code node) only when you need: array indexing, `.length`, regex, JSON parsing, complex conditionals

---

## Async Call (Fire-and-Forget) Node (`obj_type: 0`, `type: "api_copy"`)

```json
{
  "id": "507f1f77bcf86cd799439020",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_copy",
        "conv_id": "@process-alias",
        "ref": "{{unique_ref}}",
        "mode": "create",
        "is_sync": false,
        "data": {
          "field1": "{{value1}}",
          "field2": "{{value2}}"
        },
        "data_type": {
          "field1": "string",
          "field2": "string"
        },
        "err_node_id": "<error_node_id>"
      },
      {
        "type": "go",
        "to_node_id": "<next_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Notify Analytics",
  "description": "Fire-and-forget: sends event to analytics process without waiting for result",
  "x": 500,
  "y": 500,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- Does NOT wait for the target process to finish — continues immediately after sending
- Use for async fan-out, logging, notifications, background processing
- Use `api_rpc` instead when you need the output from the called process
- `conv_id`: use `@alias` (preferred) or numeric process ID
- `data` / `data_type` instead of `extra` / `extra_type` (unlike `api_rpc`)
- `err_node_id` is **required**

---

## Condition Node (`obj_type: 0`, `type: "go_if_const"`)

```json
{
  "id": "507f1f77bcf86cd799439021",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "go_if_const",
        "to_node_id": "<success_node_id>",
        "conditions": [
          {"param": "status", "const": "active", "fun": "eq",  "cast": "string"},
          {"param": "age",    "const": "18",     "fun": "ge",  "cast": "number"}
        ]
      },
      {
        "type": "go_if_const",
        "to_node_id": "<pending_node_id>",
        "conditions": [
          {"param": "status", "const": "pending", "fun": "eq", "cast": "string"}
        ]
      },
      {
        "type": "go",
        "to_node_id": "<default_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Check Status",
  "description": "Routes task based on status and age: active+adult → success, pending → pending handler, else → default",
  "x": 500,
  "y": 300,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- Multiple `conditions` items within one `go_if_const` = **AND** logic (all must match)
- Multiple `go_if_const` entries in a node = **OR / if-else-if** (first match wins)
- Must end with a fallback `go` as the last logic entry
- `fun` values: `eq`, `ne`, `not_eq`, `gt`, `lt`, `ge`, `le`, `regex`
- `cast` values: `"string"`, `"number"`, `"boolean"`
- `param`, `const`, `fun`, `cast` are on `conditions` items, NOT on the `go_if_const` object

---

## Call a Process Node (`obj_type: 0`, `type: "api_rpc"`)

```json
{
  "id": "507f1f77bcf86cd799439013",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_rpc",
        "conv_id": 9876543,
        "err_node_id": "<error_node_id>",
        "extra": {
          "param1": "value1",
          "param2": "42",
          "param3": "{\"name\":\"John\",\"age\":30}"
        },
        "extra_type": {
          "param1": "string",
          "param2": "number",
          "param3": "object"
        },
        "group": "all"
      },
      {
        "type": "go",
        "to_node_id": "<next_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Call Payment Process",
  "description": "Calls the payment validation process with customer_id and amount",
  "x": 500,
  "y": 500,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- `type` must be `"api_rpc"` — NOT `"call_process"`
- `conv_id`: use `@alias` (preferred) or numeric process ID (can also be `"{{param}}"` for dynamic)
- `extra` and `extra_type` are **required** (use `{}` if no params needed)
- Keys in `extra` and `extra_type` must **match exactly**
- Object values in `extra` must be stringified: `"{\"key\":\"value\"}"`
- `group: "all"` — waits for the called process to reply
- `err_node_id` is **required**

---

## Reply to Process Node (`obj_type: 0`, `type: "api_rpc_reply"`)

### Success reply

```json
{
  "id": "507f1f77bcf86cd799439014",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_rpc_reply",
        "mode": "key_value",
        "res_data": {
          "result": "success",
          "payload": "{{response_data}}"
        },
        "res_data_type": {
          "result": "string",
          "payload": "object"
        },
        "throw_exception": false
      },
      {
        "type": "go",
        "to_node_id": "<final_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Reply Success",
  "description": "Returns successful result to calling process",
  "x": 500,
  "y": 700,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

### Error reply

```json
{
  "id": "507f1f77bcf86cd799439015",
  "obj_type": 0,
  "condition": {
    "logics": [
      {
        "type": "api_rpc_reply",
        "mode": "key_value",
        "res_data": {
          "result": "error",
          "error_message": "{{error_message}}"
        },
        "res_data_type": {
          "result": "string",
          "error_message": "string"
        },
        "throw_exception": true,
        "exception_reason": "Describe why this error is thrown"
      },
      {
        "type": "go",
        "to_node_id": "<error_end_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Reply Error",
  "description": "Returns error to calling process",
  "x": 800,
  "y": 700,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- `mode` must be `"key_value"` (object) or `"keys"` (array) — must be consistent with `res_data` format
- `res_data` and `res_data_type` keys must **match exactly**
- `throw_exception: true` routes the caller to its `err_node_id` path
- `exception_reason` — include on error replies to describe the failure cause
- Object values must be properly stringified with escaped quotes

---

## Escalation Node (`obj_type: 3`)

Escalation nodes handle errors that require active logic: replying to the calling process,
conditional retry routing, or performing any action before terminating.

```json
{
  "id": "507f1f77bcf86cd799439022",
  "obj_type": 3,
  "condition": {
    "logics": [
      {
        "type": "api_rpc_reply",
        "mode": "key_value",
        "res_data": {
          "description": "{{__conveyor_api_return_description__}}"
        },
        "res_data_type": {
          "description": "string"
        },
        "throw_exception": true,
        "exception_reason": "API call failed or timed out"
      },
      {
        "type": "go",
        "to_node_id": "<error_final_node_id>"
      }
    ],
    "semaphors": []
  },
  "title": "Error Handler",
  "description": "Returns error reply to calling process when API call fails",
  "x": 800,
  "y": 300,
  "extra": "{\"modeForm\":\"expand\",\"icon\":\"\"}",
  "options": null
}
```

**Rules:**
- `obj_type: 3` marks this node as an escalation node
- Must always end with `go` routing to a Final Error node (`obj_type: 2`)
- `{{__conveyor_api_return_description__}}` is the system variable that captures the error message from the failed node
- API Call nodes also need a separate timeout escalation node via `semaphors`

**When to use an escalation node vs. a direct final node:**

Use an escalation node (`obj_type: 3`) only when the error path needs actual logic:
- Replying to the calling process (`api_rpc_reply` with `throw_exception: true`)
- Conditional routing based on error type (`go_if_const` on `__conveyor_*_return_type_tag__`)
- Any other action before terminating

Wire `err_node_id` **directly to a Final Error node** (`obj_type: 2`) when the error path
simply needs to terminate with no additional logic:

```
[Functional Node] --err_node_id--> [Final Error obj_type:2]      ✓ no logic needed
[Functional Node] --err_node_id--> [Escalation obj_type:3]       ✓ reply / routing needed
                                          └──go──> [Final Error obj_type:2]
[API Call Node]   --semaphor-----> [Timeout Escalation obj_type:3] --go--> [Timeout Final obj_type:2]
```

**Anti-pattern — passthrough escalation (flagged by `lint-process`):**

An escalation node with only a bare `go` and no action logic is pointless clutter.
Replace it with a direct `err_node_id` pointer to the final node:

```json
// ✗ wrong — empty escalation node, no logic inside
{
  "id": "...",
  "obj_type": 3,
  "condition": {
    "logics": [{"type": "go", "to_node_id": "<final_error_id>"}],
    "semaphors": []
  }
}

// ✓ correct — err_node_id points straight at the final error node
"err_node_id": "<final_error_id>"
```

---

## End Node / Final Node (`obj_type: 2`)

### Success end

```json
{
  "id": "507f1f77bcf86cd799439016",
  "obj_type": 2,
  "condition": {
    "logics": [],
    "semaphors": []
  },
  "title": "Process Completed",
  "description": "",
  "x": 600,
  "y": 900,
  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"success\"}",
  "options": "{\"save_task\":true}"
}
```

### Error end

```json
{
  "id": "507f1f77bcf86cd799439017",
  "obj_type": 2,
  "condition": {
    "logics": [],
    "semaphors": []
  },
  "title": "Process Failed",
  "description": "",
  "x": 800,
  "y": 900,
  "extra": "{\"modeForm\":\"collapse\",\"icon\":\"error\"}",
  "options": "{\"save_task\":true}"
}
```

---

## extra / extra_type Rules

These rules apply to **all** nodes that use `extra`/`extra_type` or `res_data`/`res_data_type`:

| Value type | `extra` value | `extra_type` value |
|------------|---------------|--------------------|
| String | `"hello"` | `"string"` |
| Number | `"42"` | `"number"` |
| Boolean | `"true"` | `"boolean"` |
| Object | `"{\"key\":\"val\"}"` | `"object"` |
| Array | `"[1,2,3]"` | `"array"` |
| Dynamic ref | `"{{param_name}}"` | `"string"` |
| Dynamic object | `"{\"id\":\"{{user_id}}\"}"` | `"object"` |

**Critical:** Object and array values must always be stringified JSON with escaped quotes. Never use raw JSON objects as values in `extra`.

---

## Node Positioning Guidelines

Nodes are arranged in vertical lanes:

| Lane | x position | Contents |
|------|-----------|---------|
| Main flow (happy path) | x ≈ 1000 | Start, logic nodes, Reply Success, Final Success |
| Timeout handlers | x ≈ 740–800 | Timeout Escalation, Timeout Final |
| Error handlers | x ≈ 1150–1264 | Error Escalation, Error Final |

- Vertical step: 160–200px between nodes in the same lane
- Start and Final nodes: `x = center_of_lane + 100` (offset due to center pivot point)
- Expand logic nodes (`modeForm: "expand"`), collapse Start and Final (`modeForm: "collapse"`)
- No overlapping nodes — each needs its own `y` position

Example coordinates for a typical process with API call:

```
Start:                  x=1100, y=100
Code/Prepare node:      x=1000, y=300
API Call node:          x=1000, y=500  → err_node_id → Error Escalation
Reply Success:          x=1000, y=700
Final (success):        x=1100, y=900

Timeout Escalation:     x=800,  y=500  (semaphor target of API Call)
Timeout Final:          x=800,  y=700

Error Escalation:       x=1264, y=500  (err_node_id target of API Call)
Error Final:            x=1264, y=700
```

For simpler processes without timeout handling:

```
Start:              x=600,  y=100
Code/Logic node:    x=500,  y=300
Call Process node:  x=500,  y=500
Reply Success:      x=500,  y=700
Final (success):    x=600,  y=900
Error node:         x=800,  y=300  (right of its parent)
Reply Error:        x=800,  y=500
Final (error):      x=900,  y=700
```