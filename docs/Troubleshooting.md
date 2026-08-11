# Troubleshooting

Common problems and fixes for the Corezoid AI plugin.

---

## Authentication

### Browser did not open during `login`

The MCP server prints the authorization URL to stderr when it cannot open a browser:

```
If it did not open automatically, visit:
https://account.corezoid.com/oauth2/authorize?...
```

Copy that URL into a browser manually to complete the OAuth flow.

**Headless / remote environments:** Edit `~/.corezoid/config.json` and add or update the Folder for your working directory:

```json
{
  "version": 1,
  "folders": [
    {
      "root_path": "/absolute/path/to/your/workspace",
      "account_url": "https://account.corezoid.com",
      "corezoid_url": "https://admin.corezoid.com",
      "workspace_id": "<id>",
      "stage_id": <id>,
      "access_token": "<your-token>"
    }
  ]
}
```

File mode must be `0600`; directory `0700`.

---

### Access token expired

The token's expiry is stored on the Folder as `expires_at` (RFC3339). If the server reports an expired token, run the `login` MCP tool again — it will overwrite the stale token automatically.

To check expiry manually:

```bash
python3 -c 'import json; print([f["expires_at"] for f in json.load(open("$HOME/.corezoid/config.json"))["folders"]])'
```

---

### Port already in use during OAuth callback

The OAuth callback server picks a random free port automatically. If it still fails, ensure no firewall rule blocks loopback connections on ephemeral ports (1024–65535).

---

### Credentials not loaded

The MCP server loads all credentials and workspace config from a single file:

| File | Contents |
|------|----------|
| `~/.corezoid/config.json` | `folders[]` — one entry per working directory. Each has `account_url`, `corezoid_url`, `workspace_id`, `stage_id`, `access_token`, `expires_at`, `api_login`, `api_secret`, plus cached `project_id` / `git_url` / `git_stage_path`. |

The MCP server picks a Folder by matching the current working directory (or `$COREZOID_WORK_DIR`, set by Claude Code / Codex / Kiro) against each `root_path`; the longest-prefix match wins. Make sure your `root_path` is the absolute path where you are running the client.

There is no environment-variable override for `access_token`, `workspace_id`, etc. — all state lives in `~/.corezoid/config.json`.

---

## Process operations

### `push-process` fails with validation error

Check the error message for the specific rule that was violated. Common causes:

| Error | Fix |
|-------|-----|
| Node ID not 24-char hex | Regenerate the ID: `openssl rand -hex 12` |
| `extra` / `extra_type` mismatch | Every `extra` key must have a matching `extra_type` key with the correct type |
| Object value in `extra` not stringified | Serialize nested objects to a JSON string: `"{\"key\":\"val\"}"` |
| Missing `err_node_id` | Nodes that can fail (`set_param`, `api_rpc`, `api_code`, `api_copy`, `db_call`, `git_call`, `api_sum`, `api_reply`) require an `err_node_id` |
| Hardcoded URL or token | Replace with `{{env_var[@variable-name]}}` |

Run `lint-process` before pushing to catch most issues locally without an API call.

---

### `pull-process` / `pull-folder` returns 401 or 403

- `access_token` is missing or expired → re-run `login`.
- `workspace_id` or `stage_id` in the current Folder points to a workspace/stage you do not have access to.

---

### `run-task` times out

The default task timeout is determined by the process configuration in Corezoid. If a task never leaves the queue, check that the process is deployed and active in the correct stage.

---

### `run-task` reports "the deployed node list could not be read"

`run-task` never commits or deploys, so it works with run-only access and on immutable stages. To name the node a task settles on it additionally reads the deployed scheme; when that read is denied the task is still sent, and the summary reports `NodeName: (unknown)`. Follow the task with `list-task-history` or `list-node-tasks`, or ask for read access to the process to get the full report.

---

## MCP server

### MCP server does not start

1. Confirm Go ≥ 1.24 is installed: `go version`
2. Check that the `mcp-server` source compiles: `cd plugins/corezoid/mcp-server && go build ./...`
3. Look at the debug log: `cat /tmp/corezoid.log`

---

### Go toolchain auto-download hangs or fails

The `go.mod` specifies `go 1.24.0`. If your local Go installation is older, the Go toolchain manager will attempt to download `go1.24.0` from `proxy.golang.org` automatically. This can fail in air-gapped environments or stall on slow networks.

**Fix:** Install Go 1.24+ directly from [go.dev/dl](https://go.dev/dl/) and make sure `go version` reports `go1.24.x` or later.

To suppress automatic toolchain downloads entirely, set:

```bash
export GOTOOLCHAIN=local
```

With `GOTOOLCHAIN=local`, Go will use whatever version is installed and refuse to auto-download a newer one. The MCP server is compatible with any Go 1.24.x release.

---

### How to enable debug logs

The MCP server always writes debug output to `/tmp/corezoid.log` when running in MCP mode. In CLI mode, set `COREZOID_DEBUG=1`:

```bash
COREZOID_DEBUG=1 go run . pull-process process_id=123
```

---

### MCP tool returns "Not authenticated"

Either no Folder in `~/.corezoid/config.json` matches your current working directory, or the current Folder has no `access_token` (nor `api_login` + `api_secret`). Run the `login` MCP tool to authenticate.

---

## Workspace / stage setup

### `list-workspaces` returns empty list

Personal accounts have no organization workspace. In this case `workspace_id` on the current Folder should be left empty; the plugin uses the personal workspace automatically.

### No stages visible after login

Stages are attached to a specific workspace. Confirm `workspace_id` on the current Folder is set correctly, then run `list-stages` again.

---

## Common Corezoid API errors

| HTTP status | Meaning |
|-------------|---------|
| 401 | Token missing or invalid |
| 403 | Token valid but insufficient permissions for this workspace/stage |
| 404 | Process or folder ID does not exist in the selected stage |
| 422 | Validation error in the process JSON — check the error body for details |
| 429 | Rate limited — wait a few seconds and retry |
| 5xx | Corezoid API error — check [status.corezoid.com](https://corezoid.com) or retry |

---

## Where credentials are stored

All credentials and per-workspace config live in a single user-level file:

| File | Permissions | Contents |
|------|-------------|----------|
| `~/.corezoid/config.json` | `0600`, dir `0700` | `folders[]` — one entry per working directory. Each holds `account_url`, `corezoid_url`, `workspace_id`, `stage_id`, `access_token`, `expires_at`, `api_login`, `api_secret`, plus cached `project_id` / `git_url` / `git_stage_path`. |

The file lives outside every project tree so nothing can accidentally be committed to git.

Concurrent MCP-server processes (e.g. two IDE windows) serialise writes via `flock` on `~/.corezoid/config.json.lock` and atomic temp-file + rename — no lost updates.

To fully log out for the current working directory, run the `logout` MCP tool. It removes the matching Folder entry from `folders[]` (leaves other workspaces untouched).
