# Process Lifecycle and Object Moves

This document describes the Corezoid behavior behind `pause-process`,
`resume-process`, `move-process`, and `move-folder`. The wire behavior was
verified against Corezoid UI/API v6.12.0.

## Safety model

These operations are never implicit cleanup or refactoring steps. Invoke them
only after the user directly identifies the object and asks to pause, resume,
or move it.

Every tool follows the same two-call workflow:

1. Call without `apply`, or with `apply=false`, to read live state and produce
   a dry-run.
2. Show the dry-run to the user and obtain explicit approval.
3. Re-run with `apply=true` and the exact `confirm` token from that dry-run.
4. The tool reads live state again, performs the mutation, and verifies the
   resulting status or parent with another `show` call.

The confirmation token includes the current state. A token becomes invalid if
another user or agent changes that state between dry-run and apply.

The token is a stale-state and accidental-invocation guard, not an
authentication boundary or cryptographic proof of human approval. The server
does not persist dry-run issuance, and a trusted MCP client can reconstruct a
token from live IDs/state. Human authorization therefore remains part of the
host/operator trust boundary: tool descriptions and skills require showing the
preview and obtaining approval, while the server independently validates the
exact state-bound argument and post-verifies the mutation. Do not treat the
ability to construct a token as permission to apply it.

## Pause and resume

Corezoid stores process status on the `conv` object. The minimal mutation is:

```json
{
  "type": "modify",
  "obj": "conv",
  "obj_id": 123,
  "company_id": "...",
  "status": "paused"
}
```

Use `status: "active"` to resume. The minimal operation is intentional: title,
description, graph, parameters, and deployment version are not resent and
therefore cannot be overwritten by a status change.

Observed behavior:

- `active -> paused` returns `is_changed: true`.
- Repeating `paused -> paused` returns `is_changed: false`.
- `paused -> active` returns `is_changed: true`.
- Repeating `active -> active` returns `is_changed: false`.
- Creating a new task while paused is rejected with
  `proc: "conveyor_is_not_active"` and description
  `"conveyor is not active"`.
- Creating a task after resume succeeds immediately.

Pause is admission control for new tasks. It is not a graph deploy, immutable
stage, task deletion, or a guarantee that tasks already running or parked in
nodes have stopped. Inspect such tasks separately with task-history/node-task
tools. Resume can expose the process to API callers, schedules, callbacks, and
other processes immediately.

Valid uses include an explicitly requested maintenance window, temporarily
preventing new calls while a known change is being coordinated, or observing
whether callers still depend on a candidate process. A review finding, an
apparently unused process, or completion of an edit is not authorization to
pause or resume it.

## Move process or folder

Corezoid represents a move as a folder link operation:

```json
{
  "type": "link",
  "obj": "folder",
  "obj_type": "conv",
  "obj_id": 123,
  "folder_id": 456,
  "parent_id": 111,
  "company_id": "..."
}
```

For a folder, use `obj_type: "folder"`. `folder_id` is the destination and
`parent_id` is the current parent. Destination `0` is the workspace root.

A move reparents the existing object. It preserves the process/folder ID and
does not copy, import, or deploy a graph. References that use the numeric
process ID therefore still address the same object.

### Current-parent guard

The Corezoid API accepts a stale `parent_id` and may echo that supplied value
as `from_folder`; the move response alone is not reliable proof of the actual
old location. The MCP tools therefore:

1. Read the current parent from `show conv` or `show folder`.
2. Read the destination's current parent and resolve the effective project and
   stage for both sides.
3. Include both parent values and both effective contexts in the confirmation
   token. This also invalidates the token if an ancestor moves either side to
   another project/stage while the immediate parent remains unchanged.
4. Send the freshly read parent in the link operation.
5. Read the object again and require its live parent to equal the destination.
6. Re-read the destination and require its parent and effective project/stage
   context to match the preflight values. If they changed concurrently, report
   that the object may already be moved and require a fresh review; never claim
   an unqualified success.

Example tokens:

```text
process#123:111->456@222:ctx=100/200->100/200
folder#789:111->456@222:ctx=100/200->100/200
```

Here `@222` means destination folder `456` currently belongs to `222`, and
`ctx=<project>/<stage>` records each side's effective context. Moving the
source, destination, or an ancestor across project/stage boundaries invalidates
the token. For workspace root, IDs in that side's context are zero.

### Folder hierarchy rules

`move-folder` moves only normal folders (`obj_type: 0`). Projects (`obj_type: 5`)
and stages (`obj_type: 10`) are rejected and remain under their dedicated
lifecycle controls. The MCP also recognizes legacy project/stage values `2/3`
for compatibility with older Corezoid responses.

Before a folder move, the tool walks the destination ancestry and rejects:

- moving a folder into itself;
- moving a folder into any descendant;
- an already-corrupt/cyclic ancestry chain;
- hierarchy depth beyond the defensive traversal limit.

The server also rejects a parent-to-child move with
`Move parent folder to child is not allowed`, but the client-side check keeps
that invalid mutation from reaching the API.

### Cross-stage and root moves

A move that changes project/stage context, or enters/leaves workspace root,
requires `allow_cross_stage=true` in addition to the exact confirmation token.
This is a separate acknowledgement because moving does not migrate or rewrite:

- stage-scoped aliases;
- environment-variable definitions;
- object access rules;
- deployment/immutable-stage behavior;
- any external inventory that assumes the previous location.

For a folder, these risks apply to every descendant. Review dependencies
before confirming.

### Local mirrors

Move tools change Corezoid server state only. They do not relocate local
`.conv.json` files or folder-marker directories, because a workspace may have
multiple mirrors/worktrees and choosing one automatically can destroy or
duplicate local work. Re-pull the destination, verify the new mirror, and only
then remove a stale local copy when explicitly requested.
