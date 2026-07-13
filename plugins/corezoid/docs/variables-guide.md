# Variables Guide

Variables store constants (URLs, tokens, endpoints, credentials) that should never be hardcoded in process logic.

---

## Naming Rules

- Only lowercase letters `[a-z]`, digits `[0-9]`, and hyphens `-`
- NAME and DESCRIPTION must be **at least 3 characters**
- Good examples: `paypal-url`, `api-token-123`, `database-host`, `service-endpoint`
- Bad examples: `URL`, `ACCESS_TOKEN`, `x`, `ab`

---

## Workflow

### 1. Check if variable already exists

Check both local cache files before creating anything. If the variable exists, reuse it:

- `_ENV_VARS_.json` — created by `pull-folder` (contains all variables exported from Corezoid)
- `.processes/variables.json` — created by the MCP `create-variable` tool during the current session

### 2. Create a new variable

Call MCP tool **`create-variable`** with `name`, `description`, and `value`.

Manage existing variables with **`list-variables`** (read-only, secrets masked),
**`modify-variable`** and **`delete-variable`**. The write tools are dry-run by
default and require `apply=true` + `confirm="<short_name>#<obj_id>"` after the user
has explicitly approved the shown diff / deletion warning. Deletion is PERMANENT —
environment variables have no recycle bin. `env_var_type` (visible/secret) cannot be
changed after creation.

Example: `name: "payment-api-url"`, `description: "Payment Service API URL"`, `value: "https://api.payments.example.com"`

### 3. Reference in process logic

Use the syntax `{{env_var[@variable-name]}}` anywhere a value is expected.

---

## Usage by Node Type

### Set Parameters node

```json
{
  "type": "set_param",
  "extra": {
    "baseUrl": "{{env_var[@payment-api-url]}}",
    "token": "{{env_var[@payment-api-token]}}"
  },
  "extra_type": {
    "baseUrl": "string",
    "token": "string"
  },
  "err_node_id": "<error_node_id>"
}
```

### API Call node

```json
{
  "type": "api",
  "url": "{{env_var[@payment-api-url]}}/charge",
  "method": "POST",
  "extra_headers": {},
  "max_threads": 5,
  "err_node_id": "<error_node_id>"
}
```

### Call a Process node (passing variable as parameter)

```json
{
  "type": "api_rpc",
  "conv_id": 9876543,
  "extra": {
    "endpoint": "{{env_var[@service-endpoint]}}",
    "secret": "{{env_var[@service-secret]}}"
  },
  "extra_type": {
    "endpoint": "string",
    "secret": "string"
  },
  "err_node_id": "<error_node_id>"
}
```

### Code node (within JavaScript src)

```javascript
// Variables are pre-resolved before code runs, so access them via data object
// First set the variable via set_param node into a data field, then use in code:
var url = data.baseUrl + "/endpoint";
```

---

## Important Notes

- Variables are resolved **at runtime** — the value `{{env_var[@name]}}` is replaced with the actual variable value before the node executes
- Variables are **stage-scoped** — shared across all processes within the same stage
- Changes to variable values take effect immediately without redeploying processes
- Never store secrets directly in process JSON — always use variables