# Corezoid — Claude Code & Codex Plugin

A plugin for [Claude Code](https://claude.ai/code) and [Codex](https://openai.com/codex) that connects the [Corezoid](https://corezoid.com) platform to Claude via MCP. Claude gets direct access to Corezoid processes and deep platform knowledge to create, edit, review, and deploy workflows through natural conversation.

> *Not just an MCP wrapper over the Corezoid API — an AI-Native management layer for the platform.*

## What it does

The plugin bundles a Go MCP server that exposes Corezoid operations as MCP tools and provides specialist skills that teach Claude the platform model and common workflows:

| Skill                          | Activate with                            | Covers                                            |
|--------------------------------|------------------------------------------|---------------------------------------------------|
| `corezoid`                     | "Corezoid", "process", "conv.json"       | Full platform overview, all node types, MCP tools |
| `corezoid-init`                | "set up", "login", "pull workspace"      | OAuth login, workspace pull, environment setup    |
| `corezoid-create`              | "create a process", "new process"        | Building processes from scratch                   |
| `corezoid-edit`                | "edit", "modify", "update" a process     | Modifying existing `.conv.json` files             |
| `corezoid-review`              | "review", "audit", "check" a process     | Analysis, dead code, best-practice violations     |
| `corezoid-project-review`      | "review project", "audit folder"         | Cross-process audit of an entire folder           |
| `corezoid-stage-scan`          | "scan stage", "check stage before merge", "why does the merge fail" | Offline pre-merge validation of exported stage `.zip`s: non-active/empty processes, broken node links, broken/inactive `conv_id` refs |
| `corezoid-dashboard-manager`   | "create dashboard", "add chart", "visualize metrics" | Dashboards, charts, node metrics, real-time monitoring |
| `corezoid-process-tech-writer` | "document", "write docs", "describe process" | Markdown docs + enriched JSON with node descriptions |
| `corezoid-access`              | "share", "give access", "create group", "create api key" | Object sharing, user groups, API keys, invites    |
| `corezoid-alias-manager`       | "alias", "short name", "rename alias"    | Create, list, modify, delete process aliases      |
| `corezoid-variable-manager`    | "variable", "env var", "create variable" | Create, list, modify, delete environment variables |
| `corezoid-api-connector`       | "call Corezoid API", "api/2/json", "api_secret_outer" | Processes that call the Corezoid public API       |
| `corezoid-process-optimizer`   | "optimize", "reduce tacts", "improve"    | Merge nodes, clean data flow, add resilience      |
| `corezoid-describe`            | "update description", "add description", "describe this process" | Set or refresh the description of a process, folder, or project |
| `corezoid-feedback`            | "report a bug", "this is broken", "send feedback" | Collect and submit bug reports / improvement requests |
| `marketplace-publish-validation` | "publish to marketplace", "check before publish" | Pre-publication checklist for Corezoid marketplace |
| `corezoid-gitcall`             | "git call", "gitcall", "run my code", "custom code node", "python/go/php in a process" | Custom code (Python/Go/Java/PHP/JS/…) as a git_call step — parsing, libraries, crypto, attachments; handles the container build on push |

## Design philosophy

This plugin is not simply an MCP wrapper over the Corezoid API. It is an attempt to
build an AI-Native management layer — one that understands process structure,
validation rules, and platform conventions deeply enough to create, audit, and deploy
workflows through natural conversation.

## Requirements

- [Claude Code](https://claude.ai/code) or [Codex](https://openai.com/codex) installed
- A Corezoid account

## Installation

### Claude Code
 
**From the GitHub marketplace:**

```bash
claude plugin marketplace add corezoid/corezoid-ai-plugin
claude plugin install corezoid@corezoid
```

**Or from a local clone:**

```bash
git clone https://github.com/corezoid/corezoid-ai-plugin
claude plugin marketplace add ./corezoid-ai-plugin
claude plugin install corezoid@corezoid
```

### Codex

**From the GitHub marketplace:**

```bash
codex plugin marketplace add corezoid/corezoid-ai-plugin
codex plugin install corezoid@corezoid
```

**Or from a local clone:**

```bash
git clone https://github.com/corezoid/corezoid-ai-plugin
codex plugin marketplace add ./corezoid-ai-plugin
codex plugin install corezoid@corezoid
```

No build step, no extra setup. The MCP server starts automatically on first use.

> **Telemetry:** the plugin collects anonymous usage data (tool name, duration, error type, transport version) to improve reliability. No tokens, workspace IDs, or process content are ever sent. To opt out:
> ```bash
> export COREZOID_ANALYTICS_DISABLED=1   # add to ~/.zshrc or ~/.bashrc to persist
> ```

### Updating

```bash
claude plugin update corezoid@corezoid   # Claude Code
codex plugin update corezoid@corezoid    # Codex
```

Restart Claude Code / Codex after updating to apply the new version.

## Authentication

On the first Corezoid operation Claude detects that no token is present and runs the `login` tool automatically — your browser opens for OAuth2 sign-in and the session continues without interruption.

The token is saved to `~/.corezoid/credentials` and reused on every subsequent session across all projects. When it expires, the login flow triggers again automatically.

You can also trigger login manually at any time:

```
log in to Corezoid
```

### Static token (optional)

If you prefer to manage the token yourself, write it to `~/.corezoid/credentials`:

```
ACCESS_TOKEN=your_token_here
```

Or export it as an environment variable before starting Claude Code or Codex:

```bash
export ACCESS_TOKEN=your_token_here
```

## Configuration

| Environment variable       | Required | Description                                       |
|----------------------------|----------|---------------------------------------------------|
| `ACCESS_TOKEN`             | No       | Static token — overrides OAuth2 saved credentials |
| `COREZOID_API_URL`         | No       | Override the default Corezoid API base URL        |
| `WORKSPACE_ID`             | No       | Default workspace ID                              |
| `COREZOID_STAGE_ID`        | No       | Default stage ID                                  |
| `COREZOID_APIGW_URL`       | No       | Override the API Gateway URL                      |
| `COREZOID_OAUTH_CLIENT_ID` | No       | OAuth2 client ID — on-prem deployments with a custom authorization server should set this to their own client ID; cloud (account.corezoid.com) users do not need it |
| `COREZOID_HTTP_PORT`       | No       | Activate the Streamable HTTP transport on this port (e.g. `8080`). When set the server listens for MCP over HTTP instead of stdio — intended for hosted marketplace deployments. Credentials must be pre-configured via env vars; the browser OAuth login flow is not available in HTTP mode |

## Telemetry

The MCP server collects anonymous usage data (tool name, duration, error type, API hostname) to help improve the plugin. **Tokens, workspace identifiers, process content, and personal data are never sent.**

To opt out, set the environment variable before starting Claude Code:

```bash
export COREZOID_ANALYTICS_DISABLED=1
```

See [SECURITY.md](SECURITY.md) for the full list of collected fields.

## Usage

Once installed, just talk to Claude naturally:

```
Pull my Corezoid workspace and show me what processes are in the Payments folder.
```

```
Create a process that calls any weather API, handles errors,
and sends the forecast back to the caller.
```

```
Create a folder named "Services" in Corezoid.
```

```
Edit the "payment" process — add retry logic on API timeout
with exponential backoff up to 3 attempts.
```

```
Edit process with id 1278273  — add retry logic
on API timeout with exponential backoff up to 3 attempts.
```

```
Review process ID 2778176 for dead nodes, missing error handlers,
and hardcoded values.
```

```
Review the process at 1278273_Business.folder/2778176_payment.conv.json
for dead nodes, missing error handlers, and hardcoded values.
```

```
Push the updated payment process to Corezoid and run a test task with
{"amount": 100, "currency": "USD"}.
```

```
Audit the entire Payments folder — list all processes, check for
validation errors, and summarize what each process does.
```

## MCP Tools

| Tool                | Description                                        |
|---------------------|----------------------------------------------------|
| `login`             | Authenticate via OAuth2 (opens browser)            |
| `logout`            | Remove saved credentials                           |
| `list-workspaces`   | List available workspaces and stages               |
| `list-stages`       | List stages in a workspace                         |
| `list-projects`     | List folders and processes in a stage              |
| `create-project`    | Create a new project (with optional stages) in a workspace |
| `modify-project`    | Update a project's title, short_name and/or description |
| `delete-project`    | Move a project to the recycle bin (Trash)          |
| `show-project`      | Show a project's stages and parent folder          |
| `pull-folder`       | Export an entire folder/stage to local files       |
| `pull-process`      | Export a single process to a `.conv.json` file     |
| `push-process`      | Validate and deploy a `.conv.json` to Corezoid     |
| `lint-process`      | Validate process structure locally (no API call)   |
| `run-task`          | Send a task to a deployed process                  |
| `list-node-tasks`   | List tasks currently sitting in a node             |
| `list-task-history` | Show task execution history                        |
| `get-node-stat`     | Return time-series in/out statistics for a node   |
| `delete-task`       | Remove a task from a node                          |
| `modify-task`       | Update task parameters                             |
| `create-process`    | Create a new empty process in a folder             |
| `create-state-diagram` | Create a new empty state diagram (conv_type "state") in a folder |
| `create-folder`     | Create a new subfolder                             |
| `show-folder`       | Show folder metadata (title, kind, parent)         |
| `list-folders`      | List immediate children of a folder (no disk I/O)  |
| `modify-folder`     | Rename a folder or update its description          |
| `delete-folder`     | Move a folder to the recycle bin                   |
| `delete-process`    | Move a process or state diagram to the recycle bin |
| `create-alias`      | Create a short alias for a process                 |
| `create-variable`   | Create a Corezoid environment variable             |
| `create-dashboard`  | Create a new dashboard for visualizing node metrics |
| `get-dashboard`     | Get a dashboard with its charts and series         |
| `add-chart`         | Add a chart (column, pie, funnel, table) to a dashboard |
| `modify-chart`      | Modify an existing chart (full series replace)     |
| `get-chart`         | Get a single chart with its series data            |
| `set-dashboard-layout` | Save chart positions on a dashboard grid        |
| `share-object`      | Grant or revoke access on a process/folder/stage/project for a user, API key or group (use privs="none" to revoke) |
| `list-shares`       | List principals with access to a shared object     |
| `create-group`      | Create a new user group (optional description)     |
| `modify-group`      | Rename a group or update its description           |
| `list-group-objects`| List processes currently shared with a group       |
| `delete-group`      | Delete a user group (refuses by default if shares active; force=true to override) |
| `add-to-group`      | Add a user or API key to a group                   |
| `remove-from-group` | Remove a user or API key from a group              |
| `list-groups`       | List user groups in the workspace                  |
| `create-api-key`    | Create a new API key (secret written to ~/.corezoid/api-keys/, never printed in chat) |
| `modify-api-key`    | Rename or re-describe an API key                   |
| `delete-api-key`    | Delete an API key (invalidates secret immediately) |
| `list-api-keys`     | List API keys in the workspace                     |
| `find-principal`    | Resolve user / group / API-key name to obj_id      |
| `invite-user`       | Invite an external email and share an object in one call |
| `send-feedback`     | Submit feedback about plugin behavior (returns ticket id) |

## Feedback

When the plugin does something unexpected, the `corezoid-feedback` skill guides you through collecting a description of the problem and sends it to the Corezoid team via the `send-feedback` MCP tool.

**Privacy guarantees:**

- Feedback is sent **only after your explicit confirmation**. Nothing is sent automatically.
- All fields are scanned for tokens, API keys, JWTs, and long hex secrets before transmission — any matches are replaced with `[REDACTED]`.
- To disable feedback entirely (e.g. in corporate environments), set `COREZOID_FEEDBACK_DISABLED=1`.

**Telemetry environment variables:**

| Variable | Default | Purpose |
|---|---|---|
| `COREZOID_ANALYTICS_DISABLED` | — | Opt out of anonymous tool-call telemetry |
| `COREZOID_ANALYTICS_ENDPOINT` | built-in prod URL | Override analytics endpoint |
| `COREZOID_ANALYTICS_CONV_ID` | `1852976` | Override analytics conv_id |
| `COREZOID_FEEDBACK_DISABLED` | — | Disable user-initiated feedback submission |
| `COREZOID_FEEDBACK_ENDPOINT` | built-in prod URL | Override feedback endpoint |
| `COREZOID_FEEDBACK_CONV_ID` | `1871779` | Override feedback conv_id |

## Architecture

```
Claude Code / Codex
  └── corezoid MCP server (prebuilt binary)
        ├── Auth          login, logout
        ├── Workspace     list-workspaces, list-stages, list-projects,
        │                 create-project, modify-project, delete-project, show-project
        ├── Processes     pull-process, pull-folder, push-process, lint-process
        │                 create-process, create-folder, create-alias, create-variable
        │                 show-folder, list-folders, modify-folder, delete-folder, delete-process
        ├── Tasks         run-task, list-node-tasks, list-task-history
        │                 modify-task, delete-task
        ├── Dashboards    create-dashboard, get-dashboard, add-chart,
        │                 modify-chart, get-chart, set-dashboard-layout
        ├── Access        share-object, list-shares,
        │                 create-group, modify-group, delete-group, list-group-objects,
        │                 add-to-group, remove-from-group, list-groups,
        │                 create-api-key, modify-api-key, delete-api-key, list-api-keys,
        │                 find-principal, invite-user
        └── Feedback      send-feedback
```

## Project structure

```
corezoid-ai-plugin/
├── .claude-plugin/
│   └── marketplace.json         # Claude Code marketplace listing (points to plugins/corezoid)
├── .agents/
│   └── plugins/
│       └── marketplace.json     # Codex marketplace listing (points to plugins/corezoid)
├── plugins/corezoid/            # Plugin root (CLAUDE_PLUGIN_ROOT for both Claude Code and Codex)
│   ├── .claude-plugin/
│   │   └── plugin.json          # Claude Code plugin manifest
│   ├── .codex-plugin/
│   │   └── plugin.json          # Codex plugin manifest
│   ├── .mcp.json                # MCP server configuration
│   ├── mcp-server/              # MCP server source
│   ├── skills/
│   │   ├── corezoid/                    # Universal assistant skill
│   │   ├── corezoid-init/               # Environment setup skill
│   │   ├── corezoid-create/             # Process creation skill
│   │   ├── corezoid-edit/               # Process editing skill
│   │   ├── corezoid-review/             # Process review skill
│   │   ├── corezoid-project-review/     # Project audit skill
│   │   ├── corezoid-dashboard-manager/  # Dashboard & chart management skill
│   │   └── corezoid-process-tech-writer/ # Process documentation skill
│   ├── docs/                    # Node and process documentation
│   └── samples/                 # Example .conv.json processes
```

## Debugging

The MCP server always writes debug output to `/tmp/corezoid.log` when running in MCP mode. View it with:

```bash
tail -f /tmp/corezoid.log
```

In CLI mode, enable verbose output with:

```bash
COREZOID_DEBUG=1 ./convctl pull-process process_id=123
```

## Troubleshooting

See [docs/Troubleshooting.md](docs/Troubleshooting.md) for solutions to common problems:

- Browser did not open during `login`
- Expired or missing `ACCESS_TOKEN`
- `push-process` validation errors
- MCP server startup failures
- Common Corezoid API error codes

## Compatibility

| Component         | Supported versions            | Notes |
|-------------------|-------------------------------|-------|
| Claude Code       | ≥ 1.x                         | MCP protocol 2025-03-26 |
| Codex             | current stable                | Same MCP server, same skills |
| macOS             | 13 Ventura and later          | Tested on arm64 and amd64 |
| Linux             | Ubuntu 22.04+, Debian 12+     | amd64 tested in CI |
| Windows           | not tested                    | Likely works; PRs welcome |

## Links

- [Corezoid](https://corezoid.com)
- [Claude Code](https://claude.ai/code)
- [Changelog](CHANGELOG.md)

## License

MIT
