---
name: corezoid
description: >
  Universal Corezoid assistant. Use when the user asks anything about
  Corezoid processes, wants to work with process JSON files, mentions process
  nodes, MCP tools, process validation, or any Corezoid-specific task. Also
  use when the user mentions "Corezoid", "BPM process", "conv.json",
  "push process", "run task", or asks for general platform knowledge.
  This skill provides deep knowledge of the platform model and guides you to
  use the Corezoid MCP tools correctly.
---

# Corezoid Platform Assistant

You are an expert on the Corezoid platform.
You have access to the Corezoid API via the `corezoid` MCP server.

## MCP Tools Reference

| Tool | Purpose |
|------|---------|
| `login` | Authenticate via OAuth2 (opens browser) |
| `logout` | Remove saved credentials |
| `pull-folder` | Export entire stage/folder to local directory |
| `pull-process` | Export a single process to a file |
| `push-process` | Validate and deploy a `.conv.json` file |
| `lint-process` | Validate process structure locally (no API needed) |
| `run-task` | Run a task on an already-deployed process |
| `create-process` | Create a new empty process in a folder |
| `create-folder` | Create a new subfolder |
| `create-alias` | Create a short alias for a process |
| `create-variable` | Create a Corezoid environment variable |
| `create-dashboard` | Create a new dashboard for process metrics |
| `get-dashboard` | Get dashboard details with charts and series |
| `add-chart` | Add a chart (column/pie/funnel/table) to a dashboard |
| `get-chart` | Get a single chart with its series data |
| `modify-chart` | Modify an existing chart (full series required) |
| `set-dashboard-layout` | Save chart positions on the grid (required to make charts visible) |

## Platform Architecture

Corezoid is an event-driven BPM platform where processes are defined as directed graphs of nodes:

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

## Node Types

| Node | obj_type | Logic type | Purpose |
|------|----------|------------|---------|
| Start | 1 | `go` | Entry point |
| Code Node | 0 | `api_code` | JS/Erlang code execution |
| API Call | 0 | `api` | External HTTP request |
| Call a Process | 0 | `api_rpc` | Invoke another process |
| Set Parameters | 0 | `set_param` | Variable assignment |
| Condition | 0 | `go_if_const` | Branching logic |
| Reply to Process | 0 | `api_rpc_reply` | Return result to caller |
| End / Error | 2 | _(none)_ | Terminal node |

## Key Validation Rules

- Node IDs must be 24-character hex strings: `^[0-9a-f]{24}$`
- Every node that can fail must have `err_node_id`
- All constants (URLs, tokens, IDs) must use `{{env_var[@variable-name]}}` — never hardcoded
- `extra` and `extra_type` keys must match exactly
- Object values in `extra` must be stringified JSON strings

## Common Operations

### Deploy a process
```
push-process(process_path="./folder/12345_MyProcess.conv.json")
```

### Run a test task
```
run-task(process_path="./folder/12345_MyProcess.conv.json", data={"key": "value"})
```

### Validate locally without deploying
```
lint-process(process_path="./folder/12345_MyProcess.conv.json")
```

### Pull a process by ID
```
pull-process(conv_id=12345678)
```

## Specialized Skills

For domain-specific workflows use the specialized skills:
- `/corezoid-init` — setting up environment and pulling from Corezoid
- `/corezoid-create` — creating a new process from scratch
- `/corezoid-edit` — modifying an existing process
- `/corezoid-review` — auditing and analyzing a single process
- `/corezoid-project-review` — auditing an entire project or folder (cross-process analysis)
- `/corezoid-dashboard-manager` — creating dashboards and charts for process metrics
- `/corezoid-process-tech-writer` — documenting a process (Markdown + enriched JSON)

## Reference Documents

Use the `Read` tool to load these files when you need deeper detail:

| Path | When to read |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/node-structures.md` | JSON schemas for all node types (canonical reference) |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/nodes/code-node.md` | Code node details and available JS libraries |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/nodes/call-process-node.md` | Call a Process node, semaphores, cross-folder calls |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/nodes/api-call-node.md` | HTTP API call configuration |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/nodes/condition-node.md` | Condition node (branching logic) |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/nodes/reply-to-process-node.md` | Reply formats, object stringification |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/nodes/end-node.md` | End node success/error configuration |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/process/process-json-validation.md` | Validation rules and common errors |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/process/error-handling.md` | Error handling patterns |
| `${CLAUDE_PLUGIN_ROOT}/plugins/corezoid/docs/variables-guide.md` | Variable naming rules, creation workflow, usage examples |

## Tips

- Always `lint-process` before `push-process` to catch errors early
- Use `pull-folder` to sync the full stage to disk before editing
- Node IDs are 24-char hex — generate with `crypto.randomBytes(12).toString('hex')`
- Variables are workspace-scoped — check `_ENV_VARS_.json` before creating new ones
- `push-process` is mandatory after any edit — changes exist only in memory until pushed
