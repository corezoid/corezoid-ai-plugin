# Connector contract

Fill this table for ONE endpoint, show it to the user and get explicit confirmation before
building. The confirmed contract is reused by the test (Step 9) and the registration (Step 10).

| Part | What to fill | Where it lands |
|---|---|---|
| Endpoint | `METHOD` + `path` (with path params, e.g. `/v1/customers/{id}`) | API Call node; Smart API actor (`method`, `path`) |
| Provider | provider name + base host | variable `<provider>-api-host`; Smart API Service (`providerName`, `host`) |
| Input | each business param: name, type, required, regex / format, description, where it goes (path / query / body / header) | process `params` (flag `input`) |
| Output | each result field of a successful answer: name (by meaning), type, source path in the response body | process `params` (flag `output`); success Reply |
| Errors | expected HTTP codes and Target API error codes worth naming (`resource_missing`, `card_declined`) | error Replies (`error_type`, `error_code`) |
| Auth | type (API key, Bearer, Basic, OAuth client credentials) + where it is sent (header / query) | variables only, never params |
| Validation needed? | yes / no + which rules (required, format, range, enum) | validation Code node + Condition |
| Timeout | seconds, default 30 | time semaphore of the API Call |
| Test data | valid example input; invalid example for the negative test; sandbox host if the method is unsafe | Step 9 |

## Example — Stripe `GET /v1/customers/{id}`

| Part | Value |
|---|---|
| Endpoint | `GET /v1/customers/{id}` |
| Provider | Stripe, host variable `stripe-api-host` = `https://api.stripe.com` |
| Input | `customer_id`: string, required, regex `^cus_[A-Za-z0-9]+$`, path param `{id}` |
| Output | `customer`: object — the whole response body |
| Errors | 404 → `error_code` from `body.error.code` (`resource_missing`); 401 → invalid key |
| Auth | Bearer, header `Authorization: Bearer {{env_var[@stripe-api-key]}}` (secret variable) |
| Validation needed? | yes: `customer_id` required and must match the regex |
| Timeout | 30 s |
| Test data | valid: an existing test customer id; invalid: `customer_id = "abc"` |

## Naming the output fields

- Name result fields by meaning: a single object → its entity name (`customer`, `invoice`,
  `forecast`); a list → plural + `total` when the API returns a count (`items`, `total`).
- Never `data`, `response`, `result_data`.
- Keep the Target API field names inside the object as they are — do not rename nested fields.
