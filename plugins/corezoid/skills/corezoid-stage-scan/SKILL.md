---
name: corezoid-stage-scan
description: >
  Pre-merge / pre-deploy static validator for exported Corezoid stages. Use when
  the user has one or more exported stage files (.zip) or extracted stage folders
  and wants to find, BEFORE merging or deploying, what will break: processes that
  are not active, empty/battered processes, broken intra-process node links
  (to_node_id / err_node_id pointing at a missing node), and broken or inactive
  cross-process conv_id references (api_rpc / api_copy / api_get_task). Activate
  when the user says "scan stage", "check stage before merge", "validate export",
  "why does the merge fail", "find broken links", "find non-active processes",
  "Can't create more start nodes", "Only active process can be used",
  "referenced node ... does not exist", "Access user to conveyor is denied",
  "проверь стейдж", "почему не мерджится", "битые ссылки", "найди неактивные процессы",
  or attaches two stage ZIPs and asks what is wrong with the merge.
---

# Scan a Corezoid Stage (pre-merge / pre-deploy validation)

You statically validate **exported** Corezoid stages — `.zip` exports or extracted
folders — without touching the live environment. This is the fast first answer to
"why does my merge / deploy fail" and the safe pre-flight check before any
cross-stage merge.

The check is grep/AST-level over every `*.conv.json` in the export. It is fully
offline and deterministic, so it works on attachments (e.g. files pulled from a
Jira ticket) even with no Corezoid credentials.

## What it detects

These map 1:1 to the errors the platform shows in the merge **Errors list** dialog:

| Finding | Platform error it explains |
|---|---|
| `[1]` process `status != active` | `Only active process can be used` |
| `[1b]` empty (no-nodes) process | battered / half-recreated shell; deploy of an empty conv |
| `[2a]` broken **node** link (`to_node_id` / `err_node_id` / `go_to` → missing node in same process) | `Key 'to_node_id'. 'referenced node X does not exist'` |
| `[2b]` broken / inactive **conv_id** ref (`api_rpc` / `api_copy` / `api_get_task` → process missing from stage or not active) | `Only active process can be used` · `Key 'conv_id'. 'Access user to conveyor is denied in logic'` |
| `[2c]` `api_get_task.node_id` missing in the **target** process | invalid get-task node reference |

`{{...}}` and `@alias` conv_id references are reported as *unresolvable* (counted,
never flagged as broken) — they resolve at deploy time against the live stage.

> Note: `api_get_task.node_id` points at a node in the **target** `conv_id` process,
> not the current one — the scanner checks it cross-process, so it does not produce
> false "missing node" positives for get-task logics.

## How to run

The skill ships a self-contained Python 3 scanner (stdlib only — `zipfile`, `json`,
`re`; no install). Run it directly on the export(s):

```bash
python3 "${CLAUDE_PLUGIN_ROOT}/skills/corezoid-stage-scan/scripts/scan_stage.py" \
  <stage.zip | stage_dir> [more ...] [--json report.json] [--quiet]
```

- One input → scans that stage.
- Two inputs → scans both (e.g. **source** and **target** of a merge) and prints a
  report per stage so you can compare.
- `--json` writes a machine-readable report (array if multiple inputs).
- Exit code `1` if any blocker is found, `0` if clean → drop it into CI before merge.

Every finding carries both `path` (full file path inside the export) and `folder` (the
human-readable folder location in the tree, e.g. `4570_CRM / 4526_Push: Pin block`), so you
can always tell the user **where** to find the object. When reporting back, include the folder
for each `conv_id` — "what's broken" without "where it is" is not actionable. Exported folder
names keep their `id_` prefix (and may show mojibake for non-ASCII); the folder id always locates
the object even if the display name is garbled.

Pass the stage exports as positional args. If the user attached ZIPs to a ticket,
download them first, then point the scanner at the local files.

## Workflow

1. **Gather inputs.** Identify the exported stage file(s). For a merge problem ask
   for / locate **both** the source (откуда) and target (куда) exports — most
   merge failures are explained by comparing the two.
2. **Run the scanner** on each (or both at once).
3. **Map findings to the user's symptom.** If they quoted a specific error, point at
   the exact `conv_id` / node from the matching section above.
4. **Recommend the fix** per finding:
   - `status != active` → set the process `active`, or delete it if unused. Do this on
     **both** stages so the merge sees a consistent state.
   - empty / battered process → deploy real content or delete the shell.
   - broken node link → re-point or remove the dangling `to_node_id` / `err_node_id`.
     If the export is internally consistent but the **live** stage still errors, the
     live process is in a half-merged state — re-deploy or roll back that one process
     on the environment (a stale merge that was never rolled back).
   - broken / inactive conv_id ref → fix the target (create / activate it, or point to
     the correct existing process / alias). If the error is
     `Access user to conveyor is denied`, the referenced object is owned by a removed
     user — reassign ownership to a live account.
5. **Re-export and re-scan** after fixes; merge only when the scan is clean on both.

## Prevent recurrence (advise the user)

- Always **roll back** a failed or aborted merge — never leave processes in merge state.
- Avoid **cross-version** merges (newer-version structures into an older `capi` validate
  differently). Align versions first.
- Run this scan in CI / before every merge as a gate (`--quiet`, non-zero exit).

## Notes

- Read-only and offline; it never calls the API or mutates anything.
- Folder/file names in exports may show garbled non-ASCII (UTF-8 shown as latin-1) —
  cosmetic only; matching is by `conv_id` / `uuid`, not by name.
- The export schema this relies on: top-level `status`, `obj_id`, `uuid`, `conv_type`,
  and `scheme.nodes[].condition.logics[]` with `type`, `to_node_id`, `err_node_id`,
  `conv_id`, `node_id`.
