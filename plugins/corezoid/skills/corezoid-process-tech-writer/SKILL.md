---
name: corezoid-process-tech-writer
description: >
  Document a process: Markdown doc plus JSON with node and parameter
  descriptions. Triggers: "document this process", "задокументируй".
---

# Corezoid Process Tech Writer

Always produce **two outputs** for every process:
1. Markdown documentation file at `.processes/<name>-docs.md`
2. Enriched process JSON (same file, `description` fields filled in) at `.processes/<name>-enriched.json`

---

## Step 0 — Load the process

If the user provides a file path, read it directly. If they provide a process name or ID, use
`pull-process` to fetch it first.

---

## How to extract information from the process JSON

### Inputs
Read the `params` array. Each entry has:
- `name` — parameter name
- `type` — data type
- `descr` — description (may be empty — infer from context)
- `flags` — `"required"` flag means mandatory; `"input"` = input param, `"output"` = output param
- `regex` — validation pattern (document if non-empty)

### Outputs
Find all nodes with `api_rpc_reply` logic in `condition.logics`:
- `throw_exception: false` → success response — document `res_data` keys and types
- `throw_exception: true` → error response — document what triggers it (node title, `exception_reason` if present)

### Process flow
Walk `scheme.nodes` following `go` entries from the Start node (`obj_type: 1`):
- Start → node with `id` matching the `to_node_id` in Start's `go` logic
- Continue following `go` entries to map the happy path
- Note branches at Condition nodes or `go_if_const` entries
- Note error paths via `err_node_id` references

### External dependencies
- API Call nodes (`api` logic): extract `url`, `method`, `extra_headers`
- `{{env_var[@name]}}` references: list all unique variable names used
- Code nodes (`api_code`): look for referenced services or data transformations
- Call Process nodes (`api_rpc`): extract `conv_id` values (called process IDs)

---

## Output 1: Markdown documentation

Save to `.processes/<process-name-in-snake-case>-docs.md`.

Use this exact structure:

```markdown
# <Process Title>

## Overview
<1-2 sentences: what this process does and when to call it. Be specific about the business purpose.>

## Input Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| field_name | string | Yes | Description from params.descr |
| optional_field | number | No | Description |

<If any field has regex validation, add a "Validation" subsection listing the rules.>

## Output

### Success response
| Field | Type | Description |
|-------|------|-------------|
| response | object | The API response body |

### Error cases
| Error | Trigger condition |
|-------|------------------|
| "Code node error" | JavaScript execution failed in the preparation step |
| "API call error" | External API returned an error or was unreachable |

## How to Call

Example `task_data` with realistic values:
​```json
{
  "field_name": "example_value",
  "optional_field": 42
}
​```

## Process Flow

1. **Start** — Entry point, receives the task
2. **<Code Node title>** — <plain English: what this step does>
3. **<API Call / Call Process title>** — <plain English: what is called and why>
4. **<Reply node title>** — <what is returned on success>
5. **Final** — Task stored, process complete

<For error paths, describe them after the happy path:>

**Error path (Code Node failure):** If the preparation step fails, an error reply is returned
with the exception description, and the task ends at the Error node.

## External Dependencies

| Dependency | Type | Variable / URL |
|-----------|------|----------------|
| <service name> | HTTP API | `{{env_var[@variable-name]}}` |
| <process name> | Corezoid process | ID: `<conv_id>` |

## Notes

- <Any timeouts configured via semaphors — e.g. "API call has a 30-second timeout">
- <Rate limiting (max_threads setting)>
- <Any other relevant technical notes>
```

---

## Output 2: Enriched process JSON

Read the original process JSON, add `description` fields to every node, and write the result
to `.processes/<name>-enriched.json`.

### Rules for node enrichment

**What to fill:**
- `description` field on every node — one plain English sentence: what this node does in the context of this process
- `params[].descr` — if empty, infer from the field name and process context

**What NOT to change:**
- `id`, `obj_type`, `condition`, `logics`, `semaphors` — never touch these
- `x`, `y`, `extra`, `options` — leave as-is
- `title` — only fill if the field is completely empty (`""`)

**Description style:**
- One sentence, active voice, present tense
- Specific to this process — not generic ("Handles errors" is bad; "Returns an error reply if the actor creation API call fails" is good)
- Reference actual data fields and external services where relevant

**Examples:**

| Node type | Bad description | Good description |
|-----------|----------------|-----------------|
| Code node | "Prepares data" | "Builds the request body with actor_name, form_id, and authorization_header for the Simulator API call" |
| API Call | "Makes API call" | "Sends POST request to Simulator API to create a new actor with the prepared parameters" |
| Reply Success | "Returns response" | "Returns the created actor data from the Simulator API back to the calling process" |
| Reply Error | "Returns error" | "Returns error reply with throw_exception:true when the actor creation API call fails" |
| Final | "Final" | "Stores the completed task with actor creation result and marks the process as successful" |

---

## Both files must be produced in the same response

Do not produce one without the other. If the process JSON is very large, produce the Markdown
first, then the enriched JSON.
