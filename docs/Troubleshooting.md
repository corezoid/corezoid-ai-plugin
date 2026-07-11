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

**Headless / remote environments:** Write `ACCESS_TOKEN` to `~/.corezoid/credentials`:

```
ACCESS_TOKEN=<your-token>
```

---

### `ACCESS_TOKEN` expired

The token's expiry is stored in `~/.corezoid/credentials` as `ACCESS_TOKEN_EXPIRES_AT`. If the server reports an expired token, run the `login` MCP tool again — it will overwrite the stale token automatically.

To check expiry:

```bash
grep ACCESS_TOKEN_EXPIRES_AT ~/.corezoid/credentials
```

---

### Port already in use during OAuth callback

The OAuth callback server picks a random free port automatically. If it still fails, ensure no firewall rule blocks loopback connections on ephemeral ports (1024–65535).

---

### Credentials not loaded

The MCP server loads credentials and config from two places:

| File | Contents |
|------|----------|
| `~/.corezoid/credentials` | `ACCESS_TOKEN`, `ACCESS_TOKEN_EXPIRES_AT` |
| `<project>/.env` | `WORKSPACE_ID`, `COREZOID_STAGE_ID`, `COREZOID_API_URL`, etc. |

The project `.env` is found by walking up from `$COREZOID_WORK_DIR` (the directory where Claude Code was opened), stopping at the project root (directory containing a `*stage.json` file). Make sure you are running Claude Code from inside a pulled Corezoid workspace.

To set variables explicitly without a file:

```bash
export ACCESS_TOKEN=...
export COREZOID_API_URL=...
export WORKSPACE_ID=...
export COREZOID_STAGE_ID=...
```

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

- `ACCESS_TOKEN` is missing or expired → re-run `login`.
- `WORKSPACE_ID` or `COREZOID_STAGE_ID` points to a workspace/stage you do not have access to.

---

### `run-task` times out

The default task timeout is determined by the process configuration in Corezoid. If a task never leaves the queue, check that the process is deployed and active in the correct stage.

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

Either `ACCESS_TOKEN` is absent from `~/.corezoid/credentials`, or the token was not loaded because the server started before the credentials file existed. Restart the MCP server or run the `login` tool to authenticate.

---

## Workspace / stage setup

### `list-workspaces` returns empty list

Personal accounts have no organization workspace. In this case `WORKSPACE_ID` should be left empty; the plugin uses the personal workspace automatically.

### No stages visible after login

Stages are attached to a specific workspace. Confirm `WORKSPACE_ID` is set correctly, then run `list-stages` again.

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

Credentials are split across two files:

| File | Permissions | Contents |
|------|-------------|----------|
| `~/.corezoid/credentials` | `0600`, dir `0700` | `ACCESS_TOKEN`, `ACCESS_TOKEN_EXPIRES_AT` — shared across all projects |
| `<project>/.env` | `0600` | `WORKSPACE_ID`, `COREZOID_STAGE_ID`, `COREZOID_API_URL` — project-specific |

The token file lives outside the project tree so it can never be accidentally committed to git. The project `.env` contains no secrets and can be shared within a team.

To fully log out and remove the stored token, run the `logout` MCP tool. It removes `ACCESS_TOKEN` and `ACCESS_TOKEN_EXPIRES_AT` from `~/.corezoid/credentials` AND from the project `.env`, and writes a backup to `~/.corezoid/credentials.bak` first — restore with `cp ~/.corezoid/credentials.bak ~/.corezoid/credentials` if you logged out by mistake. ⚠ On installations where re-authentication is unavailable, that backup may be your only credential.

**"The browser opens the admin dashboard instead of the consent page"** — your `ACCOUNT_URL` points at the admin UI host (e.g. `https://admin.corezoid.com`). The admin UI has no OAuth endpoints. Set `ACCOUNT_URL` to the account service (cloud: `https://account.corezoid.com`) and keep the admin host in `COREZOID_API_URL`. `login` detects and reports this misconfiguration.

**"`/access_tokens` redirects to the dashboard"** — you opened it on the admin host. The Access Tokens page lives on the ACCOUNT host (cloud: `https://account.corezoid.com/access_tokens`). The redirect is expected SPA behavior, not a missing page.
