# Hosted HTTP mode — what would be required

**Status: placeholder, not a task list to execute now.** A real hosted deployment
is a separate future initiative. Nothing here is scheduled, and no part of it
should be implemented piecemeal — a half-built auth layer is worse than an
honestly local-only one.

## Where things stand today

The Streamable HTTP transport (`COREZOID_HTTP_PORT`) is **local-only**:

- It binds to `127.0.0.1` by default. `COREZOID_BIND_ADDR` can move it, but
  `resolveHTTPBindAddr` in `mcp_http.go` fails closed on anything that is not
  loopback unless the operator sets
  `COREZOID_ALLOW_UNAUTHENTICATED_REMOTE=yes-i-know-there-is-no-auth`.
- There is **no authentication**. `httpMCPEndpoint` serves every request that
  reaches it.
- `Authorization` is announced in the CORS allowlist (`corsWrap`) but its value
  is never read. That is intentional for now: MCP clients send the header, and
  refusing it in preflight would break them without adding any security. Do not
  mistake its presence for a check.
- Corezoid credentials are loaded once at startup into the process-global
  `apiToken`. Every request shares one identity — there is no tenant concept.
- `Mcp-Session-Id` is a plain identifier for client bookkeeping. It is not
  cryptographic, carries no credentials, and must never be treated as one.
- Nothing validates the request `Origin` or `Content-Type`. `corsWrap` only
  decides which responses are *readable* cross-origin — by default none are,
  because `COREZOID_HTTP_ALLOWED_ORIGINS` is unset and no
  `Access-Control-Allow-Origin` is sent — while `httpHandlePost` decodes the
  body whatever the content type says. A page open in the operator's browser
  can therefore POST to `http://127.0.0.1:<port>/mcp` with a CORS-safelisted
  `Content-Type: text/plain`, which needs no preflight, and the tool call
  runs. The attacker cannot read the reply, but write tools (`push-process`,
  the delete tools) still take effect. Browsers that enforce Private Network
  Access preflights block this; not all of them do.

For a single operator on their own machine this is an acceptable trade-off,
with one caveat worth stating plainly: loopback is reachable from the
operator's own browser, not only from their coding agent, so "local" is not
the same as "only my agent can call it". It is in any case not a hosted
deployment model. To expose the server today, put it behind a reverse proxy
(Nginx / Caddy / Cloudflare Access) that terminates TLS, authenticates the
caller, and rate-limits — the MCP server does not replace any of that.

## What a real hosted mode would need

1. **Fail-closed Bearer middleware** — reject every unauthenticated request with
   `401` and a proper `WWW-Authenticate: Bearer resource_metadata="..."`
   challenge. Default deny; no bypass env var.
2. **Per-tenant credential resolution** — resolve Corezoid credentials from the
   validated token per request instead of one process-global `apiToken`. This
   is the largest change: `apiToken`, `apiURL`, `workspaceID` and friends are
   package-level globals shared by every handler and by stdio mode.
3. **OAuth 2.0 Protected Resource Metadata** (RFC 9728) — serve
   `/.well-known/oauth-protected-resource` so MCP clients can discover the
   authorization server and complete the flow themselves.
4. **Rate limiting** — per token and per IP, on both tool calls and SSE streams.
5. **Audit log** — who called which tool against which workspace, and when,
   separate from the debug log and from analytics.
6. **TLS** — either terminated in-process or a documented, enforced proxy
   contract; plaintext bearer tokens on a routable interface are not acceptable.
7. **Session hardening** — cryptographically random session IDs bound to the
   authenticated principal, so a guessed `Mcp-Session-Id` grants nothing.
8. **Origin and content-type validation** — reject requests carrying a browser
   `Origin` the operator has not allowlisted, and require a JSON content type,
   so a web page cannot drive the endpoint on the operator's behalf. Unlike the
   rest of this list, this one would harden the local-only mode too — it is the
   only gap here that is reachable in the default loopback configuration.

Until all of the above exist, the bind guard stays fail-closed and the README
must keep describing HTTP mode as local-only.
