---
name: corezoid-edit-bot
description: Iteratively edits an already-built messenger bot from corezoid-gen-bot — a Corezoid Communications Orchestrator serving Telegram, Viber, Apple Messages for Business and Facebook Messenger over a backend of existing Corezoid processes called with api_rpc. Runs a lean plan → confirm → apply → lint → push → smoke-test loop. `plan` writes `.corezoid-gen-bot/CHANGE.md` (scope + processes + tasks + the smoke test that proves it) and stops; `execute` reads CHANGE.md, applies it, lints and pushes each touched process, updates Localization/Attachments tasks with deep_merge, and smoke-tests every affected command through the Router and through Send Message; `deploy` promotes one stage onto another. Trigger on phrases like "измени бота", "поправь бота", "добавь команду", "убери команду", "переименуй команду", "подключи ещё процесс", "поменяй текст бота", "поменяй клавиатуру", "добавь язык боту", "добавь шаг в диалог", "бот отвечает пустым сообщением", "бот отвечает дважды", "почему бот не отвечает", "edit bot", "modify bot", "add command to bot", "wire another process into the bot", "change bot copy", "fix the bot", "задеплой бота", "promote stage", or when the user references PLAN.md/CHANGE.md for a bot and asks for a change. Assumes CWD is the Corezoid workspace built by corezoid-gen-bot (contains `.corezoid-gen-bot/PLAN.md`, a `*.stage.json` marker and the pulled orchestrator folder). For a Smart Form web app over the same processes use `simulator-app-generator`.
---

# corezoid-edit-bot

Follow-up skill to [[corezoid-gen-bot]]. Edits an orchestrator that already
exists. Reuses the same PLAN.md as the architectural source of truth, layers a
per-change `CHANGE.md` on top, and never re-audits the domain processes unless
the user asks for `corezoid-gen-bot refresh`.

An edit is riskier than a build, for one reason: **the bot is live and someone
is talking to it.** A build that is wrong is a bot nobody has used yet; an edit
that is wrong breaks a working conversation, and most of the ways it breaks are
silent — nothing fails at push time, the chat just misbehaves. So this skill's
weight is not in generation, it is in knowing what a request is allowed to
touch and in proving afterwards that the *user* sees the right thing, which is
strictly more than proving the graph ran.

Read these before touching anything:

| Document | What it settles |
|---|---|
| `references/change_kinds.md` | what each request kind may touch, its verification, and what this skill deliberately cannot do |
| `references/invariants.md` | the send-side and dispatch invariants every change must preserve — all of them silent when broken |
| `references/repair_loop.md` | symptom → cause → the layer at fault, and when to stop looping and report |
| `corezoid-gen-bot/references/template_map.md` | the orchestrator's own contracts (Router, Send Message, Localization, Attachments) |
| `corezoid-gen-bot/references/contract_extraction.md` | how to read a domain process — needed for `wire-process` and `process-remap` |
| `corezoid-gen-bot/references/rpc_call.nodes.json` | the `api_rpc` / `api_copy` / state-read fragments |
| `corezoid-gen-bot/references/bot_reply.skeleton.json`, `bot_dialog.skeleton.json` | the two command shapes, for a new command |
| `corezoid-gen-bot/references/attachments.seed.json`, `localization.seed.json` | per-channel attachment shapes, per-page caps, interpolation rules |

## When to use

- CWD is a corezoid-gen-bot workspace: `.corezoid-gen-bot/PLAN.md` with a
  non-null `orchestrator.folder_id`, a `*.stage.json` marker, and the pulled
  orchestrator folder on disk.
- User wants to add/remove/rename a command, wire in another Corezoid process,
  change copy or a keyboard, add a language, add or drop a dialog step, remap a
  command onto a different process or argument, fix a bug, or promote a stage.
- **Not for adding or removing a messenger channel** — the wizard is not
  incremental. See `references/change_kinds.md`.
- **Not for contract drift in the domain processes** — if a handed-in process
  changed (lost a reply node, got paused, gained an alternate outcome), run
  `/corezoid-gen-bot refresh` first so PLAN.md and `bot-contract.json` reflect
  reality, then come back.
- **Not for building from scratch** — that is [[corezoid-gen-bot]], and running
  it again here would build a whole second orchestrator that steals the
  webhooks from this one.

## 0. Preflight — the tools, and the three things none of them can do

Corezoid MCP tools this skill uses: `pull-process`, `pull-folder`,
`push-process`, `lint-process`, `layout-process`, `create-process`,
`delete-process`, `pause-process`, `resume-process`, `create-alias`,
`create-snapshot`, `run-task`, `show-task`, `modify-task`,
`list-task-history`, `list-node-tasks`, `create-variable`, `modify-variable`,
`deploy-stage`. Missing → tell the user to install the Corezoid plugin and run
`/corezoid-init`.

Three things are **not possible** with this toolset. Say so plainly rather than
improvising something that looks like it worked:

1. **An alias cannot be deleted, repointed or renamed.** `create-alias` is the
   only alias tool in the plugin — there is no delete, modify, unlink or list
   counterpart. So a name once taken is taken for good, a rename means
   *creating a second alias* and leaving the first one dangling, and removing a
   command does not free its name. Removing or repointing an alias needs raw
   Corezoid API calls — hand that to `corezoid:corezoid-alias-manager` with the
   user's agreement, and until it is done, describe the alias as still live.
2. **A channel cannot be added to or removed from an existing orchestrator.**
   `create-communications-orchestrator` builds a whole folder including the
   per-channel receiver and API-method processes; there is nothing to graft onto.
3. **An orchestrator build cannot be undone.** Which is why this skill never
   calls the wizard: a second build steals the webhook from the first and leaves
   ~150 dead processes behind.

**Stage resolution: probe, do not refuse.** Most tools read the stage from the
`<id>_<name>.stage.json` marker at the workspace root, but the MCP server also
resolves it from `~/.corezoid/config.json` keyed by the working directory — so
a workspace that has been logged into but never pulled works with no marker.
If a marker is absent, try one `pull-process` before declaring a `login`
problem (see `corezoid-init`).

## Modes

| Invocation | Mode | What runs |
|---|---|---|
| `/corezoid-edit-bot plan <ask>` (or "спланируй изменение", "just plan") | **plan** | Steps 1–3: understand → write `.corezoid-gen-bot/CHANGE.md` → summary, stop |
| `/corezoid-edit-bot execute` (or "погнали", "execute", "сделай") | **execute** | Steps 4–7: apply → lint → push → seed tasks → smoke-test → report |
| `/corezoid-edit-bot <ask>` no subcommand | **full** | Plan, print summary, **pause**, continue to execute after the user confirms |
| `/corezoid-edit-bot deploy` (or "задеплой", "promote stage") | **deploy** | Step 6b only: `deploy-stage` dry-run → confirm → apply → verify |

## Preconditions (every mode)

Verify before doing anything else. If any fails, stop and say what to run.

- `.corezoid-gen-bot/PLAN.md` exists and `orchestrator.folder_id` is non-null.
  Missing or null → point at `/corezoid-gen-bot plan <process ids>` (or, if the
  folder exists in Corezoid but not in PLAN.md, ask for the folder id and fill
  the block in — do **not** call the wizard).
- `template_ids` in PLAN.md is populated. If it is not, pull (below) and fill it
  in before planning anything — a change that guesses an id writes into another
  workspace's process.
- `.corezoid-gen-bot/bot-contract.json` exists. It is the derived contract of
  every domain process the bot calls; without it any change to a domain call is
  a guess. Missing → run `/corezoid-gen-bot refresh` to regenerate it.
- **The pulled mirror is current.** Pull before planning: someone may have
  edited the orchestrator in the Corezoid UI since the last pull, and
  `push-process` will block on the concurrency gate anyway — pulling first turns
  that block into a diff you can read.

  ```
  pull-folder  folder_id: {obj_id from <stage_id>_<name>.stage.json}   # the STAGE id
  ```

  **`folder_id` is a required argument, and the value must be the stage id — never
  the orchestrator's `folder_id`.** `pull-folder` unzips a server-produced archive
  into the **stage root** (the `RootPath` registered for this workspace, not the
  cwd) and picks the archive by what the id turns out to be: the stage id fetches
  the whole stage, a *sub*folder id fetches only that folder and still unzips it
  at the stage root. So passing the orchestrator's id spills its contents over
  the stage root, the `<folder_id>_Communications_Orchestrator/` directory never
  appears, and every id read afterwards comes from the wrong tree. (`folder_id: 0`
  is a third thing again — a workspace-root "No Project" pull.)

  After the pull, find the orchestrator by its `<folder_id>_` name prefix, where
  `folder_id` is `orchestrator.folder_id` from PLAN.md. If that directory is
  absent, stop and say so — do not plan against whatever else the pull produced.

  ```bash
  stage_root="$(dirname "$(ls */*.stage.json *.stage.json 2>/dev/null | head -n1)")"
  orch="$stage_root/{orchestrator.folder_id}_Communications_Orchestrator"
  ls -d "$orch" || { echo "orchestrator folder not found — wrong pull scope or wrong stage"; exit 1; }
  ```

- **Re-read `template_ids` off that directory** rather than trusting PLAN.md's
  copy blindly: the wizard mints fresh ids per build and a stale id fails
  silently — the task simply never arrives. Ids in
  `corezoid-gen-bot/references/template_map.md` are examples from one build and
  must never be copied into a process.
- If the workspace is a git repo, the working tree is clean **or** the only
  dirty files are ones the user is currently discussing. `pull-folder` and
  `pull-process` overwrite local files; a dirty file is somebody's unpushed work.

## Step 1: Understand the ask

Parse the request into one kind from `references/change_kinds.md`. Ask at most
one `AskUserQuestion`, and only for a slot you genuinely cannot infer.

| Kind | Mandatory slots | Inferable from PLAN.md |
|---|---|---|
| add-command | backing process, command name | shape, skeleton, texts, keyboard, menu position |
| remove-command | command | dependent texts, buttons, alias (which survives) |
| rename-command | old, new | process title, payloads, menu; the new alias |
| copy | which text and the new wording | `text_id`, locales, which processes send it |
| keyboard | which command, what changes | `attachment_id`, per-channel shapes |
| add-locale | the new language code | every key needing a translation |
| dialog-step | which command, what the new step asks | `text_id`, validation regex from the contract |
| wire-process | process id, what the command should do with it | callability, inputs, outcomes — from `bot-contract.json`, or re-derived if absent |
| process-remap | command, new process or new argument mapping | `api_rpc` `extra`, outcome mapping, whether namespacing is now required |
| bugfix | symptom + one repro | root cause, the smoke test to add |
| template-edit | the exact process and why no command-level change works | — (never inferred) |
| deploy | source stage, target stage | project and workspace ids |

Cross-reference PLAN.md's `Commands`, `Coverage`, `Localization keys`,
`Attachments`, `Menu wiring` and `template_ids`, plus `bot-contract.json`,
before answering anything yourself — those are the contract. **Never re-extract
a domain contract for an edit** unless the user asks for `refresh`; use what
Phase 2 captured.

### 1.1 The blast radius is bigger than the request, in two specific ways

Work both out here, not in Step 6 when the fix is expensive:

- **New or changed copy that carries a `{{placeholder}}` is a process change,
  not a task change.** Send Message is called with `group: ""`, so it receives
  only the fields named in the calling node's `data`; a `{{var}}` in the
  resolved text that was not forwarded arrives in the chat as the literal
  `{{var}}`. So "just change the wording" stops being Localization-only the
  moment the new wording interpolates something the sending node does not
  already forward. Find every sender before promising a one-task change:

  ```bash
  grep -rln '"text_id": *"balanceDone"' "$orch"          # who sends this text
  ```

  Then check the placeholders against that node's `data` and against the
  process's `success.keys` in `bot-contract.json`. Full rule in
  `references/invariants.md` §1.
- **A shared key is a global change.** `mainMenu`, `mainKeyboard`,
  `serviceError`, `timeout`, `commandNotFound`, `selectError`, `yes`/`no` and
  `carouselPattern` are referenced by every command and by the Router itself.
  Editing one has a `template-edit`-sized regression scope even though it
  touches no process: set `regression_scope: all` in CHANGE.md and smoke-test
  every command.

### 1.2 For `wire-process` and `process-remap`

Answer the callability question first (`contract_extraction.md` §2): a
`process` with a reachable `api_rpc_reply` gets `api_rpc`; one without gets
`api_copy` and a command that can only acknowledge; a `state` gets an inline
`set_param`; a paused one gets nothing until the user says otherwise. An
`api_rpc` into a paused or reply-less process parks the task until the semaphore
fires — the user waits the full 30 s for an error, and `params` does not reveal
it.

To answer it you need the process's file. **Look on disk before pulling:**
`find "$stage_root" -name "<id>_*.conv.json"` — the workspace is an
already-pulled stage and the process is very likely there. Match the `<id>_`
prefix exactly, so `1906756_` is not satisfied by `11906756_`. Only
`pull-process(process_id=<id>)` when it is absent, and never over a file that is
dirty in git without asking: `pull-process` overwrites, taking the unpushed edit
with it. Record in CHANGE.md whether the contract came from a reused file (with
its mtime) or a fresh pull — a reused snapshot is of unknown age, and wiring
against a stale contract is how a call starts sending an empty `extra` key.

**Never probe a domain process automatically.** `run-task` against somebody
else's process can send a real SMS or charge a real card. Show the side-effect
classification from `bot-contract.json` and let the user authorise each probe.
The same caution applies to the smoke test in Step 6: a command whose side
effects are `likely` or `unknown` fires them for real, so agree a safe test
input with the user first, or exercise only the dialog up to the confirm step.

### 1.3 For `add-command` and `rename-command`, settle the alias now

The alias *is* the dispatch mechanism: the Router computes
`commandAlias = command.replace("/","")` and calls `@{{commandAlias}}`. There is
no registration table to edit, and no way to fix a bad name later — see §0.

- `^/[a-z0-9][a-z0-9-]{2,}$` after the slash. A camelCase command deploys fine
  and is unreachable forever.
- Never `/start`, `/end` or `/exit` — Main, Router and System Diagram consume
  them.
- **Check the name against `_ALIASES_.json` at the stage root, which is the
  authoritative list** — a fresh orchestrator ships ~60 aliases. Do not work
  from a remembered list of sample-bot names.

  ```bash
  python3 - <<'EOF'
  import json
  taken = {a['short_name']: a.get('obj_to_id') for a in json.load(open('_ALIASES_.json'))}
  for c in ['order-status']:                       # candidate commands, minus the slash
      print(c, '->', 'FREE' if c not in taken else 'TAKEN by %s' % taken[c])
  EOF
  ```

  Watch for `obj_to_id: null`: a dangling row holds the name just as firmly as a
  live one, and `@name` currently resolves to nothing, so anything already
  dispatching there answers `commandNotFound`. You cannot reclaim it from the
  plugin — pick another name, or ask the owner to delete the row in the Corezoid
  UI, and say which you did. If `_ALIASES_.json` is not on disk, say the name is
  unverified rather than assuming it is free.
- **A rename does not move the old name.** The new command needs a new alias;
  the old alias stays and keeps pointing at the (now renamed) process, so the
  old command keeps working unless its alias is deleted through the raw API.
  State that in CHANGE.md and in the report rather than claiming the old command
  is gone.

## Step 2: Write `.corezoid-gen-bot/CHANGE.md`

Overwrite (do not append):

````markdown
---
kind: {add-command|remove-command|rename-command|wire-process|process-remap|copy|keyboard|add-locale|dialog-step|bugfix|template-edit|deploy}
title: {one-line title}
ask: {verbatim user request, single paragraph}
commands_affected: [{/command}, …]
processes_touched: [{path}, …]
processes_created: [{name → folder}, …]
processes_deleted: [{id, path}, …]
tasks_touched: [{process: localization|attachments, ref: {ref}}, …]
text_ids_touched: [{text_id}, …]
attachment_ids_touched: [{attachment_id}, …]
forwarding_impact: [{process path → send node → keys that must now be forwarded}, …]
env_vars_touched: [{short_name}, …]
aliases_to_create: [{short_name → process}, …]
aliases_left_dangling: [{short_name → why it cannot be removed}, …]
domain_processes: [{id, title, callable: api_rpc|api_copy|state|paused, source: local <mtime>|pulled}, …]
namespacing_required: {true|false}     # true once a command makes 2+ domain calls
side_effects: {none|unlikely|unknown|likely}   # of anything the smoke test will fire
regression_scope: {touched|all}        # all for a shared key or a template edit
mirror_pulled: {ISO-8601}
plan_impact: {sections of PLAN.md this change edits, or "none"}
template_edit_confirmed: {true|false|n/a}
timestamp: {ISO-8601}
---

## Scope
- Processes to create: {name → folder — what it does}
- Processes to edit: {path — which nodes, by title}
- Processes to delete or pause: {path — which, and why that one}
- Aliases to create: {short_name → process}
- Aliases that will survive this change: {short_name → consequence}
- Domain calls to add / change: {process id, wiring, extra keys, semaphore}
- Tasks to write: {process, ref, which keys, deep_merge yes/no}
- Send-side forwarding to add: {process → send node → keys}
- Menu changes: {mainKeyboard buttons, mainMenu text}
- Coverage impact: {which PLAN.md coverage rows change}

## Out of scope
{Anything adjacent the user did not ask for. Name it so the diff stays honest.}

## Smoke test that proves this change
- {command} — `run-task` on the Router with `{data}` → expect {observable result}
- {command} — `run-task` on **Send Message** with `{channel, chat_id, text_id, attachment_id, + interpolated values}` → expect `data.text` == {rendered string} and `reply_markup` {shape}
- {for dialogs} follow-up `modify` into `{process}` ref `{channel}_{chat_id}` → expect {next question}
- {for copy/keyboard} `show-task` on `{process}` ref `{ref}` → expect {keys}

## Verification checklist
- [ ] no `{{UPPER_CASE}}` placeholders left in any touched process
- [ ] no channel token anywhere in the tree
- [ ] `lint-process` clean on every touched process
- [ ] alias present and equal to the command minus its slash
- [ ] every `api_rpc` into a domain process has a `time` semaphore ≥ 30 s routed to a node that tells the user and copies `/end`
- [ ] namespacing `api_code` present after every call, if the command now makes 2+
- [ ] every referenced `text_id` exists in every locale the wizard created
- [ ] every `{{placeholder}}` in a text is (a) a key of the sending node's `data` **and** (b) a key the process actually produces
- [ ] `{{t'key}}` written with no dot; no dotted `{{a.b}}` in any text
- [ ] every referenced `attachment_id` has one key per channel in `channels` (`facebook`, not `fbmessenger`)
- [ ] dynamic attachments: `items` + `currentPage` forwarded; empty path sends `items: ""`, not `[]`
- [ ] every path ends `text_id: ""` + non-empty `attachment_id` into the Router, or the reply is sent twice
- [ ] ask/confirm steps use a reply `keyboard`, not `inline_keyboard`
- [ ] smoke test above observed green, including the Send Message render
- [ ] regression per `regression_scope` observed green
````

### Chat summary format

Print this and stop:

```
📝 CHANGE.md готов — {kind}: {title}.
Команды: {commands_affected}.
Процессы: {N} ({add} новых, {edit} правок, {del} удалений/пауз).
Задачи: {tasks_touched summary}.
Проброс значений: {forwarding_impact summary, or "не требуется"}.
Регрессия: {touched commands | все команды — общий ключ/шаблон}.
Проверка: {one line naming the smoke test}.
{One bullet per risk, if any — dangling alias, live side effects, shared key.}

Поправить или /corezoid-edit-bot execute?
```

Do not proceed to Step 4 until the user says `execute` / `погнали` / `go`. For
`kind: template-edit`, the confirmation must name the process — a bare "go" is
not consent to edit Router. For a change whose smoke test fires `likely` side
effects, the confirmation must name that too.

## Step 3: Refining CHANGE.md

Same rules as corezoid-gen-bot §3a: targeted `Edit`s, re-print the summary, one
sentence of prose at most. Never widen the scope silently — if the user's
follow-up is a different change kind, rewrite CHANGE.md rather than appending
to it. Never re-extract a domain contract for a CHANGE.md edit.

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
  Code nodes.
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

These hold runtime task data, so they are written with task tools, never
`push-process`.

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

## Step 6: Verify

Run the CHANGE.md verification checklist, in this order, stopping at the first
failure. `references/repair_loop.md` maps a symptom to the layer at fault.

1. `grep -rnE '\{\{[A-Z_]+\}\}'` over touched processes — empty.
2. `grep -rniE 'bot[0-9]{6,}:|page_access_token|viber_token|abc_token'` over
   the whole tree including PLAN.md/CHANGE.md — empty.
3. `lint-process` clean on every touched process.
4. `show-task process_id: {template_ids.localization} ref: localization` — every
   `text_id` any touched process
   references exists in **every language the wizard seeded**, and every
   `{{placeholder}}` in it is a key the sending process actually produces
   (`success.keys` in `bot-contract.json`). Also: `{{t'key}}` with no dot, and
   no dotted `{{a.b}}` anywhere — the interpolation regex is `/{{\w+}}/ig` and
   `\w` excludes `.`, so a dotted placeholder reaches the chat verbatim.
5. `show-task process_id: {template_ids.attachments} ref: <attachment_id>` per
   touched attachment — one key per channel in `channels`,
   remembering the naming asymmetry: the wizard's messenger key is
   `fbmessenger`, but Send Message dispatches on `channel == "facebook"`, so
   attachment keys and `go_if_const` conditions use `facebook`. A check written
   against `fbmessenger` passes while the channel renders nothing.
6. **Forwarding check — the one nothing else catches.** For every Send Message
   `api_copy` in every touched process, take the `text_id` it sends, look up
   that string in Localization, and confirm each `{{var}}` in it is a key of
   **that node's `data`**; then confirm every node whose `attachment_id` is a
   dynamic pattern also forwards `items` and `currentPage`. Do this as a script
   over the schemes, not by eye — `show-task` returns hundreds of lines of
   template copy with no way to diff it against the graph. Two shapes need care:
   a node sending the literal `{{text_id}}` can carry any `text_id` a Code node
   assigns, so union them (and take a ternary's strings from after the `?`); and
   where the union is wider than a branch can reach, forward the extra values
   anyway — it costs a few fields and keeps the invariant true if a branch is
   ever repointed.
7. **`/end` check.** Every `END -> Router` `api_copy` carries `text_id: ""`
   **and** a non-empty `attachment_id`, or the reply is delivered twice — the
   second copy stripped of every interpolated value
   (`references/invariants.md` §2).
8. **The command on its own** — `run-task` at the touched command with
   `{"channel":"telegram","chat_id":"<test id>","message":{"type":"text","text":"/<command>"}}`.
   Assert the domain `api_rpc` returned, the namespaced keys are populated where
   CHANGE.md says namespacing is required, `text_id` was set, and the task
   reached `END -> Router`. A dialog parks on its wait node here **by design** —
   that is the `api_callback` working; assert it *reached* the node, then deliver
   the answer the way the Router does (an `api_copy` `mode:"modify"` on ref
   `<channel>_<chat_id>`, or `modify-task` on the same ref) and confirm it
   advances. A task parked forever on the domain call means the callee is paused
   or reply-less: that is a contract misread, not a wiring bug — go back to
   `/corezoid-gen-bot refresh`. Inspect with `list-task-history` when a task did
   not go where you expected.
9. **Render the message the way the user receives it.** Step 8 proves the
   command computed the right things; it does **not** prove the user sees them.
   The task data at `Done` will happily show
   `bonusAmount: "0.00", text_id: "balanceDone"` while the delivered message
   reads `Your card has {{bonusAmount}} bonus points.`, because the defect lives
   in the `api_copy`'s `data` block. So per distinct `text_id` this change
   touched, `run-task` on **Send Message** itself with
   `{channel, chat_id, text_id, attachment_id}` plus the values the text
   interpolates, and assert on `data.text` and `reply_markup`. With a synthetic
   `chat_id` the Telegram call fails with `Bad Request: chat not found` — that is
   expected and fine, because the text and attachment are resolved *before* the
   send. **For a `copy` or `keyboard` change this is the primary test**, not the
   Router run.
10. **The smoke test from CHANGE.md**, observed, not assumed:
    ```
    run-task  process_path: <Router path>  wait_sec: 60 \
              data: {"channel":"telegram","chat_id":"<test chat id>","command":"/order-status","message":{"type":"text","text":"/order-status"}}
    ```
    This is the only layer that proves the alias dispatch. `Command not found`
    means the alias is wrong or missing. Then `show-task` on the command process
    (ref `telegram_<chat_id>`) to confirm it started. Walk every alternate
    outcome you can trigger — those are what a happy-path-only test misses.
11. **Regression**, per `regression_scope`: at least one command this change did
    not touch, or **every** command for a shared Localization/Attachments key or
    a `template-edit` — that is what "shared by all commands and all four
    channels" means.
12. **Live check** where the user can do one. `run-task` proves the graph; only a
    real client proves the webhook, the token and the rendered keyboard.

A clean lint is not a passing test. Never report a command as working without an
observed run behind it.

### Step 6b: deploy mode

```
deploy-stage  project_id: <int>  source_stage_id: <int>  target_stage_id: <int> \
              company_id: "<workspace id, string>"
              # apply defaults to false — dry run, shows the diff and conflicts
```

Show the user the diff. Only then, with their confirmation of the exact
source→target:

```
deploy-stage  … apply: true  confirm: "<source_stage_id>-><target_stage_id>"
```

Destructive, and irreversible on an immutable target. After it lands, re-run the
Step 6 smoke test **against the target stage**: aliases and env vars are
stage-scoped and are not migrated by the merge, the target's Localization and
Attachments tasks are runtime data that a scheme merge does not carry, and the
domain processes the bot calls may not exist there at all. A command carrying a
**numeric** `conv_id` promotes into a stage where that id means something else
or nothing — which is why domain calls use `@alias`, and why this check exists.

## Step 7: Update PLAN.md and report

1. **Apply `plan_impact`** — edit PLAN.md's `Commands`, `Coverage`,
   `Localization keys`, `Attachments`, `Menu wiring`, `locales` and
   `template_ids` to match what now exists, and `bot-contract.json` if this
   change re-derived a contract. PLAN.md describes the live orchestrator; a
   stale plan makes the next edit guess, and a stale `Coverage` table is how a
   process quietly stops being served.
2. Leave CHANGE.md in place as the record of this change. The next `plan` run
   overwrites it.
3. Report in one compact block:
   - What changed — processes created/pushed/paused/deleted, aliases created,
     tasks written, env vars touched.
   - **Aliases left dangling**, and what still resolves because of it.
   - **Smoke-test results** — one row per command exercised, with the observed
     outcome and, for anything red, the node the task parked at. Include the
     Send Message render for every touched `text_id`.
   - Regression result, and its scope.
   - Any waived gate (`allow_no_snapshot`, `overwrite_server_change`) and why.
   - Anything the user must do by hand (delete an alias through the API,
     register a webhook, translate a string you could not).
   - `folder_url` for convenience.

## Rules

- **Never call `create-communications-orchestrator` from this skill.** It builds
  a whole second orchestrator that steals the webhooks from this one, and there
  is no undo. Adding a channel is the one request that genuinely needs a new
  build — say so and hand back to `corezoid-gen-bot`.
- **`create-alias` is the only alias tool there is.** An alias cannot be
  deleted, repointed or renamed by any MCP tool, so a rename creates a second
  alias and a removal leaves the name resolving. Say what survives; hand real
  alias surgery to `corezoid:corezoid-alias-manager`.
- **`pull-folder` requires a `folder_id`, and that value is the *stage* id from
  the marker.** It writes to the stage root, not the cwd. The orchestrator's own
  folder id fetches only that folder and still unzips it at the stage root,
  destroying the folder boundary — after any pull, find the orchestrator by its
  `<folder_id>_` name prefix, and stop if it is absent.
- **Never hardcode a template process id.** Read every id from the pulled
  mirror into PLAN.md `template_ids`. Ids in
  `corezoid-gen-bot/references/template_map.md` are examples from one build, and
  a stale id fails silently — the task simply never arrives.
- **Never rename a pulled `<ID>_<Title>.conv.json`.** `push-process` and
  `lint-process` recover the process id from the filename.
- **`push-process` cannot create a process.** `create-process`, then
  `pull-process` for the baseline, then write the scheme, then push.
- **Answer the callability question before wiring a process**
  (`contract_extraction.md` §2). An `api_rpc` into a paused or reply-less
  process parks the task until the semaphore fires — a user waiting the full
  30 s for an error — and `params` does not reveal it.
- **Every `api_rpc` into a domain process carries a `time` semaphore of ≥ 30 s**,
  routed to a node that tells the user and then copies `/end` into the Router.
  Lint rejects anything below 30 s, so 30 s is also the floor on how fast a
  command can fail.
- **Call a domain process with `group: ""` and an explicit `extra`.**
  `group: "all"` forwards the whole task — `channel`, `chat_id`, `message`, the
  Router's bookkeeping — into somebody else's process.
- **Adding a second domain call to a command means namespacing both calls**,
  with an `api_code` rather than a `set_param`.
- **A generated command must forward everything its message needs.** The
  `api_copy` into Send Message sends only the fields in its `data`, and
  `{{var}}` resolves against Send Message's own task — so forward each value the
  text interpolates, plus `items`/`currentPage` for a dynamic attachment, and
  use `items: ""` (not `[]`) when there are no rows. Changing copy that adds a
  placeholder is therefore a process change, not just a task change.
- **End every path with `text_id: ""` and a non-empty `attachment_id`.**
  `group:"all"` leaks the command's own `text_id` into `/end`, and the Router
  then sends the same message a second time without any interpolated value.
- **`group:"all"` carries the whole task; `group:""` sends only `data`.** Send
  Message takes `group:""` with explicit `channel`/`chat_id`; the Router takes
  `group:"all"`.
- **Every command ends by copying into the Router with `{"command":"/end"}` and
  `group:"all"`** — including its error and timeout paths. A command that exits
  any other way leaves the chat's System Diagram state `active`, so the user's
  next message goes to a finished bot.
- **Never edit a handed-in domain process.** Hand it to
  `corezoid:corezoid-edit` with the user's agreement instead, then
  `/corezoid-gen-bot refresh`.
- **Never probe or smoke-test a `likely`/`unknown` side effect without the
  user's agreement.** A `run-task` can send a real SMS or charge a real card.
- **Keep the coverage table honest.** If a change stops a handed-in process
  being served, move it to `Skipped processes` with a reason — never leave the
  coverage row pointing at a command that no longer calls it.
- **A command must be a legal alias once the `/` is stripped**
  (`^/[a-z0-9][a-z0-9-]{2,}$`), and it must have an alias. Check candidates
  against `_ALIASES_.json` — including `obj_to_id: null` rows, which hold a name
  and resolve to nothing — and match on `obj_to_id`, not on the name, before
  *calling* anything by `@alias`.
- **Never generate or rename to `/start`, `/end`, `/exit`**, or to an alias
  already in the stage.
- **`modify-task` always with `deep_merge: true`** on Localization and
  Attachments, and `show-task` before writing. Shallow is the default and it
  silently drops the sub-keys you did not send.
- **Seed every language the wizard created (`en`, `ru`, `uk`), not just `lang`.**
  Send Message reads `User Profile.language` from the client's locale and there
  is no fallback: a missing language for a `text_id` delivers an empty message.
- **`serviceError` is not shipped by the wizard** — if a command you touch
  references it and Localization lacks it, seed it, or every error path delivers
  an empty message.
- **`{{t'key}}` has no dot, and `{{var}}` is flat-keys-only.** `{{t'.key}}`
  matches the replacer's regex and then fails the lookup; `{{a.b}}` is never
  matched at all (`\w` excludes `.`) and reaches the chat verbatim. Flatten in a
  Code node first.
- **Attachment and condition keys use `facebook`; only the wizard's messenger
  argument is `fbmessenger`.** A per-channel check written against the wrong one
  passes while that channel renders nothing.
- **Ask and confirm steps use a reply `keyboard`, never an `inline_keyboard`.**
  A wait node reads `message.text`; `inline_keyboard` sends `callback_data`,
  which `Main → PARSE command` interprets as a new command.
- **Button payloads follow `/cmd__k1-v1_k2-v2`.** A literal `-` or `_` inside a
  value splits it — never put free text in a payload.
- **Reference nodes by title, re-read after every push.** `push-process`
  regenerates node ids and rewrites the local file.
- **A state-diagram `run-task` reporting "parked at a non-final node"
  succeeded.** Do not retry it or treat it as an error.
- **Corezoid Code nodes are ES5, and `{{...}}` is not interpolated in `src`.**
  No `let`/`const`, arrow functions or template literals; read task data as
  `data.x`. Any `{{UPPER_CASE}}` in a skeleton's `src` is substituted by the
  generator, not at runtime.
- **Alternate outcomes are outcomes, not errors.** `recovery`, `registration`
  and friends keep their own `text_id`; mapping them to `serviceError` makes the
  bot answer "something went wrong" to a normal user.
- **A success text may only use keys in that process's `success.keys`.**
- **Never `push-process --force`** past a structural lint finding, and prefer
  `merge=true` over `overwrite_server_change` when the server has changed. Never
  `overwrite_server_change` or `allow_no_snapshot` without showing the user the
  report and getting explicit agreement — and never on an immutable or
  production-like stage, where the platform refuses them anyway.
- **A template edit needs named confirmation, a snapshot, and a full-command
  regression.** The wizard's processes are shared by every command and all four
  channels.
- **A shared Localization or Attachments key has a template-sized blast
  radius.** `mainMenu`, `mainKeyboard`, `serviceError`, `timeout`,
  `commandNotFound`, `selectError`, `carouselPattern` — regression-test every
  command.
- **A clean `run-task` at `Done` is not proof the user saw the right message.**
  Render each touched `text_id` through Send Message and assert on `data.text`
  and `reply_markup`.
- **Cap the repair loop at about three passes per defect.** If it is not
  converging, stop and report precisely what fails, what you tried and what you
  think the cause is. "7 of 8 commands pass, this one doesn't, here's why" is
  worth far more than a loop that quietly gives up.
- **State the evidence for any claim about the backend.** Name the field you
  read before calling a domain process broken.
- **Tokens and credentials never touch a file.** Not PLAN.md, not CHANGE.md,
  not process JSON, not the chat summary.
- **Never call `EnterPlanMode`.** The plan phase is normal execution — Steps
  1–3 need Bash, Write and MCP tool calls.
- **CHANGE.md is written by the AI only,** and it is the sole source of truth
  for Steps 4–7. If a fact needed there is missing, that is a bug in Step 2 —
  fix CHANGE.md first.
- **Report only what was observed.** No command is green on the strength of a
  clean lint.
