# Connector test rules

The test runs with `run-task` after `push-process`. A connector is ready only after a
successful **positive** test. Metrics are not checked at this stage.

## 1. Test data

- From the contract: required fields, formats, examples from the API documentation.
- Missing values — ask the user (never invent ids of real customers, accounts, payments).
- Unsafe methods (POST / PUT / PATCH / DELETE): ask the user to confirm the call, or run
  against the sandbox host variable (e.g. `<provider>-api-host-sandbox`). GET runs freely.

## 2. Positive test

`run-task` with valid data (it waits up to 30 s for the task to finish) and
`fields: ["result", "http_code", <every output param>, "data", "response"]` — an API
response easily runs past the 4 KB `run-task` prints by default, and without `fields`
large data comes back as keys only. Pass when ALL hold:

- [ ] the task reached the success final;
- [ ] `result = "ok"`;
- [ ] `http_code` equals the expected code (usually 200 / 201);
- [ ] every output param is present on the `Fields` line with the declared type;
- [ ] `data` and `response` are both listed under `MissingFields`.

## 3. Negative validation test (if the connector validates input)

`run-task` with deliberately invalid data and
`fields: ["result", "error_type", "invalid_params", "http_code"]`. Pass when:

- [ ] `result = "error"`, `error_type = "validation"`;
- [ ] `invalid_params` lists the broken params;
- [ ] the task did not pass through the API Call node (no `http_code` on the `Fields` line).

## 4. On failure

1. Show the node where the task stopped and the reply / task data.
2. Name the likely cause (wrong path, auth variable empty, response mapping, missing Reply).
3. Propose the fix, apply it after the user agrees, push, re-run the failed test.

A task that did not reach a final within the `run-task` wait counts as a failed test — show
which node it is waiting in.

## 5. Record the result

Keep for the registration step:

| Field | Value |
|---|---|
| `conv_id` | process id |
| `task_ref` | ref of the positive test task |
| `tested_at` | ISO time |
| `result` | `passed` / `failed` |
| `http_code` | observed |
