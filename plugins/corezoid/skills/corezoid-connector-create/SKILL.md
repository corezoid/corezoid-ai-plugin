---
name: corezoid-connector-create
description: >
  API connector builder. Use when the user wants a Corezoid process that calls ONE endpoint
  of ANY HTTP API — external (Stripe, OpenWeather, Google, banks, CRMs), internal
  (Simulator.Company, own services) or Corezoid itself — built from a text description, a
  documentation link, an OpenAPI/Swagger file, or a list of endpoints produced while
  reasoning about a task. Produces an atomic connector: input contract, validation when
  needed, a single API Call, Reply to Process on every outcome, unified response format,
  test, and Smart API registration. Activate on: "create a connector", "API connector",
  "integrate with <API>", "connect to <API>", "wrap this endpoint",
  "connector from this openapi.yaml", "сделай коннектор", "коннектор к API",
  "интеграция с", "подключи API", "обёртка над API", "зроби конектор", "конектор до API",
  "інтеграція з", "підключи API".
  NOT for processes with business logic or several calls — use /corezoid-create.
---

# Create an API Connector Process

You build **connector processes**: a Corezoid process that atomically calls **one endpoint**
(method + path) of a Target API and always answers its caller.

A connector is called by other processes through `api_rpc`, so it MUST reply on every path.
After a successful test it is registered as a Smart API Connector in Simulator.Company.

## Routing — check first

| The request is… | Use |
|---|---|
| the user explicitly called `/corezoid-api-connector` | that skill — do not intercept |
| a process with business logic, several calls, orchestration | `/corezoid-create` — stop here |
| one endpoint of any HTTP API, Corezoid API included (or several endpoints → several connectors) | **this skill** |
| an endpoint that needs files, git or a protocol the API Call node does not support | this skill, the call node is `git_call` (read `/corezoid-gitcall` first) |

## Hard rules (every connector, no exceptions)

1. **One process = one endpoint.** Several operations → several processes, one by one.
2. **Input contract in `params`.** Every business parameter is declared with type, `required`
   flag and regex where the format is known. Output fields are declared too (flag `output`).
3. **No secrets, hosts or keys in task data or hardcoded.** Host, API keys, tokens — stage
   variables only: `{{env_var[@<name>]}}` (create with `cz-variables`).
4. **Validation when the contract needs it** (required fields, formats, ranges) — before the
   API Call, with its own Reply. A Code node is allowed for validation.
5. **Exactly one call node**: `api`, or `git_call` only by the exception in Routing.
6. **`debug_info: true`** on the API Call node — the connector reads `__conveyor_api_debug__`
   (HTTP code, timings) to build its response.
7. **Reply to Process before EVERY final** — success, validation error, API error, connection
   error, timeout. A path that reaches a final without `api_rpc_reply` hangs the caller.
8. **Unified response format** — see `references/reply-format.md`. Result fields are named
   after the endpoint (`customer`, `forecast`, `items` + `total`), never a generic
   `data` / `response`.
9. **No Sum nodes, no metric sub-processes.** Metrics are collected outside the connector
   (HTTP-worker logs → Elasticsearch).
10. **A connector is ready only after a successful positive test** (Step 9).

## Workflow

```
1 Intake → 2 Contract → 3 Duplicate check → 4 Create process, params, variables
→ 5 Build nodes → 6 Layout → 7 Lint → 8 Push → 9 Test → 10 Register → 11 Report
```

Do the steps in order. Do not skip Contract confirmation, Test or Register.

---

## Step 1: Intake

Accept any of:

- a text description of the API / endpoint;
- a documentation link or an OpenAPI / Swagger URL — fetch and read it;
- an OpenAPI / Swagger / Postman file — read it;
- a list of connectors produced earlier in the conversation ("which connectors does this task need").

Output of the step — a numbered list of endpoints: `METHOD path — purpose`. Show it to the
user. If there are several, confirm which to build and build them **one at a time**
(Steps 2–11 per endpoint).

Also ask (if not obvious): target folder for the process, Corezoid stage (default: develop).

## Step 2: Contract

Fill the contract table from `references/contract-template.md` for the chosen endpoint:
endpoint, provider + base host, input, output, expected errors, auth, test data.

Rules:

- Take everything you can from the documentation; ask the user only for what is missing.
- Auth goes to variables, never to input params (e.g. `stripe-api-key` variable,
  header `Authorization: Bearer {{env_var[@stripe-api-key]}}`).
- Decide **"validation needed?"**: yes if there are required fields, formats (email, UUID,
  dates, enums) or ranges that the Target API would reject.
- **Show the contract to the user and get explicit confirmation** before building.

Keep the confirmed contract — Steps 9 and 10 use it.

## Step 3: Duplicate check

Search the exported `.conv.json` files of the project (run `pull-folder` if needed) for an
`api` node with the same method and the same host + path (after resolving variables).
If found — tell the user and offer: reuse it, update it (→ `/corezoid-edit`), or build a new
one anyway.

## Step 4: Create the process, params and variables

1. **Variables.** For host, keys and tokens: list existing ones (`cz-variables`
   `list-variables`), create missing ones (`create-variable`; secrets as secret variables).
   Naming: `<provider>-api-host`, `<provider>-api-key`.
2. **Process.** Call `create-process` with `process_name` = `<Provider>: <METHOD> <path>`
   (e.g. `Stripe: GET /v1/customers/{id}`) and the target `folder_path`. Check
   `scheme.nodes` of the created file — do not add a second Start.
3. **Description.** 1–2 sentences, starting with a verb: what the endpoint does and what it
   returns (see Description Update Rule in `corezoid/SKILL.md`).
4. **Params.** Declare input and output params in the full shape:

```json
{"name": "customer_id", "type": "string", "descr": "Stripe customer id",
 "flags": ["required", "input"], "regex": "^cus_[A-Za-z0-9]+$",
 "regex_error_text": "customer_id must look like cus_..."}
```

Output params: same shape with `"flags": ["output"]`, one per result field of the reply.

## Step 5: Build the nodes

Use the skeleton in `references/process-skeleton.md`. Main path:

```
Start
→ [Validate input]            (Code node, only if validation is needed)
→ [Check validation]          (Condition: invalid → Reply validation error)
→ [Prepare request]           (Code / Set Parameters, only if the body/query needs building)
→ API Call                    (single api node, debug_info: true, time semaphore)
→ Reply ok                    (result = ok + named result fields + http_code)
→ Final
```

Error paths (each with its own Reply and a named Error final):

| Failure | How it is caught | Reply |
|---|---|---|
| input invalid | Condition after validation | `error_type: validation` + `invalid_params` |
| Target API answered non-2xx | `err_node_id` → Condition on `__conveyor_api_return_type_tag__` = `api_bad_answer` (also `api_bad_answer_format`) | `error_type: api` |
| connection / DNS / TLS | same Condition, tag `api_connection_error` | `error_type: connection` |
| Target API call itself timed out (hardware) | same Condition, tag `api_timeout` — routes to the same Reply as the semaphore below | `error_type: timeout` |
| no answer within the process-level wait | time semaphore of the API Call (default 30 s) | `error_type: timeout` |
| anything else from the API Call | same Condition, default branch | `error_type: api` |

Node rules — follow the **Core rules** of `/corezoid-create` (Step 4 there), especially:
24-hex temporary node IDs; connect only via `go`; every `err_node_id` / semaphore target is
`obj_type: 3`; never mix an action logic with `go_if_const` in one node; error clusters
collapsed and named after the failure (`Customer Not Found Error`, not `Error`); a dedicated
error cluster per failing node (validation Code node, prepare node, API Call).

API Call node — author the **full canonical `api` logic** (see `docs/nodes/api-call-node.md`,
"Required node shape"); the reference shape is in `references/process-skeleton.md`.
`debug_info` MUST be `true`.

## Step 6: Layout

Leave coordinates at `x: 0, y: 0` while building, then call `layout-process`.

## Step 7: Lint

Call `lint-process` (with `profile: "connector"` once the plugin supports it). Fix every
error and re-run until clean. In addition, check by hand until the profile exists:

- [ ] exactly one `api` (or `git_call`) node;
- [ ] `debug_info: true` on it;
- [ ] every final is reachable only through an `api_rpc_reply`;
- [ ] no hardcoded `http://` / `https://` host in the URL, no keys in params;
- [ ] no `api_sum` nodes.

## Step 8: Push

Call `push-process`. Then `pull-process` to get canonical node IDs (needed for `nodeId` in
Step 10).

## Step 9: Test

Follow `references/test-rules.md`. In short:

1. Build test data from the contract (docs examples; ask the user for what is missing).
2. **Positive test** via `run-task` — expect `result = ok`, the expected `http_code`, result
   fields matching the output params by name and type.
3. **Negative validation test** (if validation exists) — invalid input → `error_type =
   validation` + `invalid_params`, no API call made.
4. Unsafe methods (POST / PUT / PATCH / DELETE) — only after the user confirms, or against a
   sandbox host variable.
5. On failure — show the node where the task stopped and the reply, propose a fix, re-test.
6. Record the test result: `conv_id`, task ref, time, result.

**No successful positive test → the connector is not ready. Do not register it.**

## Step 10: Register the Smart API Connector

Follow `references/registration.md`. In short:

1. Find the receiver in the current workspace by names: project short_name "smart-api" → its
   stage "production" → alias "api-gw-create-smart-api" → the process it points to. A missing
   project/stage/alias is an expected outcome, not an error — the user may lack access, or
   the resource may have been renamed/removed; don't guess which. Stop here and finish: the
   connector is ready, Smart API was not registered (not found). Never block.
2. `run-task` into the receiver process, exactly ONCE, with the task data in
   `references/registration.md`. Task ref = `conv_id` of the connector.
3. Report whatever comes back, verbatim, and stop there: a successful Reply → show the user
   the Smart API actor id; any error, rejection, timeout, or no answer → tell the user Smart
   API was not registered and why, quoting the receiver's own error. **Never** retry, guess a
   different payload and resend, or pull/inspect the receiver process or any of its
   sub-processes to "fix" a rejection — that is debugging someone else's production system,
   not this skill's job. One attempt, one honest report, done.

## Step 11: Report

Tell the user, briefly: process name and link, contract summary (method, path, inputs,
outputs), test result, Smart API id (or why it was not registered), variables created.

Then do the **Final Step: Update Git Context** exactly as in `/corezoid-create`.

---

## References

| Path | When |
|---|---|
| `references/contract-template.md` | Step 2 — contract table and examples |
| `references/process-skeleton.md` | Step 5 — node skeleton, API Call shape, error routing |
| `references/reply-format.md` | Steps 5, 9 — response format, error mapping |
| `references/test-rules.md` | Step 9 |
| `references/registration.md` | Step 10 |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-create/SKILL.md` | Core rules for nodes and error clusters |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | API Call fields, error tags, semaphores |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/reply-to-process-node.md` | Reply formats, stringification |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-with-parameters.md` | `params` shape |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variables |
| `${CLAUDE_PLUGIN_ROOT}/samples/api-post.json` | HTTP POST example (note: its `debug_info: false` and generic `response` reply do NOT meet the rules above) |
