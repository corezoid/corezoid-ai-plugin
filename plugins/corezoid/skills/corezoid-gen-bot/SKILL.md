---
name: corezoid-gen-bot
description: Generates a multi-platform messenger bot (Telegram, Viber, Apple Messages for Business, Facebook Messenger) on Corezoid from a set of EXISTING Corezoid processes plus a description of the desired bot. Use when the user has a working Corezoid backend (process ids, a folder id, or pulled .conv.json files) and wants it reachable from a chat. Runs in phases via subcommands plan/execute/refresh — see the "Modes" section. Trigger on "сделай бота из этих процессов", "сгенерируй бота", "бот на базе процессов корезоид", "telegram/viber/fbmessenger bot from corezoid processes", "corezoid gen bot", "оберни процессы в бота", "создай оркестратор коммуникаций", "communications orchestrator", "згенеруй бота з цих процесів", and on "execute plan" / "погнали" / "refresh plan" when a `.corezoid-gen-bot/` directory is present. For a Smart Form web app over the same processes use `simulator-app-generator`; to change a bot that already exists use `edit-bot`.
---

# corezoid-gen-bot

Turns **a set of existing Corezoid processes + a description of the bot** into a
**deployed, smoke-tested messenger bot**. One bot serves every channel the user
has a token for — Telegram, Viber, Apple Messages for Business (`abc`), Facebook
Messenger (`fbmessenger`) — and one channel is enough; the command processes are
the same whichever channels are wired.

The skill owns three things nothing else does:

1. **Contract extraction** — deriving what each handed-in process really
   consumes and produces, and whether it can be called at all.
2. **Command design** — turning N process contracts into a coherent set of chat
   commands that uses all of them.
3. **Verification** — proving each command actually reaches its process and its
   user, and repairing it when it doesn't.

Everything else is a template. `create-communications-orchestrator` builds ~150
Corezoid processes (channel receivers, router, per-channel API methods, config
state diagrams, sample bots); the skill adds one process per command and the
copy those processes reference.

Read these before generating anything:

Load them with the `Read` tool. `${CLAUDE_PLUGIN_ROOT}` resolves to the
installed plugin root; a bare `references/…` does **not** — this skill runs with
the Corezoid workspace as the working directory, where no such directory exists.
Every short `references/<file>` named later in this document lives in
`${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/`.

| Document | What it settles |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/contract_extraction.md` | how to read a handed-in process; the manifest format |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/command_patterns.md` | contract shape → command shape; the coverage rule |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/template_map.md` | the orchestrator's contracts, and how a command calls a domain process |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/rpc_call.nodes.json` | the `api_rpc` / `api_copy` / state-read fragments |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/bot_reply.skeleton.json`, `bot_dialog.skeleton.json` | the two command shapes |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/localization.seed.json`, `attachments.seed.json` | the runtime copy and keyboard documents |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/plan_md_schema.md` | the exact PLAN.md schema Phase 3 writes |

Phases 4–8 (execute mode) are each detailed in their own reference file — load
the one for the phase you're about to run, not all of them up front:

| Document | Phase |
|---|---|
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/phase4_build_orchestrator.md` | Phase 4 — build the orchestrator from the template |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/phase5_generate_commands.md` | Phase 5 — generate one process per command |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/phase6_seed_tasks.md` | Phase 6 — seed Localization, Attachments and the menu |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/phase7_verify_repair.md` | Phase 7 — verify, and repair |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/phase8_report.md` | Phase 8 — APPROACH.md, then report |
| `${CLAUDE_PLUGIN_ROOT}/skills/corezoid-gen-bot/references/rules.md` | Full rule list — read before Phase 4 |

## 0. Preflight

- **Corezoid MCP tools present** — `pull-process`, `pull-folder`, `push-process`,
  `lint-process`, `layout-process`, `create-alias`, `run-task` and
  `create-communications-orchestrator`, plus the router actions `show-task`,
  `modify-task`, `list-node-tasks` (`cz-tasks`) and `create-variable`
  (`cz-variables`). Missing → tell the user to install the Corezoid plugin
  and run `/corezoid-init`.
- **Router call shape** — an action is called as
  `cz-tasks {"action": "show-task", "args": {"process_id": 123, "ref": "localization"}}`,
  every argument inside `args`; the `show-task process_id: … ref: …` shorthand
  used below means exactly that. `"help": true` returns an action's full schema
  and runs nothing.
- **A stage resolves.** The usual sign is a `<id>_<name>.stage.json` marker at
  the workspace root, and most tools resolve stage from it. But the marker is
  not the only source: the MCP server also resolves stage from
  `~/.corezoid/config.json`, keyed by the working directory, so a workspace that
  has been logged into but never pulled has **no marker and still works** — the
  marker appears with the first `pull-folder`. So probe rather than refuse: try
  `pull-process` on one handed-in id. If it succeeds you have a stage (read
  `project_id`/`stage_id` from the config entry for this directory and pass them
  explicitly in Phase 4, since the wizard's defaults lean on the marker). Only if
  it fails is this a `login` problem (see the `corezoid-init` skill).
- **The workspace id** is known; the wizard needs it.

## What this skill accepts

| Input | Form | Required |
|---|---|---|
| **Processes** | a list of numeric process ids, **or** one folder id, **or** paths to already-pulled `<ID>_<Title>.conv.json` files | yes |
| **Bot description** | free text: who talks to it, what they should be able to do | yes |
| **Channels + tokens** | at least one; exact field names below | yes |
| Languages | the wizard's `lang` + every language to seed | no (`en`) |
| Corezoid target | `stage_id` / `project_id` | no (from the marker, or from `~/.corezoid/config.json` when there is none) |
| Test data | inputs that make a process return something real | no |
| Reference materials | an existing bot, screenshots of the flow, copy deck, product brief | no, but strongly encouraged |

Channel token fields — the wizard rejects anything else with a generic
`Value is not valid`, so get them exactly right:

| Channel | Token field | Extra |
|---|---|---|
| `telegram` | `key` | — |
| `viber` | `viber_token` | — |
| `fbmessenger` | `page_access_token` | — |
| `abc` | `abc_token` | `user_id` (int), `email`, `name` — the brand contact |

One entry per channel; a channel listed twice is rejected. `messengers` is
mandatory for the wizard — there is no "build the template now, wire channels
later" path.

If the description is missing or vague, ask **once** for: who uses the bot, what
they should be able to do, and which of the handed-in processes matter most. Do
not start designing without it.

**Credentials never enter a file.** Channel tokens are wizard arguments and
nothing else — not PLAN.md, not APPROACH.md, not a process JSON, not the chat
summary. A pulled mirror is a git-tracked artifact. In PLAN.md record only
`channels: [...]` and `tokens_supplied: true`.

## Modes

| Invocation | Mode | What runs |
|---|---|---|
| `/corezoid-gen-bot plan <ids or folder>` | **plan** | Phases 1–3 — pull, extract contracts, design, write PLAN.md, stop |
| `/corezoid-gen-bot execute` (or "погнали", "go") | **execute** | Phases 4–8 — build, generate, seed, verify, report |
| `/corezoid-gen-bot refresh` | **refresh** | Phase 2 only — re-pull, re-derive, patch PLAN.md, print diff, stop |
| `/corezoid-gen-bot <ids or folder>` no subcommand | **full** | plan → summary → **pause** → execute only after the user confirms; never automatically |

`.corezoid-gen-bot/PLAN.md` is the source of truth between phases, with
`.corezoid-gen-bot/bot-contract.json` as the machine-readable manifest beside it.
The user never opens either: they read the chat summary and ask for changes in
natural language ("убери /history", "переименуй в /order", "не спрашивай
телефон"), and you translate that into targeted `Edit`s and re-print the
summary.

### `execute` is not idempotent — the sharp edge of this skill

`create-communications-orchestrator` mints a **new folder of ~150 processes**
every call, and there is no delete-orchestrator counterpart. Running execute
twice leaves two orchestrators bound to the same channel tokens, and a messenger
webhook points at one — so the second build silently steals the traffic and the
first becomes ~150 dead processes someone removes by hand.

- Check PLAN.md's `orchestrator.folder_id` before Phase 4. Set → resume from
  Phase 5.
- The tool now refuses to build without `apply=true` plus a confirm token bound
  to the target stage, the channel set and the channel credentials, so a stray
  call previews instead of building. Treat that as a backstop, not as permission to skip the checks
  here: the token is trivial to supply, and the gate cannot tell an approved
  build from a repeated one.
- Write the wizard's response into PLAN.md **immediately**. A crash between
  "wizard returned" and "plan updated" is the one state that cannot be
  recovered automatically.
- The wizard polls 10×3 s. On timeout it returns the job id — **do not retry.**
  Ask the user to check the stage and resume with the folder id they report.
- Changing an orchestrator that exists is `edit-bot`, never a second execute.

### Never call `EnterPlanMode`

The plan phase here is normal execution: it needs Bash, Write and MCP tool calls
to pull processes and probe. `EnterPlanMode` blocks all three.

### Choosing a mode from a plain-text request

- Process ids / a folder id **and** a plan keyword (`plan`, `только план`) → **plan**.
- Execute keyword and PLAN.md exists → **execute**.
- Refresh keyword and PLAN.md exists → **refresh**.
- Process ids with no phase keyword → **full**.
- PLAN.md exists and the user asks to change something in it → PLAN.md edit (§5).
- PLAN.md has a `folder_id` and the user asks for a change → hand off to `edit-bot`.

## Phase 1 — Acquire

### 1.1 Inventory what is already on disk — before pulling anything

The workspace is usually an already-pulled stage, so most or all of the
handed-in processes are likely sitting there. **Take inventory first and pull
only what is missing.** Re-pulling a process that is already local costs an API
round-trip per process, and `pull-process` **overwrites the local file** — if
somebody had unpushed edits in it, a blind re-pull destroys them.

The id is the identity: a process is on disk iff a file named
`<id>_<anything>.conv.json` exists anywhere under the stage root. Match the
prefix exactly — `1906756_` must not be satisfied by `11906756_`.

```bash
# stage root = the directory holding the <id>_<name>.stage.json marker
root="$(dirname "$(ls *.stage.json */*.stage.json 2>/dev/null | head -n1)")"; root="${root:-.}"

for id in 1760349 1760347 …; do
  hit="$(find "$root" -name "${id}_*.conv.json" -print -quit)"
  if [ -n "$hit" ]; then echo "local   $id  $hit"
  else                   echo "missing $id"; fi
done
```

Report the split before pulling: `{L} of {N} processes already on disk, pulling
{M}`. If the working tree is a git repo, also check whether any of the local
hits are dirty (`git status --short`) — a dirty file is somebody's work in
progress, and it is the one case where you must ask before re-pulling it.

### 1.2 Pull only the missing ones

`pull-process(process_id=<id>)` per missing id — it writes
`<ID>_<Title>.conv.json` into a directory mirroring the process's location in
Corezoid.

If the user gave a **folder** instead of ids, check for that folder locally
first (`<folder_id>_*` under the stage root). If it is absent, call
`pull-folder` **with the stage id** (`folder_id` is required over MCP — see
§4.2): it mirrors the whole stage into the **stage root** (not the current
directory), so the processes land at their real paths. Then locate the folder by
its `<folder_id>_` name prefix. Passing that folder's id to `pull-folder`
instead unzips its contents over the stage root and loses the folder boundary —
the same trap as §4.2.

> **Never rename these files.** The `<ID>_` prefix is load-bearing:
> `push-process` and `lint-process` recover the process id from it and fail with
> a format error otherwise.

### 1.3 Record provenance

Per process record the local path, the alias if it has one (`@alias` survives a
stage promotion where a numeric id does not), and **where the file came from**:
`source: "local"` (reused, with its mtime) or `source: "pulled"`. This goes in
the manifest and in the plan summary.

Provenance matters because a reused file is a **snapshot of unknown age**, and
Phase 2 derives the whole contract from it: a process that gained an alternate
outcome or lost a reply node since that snapshot yields a command that
mis-branches at runtime. There is no cheap staleness check — confirming a local
file is current *is* pulling it — so do not pretend to verify it. State the age
plainly, and if a reused file is more than a few days old, or its contract turns
out to be the load-bearing one for a command, offer to re-pull that process
rather than guessing.

In **refresh** mode this reuse is skipped entirely: refresh exists to re-read
the server, so it re-pulls every process in `sources` unconditionally.

## Phase 2 — Contract extraction

Follow `references/contract_extraction.md` and produce
`.corezoid-gen-bot/bot-contract.json`. The short version of what it covers, in
the order the questions matter:

1. **Is it callable?** `conv_type: "process"` with ≥1 `api_rpc_reply` *reachable
   from Start* → `api_rpc`. No reachable reply node → `api_copy` only, and the
   command can never show a result. `conv_type: "state"` → not called, read
   inline. Paused → not wired at all until the user says so. An `api_rpc` into a
   reply-less or paused process **hangs until the semaphore fires**, which in a
   chat is a user waiting the full 30 s for an error — this is the most
   expensive misread in the skill and it is invisible in `params`.
2. **Outputs** from the `api_rpc_reply` nodes' `res_data` / `res_data_type`,
   grouped into **success / alternate / error**. `throw_exception: true` replies
   reach the caller as an error on the calling node, not as data.
3. **Alternate outcomes are the most commonly missed signal** — `402/registration`,
   `401/recovery` are control flow, not failure. A command that reads only the
   200 branch answers "something went wrong" to a perfectly normal new user.
4. **Types** by majority vote across sibling reply nodes; they disagree, and a
   disagreement is not an error.
5. **Array element shape** from the reply node's `description`, then an upstream
   trace, then a live probe. `unknown` is a legitimate answer to record.
6. **Inputs** from `params[flags ∋ input]` ∪ placeholders consumed before being
   produced. Payloads live in three fields: `extra` (`api_rpc`/`set_param`),
   **`data` (`api_copy`)**, **`raw_body`** (raw `api`). Read all three or you
   will report a process as input-free when it is not.
7. **Which inputs the bot already has** — `chat_id`, `channel`, and everything
   on User Profile. A dialog that asks for a value the bot can read is a worse
   bot; a dialog that asks for a token or password is a security defect.
8. **Side effects** `likely | unlikely | unknown`, scored over the
   **reachable-from-Start** subgraph only. This gates probing and whether the
   command needs a confirmation step.

`params[]` is a hint and a cross-check, **never the source of truth** — it
drifts, and in the reference set five of twelve processes declared zero outputs
while clearly replying with data.

### Phase 2b — Ground-truth probing (opt-in, user-gated)

`run-task(process_path, data)` returns a process's **real** reply — the only
reliable way to resolve an unknown array shape or an undocumented envelope.

**Never probe automatically.** Show the side-effect classification, let the user
pick which processes are safe to call with test data, and probe only those.
Calling a process blind can send a real SMS or Telegram message to a real
customer, or burn a real card number. Feed anything learned back into the
manifest.

### Report the contracts before designing

Show a contract table and explicitly flag: processes with no reachable reply
node, paused processes, arrays with unknown element shape, the side-effect
classification, any lopsided reachable/total node ratio, and every place
`params` disagreed with the reply nodes — including declared inputs no node
consumes.

**State the evidence for any claim about the backend.** "This process is broken"
is a finding about someone else's system, and the payload-carrier traps make it
easy to get wrong. Name the field you read.

## Phase 3 — Command design, then PLAN.md

Turn N contracts into a command map with `references/command_patterns.md`. The
three rules that carry the most weight:

- **Coverage.** Every handed-in process appears in the map, in an explicit
  coverage table. If one genuinely does not fit the described bot, say so and
  ask — drop it, give it a plain command, or reshape the bot. Silent omission is
  the main way this skill produces a wrong result that still looks finished.
- **Six `ask` inputs is the practical ceiling for a chat.** Every question is a
  place the user abandons. Past that, split the command, replace inputs the bot
  can read, or use a channel form — and say which you chose.
- **Every command name must be a free alias, checked now.** `create-alias`
  cannot repoint an existing alias and the plugin has no delete-alias tool, so a
  taken name is taken for good — and a name that is only discovered to be taken
  in Phase 5 costs a rename that cascades through `text_id`s, button labels, the
  menu and the process title. Check the candidates against `_ALIASES_.json`
  before writing PLAN.md (`command_patterns.md` → "Check every name is free"),
  and watch for rows with `obj_to_id: null` — a dangling alias holds the name
  just as firmly as a live one. Record the result in PLAN.md, and if a preferred
  name is blocked, say which name you used instead and why.

### PLAN.md schema

The exact schema — frontmatter fields plus the `Contract summary`, `Coverage`,
`Commands`, `Localization keys`, `Attachments`, `Menu wiring`, `Skipped
processes`, `Reference materials consulted` and `Known unknowns / risks`
sections — is in `references/plan_md_schema.md`. Read it before writing
PLAN.md for the first time in this run.

### Chat summary format

Print **exactly** this block (≤ 16 lines) and stop:

```
📋 PLAN.md готов ({P} процессов → {N} команд, каналы: {channels}, языки: {locales}).
Команды: {comma-separated /command list}.
Покрытие: {covered}/{P} процессов задействовано{, не вошли: {list}}.
Контракты: {rpc_count} вызываемых, {copy_count} без ответа, {state_count} состояний, {paused} на паузе.
Источники: {local_count} переиспользовано с диска (старейший {oldest_mtime}), {pulled_count} стянуто.
Оркестратор: будет создан в стейдже {stage_id} (~150 процессов, вызов необратим).

{One bullet per entry in "Known unknowns / risks", if any.}

Что-то поправить или /corezoid-gen-bot execute?
```

### Approval gate — Phase 4 always waits for the user

**There is no auto-proceed.** In every mode, print the summary and stop; enter
Phase 4 only after the user answers `execute` / `погнали` / `go`. Phase 4 has no
undo — it mints ~150 processes, and a second build on a channel token that
already serves a bot takes that bot's webhook over — so the one message it costs
to ask is not a trade worth making. `/corezoid-gen-bot <ids>` with no subcommand
authorises *planning* the bot, not building it: the user has not seen the command
map at the point they typed it.

The checklist below is not an auto-proceed condition. It is what has to hold
before the plan is fit to *offer* for execution at all — if any item fails, say
which one and what it means, and fix the plan first:

- `Known unknowns / risks` is empty.
- Every handed-in process appears in the coverage table.
- No process is `callable: api_rpc` with an unresolved array shape a command
  renders.
- No command reaches a `likely`/`unknown` side effect without a confirm step.
- `orchestrator.folder_id` is null.

## Phase 3a — Refining PLAN.md on user request

Targeted `Edit`s, then re-print the summary. One sentence of prose at most.

- **Never re-extract** for a PLAN.md edit unless the user asks for refresh mode.
  Use only what Phase 2 captured.
- Asked to add a command for a process that was skipped — check `Skipped
  processes` first. Skipped for shape (paused, reply-less, too many inputs) →
  explain the consequence and offer the honest variant (an acknowledgement-only
  command, a split). Not among the handed-in processes at all → refuse; do not
  pull a process the user did not give you without asking.
- Asked to remove a command — remove it from `Commands`, `Localization keys`,
  `Attachments`, `Menu wiring`, and move its processes to `Skipped processes`
  with reason "removed by request". **Update the coverage table** — it is the one
  section that must never silently disagree with reality.
- Asked to rename — cascade through command, alias, process title, `text_id`s,
  `attachment_id`s, button payloads, `Menu wiring`. Re-check
  `^/[a-z0-9][a-z0-9-]{2,}$`.
- Asked to stop asking for an input — check §7a of `contract_extraction.md` for
  a source the bot already has; if there is none and the input is required,
  say so rather than dropping a required argument.
- Do **not** proceed to Phase 4 until the user says `execute` / `погнали` / `go`.

### Refresh mode

1. Re-pull **every** process in `sources` — unconditionally, ignoring the
   §1.1 reuse path, because re-reading the server is the entire point of
   refresh — and re-run Phase 2 (including the callability gate: a process can
   be paused or have its reply nodes removed between runs). Ask first about any
   local file that is dirty in git; overwriting somebody's unpushed edit is not
   a refresh.
2. Diff against `bot-contract.json` and `Commands`.
3. **New processes in a re-pulled folder** → add to the contract summary and to
   `Skipped processes` with "no command planned yet". Never auto-add a command.
4. **Removed or newly paused processes** → mark the dependent command
   `⚠️ callee no longer callable` under `Known unknowns / risks`. Never silently
   delete a command.
5. **Changed contracts** → update inputs, outcomes and namespacing; a lost
   `api_rpc_reply` or a new alternate outcome is a `⚠️ contract drift` note.
6. Bump `extraction_timestamp`, re-emit the summary with a `Diff:` sub-block,
   stop.

If `orchestrator.folder_id` is set, refresh changes PLAN.md only — applying the
diff to the live bot is `edit-bot`.

## Phase 4 — Build the orchestrator from the template

Read `references/phase4_build_orchestrator.md` before running this phase.

Preflight reads PLAN.md and `bot-contract.json` — every input for Phases 4–8
comes from those files, not from a fresh conversation. Channel tokens are the
one exception: they are deliberately not in PLAN.md, so ask for them once,
immediately before §4.1. If `orchestrator.folder_id` is already set, skip to
Phase 5. Otherwise: call `create-communications-orchestrator` with
`apply: false` to preview, show the user the preview, then repeat the call with
`apply: true` and the printed `confirm:` token — this is the one irreversible
step in the whole skill (~150 processes, no undo, a second build steals the
webhook from the first). Then `pull-folder` with the **stage** id (never the
orchestrator's own folder id), locate the orchestrator by its `<folder_id>_`
prefix, sanity-check the template's contracts, and record every id into
PLAN.md's `template_ids`.

## Phase 5 — Generate one process per command

Read `references/phase5_generate_commands.md` before running this phase.

Per entry in PLAN.md `Commands`: create the process, pull it for a baseline,
build it from the right skeleton, wire the domain call with a ≥30 s `time`
semaphore, namespace when a command makes 2+ calls, forward every value the
outgoing message needs, substitute every `{{UPPER_CASE}}` placeholder
(two of them — `parent_id`, `user_id` — are numeric, not string), assign fresh
node ids, then `layout-process` → `lint-process` → `push-process` →
`create-alias`. End every path with `text_id: ""` plus a non-empty
`attachment_id` into the Router, or the reply is sent twice.

## Phase 6 — Seed Localization, Attachments and the menu

Read `references/phase6_seed_tasks.md` before running this phase.

The template's config processes are empty state diagrams — their content is
runtime task data, written with `run-task`/`modify-task`, never `push-process`.
Seed Localization (`deep_merge: true`, mandatory — a shallow write drops every
other language), seed Attachments per `attachment_id`/channel, and add every
command to `mainKeyboard` and `mainMenu`, or it ships invisible. Seed
`serviceError` explicitly — the wizard does not ship it, and both skeletons
reference it on every error path.

## Phase 7 — Verify, and repair

Read `references/phase7_verify_repair.md` before running this phase.

Four layers, cheapest first, stop at the first failure in a layer: **L1**
static checks (no leftover placeholders, no tokens in the tree, clean lint,
every referenced `text_id`/`attachment_id` exists, and the forwarding check —
the one thing L2/L3 cannot see); **L2** the command process on its own via
`run-task`, including rendering the actual message through Send Message, not
just reading the command's own task data; **L3** through the Router, which is
the only layer that proves alias dispatch; **L4** a real client where the user
can send one. The reference file's repair-loop table maps a symptom straight to
the layer at fault. Cap repair at about three passes per defect — report
precisely what fails rather than looping quietly.

## Phase 8 — APPROACH.md, then report

Read `references/phase8_report.md` before running this phase.

Write `APPROACH.md` next to PLAN.md (architecture, command table, alias and
namespacing rules, known limitations, full derived contracts as an appendix —
no tokens, no real chat ids). Then report in one compact block: orchestrator
folder, per-channel webhook URLs the user must register by hand, the command
table, coverage, one verification row per command with the observed L2/L3
result, and what the user must do next. Never report a command as working on
the strength of a clean lint alone.

## Rules

Full rule list, one per line with its reasoning: `references/rules.md`. Read it
before Phase 4 — it is a checklist to re-check against, not new material. The
handful that matter most, everywhere: `create-communications-orchestrator`
builds at most once per PLAN.md (no undo, a second build steals the webhook
from the first); never rename a pulled `<ID>_<Title>.conv.json`, hardcode a
template process id, or modify a handed-in domain process or the wizard's own
template processes; every `api_rpc` into a domain process needs a ≥30 s `time`
semaphore and `group: ""` with an explicit `extra`, never `group: "all"`;
tokens and credentials never touch a file; never `push-process --force` past a
structural lint finding or call `EnterPlanMode`; a clean lint is not a passing
test — state the evidence for any claim about the backend.
