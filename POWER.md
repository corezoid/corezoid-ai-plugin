---
name: corezoid
displayName: Corezoid
version: 2.8.0
description: Corezoid BPM platform assistant. Exposes the Corezoid REST API as MCP tools (`convctl`) plus 18 skills covering process creation, editing, review, validation, dashboards, state diagrams, variables, access, and stage-export scanning. Ships JSON schemas and per-node-type documentation for all 24 Corezoid node types.
author:
  name: Corezoid
  url: https://corezoid.com
homepage: https://github.com/corezoid/corezoid-ai-plugin
repository: https://github.com/corezoid/corezoid-ai-plugin
license: MIT
keywords:
  - corezoid
  - process
  - bpm
  - workflow
  - automation
  - mcp
---

# Corezoid Power for AWS Kiro

A Kiro Power that brings the [Corezoid](https://corezoid.com) BPM platform
into your Kiro workspace as MCP tools and skills. Create, edit, validate,
deploy, and document Corezoid processes without leaving the IDE.

## Install

```sh
git clone https://github.com/corezoid/corezoid-ai-plugin
cd corezoid-ai-plugin
plugins/corezoid/scripts/install-kiro.sh "$YOUR_KIRO_WORKSPACE"
```

Open the workspace in Kiro. The corezoid MCP server registers under
`.kiro/settings/mcp.json`, the steering file under `.kiro/steering/`, and
every skill under `.kiro/skills/<name>/`.

Every GitHub Release also ships a pre-built Kiro overlay
(`corezoid-kiro-vX.Y.Z.zip`) — extract directly over your workspace's
`.kiro/` to skip the clone step.

## What it does

- **Process JSON CRUD** — pull, push, create, modify, lint, and validate
  `.conv.json` process files.
- **Process design** — start from a connector or logic template; lift
  existing Corezoid processes from the cloud and edit them locally.
- **State diagrams** — design and edit state-machine processes
  (`conv_type: "state"`).
- **Project review** — audit a process for orphaned nodes, noop conditions,
  unused params, hardcoded constants, missing error edges.
- **Stage-export scanning** — offline static validator for exported
  `.zip` stages: detects non-active processes, broken intra-process node
  links, broken cross-process `conv_id` references. Maps to the
  platform's merge "Errors list" messages.
- **Dashboards** — column / pie / funnel / table charts pinned to process
  nodes; configures real-time and drill-down.
- **Access & variables** — manage user groups, API keys, object/folder
  sharing, environment variables.

## MCP tools (highlights)

| Tool | What it does |
|---|---|
| `login` | OAuth2 browser login; saves credentials to `~/.corezoid/`. |
| `pull-process` / `pull-folder` | Export a process / folder tree locally. |
| `push-process` | Validate and deploy a `.conv.json`. |
| `lint-process` | Static checks: orphaned nodes, noop conditions, unused params. |
| `run-task` | Execute a task on a deployed process. |
| `create-process` / `create-folder` / `create-variable` | Bootstrap resources. |
| `create-dashboard` / `add-chart` | Visualise node metrics. |
| `list-workspaces` / `list-projects` / `list-stages` | Workspace navigation. |
| `modify-task` / `delete-task` | Per-task ops on deployed processes. |
| `send-feedback` | Submit feedback/bug reports to the Corezoid team. |

## Skills

Each skill is auto-loaded from `.kiro/skills/<name>/SKILL.md`:

- `corezoid` — universal entry point + routing.
- `corezoid-init` — first-time environment setup.
- `corezoid-create` / `corezoid-edit` — process JSON authoring.
- `corezoid-review` / `corezoid-project-review` — single-process / whole-project audits.
- `corezoid-state-diagram-create` / `corezoid-state-diagram-edit` — state-machine processes.
- `corezoid-process-optimizer` — tact reduction, resilience patterns.
- `corezoid-process-tech-writer` — generate human-readable docs.
- `corezoid-dashboard-manager` — dashboards and chart wiring.
- `corezoid-access` — groups, API keys, sharing.
- `corezoid-variable-manager` — env vars and `{{env_var[@name]}}` references.
- `corezoid-alias-manager` — process aliases.
- `corezoid-api-connector` — external API wrap templates.
- `corezoid-stage-scan` — exported-stage merge validator.
- `corezoid-feedback` — bug reports and quality signals to the Corezoid team.
- `marketplace-publish-validation` — pre-publish checklist.

## Same codebase, three hosts

This repository ships the same plugin payload to:

- **Claude Code** — via `claude plugin install corezoid@corezoid`.
- **Codex** — via `codex plugin install corezoid@corezoid`.
- **AWS Kiro** — via this Power.

One Git tag → one GitHub Release → artifacts for all three hosts.
