# Corezoid Public API Integration Pattern

Reference for building Corezoid processes that call the Corezoid public API (`/api/2/json/`).

## Key difference from external API connectors

Corezoid's own API uses `api_secret_outer` for HMAC authentication — **never** a Code Node for SHA1 signing. The platform handles signing automatically when `api_secret_outer` is set.

## Required input params

| Name | Type | Description |
|---|---|---|
| `api_login` | string | API key login (from workspace settings) |
| `api_secret` | string | API key secret (used in `api_secret_outer`) |
| `workspaceId` | string | Workspace (company) ID |

Add operation-specific params (`processId`, `nodeId`, `limit`, `offset`, etc.).

## Process flow

```
Start → API Call → Reply Success → Final [obj_type:2]
              │
         (err)└──► Error Escalation [obj_type:3] ──► Error [obj_type:2]
        (time)└──► Timeout Escalation [obj_type:3] ──► Timeout [obj_type:2]
```

No Code Node is needed — `api_secret_outer` handles all authentication.

## API Call node

```json
{
  "type": "api",
  "api_secret_outer": "{{api_secret}}",
  "method": "POST",
  "url": "https://api.corezoid.com/api/2/json/{{api_login}}",
  "format": "",
  "extra": {
    "ops": "[{\"type\":\"list\",\"obj\":\"node\",\"company_id\":\"{{workspaceId}}\",\"conv_id\":{{processId}},\"limit\":{{limit}},\"offset\":{{offset}}}]"
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
  "err_node_id": "<error_escalation_node_id>"
}
```

Add a 30-second timeout semaphor to the API Call node.

## Critical rules

- `api_secret_outer` handles all auth — never compute HMAC in a Code Node
- `ops` value must be a **stringified JSON string** with type `"array"`
- `format` must be `""` (empty string), not `"raw"`
- `send_sys: true` is required
- `customize_response: false` — use default `body`/`header` response mapping
- `rfc_format: true` is required

## Reply Success node

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": { "result": "success", "response": "{{ops[0]}}" },
  "res_data_type": { "result": "string", "response": "object" },
  "throw_exception": false
}
```

## Sample

See `samples/corezoid-api-node-list.conv.json` for a complete working example (Node List operation).

## Available `ops` types

Refer to [openapi.corezoid.com](https://openapi.corezoid.com) for the full list of operations and their field schemas. Common operations:

| `obj` | `type` | Description |
|---|---|---|
| `node` | `list` | List nodes in a process |
| `conv` | `list` | List processes in a folder |
| `task` | `list` | List tasks in a node |
| `company` | `list` | List workspaces |
