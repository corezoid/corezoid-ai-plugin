---
name: corezoid-edit-bot
description: Iteratively edits an already-built messenger bot from corezoid-gen-bot — a Corezoid Communications Orchestrator serving Telegram, Viber, Apple Messages for Business and Facebook Messenger over a backend of Corezoid processes called with api_rpc. Runs a lean plan → confirm → apply → lint → push → smoke-test loop — see "Modes" for plan/execute/deploy. Trigger on "измени/поправь бота", "добавь/убери/переименуй команду", "подключи ещё процесс", "поменяй текст/клавиатуру бота", "добавь язык боту", "добавь шаг в диалог", "бот отвечает пустым/дважды", "почему бот не отвечает", "edit/modify bot", "add command to bot", "wire another process into the bot", "change bot copy", "fix the bot", "задеплой бота", "promote stage", or when the user references PLAN.md/CHANGE.md for a bot. Assumes CWD is the workspace corezoid-gen-bot built (`.corezoid-gen-bot/PLAN.md`, a `*.stage.json` marker, the pulled orchestrator folder). For a Smart Form web app over the same processes use `simulator-app-generator`.
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
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/change_kinds.md` | what each request kind may touch, its verification, and what this skill deliberately cannot do |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/invariants.md` | the send-side and dispatch invariants every change must preserve — all of them silent when broken |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/repair_loop.md` | symptom → cause → the layer at fault, and when to stop looping and report |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/template_map.md` | the orchestrator's own contracts (Router, Send Message, Localization, Attachments) |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/contract_extraction.md` | how to read a domain process — needed for `wire-process` and `process-remap` |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/rpc_call.nodes.json` | the `api_rpc` / `api_copy` / state-read fragments |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/bot_reply.skeleton.json`, `bot_dialog.skeleton.json` | the two command shapes, for a new command |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/attachments.seed.json`, `localization.seed.json` | per-channel attachment shapes, per-page caps, interpolation rules |

Load them with the `Read` tool. `${CLAUDE_PLUGIN_ROOT}` resolves to the
installed plugin root; a bare `references/…` does **not** — this skill runs with
the Corezoid workspace as the working directory. Every short
`references/<file>` named later in this document is under
`${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/`, and every
`corezoid-gen-bot/references/<file>` under
`${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/`.

Steps 4–7 (execute mode) are each detailed in their own reference file — load
the one for the step you're about to run, not all of them up front:

| Document | Step |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/step4_apply_change.md` | Step 4 — apply the change |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/step5_lint_push.md` | Step 5 — lint and push |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/step6_verify.md` | Step 6 — verify (and Step 6b — deploy mode) |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/step7_report.md` | Step 7 — update PLAN.md and report |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-edit-bot/references/rules.md` | Full rule list — read before Step 4 |

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

Corezoid MCP tools this skill uses directly: `pull-process`, `pull-folder`,
`push-process`, `lint-process`, `layout-process`, `create-process`,
`delete-process`, `pause-process`, `resume-process`, `create-alias`,
`run-task`, `deploy-stage`.

The rest are **actions of a router tool** — call them as
`<router> {"action": "<action>", "args": {…}}`, every argument inside `args`:

| Action | Router |
|--------|--------|
| `show-task`, `modify-task`, `list-task-history`, `list-node-tasks` | `cz-tasks` |
| `create-variable`, `modify-variable` | `cz-variables` |
| `create-snapshot` | `cz-snapshots` |

Add `"help": true` to any of them to get its full argument schema back without
running it. Missing → tell the user to install the Corezoid plugin and run
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

Read `references/step4_apply_change.md` before running this step.

Work only inside `processes_touched`, `tasks_touched`, `env_vars_touched` and
the create/delete lists CHANGE.md named. Covers: `push-process` cannot create a
process (create → pull for baseline → write → push); a new command starts from
the right skeleton with every `{{UPPER_CASE}}` placeholder substituted
(`parent_id`/`user_id` are numeric, not string); edited commands are referenced
by title, never a remembered id; removing a command prefers `pause-process` or
`delete-process` over deletion, and the alias survives either way; a second
domain call in a command needs a namespacing `api_code`, not a `set_param`;
every new `{{var}}` in a text or a repointed call changes the Send Message
`data` block too; and a handed-in domain process is never edited from this
skill.

## Step 5: Lint and push

Read `references/step5_lint_push.md` before running this step.

Per touched process: `layout-process` → `lint-process` → `push-process` →
`create-alias` for new commands. Never `force=true` past a structural finding.
A concurrent server change resolves with `merge=true` or a re-pull, never
`overwrite_server_change` without showing the user the report first. A
never-deployed process is exempt from the snapshot gate; anything else that
blocks on a snapshot should wait, not waive.

## Step 6: Verify

Read `references/step6_verify.md` before running this step.

Run the CHANGE.md verification checklist in order, stopping at the first
failure: no leftover placeholders or tokens in the tree, clean lint, every
`text_id`/`attachment_id` resolves in every seeded language and channel, the
forwarding check (the one nothing else catches), the `/end` check, the touched
command on its own via `run-task`, the message rendered through Send Message
(not just the command's own task data), the CHANGE.md smoke test observed, and
regression per `regression_scope`. `references/repair_loop.md` maps a symptom
to the layer at fault. A clean lint is not a passing test.

**Step 6b — deploy mode:** `deploy-stage` dry-run first, show the diff, then
`apply: true` with the exact `confirm: "<source>-><target>"`. Re-run the Step 6
smoke test against the **target** stage afterward — aliases, env vars,
Localization/Attachments task data and domain processes are not guaranteed to
carry over, and a numeric `conv_id` promotes into a stage where it means
something else or nothing. Full detail in `references/step6_verify.md`.

## Step 7: Update PLAN.md and report

Read `references/step7_report.md` before running this step.

Apply `plan_impact` to PLAN.md (`Commands`, `Coverage`, `Localization keys`,
`Attachments`, `Menu wiring`, `locales`, `template_ids`) so it still describes
the live orchestrator, leave CHANGE.md as the record of this change, then
report: what changed, aliases left dangling, smoke-test results per command
including the Send Message render, regression result, any waived gate, what
the user must do by hand, and `folder_url`.

## Rules

Full rule list, one per line with its reasoning: `references/rules.md`. Read it
before Step 4 — it is a checklist to re-check against, not new material. The
handful that matter most, everywhere: never call
`create-communications-orchestrator` from this skill (no undo, steals the
webhook from a live bot); `create-alias` is the only alias tool — an alias
cannot be deleted, repointed or renamed; never hardcode a template process id,
always re-read `template_ids` from the pulled mirror; never rename a pulled
`<ID>_<Title>.conv.json`; every `api_rpc` into a domain process needs a ≥30 s
`time` semaphore and `group: ""` with an explicit `extra`; never edit a
handed-in domain process from this skill; never probe or smoke-test a
`likely`/`unknown` side effect without the user's agreement; tokens and
credentials never touch a file; never call `EnterPlanMode`; cap the repair loop
at about three passes per defect and report precisely what fails; report only
what was observed — no command is green on the strength of a clean lint.
