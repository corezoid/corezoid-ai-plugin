Loaded only when executing Step 4 of `corezoid-edit-bot`. Assumes CHANGE.md already exists and the user approved it.

## Step 4: Apply the change

Work only inside `processes_touched`, `tasks_touched`, `env_vars_touched` and
the create/delete lists.

### 4.1 Processes

- **`push-process` cannot create a process.** A local file with `obj_id: 0` is
  rejected with `Project or stage mismatch`, and once the process exists a push
  with no recorded baseline is refused too. The working order for a new command
  is:

  ```
  create-process  process_name: /{command}  folder_id: {template_ids.bots_folder}
  pull-process    process_id: <the id it returned>     # records the baseline
  <write the generated scheme into the pulled file, keeping its obj_id>
  ```

  `adopt_existing` is **not** the flag for this — it declares you do not know
  what is on the server, whereas here you created the empty process seconds ago.
  Keep the filename `create-process`/`pull-process` produced: the `<ID>_` prefix
  is load-bearing, `push-process` and `lint-process` recover the process id from
  it.
- **New command** — start from
  `corezoid-gen-bot/references/bot_reply.skeleton.json` (single reply) or
  `bot_dialog.skeleton.json` (anything with a question), substitute every
  `{{UPPER_CASE}}` placeholder from PLAN.md `template_ids` (including
  `{{TEMPLATE_USER_ID}}` — read it off an existing `api_copy` in the pulled
  Router), assign fresh 24-hex node ids unique within the process, then
  `layout-process`. A `{{UPPER_CASE}}` inside an `api_code` `src` is a
  generator-time substitution too: Corezoid does not interpolate `{{...}}` in
  Code nodes. `{{BOTS_FOLDER_ID}}` and `{{TEMPLATE_USER_ID}}` are quoted in the
  skeletons only to keep those files parseable JSON — **substitute both as bare
  integers and drop the quotes**, or `push-process` fails schema validation
  (`'/parent_id': got string, want null or integer`, and the same for `user_id`
  on every `api_copy`), which `force` does not bypass.
- **Edited command** — reference nodes **by title**, never by a remembered id:
  `push-process` regenerates ids and rewrites the file, so any id from before
  the last push is stale. Re-read the file after every push.
- **Removed command** — prefer the reversible option and say which you chose:
  - `pause-process` leaves the graph intact and rejects new tasks
    (`conveyor_is_not_active`). It is a dry-run by default; to apply, pass
    `apply: true` with `confirm: "process#<id>:<live_status>->paused"`. Note it
    is admission control, not proof that tasks already parked in nodes stopped —
    a user mid-dialog keeps their session.
  - `delete-process` moves it to the recycle bin, restorable from the Corezoid
    UI. `pull-process` first if you want a local backup.

  Either way **the alias survives** (§0), still pointing at the paused or
  trashed process. So the Router keeps dispatching to it and the user does *not*
  get `commandNotFound` — observe what actually happens in Step 6 instead of
  assuming, and if the name must stop resolving, that is an
  `corezoid-alias-manager` job. Drop the `mainKeyboard` button and the
  `mainMenu` mention, and **leave the Localization keys**: an orphan key costs
  nothing, and deleting one another command still references renders an empty
  message.
- **Dialog step** — repeat the ask → wait → keep → validate block. Copy the new
  answer into **its own** key in the Keep node; reading `{{message.text}}` later
  in a multi-step dialog reads the latest message, not the answer that step
  asked for. Give the step a reply `keyboard` where the value set is closed —
  an `inline_keyboard` sends `callback_data`, which `Main → PARSE command`
  interprets as a new command rather than as the answer
  (`references/invariants.md` §4).
- **Wire a process in / remap** — the `api_rpc` node's `extra` / `extra_type`
  and the outcome-mapping Code node change together; a new `extra` key with no
  source in the dialog is a call that silently sends an empty string. Keep the
  `time` semaphore ≥ 30 s and prefer `@alias` over a numeric `conv_id` (a
  numeric id is stage-specific and does not survive a `deploy-stage`
  promotion). **Verify the alias you are about to call points where you think:**
  match on `obj_to_id` in `_ALIASES_.json`, never on the name — in the reference
  workspace `promolist`, `recovery-password` and `registration-verify-success`
  all resolved to deprecated `..._old/API:` copies rather than to the processes
  the user handed in. Fragments in
  `corezoid-gen-bot/references/rpc_call.nodes.json`.
- **The second domain call in a command is a structural change, not an
  addition.** An `api_rpc` callee's `res_data` merges into the caller's task at
  top level, and every process in this family replies with the same
  `{result, code}` envelope — so adding a second call means the existing call
  now needs a namespacing **`api_code`** after it too (not a `set_param`: lint
  cannot see a `data.result` read inside JavaScript and fails the file with
  `UNUSED SET_PARAM`). Set `namespacing_required: true` in CHANGE.md and touch
  both calls, or the command starts branching on the wrong verdict.
- **Forward every value the message needs.** Whenever this change adds a
  `{{var}}` to a text, adds a dynamic attachment, or repoints a call at a
  process with different payload keys, the Send Message `api_copy`'s `data`
  changes too. `references/invariants.md` §1 is the full rule; it is the single
  most common way an edit passes every test and is wrong in the chat.
- **Never edit a handed-in domain process.** They are someone else's system and
  the bot is a caller, not an owner. If one genuinely has to change, say so and
  hand it to `corezoid:corezoid-edit` with the user's agreement, then
  `/corezoid-gen-bot refresh` to pick up the new contract.
- **Template edit** — only with the guard from `references/change_kinds.md`:
  named confirmation, `create-snapshot` first, no `force`, no
  `overwrite_server_change`, no `allow_no_snapshot`, and a full-command
  regression.

### 4.2 Tasks — Localization and Attachments

These hold runtime task data, so they are written with the task actions, never
`push-process`. The lines below are shorthand: `show-task process_id: X ref: Y`
means `cz-tasks {"action": "show-task", "args": {"process_id": X, "ref": "Y"}}`.

```
show-task    process_id: {template_ids.localization}  ref: localization     # read before writing
modify-task  process_id: {template_ids.localization}  ref: localization \
             deep_merge: true  data: {"newKey": {"en":"…","ru":"…","uk":"…"}}
run-task     process_path: <Attachments path>  ref: <new attachment_id>  data: {…}
modify-task  process_id: {template_ids.attachments}  ref: mainKeyboard \
             deep_merge: true  data: {…}
```

`deep_merge: true` is **mandatory** on every `modify-task` here. The Corezoid
task API merges only top-level keys: a shallow write of one language for a
`text_id` replaces the whole language map, and a shallow write of one channel
for an `attachment_id` drops the other three. That failure is silent and only
shows up as an empty message on the channels you did not send.

**Read the document with `show-task` before writing it.** It tells you which
keys already exist (so you extend rather than clobber), which languages the
wizard actually seeded, and whether the key you are "changing" is a shared one
(§1.1). Notably `serviceError` is **not** shipped by the wizard even though both
skeletons reference it on every error path — if a command you touch relies on it
and it is absent, seed it, or every failure delivers an empty message.

**Seed every language the wizard created, not just PLAN.md's `lang`.** It seeds
`en`, `ru` and `uk` regardless, and Send Message picks the language from
`User Profile.language`, which the Telegram receiver derives from
`message.from.language_code`. A user whose client is English gets `en`; if that
key is missing for your `text_id`, they get nothing — there is no fallback.

Adding a locale means touching **every** key, not just the ones this change
introduced.

**`run-task` on a state diagram always reports the task as "still in progress /
parked at a non-final node". That is success, not an error** — on a state
diagram the task *is* the stored document, and it sits in the state node by
design. The tool echoes the data it stored; check that and move on. Do not
retry with a larger `wait_sec`.

Attachment shapes, the `keyboard` vs `inline_keyboard` rule, per-page item caps
and the `items: ""` trap are in
`corezoid-gen-bot/references/attachments.seed.json` and
`references/invariants.md`.

### 4.3 Env vars

Only if the change genuinely needs one. If a domain call has to carry a
credential the domain process does not source itself, put it in a stage env var
(`create-variable`, secret) and reference it as `{{env_var[@name]}}` — never
inline it into process JSON, because a pulled mirror is a git-tracked artifact.

`modify-variable` is a dry-run by default and needs `apply=true` plus
`confirm="<short_name>#<obj_id>"`. Show the user the diff first. Renaming a
variable breaks every `{{env_var[@old-name]}}` reference in the stage — prefer
changing the value.

