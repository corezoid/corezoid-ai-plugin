# Smart API Connector registration

After a successful positive test, send ONE task to the registration receiver. The receiver
(Simulator side) creates or updates the Smart API actor, its Service, Path, accounts and
dashboards. The plugin needs no knowledge of Simulator — only the receiver process id,
resolved fresh every time.

## 1. Find the receiver (always in the current workspace, by names — no hardcoded ids)

Nothing here is environment-specific or hardcoded — resolve all of it by name, in the
workspace the user is currently working in:

- **Project** — `cz-structure` `list-projects`, match `short_name = "smart-api"`.
- **Stage** — `cz-structure` `list-stages` inside that project, match `short_name =
  "production"` — always production, regardless of which stage the connector itself is
  being built on.
- **Alias** — `cz-structure` `list-aliases` inside that project/stage with `short_name =
  "api-gw-create-smart-api"` (see `/corezoid-alias-manager`) → its `obj_to_id` is the
  receiver process.
- **Receiver process** — the `obj_to_id` returned for that alias → its `conv_id`.

If the project, the stage, the alias, or access to any of them is missing — this is an
**expected outcome, not an error**: the user running this skill may simply have no access to
the Smart API project, or the project/stage/alias may have been renamed or removed since this
doc was written. **Never block**, and don't guess which of the two it is:

> The connector is ready and tested. Smart API was not registered: <what is missing> was not
> found (missing, renamed, or no access).

Finish the skill normally.

## 2. Call the receiver

`run-task` into the receiver process resolved in Step 1 (by `conv_id`; directly by `alias` +
`project` + `stage` once the plugin supports that), **exactly once**. Synchronous — wait for
the Reply.

Whatever comes back — success, a rejected param, a timeout, no right to run tasks in the
receiver process, or no answer at all — report it to the user verbatim (see `/corezoid-access`
if it is a rights issue) and **stop there**: the connector stays ready either way. Do **not**
retry with the same or a different payload, do not pull or inspect the receiver process or any
of its sub-processes to work out why it was rejected, and do not guess at field names the
receiver might actually want. The exact internal contract of the receiver is not something
this skill owns or can reliably reverse-engineer in-session — a rejection is information to
hand back to the user, not a debugging task.

## 3. Task data

Best-effort — the receiver's exact internal validation is not fully confirmed, and it has
been observed to reject this shape (e.g. an unconfirmed `user` object requirement). Send it
as-is; a rejection is an expected possible outcome handled by Step 2, not a sign this table
needs fixing on the fly.

Task ref = `conv_id` of the connector: a repeated send updates the Smart API, it never
creates a duplicate.

Before sending, call `cz-structure` `show-process` with `process_id` = the connector's own
`conv_id` (the process just built and tested — not the receiver) to resolve `owner_id`/
`owner_login`.

| Field | Value |
|---|---|
| `processUuid` | system UUID of the process (same on all stages; field `uuid` of the process object). Until the plugin can read it, send empty — the receiver falls back to `processId` |
| `processId` | `conv_id` of the connector on this stage |
| `stage` | stage short name (`develop`, `production`, …) |
| `processName` | current process name |
| `providerName` | provider (Service) name from the contract |
| `host` | base URL of the Target API (resolved value of the host variable) |
| `path` | endpoint path |
| `method` | HTTP method |
| `args` | input params from the contract (names, types, required) |
| `nodeId` | canonical id of the API Call node (after `pull-process`) |
| `userId` | author — becomes Process Owner |
| `ownerId` | `owner_id` of the connector process, from `show-process` |
| `ownerLogin` | `owner_login` of the connector process, from `show-process` |
| `workspaceId` | Simulator workspace |
| `contract` | the confirmed contract (input, output, errors) |
| `test` | `task_ref`, `tested_at`, `result`, `http_code` from the test |

## 4. Answer

Receiver Reply (format of `reply-format.md`): `result` + `smart_api_id` — show the id to the
user. Accounts and dashboards are created asynchronously after the answer.

Anything else — an error reply, `run-task`'s own timeout report (the node the task is parked
at), or no answer — is reported to the user exactly as returned, and the skill finishes there:
the connector stays ready, Smart API was not registered. Do not poll further with
`list-task-history`/`show-task` to see if it eventually completes, and do not retry the send —
see the "never block" rule in Step 2.
