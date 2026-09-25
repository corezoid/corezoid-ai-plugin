Loaded only when executing Phase 4 of `corezoid-gen-bot`. Assumes Phases 1–3 already ran and PLAN.md/bot-contract.json exist.

## Phase 4 — Build the orchestrator from the template

**Preflight.** Read PLAN.md and `bot-contract.json`. Every input for Phases 4–8
comes from those files, not from a fresh conversation. Missing PLAN.md → refuse
and point at `/corezoid-gen-bot plan <ids>`. Do not re-prompt for anything they
already hold; do not re-run Phase 2.

**Channel tokens are the one exception, by design.** They are deliberately not
in PLAN.md — `tokens_supplied: true` records only that the user had them — so
`execute` in a new session legitimately has no way to read them back. Ask for
the tokens of exactly the channels listed in `channels:`, once, immediately
before §4.1, and say why you are asking: the plan stores no credentials. Take
them from the chat straight into the wizard argument. Never write them to
PLAN.md, a scratch file, or the summary to make the next session easier — that
trade is the whole reason they are absent. If the user cannot supply a token
now, stop before §4.1; a partial channel set builds a different bot and needs a
different confirm token.

**If `orchestrator.folder_id` is set, skip to Phase 5.**

### 4.1 Call the wizard, exactly once

The tool gates itself: `apply=false` (the default) previews the build and
returns nothing but a confirm token, and only `apply=true` with that exact token
starts it. Two calls, always — the first is free, the second is not.

```
create-communications-orchestrator                     # 1. preview
  messengers: "[{\"channel\":\"telegram\",\"key\":\"…\"},{\"channel\":\"viber\",\"viber_token\":\"…\"}]"
  stage_id:   {corezoid.stage_id}     # omit to use the marker's stage
  project_id: {corezoid.project_id}   # omit to resolve from the stage
  lang:       {lang}
  apply:      false                   # REQUIRED, see below — never omit it
```

`apply: false` is the default, so passing it changes nothing about what the
tool does. Pass it anyway, every time. A server old enough not to know the
argument rejects the whole call as unknown — while the same call with `apply`
omitted is, to that server, a plain build request, and it mints ~150 processes
and takes over the channel's webhook at the step this skill treats as free.
Spelling the flag out is what makes a version mismatch fail loudly instead of
building a bot nobody approved.

Show the preview to the user — it names the target stage, the channel set and
what cannot be undone — and wait for the approval Phase 4's gate already
requires. Then repeat the identical call with `apply: true` and the
`confirm:` token the preview printed, copied verbatim:

```
create-communications-orchestrator                     # 2. build
  messengers: "…"                     # byte-identical to the preview call
  stage_id:   {corezoid.stage_id}
  project_id: {corezoid.project_id}
  lang:       {lang}
  apply:      true
  confirm:    "<copied verbatim from the preview output>"
```

The token is bound to the stage, the channel set **and** the channel
credentials, and its last segment is a hash — it cannot be assembled by hand,
which is deliberate. If it is rejected, the build did **not** start: something
differs from what the user approved — a changed stage, an added or dropped
channel, or a different bot token for the same channel. Re-run the preview,
show the user what differs, and never edit a token to make the call go
through.

`messengers` is a **JSON string**. The build is asynchronous; the tool polls it
and returns only when the wizard hands back a `folder_url`:

```json
{ "status": "ok", "obj_id": "6a97…", "folder_url": "https://admin.corezoid.com/folder/691905",
  "channels": ["telegram","viber"], "checks": 2,
  "webhooks_url": [{"channel":"fbmessenger","url":"https://…"}] }
```

`folder_url` is always present on success. `webhooks_url` and `dashboard_url`
appear only when non-empty.

**Immediately** write `folder_id` (the trailing number of `folder_url`),
`folder_url`, `obj_id`, `webhooks_url`, `dashboard_url` and `built_at` into
PLAN.md's `orchestrator:` block, then continue.

- Error result from the **wizard** → the message carries its own diagnosis,
  usually naming the token it rejected. Report it verbatim, fix the token with
  the user, then call again from the preview. A wizard-rejected build creates
  nothing, so retrying is safe.
- **Transport error on the build call** (timeout, 429/503, connection dropped)
  → the outcome is unknown, and the tool deliberately does not retry it: the
  request is sent exactly once, because a second delivery would build a second
  orchestrator. The message says to check the stage. Do that — look for a
  `*_Communications_Orchestrator` folder created just now — and only call again
  if none exists. If one does, record its `folder_id` in PLAN.md and resume from
  Phase 5.
- The folder the wizard builds is large — a recent single-channel build was 150
  processes. Count it from the pull rather than quoting a number; the figures in
  this document are illustrative and drift with the template.
- Still building after 10 checks → **do not retry.** The job id is in the
  message; the build may still land server-side.

### 4.2 Pull the whole stage, then find the orchestrator folder in it

**Call `pull-folder` with the STAGE id.**

```
pull-folder  folder_id: {obj_id from <stage_id>_<name>.stage.json}   # the STAGE id
```

`folder_id` is a **required** argument of the MCP tool. Zero-argument
`pull-folder` resolves the stage on its own only in the server's CLI mode; over
MCP the call is rejected with `missing required argument: folder_id` before any
stage resolution happens, so omitting it stops the build right after the
orchestrator was created — the one point in this skill where stopping costs
~150 orphaned processes.

Where the id comes from: the `<stage_id>_<name>.stage.json` marker at the
workspace root if it exists. A workspace that has been logged into but never
pulled has **no marker yet** (see Phase 0) — there the stage id is the
`stage_id` recorded for this directory in `~/.corezoid/config.json`, which is
also the value Phase 4.1 passed to `create-communications-orchestrator`. Reuse
that one; do not re-derive it with the `list-stages` action and risk a different stage.

> **Do NOT pass the orchestrator's `folder_id` here.** `pull-folder` unzips a
> server-produced archive into the **stage root** (the `RootPath` registered for
> this workspace), not into the current directory, and it picks the archive by
> what the id turns out to be: given the stage id it fetches the whole stage,
> given a *sub*folder id it fetches only that folder — and then unzips it at the
> stage root anyway. So passing the orchestrator's id spills the orchestrator's
> contents directly over the stage root, and the
> `<folder_id>_Communications_Orchestrator/` directory the rest of this phase
> looks for never appears. That is the failure mode this instruction exists to
> prevent — and it is the reason the argument is easy to get wrong in the right
> direction: the id you must pass is the stage's, and the orchestrator folder id
> you have in hand from Phase 4.1 is the one id that must not go here.

After the pull, the orchestrator is a directory at the stage root whose name
prefix is exactly the `folder_id` recorded in §4.1 — the trailing number of the
wizard's `folder_url`:

```bash
# Find the stage marker by walking UP from the current directory: it sits at
# the workspace root, and a session started in a subfolder finds nothing with a
# relative `ls`. Every path below is built from $stage_root — never relative to
# the current directory.
d="$PWD"
while [ "$d" != "/" ] && [ -z "$(ls "$d"/*.stage.json "$d"/*/*.stage.json 2>/dev/null | head -n1)" ]; do
  d="$(dirname "$d")"
done
marker="$(ls "$d"/*.stage.json "$d"/*/*.stage.json 2>/dev/null | head -n1)"
[ -n "$marker" ] || { echo "no <id>_<name>.stage.json marker at or above $PWD — run corezoid-init"; exit 1; }
stage_root="$(dirname "$marker")"

orch="$stage_root/{orchestrator.folder_id}_Communications_Orchestrator"
ls -d "$orch" || { echo "orchestrator folder not found — see the warning above"; exit 1; }
```

If that directory is not there, **stop**: either the pull was scoped wrongly
(the warning above) or the wizard built into a different stage than the marker
points at. Do not proceed to read ids out of whatever else the pull produced.

This check runs **after** the orchestrator already exists, so a failure here is
never a reason to call the wizard again — the folder is in the stage whether or
not this snippet found it. Resolve the path, or ask the user for the folder id
from `folder_url`, and continue from Phase 5.

Then read the real ids out of that directory into PLAN.md's `template_ids`. The
filename prefix is the process id and each `*.folder.json` carries its folder's
`obj_id`:

```bash
ls "$orch"/*.conv.json "$orch"/*/*.conv.json          # <process_id>_<Title>.conv.json
grep -h '"obj_id"' "$orch"/*/*.folder.json            # subfolder ids (Sample_Bots, Configs, …)
```

A `CLAUDE.md` process index may also be present at the stage root (and mirrored
under `.git-context/stages/<id>/`) — it is a convenience, not a guarantee: it is
regenerated locally in offline mode and copied from the git mirror online. **The
folder tree is the authoritative source**; use the index only to save a few
`ls` calls.

Also record `user_id` — read it off any `api_copy` logic in the pulled `Router`
or `Send Message`. Generated nodes carry the same value.

**Never hardcode an id from `references/template_map.md` or from an earlier
run.** The wizard mints fresh ids per build, and a stale id fails silently: the
task simply never arrives.

### 4.3 Sanity-check the template

Confirm the pulled folder carries the contracts the generator assumes:

- `Send Message` exists, `params` include `channel`, `chat_id`, `text_id`,
  `attachment_id`.
- `Router` has a `Set commandAlias` Code node and an `Init bot` node whose
  `conv_id` is `@{{commandAlias}}`.
- `Localization`, `Attachments`, `User Profile` exist and are `conv_type: state`.

Any of these missing means the wizard built a shape this skill was not written
against. Stop, name the absent contract, and do not generate — a bot written
against a guessed contract fails in a messenger, in front of a user.

