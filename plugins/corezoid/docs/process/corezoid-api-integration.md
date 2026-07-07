# Corezoid API Integration — full pattern reference

Reference doc for the `corezoid-api-connector` skill. It describes, in detail, how
to build a Corezoid process that calls the **Corezoid public API**
(`/api/2/json/`) with a native **API Call node** — no Code node, no manual SHA1.

Read this before generating any process JSON.

---

## 1. When to use this pattern

Use it whenever a process must talk to the Corezoid platform API — list/modify
nodes, processes, tasks, users, folders; create tasks in another process; run any
`ops`-based operation documented at <https://openapi.corezoid.com>.

The whole call is one **API node**. Authentication, signing and the request body
are all expressed declaratively on that node — you never compute a signature in a
Code node.

## 2. Authentication — `api_secret_outer`

Corezoid signs API requests with `SHA1(time + secret + body + secret)`. The API
node does this for you when you set **`api_secret_outer`** to the API key secret:

- `url` ends with the API **login**: `https://api.corezoid.com/api/2/json/{{api_login}}`
- `api_secret_outer: "{{api_secret}}"` — the node computes the signature and adds
  the `time`/`digest` query itself.

Never reproduce the signature in a Code node — it is error-prone and unnecessary.
Keep `api_login` / `api_secret` as process parameters (or reference a stored
key), never hard-coded.

> **On-prem:** replace `api.corezoid.com` with your own API host (the value of `COREZOID_API_URL`).

## 3. Input parameters

Every connector needs these base params; add operation-specific ones as required:

| Name          | Type   | Description                                   |
| ------------- | ------ | --------------------------------------------- |
| `api_login`   | string | API key login (goes in the URL)               |
| `api_secret`  | string | API key secret (goes in `api_secret_outer`)   |
| `workspaceId` | string | Workspace (company) id, used inside `ops`     |

Operation-specific examples: `processId`, `nodeId`, `folderId`, `limit`, `offset`.

## 4. Process shape

```
Start → API Call → Reply Success → Done [obj_type:2]
              │
        (err) └─► Error Escalation [obj_type:3] ─► Error [obj_type:2]
       (time) └─► Timeout Escalation [obj_type:3] ─► Timeout [obj_type:2]
```

- **Start** — do not add a second Start if the created process already has one.
- **API Call** — the node below; carries a 30 s timeout semaphor.
- **Reply Success** — `api_rpc_reply` returning the API response to the caller.
- **Error / Timeout escalations** (`obj_type:3`) route failures to terminal
  Error / Timeout end nodes (`obj_type:2`).

## 5. API Call node — full canonical shape

Use the **full** api-node shape. A "light" node (missing `customize_response`,
`send_sys`, `max_threads`, `format`, `cert_pem`, `debug_info`, `response` /
`response_type`, `version`) makes push hang — always include every field.

```json
{
  "type": "api",
  "version": 2,
  "method": "POST",
  "url": "https://api.corezoid.com/api/2/json/{{api_login}}",
  "api_secret_outer": "{{api_secret}}",
  "format": "",
  "extra": {
    "ops": "[{\"type\":\"<op_type>\",\"obj\":\"<obj>\",\"company_id\":\"{{workspaceId}}\"}]"
  },
  "extra_type": { "ops": "array" },
  "extra_headers": { "content-type": "application/json; charset=utf-8" },
  "send_sys": true,
  "rfc_format": true,
  "is_migrate": true,
  "customize_response": false,
  "response": { "body": "{{body}}", "header": "{{header}}" },
  "response_type": { "body": "object", "header": "object" },
  "max_threads": 5,
  "debug_info": false,
  "cert_pem": "",
  "err_node_id": "<error_escalation_id>"
}
```

Field notes:

- **`format: ""`** — empty string, *not* `"raw"`. The body is built from `extra`.
- **`extra.ops`** — the operation list as a **stringified JSON string**, with
  `extra_type.ops: "array"`. This is the single most common mistake: `ops` must be
  a string, not an inline array.
- **`send_sys: true`** — required for the platform to accept the signed request.
- **`customize_response: false`** — use the default `body`/`header` mapping so the
  response lands in `{{body}}` (the `ops` result is then `{{body.ops[0]}}`).
- **`is_migrate`** is server-managed — set it, but push will not change it.
- **`err_node_id`** points at the Error Escalation node.

Add a **30-second timeout** semaphor on this node so a hung call escalates rather
than blocking the task.

## 6. Reply Success node

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": { "result": "success", "response": "{{body.ops[0]}}" },
  "res_data_type": { "result": "string", "response": "object" },
  "throw_exception": false
}
```

Return only what the caller needs — typically the first `ops` result object.

## 7. The `ops` payload

`ops` is Corezoid's uniform request envelope: an array of operation objects, each
selected by `type` + `obj`. Common read operations:

| Goal          | `ops[0]`                                                                 |
| ------------- | ------------------------------------------------------------------------ |
| List nodes    | `{"type":"list","obj":"node","obj_id":<procId>,"company_id":"{{workspaceId}}"}` |
| List processes| `{"type":"list","obj":"conv","company_id":"{{workspaceId}}"}`             |
| Show process  | `{"type":"show","obj":"conv","obj_id":<procId>,"company_id":"{{workspaceId}}"}` |
| Create task   | `{"type":"create","obj":"task","conv_id":<procId>,"ref":"<ref>","data":{}}` |

Encode the array as a JSON **string** in `extra.ops`; interpolate params with
`{{param}}` inside that string. Check <https://openapi.corezoid.com> for the exact
`ops` fields of the target operation.

## 8. Reading the response

With `customize_response: false`, the platform response is in `{{body}}`:

- `{{body.request_proc}}` — `"ok"` on success.
- `{{body.ops[0]}}` — the first operation's result (its shape depends on the op:
  e.g. `list` returns a `list` array, `show` returns the object).

Branch on `request_proc` in a condition node if you need to distinguish partial
failures inside a `200` response.

## 9. Validate and deploy

```
lint-process  →  fix errors  →  push-process  →  run-task (smoke test)
```

Keep lint clean before and after push. `push-process` regenerates node ids, so
re-pull after pushing if you keep a local `.conv.json`.

## 10. Checklist / gotchas

- [ ] `api_secret_outer` set — **no** Code node computing SHA1.
- [ ] `ops` is a **stringified** JSON string with `extra_type.ops: "array"`.
- [ ] `format` is `""` (empty), not `"raw"`.
- [ ] `send_sys: true`, `customize_response: false`, `version: 2`.
- [ ] Full api-node shape (all fields) — a light shape hangs on push.
- [ ] 30 s timeout semaphor + Error/Timeout escalations wired via `err_node_id`.
- [ ] `api_login` / `api_secret` are parameters, never hard-coded.

See also: `${CLAUDE_PLUGIN_ROOT}/samples/corezoid-api-node-list.conv.json` (a
runnable node-list example) and <https://openapi.corezoid.com> for every operation.
