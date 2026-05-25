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

**Headless / remote environments:** Set `ACCESS_TOKEN` directly in your project `.env` file:

```
ACCESS_TOKEN=<your-token>
```

---

### `ACCESS_TOKEN` expired

The token's expiry is stored in `.env` as `ACCESS_TOKEN_EXPIRES_AT`. If the server reports an expired token, run the `login` MCP tool again or update `ACCESS_TOKEN` in `.env`.

To check expiry:

```bash
grep ACCESS_TOKEN_EXPIRES_AT .env
```

---

### Port already in use during OAuth callback

The OAuth callback server picks a random free port automatically. If it still fails, ensure no firewall rule blocks loopback connections on ephemeral ports (1024–65535).

---

### No `.env` file found / credentials not loaded

The server walks up the directory tree from `$COREZOID_WORK_DIR` (or the current directory) looking for `.env`, stopping at the project root (directory containing a `*stage.json` file).

Make sure you are running Claude Code from inside a pulled Corezoid workspace, or set all required variables explicitly:

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

Either `ACCESS_TOKEN` is absent from `.env`, or the token was not loaded because the server started before `.env` was created. Either restart the MCP server or run the `login` tool to authenticate.

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

Credentials are stored **project-locally** in `.env` in your workspace directory — never in `~/.corezoid` or any global location. The file is mode `0600` (owner read/write only).

`ACCESS_TOKEN` and `ACCESS_TOKEN_EXPIRES_AT` are the only credential keys written.

To fully log out and remove stored credentials, run the `logout` MCP tool or delete the relevant lines from `.env` manually.
