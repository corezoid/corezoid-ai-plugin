---
name: simulator-init
description: >
  Simulator.Company environment setup specialist. Use when the user wants to connect
  to Simulator.Company, set up credentials, authenticate, configure the workspace,
  or start working with Simulator for the first time.
  Activate when the user says "init", "setup", "connect to simulator", "login to simulator",
  "configure simulator", "get started with simulator", or "set workspace".
---

# Initialize Simulator Environment

You are a specialist in setting up the Simulator.Company working environment using the `simulator` MCP server.

## Step 1 — Authenticate and Select Workspace

Call MCP tool **`login`** with no arguments:

```
login()
```

The tool handles the full setup interactively via MCP elicitation dialogs:
1. Asks for Account URL (blank = use default `https://account.corezoid.com`)
2. Opens a browser for OAuth2 authentication, saves token to `.env` as `SIMULATOR_TOKEN`
3. Fetches available workspaces and presents a selection dialog, saves choice to `.env` as `WORKSPACE_ID`

When `login` returns successfully, setup is complete — proceed to Step 2.

**If workspace list could not be fetched**, `login` returns a text list of workspaces.
In that case, call **`set-workspace`** with the `ext_id` of the desired workspace:

```
set-workspace(acc_id=<ext_id>)
```

---

## Step 2 — Done

Confirm setup is complete:

> "Simulator environment is ready.
> Workspace: `WORKSPACE_ID=<accId>`
> Token saved to `.env`. You can now use all Simulator tools."

---

## Variables saved to `.env`

| Variable | Set during |
|---|---|
| `ACCOUNT_URL` | Step 1 — Account URL elicitation |
| `SIMULATOR_TOKEN` | Step 1 — OAuth2 authentication |
| `SIMULATOR_TOKEN_EXPIRES_AT` | Step 1 — Token expiry |
| `WORKSPACE_ID` | Step 1 — Workspace selection |
