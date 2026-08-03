# Hosted HTTP mode — what is missing

The Streamable HTTP transport (`COREZOID_HTTP_PORT`) is a **local, single-user**
transport. It is safe on loopback and unsafe anywhere else, which is why
`resolveHTTPBindAddr` in [`mcp_http.go`](mcp_http.go) refuses a non-loopback
bind unless `COREZOID_ALLOW_UNAUTHENTICATED_REMOTE=yes-i-know-there-is-no-auth`
is set.

This file records what a real hosted mode would need, so the escape hatch is
never mistaken for one. None of it is implemented today.

## Why the current transport is not hostable

| Property | Today | Needed for hosting |
|---|---|---|
| Request authentication | None — `/mcp` accepts any caller | Bearer token verification on every request |
| Credentials | One process-global `apiToken`, loaded at startup and shared by every request | Per-tenant credentials resolved from the caller's identity |
| Sessions | `Mcp-Session-Id` tracks client *identity* for analytics only; it grants nothing and is not a secret | Session bound to an authenticated principal |
| Login | Browser OAuth writes to the server's own credential file | Per-user OAuth, never a server-side shared file |
| Abuse control | None | Rate limiting and quotas per tenant |
| Audit | Local log file, no per-caller attribution | Structured audit log keyed by principal |

## Work items

1. **Bearer middleware.** Verify an `Authorization: Bearer` token on `/mcp`
   before dispatch; reject with `401` plus a `WWW-Authenticate` challenge.
2. **OAuth Protected Resource Metadata.** Serve
   `/.well-known/oauth-protected-resource` per RFC 9728 so MCP clients can
   discover the authorization server, as the MCP authorization spec expects.
3. **Per-tenant credential resolution.** Replace the process-global `apiToken`
   read (`authSnapshot`) on the HTTP path with a per-request credential
   resolved from the authenticated principal. This is the largest change: the
   executor and every handler currently assume one ambient identity.
4. **Session hardening.** Bind `Mcp-Session-Id` to the authenticated principal,
   reject cross-principal reuse, and shorten the idle timeout from the current
   one hour.
5. **Rate limiting and quotas** per principal, so one tenant cannot exhaust the
   Corezoid API budget of another.
6. **Audit logging** with principal attribution, distinct from the local debug
   log, plus redaction review of what the log already writes.
7. **Disable the local-only tools** on the hosted path: `login` (browser OAuth),
   and anything that reads or writes the server's own working directory
   (`layout-process`, `lint-process`, the `*-context-file` git tools) has no
   coherent meaning when the filesystem is not the user's.
8. **Re-scope the bind guard** once the above lands: the opt-in env var becomes
   unnecessary and should be replaced by a positive `COREZOID_HOSTED_MODE`
   switch that requires the auth configuration to be present.

## Related

- Bind guard and rationale: `resolveHTTPBindAddr` in [`mcp_http.go`](mcp_http.go)
- User-facing documentation: the "HTTP transport (local only)" section of the
  [root README](../../../README.md)
