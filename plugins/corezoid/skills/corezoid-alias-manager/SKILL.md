---
name: corezoid-alias-manager
description: >
  Manages Corezoid aliases — create, list, modify, delete, link/unlink, and use aliases
  in process JSON. Activate when the user mentions "alias", "short_name", "short name",
  "create alias", "list aliases", "delete alias", "modify alias", "rename alias",
  "unlink alias", "link alias", "get callback hash", "conv[@", or asks how to reference
  a process by name instead of numeric ID. Also activate when reviewing a process that
  uses numeric conv_id values and the user wants to replace them with aliases.
---

# Corezoid Alias Manager

## What aliases are

An alias is a human-readable `short_name` (e.g. `payment-service`, `send-otp`) that
maps to a process (`conv`). Aliases serve two purposes:

1. **Process reference in JSON** — use `@alias-name` as `conv_id` instead of a numeric
   process ID. This makes processes portable across environments and removes hardcoded IDs.
2. **External HTTP entry point** — each alias generates a `callback_hash` used to send
   tasks to the process via the API Gateway URL.

Alias naming rules (same as Corezoid short names):
- Only lowercase letters `[a-z]`, digits `[0-9]`, and hyphens `-`
- Must be at least 3 characters
- Must be unique within the stage
- Good: `payment-checkout`, `send-otp`, `create-user-v2`
- Bad: `MyAlias`, `PAYMENT`, `a`

---

## MCP Tools

| Tool | Purpose |
|------|---------|
| `create-alias` | Create an alias and link it to a process in one step |

> **Note:** `list`, `modify`, `delete`, and `unlink` operations are not yet exposed as
> MCP tools. Use the direct API calls documented below to perform them.

---

## Using aliases in process JSON

Once an alias exists, replace the numeric `conv_id` with `@alias-name` in any node that
calls another process:

### Call a Process node (`api_rpc`)
```json
{
  "type": "api_rpc",
  "conv_id": "@payment-checkout",
  "extra": { "amount": "{{amount}}", "currency": "{{currency}}" },
  "extra_type": { "amount": "number", "currency": "string" },
  "err_node_id": "<error_node_id>"
}
```

### Copy Task node (`api_copy`)
```json
{
  "type": "api_copy",
  "conv_id": "@send-notification",
  "ref": "{{unique_ref}}",
  "mode": "create",
  "err_node_id": "<error_node_id>"
}
```

### State store read in `set_param` or condition
```
{{conv[@user-states].ref[{{task_ref}}].status}}
```

This reads the `status` field of the task with reference `{{task_ref}}` from the
`@user-states` state diagram process.

---

## Workflow: Create an alias

### Step 1 — Resolve the process

Check whether the user provided a file path, process name, or process ID.
If a file path is given, the process ID is the leading number in the filename
(e.g. `1834583_My_Process.conv.json` → `process_id = 1834583`).

If only a name is given, search locally:
```bash
find . -name "*.conv.json" | xargs grep -l '"title": "My Process"'
```

### Step 2 — Check if the alias already exists

Before creating, verify the `short_name` is not taken. Call the list aliases API
(see "Workflow: List aliases" below) and scan the `short_name` fields.
If a conflict is found, suggest an alternative name to the user.

### Step 3 — Decide the short_name

Apply the naming rules: lowercase, hyphens, no spaces or underscores, at least 3 chars.
Suggest a name derived from the process title if the user hasn't specified one.

### Step 4 — Create the alias

Call MCP tool **`create-alias`** with:
- `process_path`: relative path to the `.conv.json` file
- `short_name`: the alias short name

```
create-alias(
  process_path="./671255_develop/1834583_My_Process.conv.json",
  short_name="payment-checkout"
)
```

The tool creates the alias, links it to the process, and returns the `alias_id`.
Requires `COREZOID_STAGE_ID` to be set in `.env`.

### Step 5 — Update and redeploy referencing processes

After creating the alias, replace any numeric `conv_id` references to this process
across the project with `@short-name`:

```bash
grep -rl '"conv_id": 1834583' . --include="*.conv.json"
```

For each file found, replace `"conv_id": 1834583` with `"conv_id": "@payment-checkout"`.
Then for each modified file, run **`lint-process`** and on success **`push-process`**.

> After pushing, tell the user: "Changes deployed. Please **refresh the page** in Corezoid to see the updated process."

---

## Workflow: List aliases

The MCP server does not yet expose a `list-aliases` tool. Use the Corezoid API directly.

**Required fields:**
- `company_id` — workspace ID (from `.env` `WORKSPACE_ID`)
- `project_id` — project ID (from the `*.stage.json` file in the project root)
- `stage_id` — stage ID (from `.env` `COREZOID_STAGE_ID`)

**API call:**
```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type": "list",
    "obj": "aliases",
    "sort": "date",
    "order": "desc",
    "id": "<WORKSPACE_ID>",
    "company_id": "<WORKSPACE_ID>",
    "project_id": <PROJECT_ID>,
    "stage_id": <STAGE_ID>
  }]
}
```

**Response fields per alias:**
| Field | Description |
|-------|-------------|
| `obj_id` | Alias numeric ID (needed for modify/delete/link) |
| `title` | Human-readable display title |
| `short_name` | The `@short-name` used in `conv_id` references |
| `description` | Optional description |
| `obj_to_id` | Process (`conv`) ID this alias points to |
| `obj_to_type` | Always `"conv"` for process aliases |
| `uuid` | Alias UUID |
| `create_time` / `change_time` | Unix timestamps |
| `project_title`, `stage_title` | Context information |

---

## Workflow: Modify an alias

To rename an alias or change its title/description (does NOT change which process it
points to — use unlink + link for that).

**API call:**
```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type": "modify",
    "obj": "alias",
    "obj_id": <ALIAS_ID>,
    "title": "New Display Title",
    "short_name": "new-short-name",
    "description": "Updated description",
    "company_id": "<WORKSPACE_ID>",
    "project_id": <PROJECT_ID>,
    "stage_id": <STAGE_ID>
  }]
}
```

> ⚠️ Changing `short_name` invalidates all `"conv_id": "@old-name"` references
> across every process in the project. Grep all `.conv.json` files and update them,
> then push each modified process.

---

## Workflow: Repoint an alias to a different process

Use two API calls: unlink from the current process, then link to the new one.

### Step 1 — Unlink from current process
```
POST {COREZOID_API_URL}/api/2/json

{
  "ops": [{
    "type": "link",
    "obj": "alias",
    "link": false,
    "obj_id": <ALIAS_ID>,
    "obj_to_id": <CURRENT_PROCESS_ID>,
    "obj_to_type": "conv",
    "company_id": "<WORKSPACE_ID>"
  }]
}
```

### Step 2 — Link to new process
```
POST {COREZOID_API_URL}/api/2/json

{
  "ops": [{
    "type": "link",
    "obj": "alias",
    "link": true,
    "obj_id": <ALIAS_ID>,
    "obj_to_id": <NEW_PROCESS_ID>,
    "obj_to_type": "conv",
    "company_id": "<WORKSPACE_ID>"
  }]
}
```

---

## Workflow: Delete an alias

> ⚠️ Before deleting, check all `.conv.json` files for `"conv_id": "@alias-name"` references.
> Deleting an alias breaks every process that uses it.

**API call:**
```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type": "delete",
    "obj": "alias",
    "obj_id": <ALIAS_ID>,
    "company_id": "<WORKSPACE_ID>"
  }]
}
```

After deletion, replace all `"conv_id": "@alias-name"` references with the numeric
process ID (or create a new alias), then push each affected process.

---

## Workflow: Get callback hash (external task submission URL / Direct URL webhook)

The callback hash is the secret in a process's or alias's **Direct URL** — the public
webhook endpoint external systems POST tasks to. A process and each of its aliases
have **independent** hashes; rotating or deleting one does not affect the others.

**Get the hash — by process (`conv_id`):**
```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type": "get",
    "obj": "callback_hash",
    "conv_id": <PROCESS_ID>,
    "company_id": "<WORKSPACE_ID>"
  }]
}
```

**Get the hash — by alias (`alias_id`):**
```
POST {COREZOID_API_URL}/api/2/json
Authorization: Simulator {ACCESS_TOKEN}
Content-Type: application/json

{
  "ops": [{
    "type": "get",
    "obj": "callback_hash",
    "obj_type": "alias",
    "alias_id": <ALIAS_ID>,
    "company_id": "<WORKSPACE_ID>"
  }]
}
```

**Response (both):**
```json
{ "request_proc": "ok", "ops": [{ "proc": "ok", "callback_hash": "19e339a865d676db68b776f440443821c49a0e30" }] }
```

Other `type` values on the same `obj: "callback_hash"` (swap `"type": "get"` for one of
these, keeping the same `conv_id`/`alias_id` field):

| `type` | Effect |
|---|---|
| `create` | Enables the Direct URL and issues the first hash |
| `get` | Returns the current hash without changing anything |
| `modify` | **Rotates** the hash — the previous URL stops working immediately |
| `delete` | Disables the Direct URL entirely |

**External submission URL:**

⚠️ The URL format is **installation-specific** — always verify against the Direct URL
shown in the Corezoid UI for that process/alias before handing it to another system.
Two formats have been observed:

- **Cloud (dedicated API Gateway):**
  ```
  POST {COREZOID_APIGW_URL}/api/1/json/<WORKSPACE_ID>/<callback_hash>
  Content-Type: application/json

  { "ops": [{ "ref": "unique-task-ref", "type": "create", "obj": "task", "data": { "key": "value" } }] }
  ```
  Where `COREZOID_APIGW_URL` defaults to `https://api-apigw.corezoid.com`.

- **Self-hosted (served off the same account host, no separate gateway):**
  ```
  By conv_id:  https://<host>/api/2/json/public/<conv_id>/<callback_hash>
  By alias:    https://<host>/api/2/json/public/@<alias_short_name>/<project_short_name>/<stage_short_name>/<company_id>/<callback_hash>
  ```
  `<host>` is the same host as `COREZOID_API_URL`. Some installations serve this under
  `/api/1/json/public/...` instead of `/api/2/` — don't assume without confirming once
  per installation.

Either way, the hash in the URL **is** the authentication — treat it like a secret.
Prefer alias-based URLs for anything referenced by external systems: an alias can be
re-pointed at a new process without invalidating URLs already handed out.

---

## Resolving environment values

All API calls above require `company_id`, `project_id`, `stage_id`. Find them:

| Value | Where to find it |
|-------|-----------------|
| `WORKSPACE_ID` (company_id) | `.env` file — `WORKSPACE_ID=...` |
| `COREZOID_STAGE_ID` (stage_id) | `.env` file — `COREZOID_STAGE_ID=...` |
| `project_id` | Read the `*.stage.json` file in the project root; field `project_id` |
| `COREZOID_API_URL` | `.env` file — `COREZOID_API_URL=...` |
| `ACCESS_TOKEN` | `~/.corezoid/credentials` or `.env` |

If `.env` is missing, run the `corezoid-init` skill to set up the environment.

---

## Two-step create (alternative manual flow)

The `create-alias` MCP tool creates and links in a single request. If you need
finer control (create first, link later), use the two-step API flow:

### Step 1 — Create the alias object
```json
{
  "ops": [{
    "type": "create",
    "obj": "alias",
    "title": "My Alias Title",
    "short_name": "my-alias",
    "description": "",
    "company_id": "<WORKSPACE_ID>",
    "project_id": <PROJECT_ID>,
    "stage_id": <STAGE_ID>
  }]
}
```
Response: `{ "obj_id": <ALIAS_ID>, "proc": "ok" }`

### Step 2 — Link the alias to a process
```json
{
  "ops": [{
    "type": "link",
    "obj": "alias",
    "link": true,
    "obj_id": <ALIAS_ID>,
    "obj_to_id": <PROCESS_ID>,
    "obj_to_type": "conv",
    "company_id": "<WORKSPACE_ID>"
  }]
}
```

---

## Common pitfalls

| Mistake | Correct approach |
|---------|-----------------|
| `"conv_id": "@My-Alias"` (uppercase) | `"conv_id": "@my-alias"` — always lowercase |
| Deleting an alias without updating referencing processes | Search all `.conv.json` first with `grep -r "@alias-name"` |
| Renaming `short_name` without updating process JSON | Same — grep, replace, push each changed process |
| Creating an alias with `short_name` that already exists | Use `list aliases` API call first to check |
| Forgetting to push processes after replacing numeric IDs with aliases | Always `lint-process` then `push-process` for every modified file |
| Using alias without `COREZOID_STAGE_ID` set | Run `corezoid-init` or set `COREZOID_STAGE_ID` in `.env` |

---

## Decision guide

| Situation | Action |
|-----------|--------|
| Process has numeric `conv_id` references from other processes | Create alias, then replace all numeric IDs |
| Setting up a new process that others will call | Create alias at creation time, use `@alias` from the start |
| Want to swap which process an alias points to (blue/green deploy) | Unlink + Link (keep the `short_name`, update the target) |
| Decommissioning a process | Check alias references, delete alias, update or deprecate callers |
| External system needs to send tasks to a process | Get callback hash for the alias, build API Gateway URL |
| Reviewing a process for hardcoded numeric `conv_id` | Flag each one, suggest alias creation and replacement |

---

## Reference Documents

| Path | When to read |
|------|-------------|
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | `api_rpc` node — where `conv_id: "@alias"` is used |
| `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md` | JSON schemas for `api_rpc` and `api_copy` (both use `conv_id`) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-dynamic-values.md` | `{{conv[@alias].ref[...].field}}` state store read pattern |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variable naming rules (same convention as alias short names) |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-review/SKILL.md` | Step 10: External Dependencies — alias audit and creation |
