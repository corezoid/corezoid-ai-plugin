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

### If the user declines the project picker

Corezoid workspaces have two separate top-level containers, visible side-by-side in the UI sidebar: **Projects** (each with its own stages) and plain **Folders** (folders/processes/state diagrams that live directly under the workspace, outside any Project). If the user clicks **Decline** on the "Select your Corezoid project" form, `login` does not fall through to Projects/Stages — it shows one more confirmation:

> "Pull Corezoid Folders contents?" — Accept / Decline

- **Accept** → `login` immediately pulls everything under the workspace's root Folders (folder_id `0`) — folders, processes, state diagrams, dashboards, and aliases, excluding Projects — and finishes with "Setup complete", reporting how much it found (including whether any aliases exist). No `COREZOID_STAGE_ID` is set or needed for this mode; `pull-folder`, `pull-process`, `list-folders`, and `show-folder` all work with an explicit `folder_id`/`process_id` regardless of whether a stage is configured.
- **Decline** → nothing happens. `login` finishes with "Setup complete" and no stage is set. Don't offer a manual Stage-ID prompt unless the user asks for one directly.

Once no project/stage is selected, treat the workspace as stage-less for the rest of the session: pull further content directly with `pull-folder(folder_id=<id>)` (root Folders is `folder_id=0`), and note that stage-scoped write operations (e.g. `create-alias`) will still require a real stage/project if the user later needs them.

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

→ Wait for the user's answer.

→ **Immediately call `login(workspace_id=<chosen_id>)`** — do this before moving on to B3, even though a full "Setup complete" isn't expected yet (no stage is set). `list-projects` and `pull-folder` don't take a workspace/company argument of their own; they read `WORKSPACE_ID` from the saved config. Skipping this step is the single most common cause of "root Folders came back empty" — `pull-folder(folder_id=0)` in B3 would run against an unset workspace.

### B3 — Select Project

→ Call **`list-projects(company_id=<workspace_id>)`** using the workspace the user chose.

→ Show the full project list to the user, and also ask whether they'd rather skip Projects entirely and pull everything under the workspace's root **Folders** instead (folders/processes/state diagrams/dashboards/aliases that live directly under the workspace, outside any Project). **Ask the user to choose** — do not select automatically.

→ If the user picks "Folders" / declines to pick a project: call `pull-folder(folder_id=0)` directly — this needs only the OAuth token and `WORKSPACE_ID` (already saved in B2), no `COREZOID_STAGE_ID`. Skip B4/B5 and Step 2 entirely; the pull already happened. Note for later in the session: this workspace has no stage/project — further pulls use `pull-folder(folder_id=<id>)` / `pull-process(process_id=<id>)` directly, and stage-scoped write operations (e.g. `create-alias`) will need a real stage if the user picks one later.

→ Otherwise, wait for the user's project choice before proceeding.

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
