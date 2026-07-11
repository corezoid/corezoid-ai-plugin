---
name: corezoid-variable-manager
description: >
  Manages Corezoid environment variables (env_var) — create, list, modify, delete, and
  use variables in process JSON. Activate when the user mentions "variable", "env var",
  "environment variable", "secret", "create variable", "list variables", "delete variable",
  "modify variable", "env_var", "{{env_var", or asks how to store a URL, token, API key,
  or any constant that should not be hardcoded in a process. Also activate when a process
  references {{env_var[@name]}} and the variable does not exist yet.
---

# Corezoid Variable Manager

## What variables are

Environment variables store constants (URLs, tokens, API keys, IDs, configuration values)
that must not be hardcoded in process logic. The reference syntax `{{env_var[@name]}}` is
resolved at runtime — changing a variable value takes effect immediately without
redeploying any process.

Variables are **stage-scoped**: shared across all processes within a stage.

---

## Variable types

### By data type

| `data_type` | When to use | Value format |
|-------------|-------------|--------------|
| `raw` | Plain string (URL, token, ID, any scalar) | `"https://api.example.com"` |
| `json` | Structured config, multi-field config, feature flags | `{"key":"value","nested":{...}}` |

### By visibility

| `env_var_type` | UI display | Accessible from | Scopes |
|----------------|------------|-----------------|--------|
| `visible` | Value shown in plain text | All node types | `[{"type":"*","fields":"*"}]` |
| `secret` | Value masked, shows only fingerprint | API Call nodes only | `[{"type":"api_call","fields":"*"}]` |

> ⚠️ **Secret variables** are designed for tokens, passwords, and API keys. They are
> never returned in plain text by the API after creation — only an MD5/SHA256 fingerprint
> is available. Use `visible` for non-sensitive configuration.

---

## MCP Tools

| Tool | Purpose |
|------|---------|
| `create-variable` | Create a `raw` + `visible` variable in one step |
| `list-variables` | List a stage's variables with obj_id, types, values (secrets masked) |
| `modify-variable` | Change value/title/data_type or rename — dry-run + confirm-gated |
| `delete-variable` | PERMANENTLY delete (no recycle bin) — dry-run + confirm-gated |

> **Note:** creating `secret` or `json` variables is not yet exposed as an MCP tool —
> use the direct API calls documented below for creation; manage them afterwards with
> the tools above.

## Double-confirmation etiquette (modify / delete)

`modify-variable` and `delete-variable` are consequential: a deleted variable is gone
FOREVER (env vars have NO recycle bin), a renamed one breaks every
`{{env_var[@old-name]}}` reference, and a changed value takes effect immediately in
running processes. The tools enforce a two-step gate, and you must drive it honestly:

1. Call the tool WITHOUT `apply` — you get a dry-run: a current → new diff (modify) or
   a red `🔴 PERMANENT DELETION` block (delete), including a local reference scan.
2. Show that dry-run output to the user **verbatim** — do not summarize away the
   warnings, especially the red block and the list of files that still reference the
   variable.
3. Ask the user explicitly whether to proceed, and wait for their clear agreement in
   the conversation.
4. Only then re-run with `apply=true` and the exact `confirm="<short_name>#<obj_id>"`
   from the dry-run output. Never fabricate the confirm string without steps 1–3, and
   never treat an earlier, unrelated "yes" as agreement for this action.

Server facts the tools rely on (verified live): modify is PARTIAL — omitted fields
keep their value, so modifying a secret's title does not require (or touch) its value;
`env_var_type` (visible/secret) can NOT be changed after creation — the server
silently ignores such attempts; delete requires project_id + stage_id and is
irreversible.

---

## Using variables in process JSON

Once a variable exists, reference it with `{{env_var[@short-name]}}` anywhere a value
is expected.

### API Call node — URL field
```json
{
  "type": "api",
  "url": "{{env_var[@payment-api-url]}}/charge",
  "method": "POST",
  "err_node_id": "<error_node_id>"
}
```

### API Call node — header field
```json
{
  "type": "api",
  "url": "{{env_var[@payment-api-url]}}/charge",
  "method": "POST",
  "extra": { "Authorization": "Bearer {{env_var[@payment-api-token]}}" },
  "extra_type": { "Authorization": "string" },
  "err_node_id": "<error_node_id>"
}
```

### Set Parameters node
```json
{
  "type": "set_param",
  "extra": {
    "baseUrl": "{{env_var[@service-url]}}",
    "token":   "{{env_var[@service-token]}}"
  },
  "extra_type": {
    "baseUrl": "string",
    "token":   "string"
  },
  "err_node_id": "<error_node_id>"
}
```

### Call a Process node — passing variable as parameter
```json
{
  "type": "api_rpc",
  "conv_id": "@target-process",
  "extra": { "endpoint": "{{env_var[@service-endpoint]}}" },
  "extra_type": { "endpoint": "string" },
  "err_node_id": "<error_node_id>"
}
```

### Condition node (`go_if_const`)

Variable references work in condition expressions as both the left-hand value and the
comparison value:

```json
{
  "type": "go_if_const",
  "conditions": [
    {
      "fun": "equal",
      "arg": "{{env_var[@feature-flag]}}",
      "val": "enabled"
    }
  ],
  "to_node_id": "<next_node_id>"
}
```

### Code node — variables must be pre-loaded via set_param
Variables are not directly accessible inside `api_code` JavaScript. First assign them
to task fields using a `set_param` node upstream, then read via `data.*` in code:
```javascript
// In set_param upstream: "apiUrl": "{{env_var[@my-api-url]}}"
var url = data.apiUrl + "/endpoint";
```

---

## Naming rules

- Only lowercase letters `[a-z]`, digits `[0-9]`, and hyphens `-`
- Name and description must be **at least 3 characters**
- Must be unique within the stage
- Good: `stripe-secret-key`, `payment-api-url`, `db-host-prod`
- Bad: `URL`, `TOKEN`, `x`, `My_Var`

---

## Local cache files

Two files store variable information locally. Check **both** before creating a new variable:

| File | Created by | Contains |
|------|------------|---------|
| `_ENV_VARS_.json` | `pull-folder` (ZIP export from Corezoid) | All variables in the stage |
| `.processes/variables.json` | MCP `create-variable` tool | Only variables created in this session |

If neither file exists, run `pull-folder` or call the list API (see below) to get the
current state.

---

## Workflow: Create a visible raw variable (MCP tool)

### Step 1 — Check if variable already exists

Read `_ENV_VARS_.json` (or `.processes/variables.json`) and search for the `short_name`.
If found, reuse it — do not create a duplicate.

### Step 2 — Create the variable

Call MCP tool **`create-variable`** with:
- `stage_id`: value of `COREZOID_STAGE_ID` from `.env`
- `name`: the `short_name` (kebab-case, e.g. `stripe-api-key`)
- `description`: human-readable label (min 3 chars), used as `title` in the API
- `value`: the actual value

```
create-variable(
  stage_id="671255",
  name="payment-api-url",
  description="Payment Service Base URL",
  value="https://api.payments.example.com"
)
```

The tool creates the variable in Corezoid and appends it to `.processes/variables.json`.

### Step 3 — Reference in process JSON

Use `{{env_var[@payment-api-url]}}` wherever this value is needed.

---

## Workflow: Create a secret variable (direct API)

Use when storing tokens, passwords, API keys — values that must be masked in the UI.

```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type":         "create",
    "obj":          "env_var",
    "obj_type":     0,
    "status":       "active",
    "data_type":    "raw",
    "env_var_type": "secret",
    "title":        "Stripe Secret Key",
    "short_name":   "stripe-secret-key",
    "description":  "",
    "value":        "sk_live_...",
    "company_id":   "<WORKSPACE_ID>",
    "project_id":   <PROJECT_ID>,
    "stage_id":     <STAGE_ID>,
    "scopes":       [{"type": "api_call", "fields": "*"}]
  }]
}
```

Response: `{ "obj_id": 2192, "proc": "ok", "fingerprints": [...] }`

> The value is never returned after creation. Store it securely before calling this API.

---

## Workflow: Create a JSON variable (direct API)

Use when a variable holds a structured config object or array.

```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type":         "create",
    "obj":          "env_var",
    "obj_type":     0,
    "status":       "active",
    "data_type":    "json",
    "env_var_type": "visible",
    "title":        "Service Config",
    "short_name":   "service-config",
    "description":  "",
    "value":        "{\"host\":\"db.example.com\",\"port\":5432,\"name\":\"prod\"}",
    "company_id":   "<WORKSPACE_ID>",
    "project_id":   <PROJECT_ID>,
    "stage_id":     <STAGE_ID>,
    "scopes":       [{"type": "*", "fields": "*"}]
  }]
}
```

> The `value` field must be a **JSON string** (the JSON content encoded as a string).
> A secret JSON variable uses `"env_var_type": "secret"` and
> `"scopes": [{"type": "api_call", "fields": "*"}]`.

---

## Workflow: List variables (direct API)

```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type":       "list",
    "obj":        "env_var",
    "sort":       "date",
    "order":      "asc",
    "id":         "<WORKSPACE_ID>",
    "company_id": "<WORKSPACE_ID>",
    "project_id": <PROJECT_ID>,
    "stage_id":   <STAGE_ID>
  }]
}
```

**Response fields per variable:**

| Field | Description |
|-------|-------------|
| `obj_id` | Numeric ID (needed for modify/delete) |
| `short_name` | The `@name` used in `{{env_var[@name]}}` |
| `title` | Human-readable display label |
| `data_type` | `raw` or `json` |
| `env_var_type` | `visible` or `secret` |
| `value` | Actual value (empty for `secret` after creation) |
| `fingerprints` | MD5 + SHA256 hashes — use to detect value changes |
| `scopes` | Access scope rules |
| `create_time` / `change_time` | Unix timestamps |
| `uuid` | Variable UUID |

---

## Workflow: Modify a variable (direct API)

Modify updates all mutable fields in one call. Always send the full payload — partial
updates are not supported.

```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type":         "modify",
    "obj":          "env_var",
    "obj_id":       <VAR_OBJ_ID>,
    "data_type":    "raw",
    "env_var_type": "visible",
    "title":        "Updated Display Title",
    "short_name":   "new-short-name",
    "description":  "",
    "value":        "new-value",
    "company_id":   "<WORKSPACE_ID>",
    "project_id":   <PROJECT_ID>,
    "stage_id":     <STAGE_ID>,
    "scopes":       [{"type": "*", "fields": "*"}]
  }]
}
```

> ⚠️ Changing `short_name` invalidates all `{{env_var[@old-name]}}` references across
> every process in the stage. After renaming, grep all `.conv.json` files for the old
> name and update them, then `push-process` each affected file.

---

## Workflow: Delete a variable (direct API)

> ⚠️ Before deleting, verify no process references `{{env_var[@short-name]}}`.
> `push-process` validates env_var references and will fail if the variable is missing.

```bash
# Check which processes reference this variable
grep -r "env_var\[@variable-name\]" . --include="*.conv.json"
```

```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type":       "delete",
    "obj":        "env_var",
    "obj_id":     <VAR_OBJ_ID>,
    "company_id": "<WORKSPACE_ID>",
    "project_id": <PROJECT_ID>,
    "stage_id":   <STAGE_ID>
  }]
}
```

---

## Resolving environment values

| Value | Where to find it |
|-------|-----------------|
| `WORKSPACE_ID` (company_id) | `.env` — `WORKSPACE_ID=...` |
| `COREZOID_STAGE_ID` (stage_id) | `.env` — `COREZOID_STAGE_ID=...` |
| `project_id` | `*.stage.json` in project root — field `project_id` |
| `COREZOID_API_URL` | `.env` — `COREZOID_API_URL=...` |
| `ACCESS_TOKEN` | `~/.corezoid/credentials` |
| `obj_id` of variable | List API response, or `_ENV_VARS_.json` |

If `.env` is missing, run the `corezoid-init` skill.

---

## Runtime behaviour

- Variables are resolved **before** the node executes — the `{{env_var[@name]}}` token
  is replaced with the live value at the moment the task reaches that node
- Updating a variable value takes effect **immediately** — no process redeploy needed
- `push-process` validates all `{{env_var[@name]}}` references: if the variable does not
  exist in the stage, deployment fails with an error

---

## Common pitfalls

| Mistake | Correct approach |
|---------|-----------------|
| `{{env_var[payment-url]}}` — missing `@` | `{{env_var[@payment-url]}}` — `@` is required |
| `{{env_var[@Payment-URL]}}` — uppercase | `{{env_var[@payment-url]}}` — always lowercase |
| Storing secrets as `visible` variables | Use `env_var_type: "secret"` for tokens and passwords |
| Trying to read a `secret` variable in a Code node | Secret variables are only accessible from `api` (API Call) nodes |
| Duplicate variable creation | Always read `_ENV_VARS_.json` or call the list API first |
| Renaming `short_name` without updating process files | Grep all `.conv.json`, update references, push each changed process |
| Deleting a variable used by active processes | `push-process` will fail; remove all references first |
| Passing large JSON config as raw string | Use `data_type: json` for structured values |

---

## Reference Documents

| Path | When to read |
|------|-------------|
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Naming rules and usage examples (quick reference) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-node.md` | How `set_param` feeds variables into task data for Code nodes |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | How variables are used in URL, headers, and body fields |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-json-validation.md` | How `push-process` validates `{{env_var[@name]}}` references |
