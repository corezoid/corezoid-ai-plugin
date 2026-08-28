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

1. **Account URL prompt** — interactive form asking for `account_url`
2. **OAuth2** — browser window opens for authentication; the token is saved to `~/.corezoid/config.json` (`access_token` field on the current Folder)
3. **Workspace picker** — fetches available workspaces and shows a dropdown; saves `workspace_id`
4. **Stage picker** — lists projects then stages for selection; the chosen stage is materialized on disk as `<id>_<name>.stage/<id>_<name>.stage.json` (the marker file — NOT stored in `config.json`)

### Interpreting the `login` response

- **`"Setup complete! ... Stage <N> selected."`** → stage was picked and pulled. Proceed to **Step 2**.
- **`"Setup incomplete: stage not selected. ..."`** → elicitation was cancelled or returned no stage. Follow the instructions in the message (usually: run `list-stages` + call `login(stage_id=…)` again). Do **not** fall back to Mode B — the token and workspace are already saved.

---

## Mode B — Elicitation not supported (chat-based collection)

When elicitation is unavailable, drive the setup yourself using explicit tool calls. Follow this sequence **exactly** — never pick a workspace, project, or stage on behalf of the user. Always present the full list and wait for the user's explicit choice.

### B1 — Collect Account URL

→ Ask the user: **"What is your Corezoid Account URL? (e.g. https://account.corezoid.com)"**

→ Call `login(account_url=<value>)`

The tool opens a browser for OAuth2 authentication and saves the token to the current Folder in `~/.corezoid/config.json`.

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

After `login` returns "Setup complete", call MCP tool **`pull-folder`** with no arguments — the MCP server resolves the stage automatically from the on-disk marker file. You do **not** need to know or pass `stage_id`.

Do not proceed until the tool returns successfully.

---

## Exception: user provides values directly

If the user pastes values (account URL, workspace ID, stage ID, or API key credentials), pass them to `login` as arguments — do **not** edit `~/.corezoid/config.json` by hand:

```
login(
  account_url=<value>,
  workspace_id=<value>,
  stage_id=<value>
)
```

`login` will persist them to the current Folder and only prompt for what's still missing.

---

## Exception: API key auth (login + secret)

When the user provides an **API key login and secret** instead of OAuth credentials, use API key auth. This is common on private/on-prem Corezoid instances where browser OAuth is not available.

**How to recognise:** user says something like "use login 12345 secret abc..." or "connect with API key".

**Exact steps — call `login` with `api_login` and `api_secret` as arguments:**

```
login(
  account_url=<host>,
  workspace_id=<company_id>,
  stage_id=<stage_id>,
  api_login=<login_id>,
  api_secret=<secret>
)
```

⚠️ **Critical:** pass `api_login` and `api_secret` **as arguments to the `login` tool** — do NOT edit `~/.corezoid/config.json` by hand. The tool writes them to the current Folder with the correct field names (`api_login`, `api_secret`). Hand-editing under wrong field names will break authentication.

The login tool will:
1. Skip the OAuth2 browser flow
2. Save `api_login` and `api_secret` to the current Folder in `~/.corezoid/config.json`
3. Set `corezoid_url` from `account_url` if not already set
4. Complete workspace/stage selection normally

---


## Credential and config file locations

All credentials and per-workspace auth state live in a user-level file. Stage identity (and normally project identity too) lives in the workspace itself, in an on-disk marker file:

| File | Contents | Notes |
|------|----------|-------|
| `~/.corezoid/config.json` | Array of Folders — one per working directory. Each Folder holds auth state: `account_url`, `corezoid_url`, `workspace_id`, `access_token`, `expires_at`, `api_login`, `api_secret`, `git_url`, `git_stage_path`, and a cached `project_id` used as a fallback when the marker's `parent_id` is missing. | Mode `0600`; never in git; secrets never leave the user's home directory. |
| `<workspace>/<stage_id>_<name>.stage.json` | Stage marker at the workspace root. Contains `obj_id` (stage_id) and `parent_id` (project_id). Materialized automatically by the stage's zip during `pull-folder`/auto-pull; parsed by every tool that needs stage/project identity. | Checked into the workspace repo; safe to share. |

The MCP server chooses which Folder to use by matching the current working directory (or `$COREZOID_WORK_DIR`, set by Claude Code / Codex / Kiro) against each `root_path` and picking the longest-prefix match. This means sub-agents launched from any subdirectory of the workspace pick up the right credentials automatically.

---

## `corezoid_url` format

⚠️ `corezoid_url` must be the **base URL only** — no path suffix:

```
✅ "corezoid_url": "https://your-corezoid-host.example.com"
❌ "corezoid_url": "https://your-corezoid-host.example.com/api/2/json"
```

The server appends `/api/2/json` or `/api/2/download` automatically.

---

## Fields reference

Every field below lives in the current Folder inside `~/.corezoid/config.json`. All are managed by the `login` tool — you should not hand-edit them unless a documented exception applies.

| Field | Set during |
|---|---|
| `account_url` | login step 1 — account URL prompt |
| `corezoid_url` | login step 2.5 — derived from account clients API (or fallback to `account_url` in API-key mode) |
| `access_token` / `expires_at` | login step 2 — OAuth2 (or manually for on-prem workaround) |
| `workspace_id` | login step 3 — workspace selection |
| `api_login` / `api_secret` | login arguments (API-key auth mode) |
| `project_id` | cached on first pull-folder / push-process |
| `git_url` / `git_stage_path` | cached on first git-pull-context |

`COREZOID_OAUTH_CLIENT_ID` remains an environment variable (not a Folder field) — pre-login only, on-prem deployments with a custom authorization server. Cloud users do not need it.

---

## Environment fallback (headless / CI)

Every field above can also come from the environment when the config file has nothing for it. Use this only where the interactive login cannot run — CI jobs, containers, the Streamable HTTP transport:

| Variable | Field |
|---|---|
| `COREZOID_ACCOUNT_URL` | `account_url` |
| `COREZOID_API_URL` | `corezoid_url` |
| `COREZOID_APIGW_URL` | `apigw_url` |
| `COREZOID_WORKSPACE_ID` | `workspace_id` |
| `COREZOID_PROJECT_ID` | `project_id` |
| `COREZOID_STAGE_ID` | `stage_id` |
| `COREZOID_ACCESS_TOKEN` | `access_token` |
| `COREZOID_TOKEN_EXPIRES_AT` | `expires_at` (RFC 3339) |
| `COREZOID_API_LOGIN` | `api_login` |
| `COREZOID_API_SECRET` | `api_secret` |
| `COREZOID_GIT_URL` | `git_url` |
| `COREZOID_GIT_STAGE_PATH` | `git_stage_path` |

Rules to keep in mind when diagnosing auth state:

- The **config file wins field by field**; a variable applies only where the matching Folder leaves that field empty, or when no Folder matches the cwd.
- **Credential pairs are the exception to that** — `COREZOID_API_LOGIN`/`COREZOID_API_SECRET` come from one source or neither, as do `COREZOID_ACCESS_TOKEN`/`COREZOID_TOKEN_EXPIRES_AT`. A login and a secret from different sources cannot authenticate, so the half-pair is refused instead of being sent.
- An **expired** stored `access_token` counts as missing, so `COREZOID_ACCESS_TOKEN` takes over.
- If a variable was **set and rejected** (malformed `*_ID`, half an API-key pair), the auth error names it. Read that before concluding the user never configured anything — re-running `login` will not help in a headless environment.
- Environment values are **never persisted** — they are used in memory only. Derived caches (`project_id`, `git_url`, `git_stage_path`) are still written as usual.
- `logout` removes the Folder but cannot unset variables; the server stays authenticated until they are removed from the environment. The `logout` response says so when this is the case.
- Minimal set: `COREZOID_ACCOUNT_URL` + `COREZOID_STAGE_ID` + (`COREZOID_ACCESS_TOKEN` or `COREZOID_API_LOGIN`/`COREZOID_API_SECRET`).
- `COREZOID_API_URL` is optional: it is **derived on the first authenticated operation** — from the account's clients endpoint with a token, or from `COREZOID_ACCOUNT_URL` with API-key credentials. If that lookup fails the error says so and names the variable; do not read it as "not logged in".

Do **not** propose this path for a normal desktop setup — `login` is better there (it discovers `corezoid_url`, lists workspaces/stages, and refreshes the token).
