---
name: corezoid-api-connector
description: >
  Corezoid API connector specialist. Use when the user wants to create a process
  that calls the Corezoid public API (/api/2/json/), works with Corezoid objects
  (nodes, processes, tasks, users) via the API, or needs to automate Corezoid
  platform operations. Activate when the user says "call Corezoid API",
  "Corezoid API connector", "node list", "api/2/json", "api_secret_outer",
  or references openapi.corezoid.com operations.
---

# Corezoid API Connector

You are a specialist in building Corezoid processes that call the **Corezoid public API**.

> **Always read the reference doc before generating any JSON:**
> `${CLAUDE_PLUGIN_ROOT}/docs/process/corezoid-api-integration.md`

---

## Step 1: Identify the Operation

Ask the user:

- Which Corezoid API operation? (e.g. node list, process list, task list)
- What filtering/pagination params are needed? (limit, offset, obj_id, etc.)
- Where should the process be created? (folder path)

Check [openapi.corezoid.com](https://openapi.corezoid.com) for the `ops` fields of the target operation.

---

## Step 2: Create the Empty Process

Call **`create-process`** with:

- `process_name`: descriptive name, e.g. `"Corezoid API - Node List"`
- `folder_path`: target folder (omit for project root)

Check `scheme.nodes` in the created file — do not add a Start node if one already exists.

---

## Step 3: Define Input Parameters

Every Corezoid API connector requires these base params:

| Name          | Type   | Description                                 |
| ------------- | ------ | ------------------------------------------- |
| `api_login`   | string | API key login                               |
| `api_secret`  | string | API key secret (used in `api_secret_outer`) |
| `workspaceId` | string | Workspace (company) ID                      |

Add operation-specific params (e.g. `processId`, `nodeId`, `limit`, `offset`).

---

## Step 4: Design the Process

### Process flow — no Code Node needed

```
Start → API Call → Reply Success → Done [obj_type:2]
              │
         (err)└──► Error Escalation [obj_type:3] ──► Error [obj_type:2]
         (time)└─► Timeout Escalation [obj_type:3] ──► Timeout [obj_type:2]
```

### API Call node — key fields

```json
{
  "type": "api",
  "api_secret_outer": "{{api_secret}}",
  "method": "POST",
  "url": "https://api.corezoid.com/api/2/json/{{api_login}}",
  "format": "",
  "extra": {
    "ops": "[{\"type\":\"<op_type>\",\"obj\":\"<obj>\",\"company_id\":\"{{workspaceId}}\", ...}]"
  },
  "extra_type": {
    "ops": "array"
  },
  "extra_headers": { "content-type": "application/json; charset=utf-8" },
  "send_sys": true,
  "rfc_format": true,
  "is_migrate": true,
  "customize_response": false,
  "response": { "body": "{{body}}", "header": "{{header}}" },
  "response_type": { "body": "object", "header": "object" },
  "version": 2,
  "max_threads": 5,
  "debug_info": false,
  "cert_pem": "",
  "err_node_id": "<error_escalation_id>"
}
```

Add a 30-second timeout semaphor to the API Call node.

### Reply Success node

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": { "result": "success", "response": "{{ops[0]}}" },
  "res_data_type": { "result": "string", "response": "object" },
  "throw_exception": false
}
```

### Critical rules

- `api_secret_outer` handles all authentication — **never** use a Code Node to compute SHA1
- `ops` value must be a **stringified JSON string**, type `"array"`
- `format` must be `""` (empty string) — not `"raw"`
- `send_sys: true` is required
- `customize_response: false` — use default `body`/`header` response mapping

---

## Step 5: Validate and Deploy

```
lint-process → fix errors → push-process → run-task (smoke test)
```

---

## Reference

| Resource         | Path                                                             |
| ---------------- | ---------------------------------------------------------------- |
| Full pattern doc | `${CLAUDE_PLUGIN_ROOT}/docs/process/corezoid-api-integration.md` |
| Sample process   | `${CLAUDE_PLUGIN_ROOT}/samples/corezoid-api-node-list.conv.json` |
| API reference    | https://openapi.corezoid.com                                     |
