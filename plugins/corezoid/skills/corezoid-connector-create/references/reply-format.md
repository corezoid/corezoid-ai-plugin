# Connector response format (Reply to Process)

Every outcome of a connector answers with ONE structure. Common fields are the same for all
connectors; success result fields are named after the endpoint and declared as output params.

| Field | When | Meaning |
|---|---|---|
| `result` | always | `ok` or `error` |
| `http_code` | when the API call happened | HTTP code of the Target API answer |
| result fields | `ok` | names and types from the contract: `customer`, `forecast`, `items` + `total`; no generic `data` |
| `error_type` | `error` | `validation` — input failed the checks; `api` — Target API returned an error; `timeout` — no answer in time; `connection` — API unreachable |
| `error_code` | `error`, if known | error code of the Target API itself (e.g. `card_declined`) |
| `error_description` | `error` | human-readable reason |
| `invalid_params` | `error_type = validation` | list of params that failed validation |

## Examples — `GET /v1/customers/{id}`

Success:

```json
{"result":"ok","http_code":200,"customer":{"id":"cus_123","email":"a@b.c"}}
```

API error:

```json
{"result":"error","error_type":"api","http_code":404,"error_code":"resource_missing","error_description":"No such customer"}
```

Validation error:

```json
{"result":"error","error_type":"validation","error_description":"customer_id is invalid","invalid_params":["customer_id"]}
```

## Where the values come from

| Field | Source in the task |
|---|---|
| `http_code` (success) | `{{__conveyor_api_debug__.http_res_code}}` — available because `debug_info: true`; verify the exact path on the first test and fix it here if it differs |
| result fields | the response body, e.g. `{{body}}` with `customize_response: true` and `response: {"body":"{{body}}","header":"{{header}}"}` |
| `http_code` (API error) | `{{__conveyor_api_return_code__}}` |
| `error_type` | which branch of the error Condition the task took (table below) |
| `error_code` | path from the contract (e.g. `{{body.error.code}}`) if the body is available; omit otherwise |
| `error_description` | Target API message from the body if available, else `{{__conveyor_api_return_description__}}` |
| `invalid_params` | array built by the validation Code node |

## Error mapping (API Call `err_node_id` → Condition)

| `__conveyor_api_return_type_tag__` | `error_type` |
|---|---|
| `api_bad_answer` (non-2xx) | `api` |
| `api_bad_answer_format`, `api_no_valid_json` | `api` |
| `api_connection_error` | `connection` |
| `api_timeout` | `timeout` |
| anything else (default branch) | `api` |

The time semaphore of the API Call routes to its own Reply with `error_type: timeout`.

## Reply node shape

Success Reply (`obj_type: 0`, reached via `go`):

```json
{
  "type": "api_rpc_reply",
  "mode": "key_value",
  "res_data": {
    "result": "ok",
    "http_code": "{{__conveyor_api_debug__.http_res_code}}",
    "customer": "{{body}}"
  },
  "res_data_type": {"result": "string", "http_code": "number", "customer": "object"},
  "throw_exception": false
}
```

Error Replies use `"throw_exception": false` as well — the caller reads `result` / `error_type`
instead of catching an exception. Keys of `res_data` and `res_data_type` must match exactly;
never put literal non-string values into `res_data` (lint flags it) — set them upstream and
reference as `{{var}}`.
