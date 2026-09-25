Loaded only when executing Step 5 of `corezoid-edit-bot`, right after Step 4 applied the change.

## Step 5: Lint and push

Per touched process, in order:

```
layout-process  process_path: <path>     # x/y only
lint-process    process_path: <path>
push-process    process_path: <path>
create-alias    process_path: <path>  short_name: <command minus slash>   # new commands only
```

Fix every deploy-blocking lint finding in the design. Do **not** pass
`force=true`: the findings this generator can plausibly trip — a logics array
not ending in a default `go`, a shared error cluster, a time semaphore under
30 s, an `err_node_id` pointing at an `obj_type:0` node, a self-referencing
`api_copy`/`api_rpc` — describe a graph the server rejects or the UI
force-converts, and `force` does not bypass them. `force` is the lint override
only; it waives neither the concurrency gate nor Stub Mode.

`create-alias` needs no `stage_id`: it derives the stage by walking the process
file's `parent_id` chain. An `Object is not in stage` error means the local file
is stale — re-`pull-process` it so its `parent_id` points at the current stage.

**A concurrent server change means someone edited this orchestrator elsewhere.**
The push reports local edits, server changes, the true overlap and the last
known author. Two honest resolutions, in order of preference:

1. `push-process merge=true` — writes a reviewable local 3-way merge plus a
   `.pre-merge` backup and deploys **nothing**. Read the merge, fix the
   conflicts, push again.
2. Re-pull and re-apply your edit on top.

Do not reach for `overwrite_server_change` without showing the user the report
and getting explicit agreement — it discards a change nobody has seen.

**Snapshots.** `push-process` takes a pre-push snapshot of every process that
already has a deployed version; a **never-deployed** process is exempt, so the
first push of a command you just created via `create-process` needs no waiver on
current builds. If a snapshot attempt does fail, the push blocks, and the safer
default is to wait for the API rather than waive. `allow_no_snapshot=true` is
defensible only on a resolved mutable non-production-like stage, for a process
whose recorded baseline you have confirmed has **zero nodes** — and it is
refused outright on immutable or production-like stages even with the flag.
Report every waived gate in the Step 7 report, never only in the log.

