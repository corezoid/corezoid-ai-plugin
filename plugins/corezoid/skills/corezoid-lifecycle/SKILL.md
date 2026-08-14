---
name: corezoid-lifecycle
description: >
  Safely pauses, resumes, or moves existing Corezoid processes and folders.
  Use only when the user explicitly asks to pause/unpause/resume/activate a
  process or to move/relocate/reorganize a specific process or folder. Trigger
  on "pause process", "resume process", "move process", "move folder",
  "поставь процесс на паузу", "сними с паузы", "возобнови процесс",
  "перемести процесс", "перемести папку", "перенеси процесс", or
  "перенеси папку". Do not activate merely because a review, edit, cleanup,
  or refactor is in progress; those are not authorization to mutate lifecycle
  or location.
---

# Corezoid Lifecycle and Move Operations

Use this skill for deliberate operational changes to existing Corezoid
objects. These tools act on live server state and require explicit user intent.

Read `${CLAUDE_PLUGIN_ROOT}/docs/process/process-lifecycle-and-move.md` before
the first lifecycle/move operation in a session.

## Non-negotiable authorization rule

Never pause, resume, or move an object automatically.

The following are not authorization:

- a process appears unused;
- a review or refactor is in progress;
- edits/tests have completed;
- a folder layout looks untidy;
- pausing or moving would be a convenient safety precaution.

The user must directly ask for the specific action. If the exact object or
destination is ambiguous, resolve candidates read-only and ask the user to
choose. Do not infer a target.

## Required two-step workflow

For all four tools:

1. Resolve and repeat back the exact object ID/title. For moves, also resolve
   the exact destination ID/path.
2. Call the tool with `apply=false` (or omit `apply`).
3. Present the dry-run, including current state/location, target, operational
   effects, and the exact confirmation token.
4. Ask for explicit confirmation. Do not invent confirmation from earlier
   general approval. The token is reconstructable and is not itself evidence
   that the user approved the operation.
5. Only after confirmation, call `apply=true` with the exact token returned by
   the fresh dry-run.
6. Report the tool's verified result. If it says verification failed or state
   may already have changed, stop and run a new dry-run; never retry blindly.

## Pause a process

Use:

```text
pause-process(process_id=<id>)
pause-process(process_id=<id>, apply=true,
              confirm="process#<id>:<live_status>->paused")
```

Explain before confirmation:

- Corezoid will reject new task creation with `conveyor_is_not_active`.
- The graph and deployment are unchanged.
- Already-running or parked tasks are not modified by this tool and must be
  inspected separately.
- Pause is temporary admission control, not evidence that a process is safe
  to delete.

Pause can support a user-chosen maintenance/observation window. Never choose
that window on the user's behalf.

## Resume a process

Use:

```text
resume-process(process_id=<id>)
resume-process(process_id=<id>, apply=true,
               confirm="process#<id>:<live_status>->active")
```

Before confirmation, verify with the user that maintenance is complete and
that the process may receive traffic. New tasks can arrive immediately from
API callers, schedules, callbacks, and other processes. Never auto-resume just
because this session originally paused the process.

## Move a process

Use:

```text
move-process(process_id=<id>, destination_folder_id=<folder>)
move-process(process_id=<id>, destination_folder_id=<folder>, apply=true,
             confirm="<exact context-bound token from the fresh dry-run>")
```

The operation reparents the existing process. It preserves the same ID and
graph; it does not copy, import, or deploy.

If the dry-run reports a project/stage/root context change, explain the alias,
environment-variable, access, and deployment risks. Proceed only after the
user accepts those risks, adding `allow_cross_stage=true` to the confirmed
call.

## Move a folder

Use:

```text
move-folder(folder_id=<id>, destination_folder_id=<folder>)
move-folder(folder_id=<id>, destination_folder_id=<folder>, apply=true,
            confirm="<exact context-bound token from the fresh dry-run>")
```

Only normal folders can be moved. The tool rejects projects/stages, self-move,
and moving a folder into a descendant. For cross-stage/root moves, the risks
apply to every descendant and `allow_cross_stage=true` is required after the
user accepts them.

## After a move

The MCP tool does not relocate local mirror files/directories. Re-pull the
destination and verify it before removing any stale local copy. Local cleanup
also requires explicit user intent; never delete the old local path merely
because the server move succeeded.
