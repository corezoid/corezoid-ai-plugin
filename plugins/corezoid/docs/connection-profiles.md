# Connection profiles (multi-environment switching)

> Status: **proposal / convention**. The `registry.example.json` template ships now; the
> MCP-server wiring described under "Implementation notes" is the developer task this PR asks for.

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

## Implementation notes (for the MCP server — the ask of this PR)

The mechanism to swap the auth globals already exists: `handleLogin` reloads
`accountURL`/`workspaceID`/`stageID`/`apiToken` under `withAuthLock`. A profile feature can reuse it:

1. Load `~/.corezoid/profiles/registry.json` (fall back to `.env` if absent — fully backward compatible).
2. Add a `use-profile` tool (and/or a resolver helper) that takes a signal, picks a profile per the
   rules above, and writes its coordinates into the globals via `withAuthLock` — no OAuth needed.
3. Keep `is_prod` confirmation as a guardrail.

No per-call `env` argument is needed: the established workflow is one chat = one environment, so
resolve-and-lock at session start is enough.
