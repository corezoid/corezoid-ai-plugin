---
name: corezoid-create
description: >
  Corezoid process creation specialist. Use when the user wants to create a new
  Corezoid process from scratch, build a new automation flow, design a new BPM
  process, or implement a new API connector. Activate when the user says
  "create a process", "build a new flow", "new process", "design from scratch",
  "implement a connector", "create an automation", or "add a new process".
---

# Create a New Corezoid Process

You are a specialist in creating Corezoid BPM processes using the `corezoid` MCP server.

## Step 1: Gather Requirements

Extract a **structured** requirements record from the user's request — this record drives both the duplicate-avoidance decision (Step 1a) and the audit log written into the created process's description (Step 4 / Step 6). Do not skip the structure just because the request is short; a one-line request produces a short record, not no record.

```
requirements:
  action:         <verb, e.g. "get", "list", "create", "notify">
  object:         <target, e.g. "accounts by actor id", "invoice", "user profile">
  inputs:         [{name, type, required}]        # empty if none
  output:         <shape summary: what fields the reply must contain>
  side_effects:   read-only | mutating | external-api
  process_type:   api-connector | business-logic
```

If any of `action` / `object` / `inputs` / `output` is genuinely missing from what the user said, ask a single clarifying question **only in interactive mode**. In autonomous mode (unattended run, no human at the console), fill unknown fields with `"unspecified"` and continue — the decision tree in Step 1a handles ambiguity by falling back to "create new" for low-match cases, so unspecified fields simply degrade to conservative behaviour rather than blocking the run.

For **API connector**, also require:
- `METHOD` — HTTP method (GET, POST, PUT, etc.)
- `URL` — endpoint URL (use a Corezoid variable, never hardcode)
- `AUTH` — authentication method and token variable name

> ⚠️ If the target API is the **Corezoid public API** (`/api/2/json/`), stop here and use `/corezoid-api-connector` instead — it follows a different pattern (`api_secret_outer`, `ops` array, no Code Node for signing).

---

## Step 1a: Duplicate-avoidance decision (MANDATORY, deterministic)

The purpose of this step is to prevent silent duplicates in autonomous runs. It is **not** a "do you want to continue?" prompt — the outcome is decided by a fixed table, not by asking the user. Skip only when `index_missing: true` (no index exists yet — Step 1a-A degrades to "create new").

### Step 1a-A: Find candidates

Search `.corezoid/project-map.json` for existing processes that could serve the requirement. The search key is `action + object` from the requirements record, **not** the domain word.

- Example (good): requirement `action="get", object="accounts by actor id"` → search keywords `"accounts actor"`, `"list accounts"`, `"accounts by actor"`.
- Example (bad): searching by the domain word `"simulator"` — this over-matches every process that mentions the domain and misses the actual duplicate whose title uses a synonym.

Call MCP tool **`describe-process`** with `identifier: "<action-object phrase>"`. If it returns > 10 candidates, narrow with a second call using 2–3 more specific keywords from the object phrase.

If `index_missing: true` — record `Decision: index-missing, cannot detect duplicates` for the audit log (Step 4) and go to Step 2 (create new). Do not gate creation on rebuilding the index.

### Step 1a-B: Score each candidate on three axes

For every candidate in the (possibly narrowed) `candidates[]`:

| Axis | Meaning | How to compute |
|---|---|---|
| **title_match** | Does the title describe the same action+object? | Word-overlap between the candidate's `title` and `action + object`. **Full** = every requirement word appears (in any order). **Partial** = ≥ half. **Low** = less. |
| **signature_match** | Do the candidate's inputs and outputs cover the requirement? | `pull-process` the candidate, read `params[]` for inputs and reply-node `extra`/`extra_type` for outputs. **Full** = every required input is present and every required output field is emitted. **Partial** = candidate covers most; requirement adds one optional param or one extra output field. **Breaking-diff** = requirement renames, retypes, or drops something the candidate has. **Low** = doesn't cover the required contract at all. |
| **blast_radius** | How many callers already depend on this process? | `calls_in_count` from the `describe-process` response — it is already there, do not skip using it. |

### Step 1a-C: Pick action from the decision table

| title_match | signature_match | callers | Action |
|---|---|---|---|
| Full or Partial | **Full** | — | **REUSE as-is.** Do not create. Return the existing `conv_id`/alias to the user (see Step 4). Exit the skill. |
| Full or Partial | **Partial, additive** (new optional param OR new reply field, no removals) | any | **EXTEND.** Hand off to `corezoid-edit` with a precise patch description ("add optional param `X`", "add field `Y` to reply"). Do not create. |
| Full or Partial | **Breaking-diff** | **0** | **MODIFY-IN-PLACE.** Hand off to `corezoid-edit`. Safe because nothing else calls this process yet. |
| Full or Partial | **Breaking-diff** | **> 0** | **FORK.** Create a new process (Step 2 onwards). The duplicate is justified — existing callers keep the old contract, new logic gets a new conv_id. |
| **Low** | — | — | **CREATE new.** No candidate is close enough. |

Notes on reading this table:
- The first row is the one that catches the reported bug: a signature-full match must reuse, never create.
- `signature_match=Full` requires **both** input coverage and output coverage; if inputs match but the reply is a different shape, that's `Partial` or `Breaking-diff`, not `Full`.
- Never ask the user "which one should I use" in autonomous mode. If the table doesn't give a unique answer (e.g. two candidates are both `Full` matches), pick the one with the higher `calls_in_count` (the more-established one) and record the tie-break in the audit log.

### Step 1a-D: Record the decision (mandatory for every path)

Regardless of which branch fired, record a one-line audit entry that will be written to the target process's `description` in Step 4 (for REUSE / EXTEND / MODIFY) or Step 6 (for FORK / CREATE). Format:

```
Decision (YYYY-MM-DD): <action> — <reason with numbers>
  requirement: <action> <object>
  matched: conv_id=<id>, callers=<n>, signature=<full|partial|breaking|low>
```

Examples:
```
Decision (2026-07-02): reuse conv_id=1877387 — signature full match, callers=3
Decision (2026-07-02): extended conv_id=1877387 with optional param "incomeType" (additive, callers=3)
Decision (2026-07-02): forked from conv_id=1877387 — breaking rename of param "user_id"→"actorId", callers=5
Decision (2026-07-02): created new — no signature match ≥ partial in the project (searched "accounts actor")
Decision (2026-07-02): created new — index-missing, cannot detect duplicates
```

### Step 1a-E: If REUSE, ensure a canonical alias exists

Before exiting the skill, check `describe-process`'s response: if `aliases[]` is empty, call MCP tool **`create-alias`** with a slug derived from `action + object` (kebab-case, no spaces, no special chars — e.g. `get-accounts-by-actor-id`). This makes the next autonomous run resolve the same requirement in one lookup instead of another candidate scoring pass.

If `aliases[]` already contains a slug that describes the same action+object, leave it — no need to add duplicates.

---

## Step 2: Create the Empty Process

Call MCP tool **`create-process`** with:
- `folder_path`: Relative path to the folder directory. Omit to use the current directory.
- `process_name`: the process name

This creates an empty process in Corezoid and saves the file as `<ID>_<Name>.conv.json` inside `folder_path`. The returned file path is `PROCESS_PATH` — all subsequent steps use it.

> ⚠️ Always verify `folder_path` points to the intended target folder. Omitting it places the process in the project root, which may not be the correct location.

> ⚠️ After `create-process`, Corezoid may create default template nodes (Start, a placeholder process node, Final) even with `create_mode: without_nodes`. Before generating the full JSON, check the current `scheme.nodes` array in the created file. If a Start node already exists, do **not** add another — doing so will cause a validation error.

If the process type is **business logic** and it needs to call existing processes, find their `conv_id` values by browsing the already-exported `.conv.json` files in the project folder.

---

## Step 3: Design the Process Structure

Every process follows this base structure:

| # | Node | obj_type | Purpose |
|---|------|----------|---------|
| 1 | Start | 1 | Entry point |
| 2 | Code Node _(optional)_ | 0 | Prepare / transform input data |
| 3 | **API Call** _or_ **Call a Process** | 0 | Core action (one or more) |
| 4 | Reply to Process (Success) | 0 | Return result to caller |
| 5 | Reply to Process (Error) | 0 | Return error to caller (one per failure point) |
| 6 | Error | 2 | Terminal error node |
| 7 | Final | 2 | Terminal success node |

**API connector** uses `type: "api"` in Step 3.
**Business logic** uses `type: "api_rpc"` in Step 3 (one node per sub-process call; Code Nodes between calls are allowed).

### Node type quick reference

| Node | obj_type | Logic type |
|------|----------|------------|
| Start | 1 | `go` |
| Code Node | 0 | `api_code` |
| Call a Process | 0 | `api_rpc` |
| API Call | 0 | `api` |
| Reply to Process | 0 | `api_rpc_reply` |
| End / Error | 2 | _(no logics)_ |

For complete JSON schemas see `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md`.

---

## Step 4: Generate the Process JSON

Produce a valid `.conv.json` file.

### Root object

```json
{
  "obj_type": 1,
  "obj_id": null,
  "parent_id": null,
  "title": "Process Name",
  "description": "Decision (YYYY-MM-DD): created new — no signature match ≥ partial in the project (searched \"...\").\n  requirement: <action> <object>",
  "status": "active",
  "params": [],
  "ref_mask": true,
  "conv_type": "process",
  "scheme": {
    "nodes": [],
    "web_settings": [[], []]
  }
}
```

`description` — must contain the audit line from Step 1a-D. This is not optional: it makes the decision auditable and lets the next autonomous run see that this process was already chosen as the mapping for a given requirement (so the next agent doesn't re-derive the same conclusion or re-fork it). Prepend the audit line to any user-supplied description text; keep it as a single line so it stays readable in the Corezoid UI's process list.

`params` — declare all input parameters the caller must pass. See `${CLAUDE_PLUGIN_ROOT}/docs/process/process-with-parameters.md`.

### Core rules

- Node IDs must be unique 24-character hex strings: `^[0-9a-f]{24}$`. These are **temporary placeholders** for new nodes — on `push-process` Corezoid reassigns its own canonical IDs (and rewires references within the push). Run `pull-process` after pushing to get the canonical IDs before any further edits.
- Connect nodes only through the `go` field
- Every node that can fail must have `err_node_id` — point it **directly at a Final Error node** (`obj_type: 2`) unless the error path needs logic (reply to caller, retry routing). Never create an Escalation node (`obj_type: 3`) that only contains a bare `go` — that is a passthrough anti-pattern flagged by `lint-process`
- All constants (URLs, tokens, IDs) must be Corezoid variables — never hardcoded:
  1. Check for existing variables: read `_ENV_VARS_.json` (from `pull-folder`) or `.processes/variables.json` (from this session)
  2. Create a new variable if needed: call MCP tool **`create-variable`** with `name`, `description`, `value`
  3. Reference in logic: `{{env_var[@variable-name]}}`
- Use descriptive `title` values (e.g., "Call Payment Process", not "RPC")
- Position nodes top-to-bottom, incrementing `y` by 200–250px; place error nodes to the right (`x + 300`)
- Place each Reply Error node at the **same `y`** as the Call/API node it handles — this creates a straight horizontal connector line for the error path

### Common pitfalls

- Using `"type": "call_process"` instead of `"type": "api_rpc"` — will fail validation
- Missing `extra`/`extra_type` in Call a Process node — both required even if empty (`{}`)
- Raw JSON objects as values in `extra` — must be stringified: `"{\"key\":\"val\"}"`
- Keys in `extra` and `extra_type` must match exactly
- Missing `rfc_format: true`, `customize_response: true`, or `version: 2` in API Call node

---

## Step 5: Validate with Lint

Call MCP tool **`lint-process`** with `process_path: "<PROCESS_PATH>"`.

Fix all reported errors and re-run until the output is clean. Do not proceed with lint errors.

---

## Step 6: Deploy and Test

Call MCP tool **`push-process`** with `process_path: "<PROCESS_PATH>"`.

After a successful deploy, run a test task to verify the process behaves as expected:

Call MCP tool **`run-task`** with:
- `process_path`: `<PROCESS_PATH>`
- `data`: `{"param1": "value1"}`

The project index refreshes automatically at the end of `push-process` — the MCP server appends a "Project index refreshed at ..." line to the deploy output. Do **not** call `build-project-index` separately; surface the appended line to the user so the new process is visibly in the index. If the line reads "Warning: project index build failed ...", tell the user but continue — deploy already succeeded.

---

## Reference Documents

Use the `Read` tool to load these files when specific node or validation details are needed:

| Path | When to read |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md` | JSON schemas for all node types + full Logics fields reference (canonical) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-built-in-functions.md` | Built-in functions: `$.math`, `$.date`, `$.random`, `$.sha1_hex`, `$.md5_hex`, `$.base64_encode`, `$.unixtime`, `$.map`, `$.filter` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-dynamic-values.md` | Dynamic values: `{{var}}`, `{{node[id].count}}`, `{{node[id].SumID}}`, `{{conv[@alias].ref[...]}}`, `{{env_var[@name].key[1]}}` |
| `${CLAUDE_PLUGIN_ROOT}/docs/tasks/task-metadata.md` | Global `root.*` fields: `root.task_id`, `root.ref`, `root.conv_id`, `root.node_id`, `root.prev_node_id`, `root.user_id`, `root.change_time`, `root.create_time` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/code-node.md` | Code node details and available JS libraries |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | Call a Process node, semaphores, cross-folder calls |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/reply-to-process-node.md` | Reply formats, object stringification |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | HTTP API call configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/delay-node.md` | Delay/timer node; 30s limit is static-literal only — dynamic absolute-timestamp `value` for scheduled or sub-30s delays |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/end-node.md` | End node success/error configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-json-validation.md` | Validation rules and common errors |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error handling patterns |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/node-positioning-best-practices.md` | Coordinate system and layout guidelines |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variable naming rules, creation workflow, usage examples |

## Example Processes

| Path | Description |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/samples/api-post.json` | HTTP POST API call (connector pattern) |
| `${CLAUDE_PLUGIN_ROOT}/samples/corezoid-api-node-list.conv.json` | Corezoid API connector (Node List, `api_secret_outer` pattern) |
| `${CLAUDE_PLUGIN_ROOT}/samples/stripe-checkout.json` | Stripe payment checkout flow |
| `${CLAUDE_PLUGIN_ROOT}/samples/create-actors.json` | Business logic with multiple process calls |
| `${CLAUDE_PLUGIN_ROOT}/samples/gpt-calculator.json` | GPT integration example |
| `${CLAUDE_PLUGIN_ROOT}/samples/create-user.json` | User creation process |
