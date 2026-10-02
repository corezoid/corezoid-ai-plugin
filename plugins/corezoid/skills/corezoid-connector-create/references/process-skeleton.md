# Connector process skeleton

## Node map

| # | Node | obj_type | Logic | Notes |
|---|---|---|---|---|
| 1 | Start | 1 | `go` | |
| 2 | Validate Input | 0 | `api_code` | only if validation is needed; builds `invalid_params` array and `is_valid` |
| 3 | Input Valid? | 0 | `go_if_const` on `is_valid` | false → node 10 |
| 4 | Prepare Request | 0 | `api_code` or `set_param` | only if body / query must be built |
| 5 | Call `<Provider> <METHOD> <path>` | 0 | `api` | single call node; `err_node_id` → 11; time semaphore → 15 |
| 6 | Reply OK | 0 | `api_rpc_reply` | `result: ok`, `http_code`, named result fields |
| 7 | Final | 2 | — | |
| 10 | Reply Validation Error | 0 | `api_rpc_reply` | collapsed; `error_type: validation`, `invalid_params` → 10a Error final `Invalid Input` |
| 11 | API Error Type? | 3 | `go_if_const` on `__conveyor_api_return_type_tag__` | collapsed; `api_bad_answer`/`api_bad_answer_format` → 12, `api_connection_error` → 13, `api_timeout` → 15, default → 12 |
| 12 | Reply API Error | 0 | `api_rpc_reply` | collapsed; `error_type: api` → Error final `<Provider> API Error` |
| 13 | Reply Connection Error | 0 | `api_rpc_reply` | collapsed; `error_type: connection` → Error final `<Provider> Unreachable` |
| 15 | Reply Timeout | 3 | `api_rpc_reply` | collapsed; target of both the time semaphore AND node 11's `api_timeout` branch; `error_type: timeout` → Error final `<Provider> Timeout` |

Code nodes 2 and 4 each get their own error cluster too: `err_node_id` → Reply (`obj_type: 3`,
`error_type: validation` for node 2, `api` for node 4) → named Error final.

Every final is reached only through a Reply. Every `err_node_id` / semaphore target is
`obj_type: 3`. Never mix an action logic with `go_if_const` in one node.

## API Call node — full canonical shape

```json
{
  "type": "api",
  "is_migrate": true,
  "rfc_format": true,
  "format": "",
  "content_type": "application/json",
  "method": "GET",
  "url": "{{env_var[@stripe-api-host]}}/v1/customers/{{customer_id}}",
  "extra": {},
  "extra_type": {},
  "extra_headers": {
    "authorization": "Bearer {{env_var[@stripe-api-key]}}",
    "content-type": "application/json; charset=utf-8"
  },
  "cert_pem": "",
  "max_threads": 5,
  "send_sys": false,
  "debug_info": true,
  "err_node_id": "<id of node 11>",
  "customize_response": true,
  "response": {"body": "{{body}}", "header": "{{header}}"},
  "response_type": {"body": "object", "header": "object"},
  "version": 2
}
```

Time semaphore on the same node:

```json
"semaphors": [
  {"type": "time", "value": 30, "dimension": "sec", "to_node_id": "<id of node 15>"}
]
```

For a JSON body: `"format": "raw"`, `"raw_body": "{{request_body}}"` built in node 4, or
`extra` / `extra_type` key-value pairs (keys must match exactly; values of `extra` are strings).

## Validation Code node — pattern

```js
var invalid = [];
if (!data.customer_id) invalid.push("customer_id");
else if (!/^cus_[A-Za-z0-9]+$/.test(data.customer_id)) invalid.push("customer_id");
data.invalid_params = invalid;
data.is_valid = invalid.length === 0 ? "true" : "false";
data.validation_description = invalid.length ? ("Invalid params: " + invalid.join(", ")) : "";
```

`params` regex and the `required` flag are the first line of defence; the Code node covers
what params cannot express (cross-field rules, ranges, enums) and builds `invalid_params`.

## Collapsed error-cluster nodes

`"extra": "{\"modeForm\":\"collapse\",\"icon\":\"\"}"` for Reply / Condition nodes,
`"extra": "{\"modeForm\":\"collapse\",\"icon\":\"error\"}"` for Error finals. Name every Error
final after the specific failure.
