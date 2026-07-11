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

## Step 0 — Verify the connection is alive

Call MCP tool **`status`** first (no auth needed). If it answers, you get the
server's version, config and token state — react to any ⚠ it prints. If the
call fails with **"No such tool available"**, the MCP server was restarted
after this session connected and the session's tool handles are dead. Do NOT
continue silently — tell the user (in the language of the conversation):
*"The connection to the corezoid MCP server was lost (the server was
restarted). Restart the Claude Code session entirely — a plain /mcp reconnect
may not be enough — and re-run the command."* Then stop.

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

## Exception: user provides values directly

If the user explicitly pastes values, write them to `.env` and skip the corresponding prompts:

```
COREZOID_API_URL=<value>
WORKSPACE_ID=<value>
COREZOID_STAGE_ID=<value>
```

Then call `login` — it will skip already-set values and only prompt for what's missing.

---

## Manual token setup (on-prem, or when the browser flow fails)

On private Corezoid installations the OAuth2 browser flow may not complete —
`localhost` may not be registered as an allowed `redirect_uri` (issue #7), and
the browser lands on the workspace dashboard instead of redirecting back. The
`login` tool now returns the consent URL and these same instructions when the
wait fails; you can also set the token up manually at any time.

**Where the token lives — this is the #1 confusion:**

The Access Tokens page is on the **ACCOUNT host**, not the admin/workspace UI:

- Cloud: `https://account.corezoid.com/access_tokens`
- On-prem: `https://<account-host>/access_tokens`

⚠ Opening `/access_tokens` on the **admin** host just redirects to the
dashboard — that is expected SPA behavior, NOT a missing page. You are on the
wrong host; switch to the account host.

The created token is **JWT-format** (starts with `eyJ...`) — that exact string
is your `ACCESS_TOKEN`.

⚠ **Users & Groups → API keys are NOT a token source for this MCP.** Those are
`api_login`/`api_secret` pairs for classic SHA-1 request signing, which this
MCP does not support today. Only Access Tokens (JWT) work.

**Manual setup steps:**

1. Create a token on the ACCOUNT host's `/access_tokens` page and copy it.
2. Write it to `~/.corezoid/credentials`:

```
ACCESS_TOKEN=<token>
```

3. Write project config to `.env` in `COREZOID_WORK_DIR` (the directory where Claude Code was opened):

```
ACCOUNT_URL=https://<account-host>
COREZOID_API_URL=https://<admin-host>
WORKSPACE_ID=<company_id>
COREZOID_STAGE_ID=<stage_id>
```

4. Restart the MCP server so it picks up the changes:
```bash
ps aux | grep "go run\|convctl" | grep -v grep | awk '{print $2}' | xargs kill
```

5. Call `login` — it validates the token with a probe, skips OAuth, and completes setup.

---

## ACCOUNT_URL vs COREZOID_API_URL — do not mix them up

| Variable | What it is | Cloud | Single-host on-prem |
|---|---|---|---|
| `ACCOUNT_URL` | Auth/account service (OAuth consent, `/access_tokens` page) | `https://account.corezoid.com` | `https://<host>` |
| `COREZOID_API_URL` | Corezoid API host (BASE URL — **no** `/api/2/json` suffix) | `https://admin.corezoid.com` | `https://<host>` |

`admin.corezoid.com` is **never** a valid `ACCOUNT_URL` — the admin UI has no
OAuth endpoints, so the browser would open the dashboard instead of the consent
page. `login` detects and reports this misconfiguration, but do not write it in
the first place. A `/api/2/json` suffix in `COREZOID_API_URL` is stripped
automatically with a warning — the server appends API paths itself.

---

## ⚠ logout destroys credentials — read before running it

`logout` removes `ACCESS_TOKEN` from `~/.corezoid/credentials` AND from the
project `.env`. **On installations where re-authentication is unavailable
(broken browser OAuth, no account-host access), that token may be your ONLY
credential — do not logout without a spare.**

`logout` writes a backup to `~/.corezoid/credentials.bak` before deleting.

**Recovery procedure after an accidental logout:**

1. `cp ~/.corezoid/credentials.bak ~/.corezoid/credentials`
2. Check `.env`: `COREZOID_API_URL` must be the BASE URL (no `/api/2/json`),
   `ACCOUNT_URL` must be the account host (see the table above).
3. Restart the MCP server (kill the `convctl` process) and re-run `login`.

If there is no backup, create a fresh token on the account host's
`/access_tokens` page (see Manual token setup above).

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
