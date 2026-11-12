---
name: corezoid
description: >
  Corezoid basics: platform model, .conv.json, nodes, MCP tools, validation.
  Use for any Corezoid task that no narrower corezoid-* skill covers.
---

# Corezoid Platform Assistant

You are an expert on the Corezoid platform.
You have access to the Corezoid API via the `corezoid` MCP server.

## MCP Tools Reference

Every tool documents its own arguments and gates in its description; read it before the first call.

Core tools, called directly: `login`, `logout`, `pull-folder`, `pull-process`, `patch-process`, `push-process`, `lint-process`, `layout-process`, `clean-process`, `run-task`, `create-process`, `create-state-diagram`, `delete-process`, `deploy-stage`, `pause-process`, `resume-process`, `create-alias`, `create-communications-orchestrator`, `send-feedback`.

### Router tools

CRUD operations sit behind seven routers. Pass the action name and its arguments **inside `args`**, never at the top level. `"help": true` returns the action's full schema and runs nothing; an unknown action answers with the router's action list.

```
cz-access {"action": "share-object", "args": {"obj": "conv", "obj_id": 1234567, "obj_to": "user", "obj_to_id": 890, "privs": "view,modify"}}
```

| Router | Actions |
|--------|---------|
| `cz-access` — sharing, groups, API keys | `share-object` (revoke with `privs: "none"`), `list-shares`, `find-principal` (name or email → id, before sharing), `list-groups`, `create-group`, `modify-group`, `delete-group`, `list-group-objects`, `add-to-group`, `remove-from-group`, `list-api-keys`, `create-api-key` (the secret goes to `~/.corezoid/api-keys/`, never into chat), `modify-api-key`, `delete-api-key`, `invite-user` |
| `cz-structure` — workspaces, projects, stages, folders | `list-workspaces`, `list-projects`, `show-project`, `create-project`, `modify-project`, `delete-project`, `list-stages`, `set-stage-immutable` (needs confirm), `list-folders`, `show-folder`, `show-process`, `list-aliases`, `create-folder`, `modify-folder`, `delete-folder`, `move-process`, `move-folder` (moves: dry-run, then the exact confirm token) |
| `cz-tasks` — tasks in a deployed process | `show-task` (one task by `ref` or `task_id`; never scan `list-node-tasks` for it), `modify-task`, `delete-task`, `list-node-tasks`, `list-task-history`, `get-node-stat` |
| `cz-dashboards` | `create-dashboard`, `get-dashboard`, `add-chart`, `get-chart`, `modify-chart` (full series), `set-dashboard-layout` (charts stay invisible until it runs) |
| `cz-variables` — stage env variables | `list-variables`, `create-variable`, `modify-variable`, `delete-variable` (both: dry-run, then confirm) |
| `cz-snapshots` | `create-snapshot`, `list-snapshots`, `get-snapshot`, `delete-snapshot` (`push-process` snapshots an existing process itself) |
| `cz-git-context` — the git mirror in `.git-context/` | `git-pull-context`, `read-context-file`, `update-context-file`, `git-push-context` |

## Platform Architecture

Corezoid is an event-driven Actor Engine where processes are defined as directed graphs of nodes:

```
Workspace
  └── Projects
        └── Stages (Root Folder)
              └── Folders (optional)
                    └── Processes (.conv.json)
                          └── Nodes (Start → Logic → End)
                                └── Tasks (data flowing through nodes)
```

**Key concepts:**
- **Processes** — stored as `.conv.json` files, named `<ID>_<name>.conv.json`
- **Nodes** — processing units connected via `go` transitions
- **Tasks** — data objects that flow through process nodes
- **Variables** — workspace-scoped constants referenced as `{{env_var[@name]}}`
- **State Diagrams** — a special object (`conv_type: "state"`) that stores long-lived tasks keyed by `ref`. Other processes read with `{{conv[<id>].ref[<ref>].<field>}}` and write with `api_copy mode: "create"/"modify"`. Allowed node set is restricted to 10 logics (Start, Condition, Code, Set Parameters, Copy Task, Modify Task, Set State, Delay, Queue, End). Use `/corezoid-state-diagram-create` and `/corezoid-state-diagram-edit`.

## Node Types

| Node | obj_type | Logic type | Purpose |
|------|----------|------------|---------|
| Start | 1 | `go` | Entry point |
| Code Node | 0 | `api_code` | JS/Erlang code execution |
| API Call | 0 | `api` | External HTTP request |
| Call a Process | 0 (`4` when Stub Mode is active) | `api_rpc` | Invoke another process; Stub Mode returns configured mock replies instead |
| Set Parameters | 0 | `set_param` | Variable assignment |
| Condition | 0 | `go_if_const` | Branching logic (business flow; `3` when it is an `err_node_id` target) |
| Reply to Process | 0 | `api_rpc_reply` | Return result to caller (`3` when it is an `err_node_id` target) |
| End / Error | 2 | _(none)_ | Terminal node |

## Key Validation Rules

- Node IDs must be 24-character hex strings: `^[0-9a-f]{24}$`
- Every node that can fail must have `err_node_id`
- All constants (URLs, tokens, IDs) must use `{{env_var[@variable-name]}}` — never hardcoded
- `extra` and `extra_type` keys must match exactly
- Object values in `extra` must be stringified JSON strings
- Active Call Process Stub Mode is `obj_type: 4` plus `condition.stub`; `obj_type: 0` with the same `condition.stub` is inactive and calls the real target process.
- Use Stub Mode only as a temporary placeholder while the called process is not ready, or as a controlled integration-test fixture. It bypasses the real called process and can hide side effects or fake success in production.
- `push-process` treats active Stub Mode as warning-only on a resolved mutable non-production-like stage, but blocks immutable, production-like, or unknown stages unless `allow_active_stub_mode=true` is passed after explicit confirmation.

## Common Operations

### Deploy a process
```
push-process(process_path="./folder/12345_MyProcess.conv.json")
```

### Run a test task
```
run-task(process_path="./folder/12345_MyProcess.conv.json", data={"key": "value"})
```

### Run a task with no local process file (e.g. a host with no local process repository)
```
run-task(process_id=12345, data={"key": "value"})
```
`process_id` needs no `pull-process` first — it identifies the process the same way `process_path`'s filename does, just without a file (same argument name `pull-process` and the `cz-tasks` actions already use). The `cz-snapshots` actions accept `process_id` the same way. Pass exactly one of the two — both together is rejected as ambiguous, and `process_id` must be greater than zero. These tools still need `stage_id`/`project_id` resolved from Corezoid credentials (`login`, or `COREZOID_*` env vars — see `corezoid-init`), even when no local file is used.

### Inspect a task by its external reference
```
cz-tasks {"action": "show-task", "args": {"process_id": 12345678, "ref": "ORDER-4711"}}
```
Read-only — never scan a node with the `list-node-tasks` action to find a known `ref`.

### Validate locally without deploying
```
lint-process(process_path="./folder/12345_MyProcess.conv.json")
```

### Pull a process by ID
```
pull-process(process_id=12345678)
```

## Specialized Skills

For domain-specific workflows use the specialized skills:
- `/corezoid-init` — setting up environment and pulling from Corezoid
- `/corezoid-create` — creating a new process from scratch
- `/corezoid-connector-create` — connector to an API endpoint (one endpoint = one process): any HTTP API — external, internal, or Corezoid itself
- `/corezoid-edit` — modifying an existing process
- `/corezoid-lifecycle` — explicitly pause/resume a process or move a process/folder; never inferred from review/refactoring
- `/corezoid-state-diagram-create` — creating a new state diagram (`conv_type: "state"`) from scratch
- `/corezoid-state-diagram-edit` — modifying an existing state diagram
- `/corezoid-review` — auditing and analyzing a single process
- `/corezoid-project-review` — auditing an entire project or folder (cross-process analysis)
- `/corezoid-dashboard-manager` — creating dashboards and charts for process metrics
- `/corezoid-process-tech-writer` — documenting a process (Markdown + enriched JSON)
- `/corezoid-alias-manager` — creating, listing, modifying, deleting, and using aliases
- `/corezoid-variable-manager` — creating, listing, modifying, deleting variables (visible/secret, raw/json)
- `/corezoid-process-optimizer` — reduce tacts (merge nodes), clean data flow, fill names, add semaphors
- `/corezoid-api-connector` — *user-run only (Claude cannot invoke it; for Corezoid API requests use `/corezoid-connector-create`)* — build processes that call the Corezoid public API (`/api/2/json/`) using `api_secret_outer`
- `/corezoid-retro` — end-of-session retrospective: extract learnings (failed→fixed push deltas, data-shape surprises, corrections) and route them to workspace CLAUDE.md, team feedback, settings, or personal memory with user confirmation
- `/corezoid-describe` — update or create the description of a process, folder, or project without editing its logic
- `/corezoid-git-context` — after a substantial session: analyse changes and update `_ext/docs/*.md` in the git mirror
- `/corezoid-gen-bot` — turn a set of existing processes into a multi-platform messenger bot (Telegram / Viber / Facebook Messenger / Apple Messages for Business): derives each process's real contract, designs the command map, creates the Communications Orchestrator and one bot process per command
- `/corezoid-edit-bot` — change a bot that already exists: add/rename/drop a command, wire another process, edit copy or keyboards, promote a stage

## Reference Documents

Use the `Read` tool to load these files when you need deeper detail:

| Path | When to read |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/docs/node-structures.md` | JSON schemas for all node types + full Logics fields reference (canonical) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-built-in-functions.md` | Built-in functions: `$.math`, `$.date`, `$.random`, `$.sha1_hex`, `$.md5_hex`, `$.base64_encode`, `$.unixtime`, `$.map`, `$.filter` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/set-parameters-dynamic-values.md` | Dynamic values: `{{var}}`, `{{node[id].count}}`, `{{node[id].SumID}}`, `{{conv[@alias].ref[...]}}`, `{{env_var[@name].key[1]}}` |
| `${CLAUDE_PLUGIN_ROOT}/docs/tasks/task-metadata.md` | Global `root.*` fields: `root.task_id`, `root.ref`, `root.conv_id`, `root.node_id`, `root.prev_node_id`, `root.user_id`, `root.change_time`, `root.create_time` |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/code-node.md` | Code node details and available JS libraries |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/call-process-node.md` | Call a Process node, semaphores, cross-folder calls |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/api-call-node.md` | HTTP API call configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/condition-node.md` | Condition node (branching logic) |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/reply-to-process-node.md` | Reply formats, object stringification |
| `${CLAUDE_PLUGIN_ROOT}/docs/nodes/end-node.md` | End node success/error configuration |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-json-validation.md` | Validation rules and common errors |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/error-handling.md` | Error handling patterns |
| `${CLAUDE_PLUGIN_ROOT}/docs/process/process-lifecycle-and-move.md` | Verified pause/resume semantics, move wire contract, confirmation and cross-stage safety rules |
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-overview.md` | State diagram concepts and allowed nodes |
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-node-structures.md` | JSON schemas for nodes inside a state diagram |
| `${CLAUDE_PLUGIN_ROOT}/docs/state-diagrams/state-diagram-process-interaction.md` | How driver processes read / create / modify state tasks |
| `${CLAUDE_PLUGIN_ROOT}/docs/variables-guide.md` | Variable naming rules, creation workflow, usage examples |

## Description Update Rule

After any successful change to a process, folder, or project, always set or refresh its description. Full authoring rules are in `/corezoid-describe`.

Summary:
- **Process** — update `description` in `.conv.json` root **before** `push-process` (no second push needed). 1–2 sentences, start with a verb (*Calls*, *Creates*, *Validates*…), under 200 characters, no *"This process…"*
- **Folder** — call `cz-structure` action `modify-folder` with `description` if the folder was structurally changed. Resolve `folder_id` from the process's parent or by name via the `list-folders` action; if unresolvable, skip.
- **Project** — call `cz-structure` action `modify-project` with `description` if project scope changed.

---

## Tips

- Always `lint-process` before `push-process` to catch errors early
- Use `pull-folder` to sync the full stage to disk before editing
- Node IDs are 24-char hex — generate with `crypto.randomBytes(12).toString('hex')`
- Variables are workspace-scoped — check `_ENV_VARS_.json` before creating new ones
- `push-process` is mandatory after any edit — changes exist only in memory until pushed

## Proactive improvement/bug reporting

When responding to a user message that reveals a **platform-level mistake** — wrong node type, wrong API choice (Corezoid vs Simulator), wrong process structure, wrong MCP tool, missing required platform field — add one line to your response, adapted to the situation:

- Bug / broken behavior → "Хотите сообщить о баге команде Corezoid?"
- Unexpected plugin choice → "Хотите сообщить об этом команде Corezoid?"
- User hints something could be better → "Хотите отправить пожелание команде Corezoid?"

This is one extra line in the same response, not a separate action. Offer once per problem; do not repeat if the user declines.

**Do not add this line** for business-logic iterations: changing a value, adding a field, renaming, adjusting a condition — these are normal user changes, not platform issues.
