---
name: corezoid-init
description: >
  Corezoid environment setup specialist. Use when the user wants to connect to
  Corezoid, set up credentials, authenticate, pull a project, configure the
  environment, or start working with a Corezoid project for the first time.
  Activate when the user says "init", "setup", "connect to corezoid", "login",
  "pull workspace", "configure environment", or "get started".
---

# Initialize Corezoid Environment

You are a specialist in setting up the Corezoid working environment using the `corezoid` MCP server.

## Step 1 — Call `login`

Call MCP tool **`login`** with no arguments. It will guide setup in one of two modes depending on whether the client supports MCP elicitation.

---

## Mode A — Elicitation supported (interactive forms)

The `login` tool handles everything automatically in sequence:

1. **API URL prompt** — interactive form asking for `ACCOUNT_URL`
2. **OAuth2** — browser window opens for authentication, token saved to `~/.corezoid/credentials`
3. **Workspace picker** — fetches available workspaces and shows a dropdown, saves `WORKSPACE_ID` to `.env`
4. **Stage picker** — lists projects then stages for selection, saves `COREZOID_STAGE_ID` to `.env`

When `login` returns "Setup complete", proceed to **Step 2**.

---

## Mode B — Elicitation not supported (chat-based collection)

When elicitation is unavailable, drive the setup yourself using explicit tool calls. Follow this sequence **exactly** — never pick a workspace, project, or stage on behalf of the user. Always present the full list and wait for the user's explicit choice.

### B1 — Collect Account URL

→ Ask the user: **"What is your Corezoid Account URL? (e.g. https://account.corezoid.com)"**

→ Call `login(account_url=<value>)`

The tool opens a browser for OAuth2 authentication and saves the token to `~/.corezoid/credentials`.

### B2 — Select Workspace

→ Call **`list-workspaces`**

→ Show the full workspace list to the user. **Ask the user to choose** — do not select automatically.

→ Wait for the user's answer before proceeding.

### B3 — Select Project

→ Call **`list-projects(company_id=<workspace_id>)`** using the workspace the user chose.

→ Show the full project list to the user. **Ask the user to choose** — do not select automatically.

→ Wait for the user's answer before proceeding.

### B4 — Select Stage

→ Call **`list-stages(project_id=<id>, company_id=<workspace_id>)`** using the project the user chose.

→ Show the full stage list to the user. **Ask the user to choose** — do not select automatically.

→ Wait for the user's answer before proceeding.

### B5 — Commit selection

→ Call `login(workspace_id=<workspace_id>, stage_id=<stage_id>)`

When `login` returns "Setup complete", proceed to **Step 2**.

---

## Step 2 — Pull the project

After `login` returns "Setup complete", call MCP tool **`pull-folder`** with:
- `folder_id`: value of `COREZOID_STAGE_ID` (now set in `.env`)

Do not proceed until the tool returns successfully.

---

## Step 3 — The project index is auto-built (no extra action)

`pull-folder` itself rebuilds the project index at the end of a successful
pull — you do **not** need to call `build-project-index` separately. The tool
appends a short "Project index refreshed at ... — processes: N, edges: M"
line to its output. Surface that line to the user verbatim so they see the
new counts (and, on first creation, the notice about the CLAUDE.md
auto-block).

This is a code-level guarantee inside the MCP server, not a skill-prompt
step to remember — see `handlePullFolder` in the plugin's `mcp-server/`. The
same code-level auto-rebuild also runs at the end of `push-process`, so
`corezoid-edit` and `corezoid-create` do not need to call
`build-project-index` after deploy either.

If the auto-rebuild line reads "Warning: project index build failed ..." (a
locked-down filesystem, a partial pull, or similar), tell the user, but do
**not** block init — the pull itself succeeded and the project is usable
without an index. The user can retry by invoking the `corezoid-index` skill.

---

## Exception: user provides values directly

If the user explicitly pastes values, write them to `.env` and skip the corresponding prompts:

```
COREZOID_API_URL=<value>
WORKSPACE_ID=<value>
COREZOID_STAGE_ID=<value>
```

Then call `login` — it will skip already-set values and only prompt for what's missing.

---

## Exception: OAuth fails on private on-prem instances

On private Corezoid installations, the OAuth2 browser flow may time out because `localhost` is not registered as an allowed `redirect_uri` (see issue #7). Symptom: browser opens the workspace UI instead of redirecting back.

**Workaround — populate credentials manually before calling `login`:**

1. Get `ACCESS_TOKEN` from the account UI at `https://<host>/access_tokens` (create a token manually)
2. Write the token to `~/.corezoid/credentials`:

```
ACCESS_TOKEN=<token>
```

3. Write project config to `.env` in `COREZOID_WORK_DIR` (the directory where Claude Code was opened):

```
ACCOUNT_URL=https://<host>
COREZOID_API_URL=https://<host>
WORKSPACE_ID=<company_id>
COREZOID_STAGE_ID=<stage_id>
```

4. Restart the MCP server so it picks up the changes:
```bash
ps aux | grep "go run\|convctl" | grep -v grep | awk '{print $2}' | xargs kill
```

5. Call `login` — it will detect `ACCESS_TOKEN` in `~/.corezoid/credentials`, skip OAuth, and complete setup.

---

## Credential and config file locations

Credentials and project config are stored in two separate files:

| File | Contents | Notes |
|------|----------|-------|
| `~/.corezoid/credentials` | `ACCESS_TOKEN`, `ACCESS_TOKEN_EXPIRES_AT` | User-level; shared across all projects; never in git |
| `<COREZOID_WORK_DIR>/.env` | `WORKSPACE_ID`, `COREZOID_STAGE_ID`, API URLs | Project-level; one per workspace |

`COREZOID_WORK_DIR` is the directory where Claude Code was opened when the MCP server started (typically the project root). This is **not** the `mcp-server/` source directory.

The MCP server loads `~/.corezoid/credentials` first, then the project `.env`. A token in `.env` overrides the user-level one — useful for environments that manage credentials externally.

---

## `COREZOID_API_URL` format

⚠️ `COREZOID_API_URL` must be the **base URL only** — no path suffix:

```
✅ COREZOID_API_URL=https://your-corezoid-host.example.com
❌ COREZOID_API_URL=https://your-corezoid-host.example.com/api/2/json
```

The server appends `/api/2/json` or `/api/2/download` automatically.

---

## Variables reference

| Variable | Stored in | Set during |
|---|---|---|
| `ACCOUNT_URL` | project `.env` | login step 1 — API URL prompt |
| `COREZOID_API_URL` | project `.env` | login step 2.5 — derived from account clients API |
| `ACCESS_TOKEN` | `~/.corezoid/credentials` | login step 2 — OAuth2 (or manually for on-prem) |
| `WORKSPACE_ID` | project `.env` | login step 3 — workspace selection |
| `COREZOID_STAGE_ID` | project `.env` | login step 4 — stage selection |
| `COREZOID_OAUTH_CLIENT_ID` | project `.env` | pre-login (on-prem only) — OAuth2 client ID for deployments with a custom authorization server; cloud users do not need this |
