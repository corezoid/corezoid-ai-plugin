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
2. **OAuth2** — browser window opens for authentication, token saved to `.env`
3. **Workspace picker** — fetches available workspaces and shows a dropdown, saves `WORKSPACE_ID` to `.env`
4. **Stage picker** — lists projects then stages for selection, saves `COREZOID_STAGE_ID` to `.env`

When `login` returns "Setup complete", proceed to **Step 2**.

---

## Mode B — Elicitation not supported (chat-based collection)

When elicitation is unavailable, drive the setup yourself using explicit tool calls. Follow this sequence **exactly** — never pick a workspace, project, or stage on behalf of the user. Always present the full list and wait for the user's explicit choice.

### B1 — Collect Account URL

→ Ask the user: **"What is your Corezoid Account URL? (e.g. https://account.corezoid.com)"**

→ Call `login(account_url=<value>)`

The tool opens a browser for OAuth2 authentication and saves the token.

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

**Workaround — populate `.env` manually before calling `login`:**

1. Get `ACCESS_TOKEN` from the account UI at `https://<host>/access_tokens` (create a token manually)
2. Write `.env` in `COREZOID_WORK_DIR` (the directory where Claude Code was opened):

```
ACCOUNT_URL=https://<host>
COREZOID_API_URL=https://<host>
WORKSPACE_ID=<company_id>
ACCESS_TOKEN=<token>
COREZOID_STAGE_ID=<stage_id>
```

3. Restart the MCP server so it picks up the new `.env`:
```bash
ps aux | grep "go run\|convctl" | grep -v grep | awk '{print $2}' | xargs kill
```

4. Call `login` — it will detect `ACCESS_TOKEN` in `.env`, skip OAuth, and complete setup.

---

## `.env` file location

The Go MCP server reads `.env` from `COREZOID_WORK_DIR` — the directory where Claude Code was opened when the server started (typically `$HOME`). This is **not** the `mcp-server/` source directory.

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

| Variable | Set during |
|---|---|
| `ACCOUNT_URL` | login step 1 — API URL prompt |
| `COREZOID_API_URL` | login step 2.5 — derived from account clients API |
| `ACCESS_TOKEN` | login step 2 — OAuth2 (or manually for on-prem) |
| `WORKSPACE_ID` | login step 3 — workspace selection |
| `COREZOID_STAGE_ID` | login step 4 — stage selection |
| `COREZOID_OAUTH_CLIENT_ID` | pre-login (on-prem only) — OAuth2 client ID for deployments with a custom authorization server; cloud users do not need this |
