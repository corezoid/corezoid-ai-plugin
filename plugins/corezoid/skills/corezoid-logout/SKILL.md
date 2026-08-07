---
name: corezoid-logout
description: >
  Log out of Corezoid: remove saved credentials for the current workspace from
  `~/.corezoid/config.json`. Activate when the user says "logout", "log out",
  "sign out", "disconnect", "выйти", "разлогинься", "разлогиниться",
  "отключись от corezoid", "убери токен", "clear credentials",
  "remove credentials", "forget account", or otherwise asks to end the
  Corezoid session for this working directory.
---

# Corezoid Logout Skill

You help the user disconnect from Corezoid by removing saved credentials for
the current workspace.

## What logout does

Calling the `logout` MCP tool removes the Folder entry that matches the
current working directory from `~/.corezoid/config.json`. That clears:

- OAuth `access_token` / `expires_at`
- `api_login` / `api_secret` (if API-key auth was used)
- `account_url`, `corezoid_url`, `workspace_id`, cached `project_id`
- `git_url` / `git_stage_path`

The on-disk stage marker (`<stage_id>_<name>.stage.json` in the workspace)
is **not** touched — it's checked into the workspace repo and stays.

Other Folders in `~/.corezoid/config.json` (for other working directories)
are left untouched.

## Step 1 — Call `logout`

Call MCP tool **`logout`** with no arguments.

## Step 2 — Report the result

On success the tool returns a message like:

```
Logged out. Folder entry removed from ~/.corezoid/config.json.

Important: your browser may still have an active SSO session at
<account_url>. If a subsequent login produces an already-expired token,
you must also log out of that site in your browser before calling login
again.
```

Relay the SSO warning to the user in the same language they used —
especially the part about the browser session, because a stale SSO
cookie is the most common cause of "logged out but next login still
fails" reports.

On error, show the error message and suggest the user check that
`~/.corezoid/config.json` is writable.

## After logout

To reconnect, the user should invoke the `corezoid-init` skill (or say
"login" / "setup") and start a fresh authentication flow.
