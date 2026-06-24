# Connection profiles (multi-environment switching)

> Status: **implemented**. Ships the `use-profile` MCP tool + `registry.example.json` template.

## Problem

The MCP server holds a single set of auth globals (`accountURL`, `apiURL`, `workspaceID`,
`stageID`, `apiToken`) loaded once at startup from the nearest `.env`
(`loadConfig` → `findAndLoadDotEnv` in `main.go`). Every tool call reads that single set
via `authSnapshot()` (`executor.go`). Consequences:

- One running process = one environment. Switching dev↔prod or market↔market requires
  swapping `.env` **and restarting** the process.
- The server has no concept of "which environment" — only "the nearest `.env`". Running
  from sibling folders that share a parent `.env` silently collapses several environments
  into one config (a real footgun teams hit).

## Concept: a "connection profile"

A **profile** is a named connection target — the generic abstraction, like `kubectl context`
or `aws --profile`. A market, a project, a prod/dev pair are all just profiles. A profile is:

```
account_url + workspace_id + stage_id  (coordinates)
+ match rules                          (how to recognise it from a signal)
+ is_prod flag                         (the one piece of semantics the server needs, for guardrails)
```

Tokens are **not** part of the profile — they stay per-user (`~/.corezoid/credentials` or the
profile's own `.env`), so the registry can be shared/committed within a team without leaking secrets.

## Registry

A single data file lists all profiles: `~/.corezoid/profiles/registry.json`
(template: `mcp-server/registry.example.json`). It is plain data — adding a new environment
is a registry edit, no code change.

## Resolution rules

Given a signal, the resolver picks one profile by matching, in priority order:

1. **Host** in a Corezoid URL (e.g. a process/env link) → `match.hosts`
2. **JIRA key prefix** (e.g. `PROJ-123`) → `match.jira_prefix`
3. **Alias / free text** (e.g. "the dev market") → `match.aliases`

Safety (dev is the safe zone):

- If a prefix/alias maps to several profiles, the **`is_prod:false`** one wins **unless** the
  signal explicitly names prod or carries a prod host.
- Activating an `is_prod:true` profile requires explicit user confirmation.
- Resolve once per session (on the first signal) and lock — not per-call switching.

## Setup behaviour

- `login`/setup writes config into a **new** profile folder; it must **never** silently merge a
  second environment into an existing profile. Merging multiple configs into one profile is
  only allowed on an explicit flag.

## The `use-profile` tool

```
use-profile profile=az-dev            # by registry key
use-profile signal=AZ-123             # by JIRA prefix
use-profile signal=https://host/...   # by Corezoid URL host
use-profile profile=az-prod confirm=true   # production requires confirm
```

It loads the resolved profile's coordinates into the running server's auth globals via
`withAuthLock`, and the `ACCESS_TOKEN` from the profile's `env_file` if set (otherwise the
current session token is kept). Crucially it does **not** write the shared `cwd/.env` — so two
chats rooted in the same directory no longer clobber each other's environment.

Implemented in `mcp-server/mcp_handlers_profile.go` (`loadProfileRegistry`, `resolveProfile`,
`handleUseProfile`); registered in `mcp_handlers.go` and `tools_registry.go`. The registry is
optional — with no `registry.json`, behaviour is unchanged (the server still uses `.env`).

No per-call `env` argument is needed: the established workflow is one chat = one environment, so
resolve-and-lock at session start is enough.
