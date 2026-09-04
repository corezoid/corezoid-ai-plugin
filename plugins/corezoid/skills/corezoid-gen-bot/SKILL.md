---
name: corezoid-gen-bot
description: Generates a multi-platform messenger bot (Telegram, Viber, Apple Messages for Business, Facebook Messenger) on Corezoid from a set of EXISTING Corezoid processes plus a description of the desired bot. Use when the user has a working Corezoid backend (a folder id, a list of process ids, or already-pulled .conv.json files) and wants it reachable from a chat. Runs in phases via subcommands — `plan` pulls every process, derives its real input/output contract from its `api_rpc_reply` nodes, classifies side effects, designs a command map that uses ALL of the processes, and writes `.corezoid-gen-bot/PLAN.md`; `execute` creates a Communications Orchestrator from the Corezoid template via `create-communications-orchestrator`, pulls the generated folder, generates one bot process per command wired to the domain processes with `api_rpc`, seeds Localization and Attachments, pushes, lints and smoke-tests every command; `refresh` re-derives the contracts of an existing plan. Trigger on phrases like "сделай бота из этих процессов", "бот на базе процессов корезоид", "сгенерируй бота", "telegram bot from corezoid processes", "bot from process ids", "corezoid gen bot", "viber bot", "apple messages for business bot", "facebook messenger bot", "чат-бот поверх корезоид", "оберни процессы в бота", "создай оркестратор коммуникаций", "communications orchestrator", "згенеруй бота з цих процесів". Also trigger on subcommand phrases "execute plan", "сгенерируй по плану", "погнали", "обнови план", "re-derive", "refresh plan" when a `.corezoid-gen-bot/` directory is present. For a Smart Form web app over the same processes use `simulator-app-generator`; to change a bot that already exists use `edit-bot`.
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

## 0. Preflight

- **Corezoid MCP tools present** — `pull-process`, `pull-folder`, `push-process`,
  `lint-process`, `layout-process`, `create-alias`, `run-task`, `show-task`,
  `modify-task`, `list-node-tasks`, `create-variable`, and
  `create-communications-orchestrator`. Missing → tell the user to install the
  Corezoid plugin and run `/corezoid-init`.
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

````markdown
---
bot_slug: {bot_slug}
description: {one paragraph — who talks to the bot and what for}
channels: [{telegram|viber|fbmessenger|abc}, …]
tokens_supplied: {true|false}          # never the tokens themselves
lang: {en|uk|ru}
locales: [{lang}, …]
corezoid:
  workspace_id: {id}
  project_id: {id|null}
  stage_id: {id|null}
sources:                                # the handed-in processes
  folder_id: {id|null}
  process_ids: [{id}, …]
manifest: .corezoid-gen-bot/bot-contract.json
orchestrator:                           # empty until Phase 4 has run
  folder_id: {id|null}
  folder_url: {url|null}
  obj_id: {wizard job id|null}
  webhooks_url: {[{channel,url}]|null}
  dashboard_url: {url|null}
  built_at: {ISO-8601|null}
template_ids:                           # read from the pulled mirror in Phase 4
  main: {id|null}
  router: {id|null}
  send_message: {id|null}
  system_diagram: {id|null}
  localization: {id|null}
  attachments: {id|null}
  user_profile: {id|null}
  bots_folder: {id|null}
  user_id: {id|null}                    # the user_id the wizard stamped on its own api_copy nodes
probed: [{process_id}, …]               # user-authorised live probes
extraction_timestamp: {ISO-8601}
---

## Contract summary
| Process | Callable | Inputs | success keys | alternate | Side effects | Nodes |
|---|---|---|---|---|---|---|
{one row per handed-in process}

## Coverage
| Process | Command | Called by | Wiring |
|---|---|---|---|
{one row per handed-in process — every one of them}

## Commands
One entry per bot process to generate:
- `/{command}` — alias `{command}`; skeleton: {bot_reply|bot_dialog}
  - calls: `{process_id} {title}` via {api_rpc|api_copy}{, then `{process_id}` …}
  - dialog steps: 1. {ask text_id} → input `{name}` (validate `{regex}`) …
  - argument sources: {name: ask|chat_id|channel|profile:<field>|const:<v>|chain:<pid>.<key>}
  - outcomes: success → `{text_id}` using {payload keys}; alternate {result/code} → `{text_id}`; error → `serviceError`
  - namespacing: {required (2+ calls) | not needed (single call)}
  - attachments: {attachment_id list}
  - confirm step: {yes — side effects {likely|unknown} | no}

## Localization keys
One line per `text_id` with the text per locale. Reuse the template's
`mainMenu`, `commandNotFound`, `timeout`, `serviceError`, `selectError`.

## Attachments
One line per `attachment_id`: the type per channel and the button payloads,
including the `mainKeyboard` additions — one button per command.

## Menu wiring
Which commands appear on `mainKeyboard` and in `mainMenu`, in order.

## Skipped processes
`{process_id} {title} — {reason}`.

## Reference materials consulted
`- {kind}: {short label} — {how it shaped the plan}`, or `- none supplied`.

## Known unknowns / risks
Unknown array shapes, `unknown` side effects wired behind a confirm, paused
processes, `params` disagreements, dead declared inputs, anything extraction
could not resolve. If none, write `- none`.
````

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

**Preflight.** Read PLAN.md and `bot-contract.json`. Every input for Phases 4–8
comes from those files, not from a fresh conversation. Missing PLAN.md → refuse
and point at `/corezoid-gen-bot plan <ids>`. Do not re-prompt for anything they
already hold; do not re-run Phase 2.

**If `orchestrator.folder_id` is set, skip to Phase 5.**

### 4.1 Call the wizard, exactly once

```
create-communications-orchestrator
  messengers: "[{\"channel\":\"telegram\",\"key\":\"…\"},{\"channel\":\"viber\",\"viber_token\":\"…\"}]"
  stage_id:   {corezoid.stage_id}     # omit to use the marker's stage
  project_id: {corezoid.project_id}   # omit to resolve from the stage
  lang:       {lang}
```

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

- Error result → the message carries the wizard's own diagnosis, usually naming
  the token it rejected. Report it verbatim, fix the token with the user, then
  call again. A failed build creates nothing, so retrying is safe.
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
that one; do not re-derive it with `list-stages` and risk a different stage.

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
stage_root="$(dirname "$(ls */*.stage.json *.stage.json 2>/dev/null | head -n1)")"
orch="{orchestrator.folder_id}_Communications_Orchestrator"
ls -d "$orch" || { echo "orchestrator folder not found — see the warning above"; exit 1; }
```

If that directory is not there, **stop**: either the pull was scoped wrongly
(the warning above) or the wizard built into a different stage than the marker
points at. Do not proceed to read ids out of whatever else the pull produced.

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

## Phase 5 — Generate one process per command

Per entry in PLAN.md `Commands`:

1. **Create the process on the server first, then pull it.** `push-process`
   **cannot create** a process: a local file with `obj_id: 0` is rejected with
   `Project or stage mismatch`, and once the process exists a push without a
   recorded baseline is refused too ("no pull baseline"). The working order is:
   ```
   create-process  process_name: /{command}  folder_id: {template_ids.bots_folder}
   pull-process    process_id: <the id it returned>      # records the baseline
   <write the generated scheme into the pulled file, keeping its obj_id>
   ```
   `adopt_existing` is **not** the flag for this — it declares you do not know
   what is on the server, whereas here you created the empty process seconds ago.
   Keep the filename `create-process`/`pull-process` produced; never rename it.

2. **Start from the right skeleton** — `bot_reply.skeleton.json` for a
   single reply, `bot_dialog.skeleton.json` for anything with a question. For a
   multi-step dialog repeat the ask → wait → keep → validate block per input;
   for a chained command splice in `rpc_call.nodes.json`; for a reply-less
   callee use its `_fire_and_forget` node; for a state read use `_state_read`.

3. **Wire the domain call** — `api_rpc` with `group: ""` and an explicit `extra`
   holding only the declared inputs, `extra_type` from the majority-voted types,
   a **`time` semaphore of at least 30 s** routed to the timeout node, and
   `@alias` in preference to a numeric `conv_id` (an alias survives a
   `deploy-stage` promotion; a numeric id does not).

4. **Namespace when the command makes 2+ calls.** An `api_rpc` callee's
   `res_data` merges into the caller's task at top level, and every process in
   this family replies with the same `{result, code}` envelope — so the second
   callee overwrites the first's verdict and the command branches on the wrong
   outcome. Follow every call with an **`api_code`** (not a `set_param` — lint
   cannot see a `data.result` read inside JavaScript and fails the file with
   `UNUSED SET_PARAM`) that copies the reply into namespaced keys and clears
   `result`/`code`. With exactly one call there is nothing to overwrite; read
   `{{result}}`/`{{code}}` directly and skip it.

5. **Forward every value the message needs — `group:""` sends nothing else.**
   The `api_copy` into Send Message carries **only the fields named in its
   `data`**, and Send Message resolves `{{someVar}}` in the localized text against
   **its own** task data. So a value the command computed but did not forward
   renders as the literal `{{someVar}}` in the chat. Two families of this:

   - **Values a text interpolates.** `balanceDone: "Ваш баланс: {{bonusAmount}}"`
     requires `"bonusAmount": "{{bonusAmount}}"` in the send node's `data`.
     Cross-check every `text_id` the command uses against the `{{...}}` in its
     Localization string and forward each one.
   - **`items` + `currentPage` for any dynamic attachment.** Without them
     `createDynamicAttachment` has nothing to render and the carousel arrives
     empty. Its gate is `items == "" && buttons == ""` → static path, so a command
     with **no rows must send `items` as the empty STRING, not `[]`**: an empty
     array takes the dynamic path and re-renders a static keyboard's `buttons` as a
     per-item pattern zero times, delivering a keyboard with no buttons at all.

   Neither failure is visible to `lint-process`, and neither is visible to a test
   that inspects the command's own task data — the value is present there and
   missing only on the far side of the `api_copy`. See Phase 7 L1.6.

6. **Substitute every `{{UPPER_CASE}}` placeholder** from PLAN.md and
   `template_ids`: `{{COMMAND}}`, `{{DOMAIN_CONV_ID}}`, `{{DOMAIN_TITLE}}`,
   `{{ARG_NAME}}`, `{{ARG_SOURCE}}`, `{{SUCCESS_TEXT_ID}}`,
   `{{ALTERNATE_TEXT_ID}}`, `{{ASK_TEXT_ID}}`, `{{ASK_ATTACHMENT_ID}}`,
   `{{ANSWER_REGEX}}`, `{{PROFILE_FIELD}}`, `{{SEND_MESSAGE_CONV_ID}}`,
   `{{ROUTER_CONV_ID}}`, `{{USER_PROFILE_CONV_ID}}`, `{{BOTS_FOLDER_ID}}`,
   `{{TEMPLATE_USER_ID}}`. Note that a `{{UPPER_CASE}}` inside an `api_code`
   `src` is also a generator-time substitution — Corezoid does **not**
   interpolate `{{...}}` in Code nodes. Grep for `{{[A-Z]` before pushing: a
   leftover placeholder deploys happily and fails at runtime.

   **Two of them are numeric — drop the surrounding quotes.** The skeletons
   ship `"parent_id": "{{BOTS_FOLDER_ID}}"` and
   `"user_id": "{{TEMPLATE_USER_ID}}"` quoted only so the template files stay
   parseable JSON. The schema declares the process's `parent_id` as
   `null|integer` and `api_copy`'s `user_id` as `integer`, so a plain textual
   substitution leaves a string and `push-process` fails schema validation on
   `parent_id` and on **every** `api_copy` node:

   ```
   - at '/parent_id': got string, want null or integer
   - at '/scheme/nodes/3/condition/logics/0/user_id': got string, want integer
   ```

   `force` does not bypass schema validation, so this has to be right before
   the first push. Every other placeholder above substitutes as a string:
   `conv_id` accepts both forms, and `text_id`/`attachment_id` are strings.

7. **Assign unique node ids** — 24 hex characters, unique within the process.
   The skeletons ship readable placeholders (`aa0000…01`) which are valid but
   collide across processes; regenerate per process. `push-process` rewrites
   them canonically anyway.

8. **Push:**
   ```
   layout-process  process_path: <path>     # tidy coordinates; rewrites only x/y
   lint-process    process_path: <path>     # must be clean of deploy-blocking findings
   push-process    process_path: <path>
   ```
   Fix every deploy-blocking finding in the design. Do **not** pass
   `force=true`: the structural findings this generator can plausibly trip
   (missing default `go`, a shared error cluster, a sub-30 s semaphore, an
   `err_node_id` pointing at an `obj_type:0` node, a self-referencing
   `api_copy`) describe a graph the server rejects, and `force` does not bypass
   them.

   **Expect the first push of each new command to be blocked on the snapshot,
   and expect that to be normal.** `push-process` takes a pre-push snapshot, and
   `CreateSnapshot` fails **deterministically for a process that has never been
   deployed** — there is no version to snapshot. The message reads like a
   transient platform fault ("please contact support"), so it invites a retry
   that cannot succeed. Retry once to rule out a real outage, then pass
   `allow_no_snapshot=true` for that push only, having first confirmed from the
   recorded baseline that the version being overwritten has **zero nodes** (it
   does: `create-process` made it seconds earlier). Say so in the report. Every
   *subsequent* push of the same process snapshots normally — if one of those is
   blocked, that is a genuine outage and the answer is to wait, not to waive.

9. **Create the alias — this is the wiring, not a nicety.**
   ```
   create-alias  process_path: <path>  short_name: {command without the slash}
   ```
   Without it the Router's `@{{commandAlias}}` resolves to nothing and the user
   gets `commandNotFound`. Verify the `short_name` is exactly the command minus
   its slash, matches `^[a-z0-9][a-z0-9-]{2,}$`, and collides with nothing already
   in the stage. **`_ALIASES_.json` at the stage root is the full list** — read it
   rather than guessing; a fresh orchestrator ships ~60 aliases.

   While you are in that file, **check the aliases you plan to *call* too.** An
   alias whose name matches a handed-in process may point somewhere else entirely:
   in the reference workspace `promolist`, `recovery-password` and
   `registration-verify-success` all resolved to deprecated `..._old/API:` copies,
   not to the processes the user handed in. Match on `obj_to_id`, never on the
   name, before using `@alias` for a domain call.

10. **End with `text_id: ""` and a non-empty `attachment_id`, or the reply is
   sent twice.** The Router's `/end` handler decides whether *it* also sends:

   | `text_id` | `attachment_id` | Router does |
   |---|---|---|
   | `""` | `""` | sends `mainMenu` |
   | set | `""` | sends `text_id`, adds `mainKeyboard` |
   | set | set | sends `text_id` |
   | `""` | set | **closes the state and sends nothing** |

   `group:"all"` forwards the whole task, so a command that set its own
   `data.text_id` leaks it into `/end` and the Router sends that text a second
   time — and the Router's send passes only
   `channel`/`chat_id`/`text_id`/`attachment_id`, so the duplicate arrives with no
   interpolated value and no `items`. The generated node must therefore be:
   ```jsonc
   {"type":"api_copy","conv_id":<router_id>,"mode":"create","group":"all",
    "data":{"command":"/end","text_id":"","attachment_id":"mainKeyboard"}}
   ```
   The wizard's sample bots take the **other** half of this contract: `/NPS`,
   `/sampleSurvey`, `/changeLanguage` and friends send no message of their own and
   hand `text_id` to the Router. That path is unavailable to any command that needs
   `items`/`currentPage` or an interpolated value, because the Router's send cannot
   carry them — which is why generated commands send their own message and silence
   the Router. (`/exchangeRates`, the one dynamic-attachment sample, dodges the
   issue by never copying `/end` at all and staying `active` forever. Do not copy
   that: it leaves the chat wedged.)

11. **Re-read the file after pushing.** The server regenerates node ids and
   rewrites the local file. Reference nodes by title, never by a remembered id.

## Phase 6 — Seed Localization, Attachments and the menu

The template's config processes are empty state diagrams: their content is
**runtime task data**, written with task tools, not `push-process`. Getting this
wrong produces a bot that deploys clean and answers every message with an empty
string.

### 6.1 Localization — one task, ref `localization`

```
show-task    process_id: {template_ids.localization}  ref: localization       # exists?
run-task     process_path: <Localization path>  ref: localization  data: {…}  # only if absent
modify-task  process_id: {template_ids.localization}  ref: localization \
             deep_merge: true  data: {"balanceDone": {"en":"…","uk":"…"}, …}
```

`deep_merge: true` is **mandatory**. The task API merges only top-level keys, so
a shallow write of `{"balanceDone": {"en": "…"}}` replaces the whole language map
and drops every other language for that key. Shape and interpolation rules
(`{{taskField}}`, `{{t'otherKey}}`) in `references/localization.seed.json`.

**Seed `serviceError` explicitly — the wizard does not ship it.** A fresh
Localization document holds `404`, `inputError`, `selectError`, `timeout`,
`commandNotFound`, `mainMenu`, `yes`/`no` and the sample bots' own copy, but
**not** `serviceError`, which both skeletons hardcode on every error and timeout
path. Miss it and every failure delivers an empty message — the exact
"deploys clean, broken in the chat" outcome this phase exists to prevent. Read the
document with `show-task` first and seed whatever your commands reference and it
lacks; do not trust this list, check the build in front of you.

**Seed every language the wizard created, not just `lang`.** It seeds `en`, `ru`
and `uk` regardless of the `lang` passed to the wizard, and `Send Message` picks
the language from `User Profile.language`, which the Telegram receiver derives from
`message.from.language_code`. A user whose client is English gets `en`; if that
key is missing for your `text_id`, they get nothing.

⚠️ **The interpolation form is `{{t'someKey}}` — no dot.** `Send Message` extracts
the key with `d[i].replace(/{{t'/, "").replace(/}}/, "")`, so `{{t'.someKey}}`
looks up `".someKey"` and silently leaves the placeholder in the text. The regex
that matches it (`/{{t'.\w+}}/ig`) has an *unescaped* dot, which is why the wrong
form looks like it works. The wizard's own documents use `{{t'/exchangeRates}}`
and `{{t'formsQuestionnaire}}`.

Success texts may use the callee's payload keys directly — a process replying
`{bonusAmount}` supports `"Ваш баланс: {{bonusAmount}}"`. Only keys in the
manifest's `success.keys` are safe: a placeholder for a key the process never
sends renders as literal `{{key}}` in the chat.

### 6.2 Attachments — one task per `attachment_id`

The task `ref` **is** the `attachment_id`; its data is keyed by channel.

```
run-task     process_path: <Attachments path>  ref: balanceKeyboard \
             data: {"telegram": {…}, "viber": {…}, "facebook": {…}, "abc": {…}}
modify-task  process_id: {template_ids.attachments}  ref: mainKeyboard  deep_merge: true  data: {…}
```

**`run-task` on a state diagram always reports the task as "still in progress /
parked at a non-final node". That is success, not an error** — on a state diagram
the task *is* the stored document, and it sits in the state node by design. The
tool echoes the data it stored; check that, and move on. Do not retry with a larger
`wait_sec` and do not treat it as a failure.

Generate only the channels in `channels` — an absent channel key means "no
attachment there", which is legal. Per-channel shapes and the per-page item caps
are in `references/attachments.seed.json`.

**A dynamic attachment is a *pattern*, and the rows come from the calling task.**
The attachment holds one template element (telegram: `buttons: [[{…}]]`); the
command must forward `items` and `currentPage` on its Send Message call
(Phase 5 §5). Forgetting either renders an empty carousel; forwarding `items: []`
instead of `items: ""` on the empty path blanks a *static* keyboard.

**Reply keyboard vs inline keyboard decides whether a dialog can read the answer.**
A dialog's wait node reads `data.message.text`. A telegram `type: "keyboard"`
(reply keyboard) sends the button's **label** as a text message, so the label
arrives as the answer — this is what an ask step needs. A `type:
"inline_keyboard"` sends `callback_data`, which `Main → PARSE command` interprets
as a **command**, not as the answer to the pending question. Use `keyboard` for
answer/confirm steps and `inline_keyboard` for display-only rows and menus.

Note the channel-name asymmetry: the wizard's messenger key is `fbmessenger`,
but `Send Message` dispatches on `channel == "facebook"`. Attachment keys and
`go_if_const` conditions use `facebook`.

### 6.3 The menu — otherwise the bot ships invisible

One button per command on `mainKeyboard` (one shape per channel), and mention
them in `mainMenu`. Payloads follow the template's grammar, parsed by
`Main → PARSE command`:

```
"/order-status__id-42_page-2"  ⇒  command /order-status, params {id:"42", page:"2"}
separator "__"   pair union "_"   key/value "-"
```

A literal `-` or `_` inside a value splits it — never put free text in a payload.

## Phase 7 — Verify, and repair

Four layers, cheapest first. Stop at the first failure in a layer before moving
on.

### L1 — Static

1. `grep -rnE '\{\{[A-Z_]+\}\}'` over generated processes — empty.
2. `grep -rniE 'bot[0-9]{6,}:|page_access_token|viber_token|abc_token'` over the
   whole tree including PLAN.md and APPROACH.md — empty.
3. `lint-process` clean on every generated process.
4. `show-task ref: localization` — every `text_id` a generated process
   references exists in every locale in `locales`, and every `{{placeholder}}`
   in a success text is a key in that process's `success.keys`. A missing key
   renders as an empty message or literal `{{key}}`, not an error.
5. `show-task` per `attachment_id` — one key per channel in `channels`.
6. **Forwarding check — the one L2/L3 cannot see.** For every Send Message
   `api_copy` in every generated process, take the `text_id` it sends, look up
   that string in Localization, and confirm **each `{{var}}` in it is a key of
   that node's `data`**. Keep the payload you seeded in Phase 6 in a local file
   and run this as a script over the generated schemes: `show-task` returns the
   whole Localization document (hundreds of lines of template copy) with no way
   to diff it against the schemes, so doing this by eye is
   how a missing forward survives once a bot has more than a couple of
   commands. Two shapes need care in such a script: a node
   that sends the literal `{{text_id}}` can carry **any** `text_id` a Code node
   assigns, so union them (and take a ternary's strings from after the `?`, or
   you will read a condition's string as a text_id); and where the union is wider
   than a branch can actually reach, forward the extra values anyway rather than
   arguing the branch is safe — it costs a few fields and keeps the invariant
   true if a branch is ever repointed. Then confirm every node whose `attachment_id` is a
   dynamic pattern also forwards `items` and `currentPage`. A value that is
   present on the command's task but absent from the `data` block renders as a
   literal `{{var}}`, and L2/L3 will still pass because they read the command's
   task, not the message.
7. **`/end` check.** Every `END -> Router` `api_copy` carries
   `text_id: ""` **and** a non-empty `attachment_id`, or the reply is delivered
   twice (Phase 5 §10).

### L2 — The command process on its own

`run-task` straight at the generated command, with the data the Router would
supply:

```jsonc
{"channel":"telegram","chat_id":"<test chat id>","message":{"type":"text","text":"/balance"}}
```

Assert: the domain `api_rpc` was reached and returned; the namespaced keys are
populated where PLAN.md says namespacing is required; `text_id` was set to the
mapped value; the task reached `END -> Router`. Inspect with
`list-task-history` when it did not.

> **A dialog command will park on its wait node here, by design.** That is the
> `api_callback` doing its job, not a failure. Assert it *reached* the wait
> node, then deliver the answer the way the Router does — an `api_copy`
> `mode:"modify"` into the command process on ref `<channel>_<chat_id>` — and
> confirm it advances. `modify-task` on that ref does the same job.

> **L2 proves the command computed the right things; it does NOT prove the user
> sees them.** The task data at `Done` will happily show
> `bonusAmount: "0.00", text_id: "balanceDone"` while the delivered message reads
> `Your card has {{bonusAmount}} bonus points.`, because the defect lives in the
> `api_copy`'s `data` block. To close that gap, **render one message per distinct
> `text_id` the way Send Message does**: `run-task` on Send Message itself with
> `{channel, chat_id, text_id, attachment_id}` plus the values the text
> interpolates, then read `data.text` off the resulting task. With a synthetic
> `chat_id` the Telegram call fails with `Bad Request: chat not found` — that is
> fine and expected, because the text and attachment are resolved *before* the
> send. Assert on `data.text` and on `reply_markup`.

### L3 — Through the Router

This is the layer that proves the alias dispatch, and nothing else does:

```jsonc
{"channel":"telegram","chat_id":"<test chat id>","command":"/balance",
 "message":{"type":"text","text":"/balance"}}
```

`run-task` on the Router with `wait_sec: 60`. Reaching `Command not found`
means the alias is wrong or missing. Then `show-task` on the command process
(ref `telegram_<chat_id>`) to confirm it started.

Walk **every** command, and every alternate outcome you can trigger — those are
the ones a happy-path-only test misses.

### L4 — A real client

Send the command from a real Telegram/Viber client where the user can.
`run-task` proves the graph; only a real client proves the webhook, the token
and the rendered keyboard.

### The repair loop

| Symptom | Layer at fault |
|---|---|
| `Command not found` | missing or misnamed alias (Phase 5 §9) |
| Task parked forever on the domain call | callee is paused or reply-less — contract misread, go back to Phase 2 §1 |
| Empty message delivered | `text_id` missing from Localization, or missing for the active locale |
| Literal `{{key}}` in the chat | success text uses a key the process never sends |
| Wrong branch after a domain call | shared `result`/`code` not namespaced (Phase 5 §4) |
| Bot answers "something went wrong" to a normal user | alternate outcome mapped as error (Phase 2 §3) |
| User's next message goes nowhere | a path exits without copying `/end` into the Router |
| Keyboard renders on one channel only | Attachments task missing that channel's key |
| **Every reply arrives twice**, the first copy showing literal `{{var}}` | `END -> Router` leaked `text_id`; the Router re-sent it (Phase 5 §10) |
| Literal `{{var}}` in an otherwise correct message | the value was not forwarded in the send node's `data` (Phase 5 §5) |
| Carousel arrives empty | `items`/`currentPage` not forwarded to Send Message |
| A *static* keyboard arrives with no buttons | empty path sent `items: []` instead of `items: ""` |
| Button label appears as an unknown command | an ask step used `inline_keyboard`; use a reply `keyboard` so the label arrives as `message.text` |
| Reply is in the wrong language | `User Profile.language` comes from the client locale; that language is missing for this `text_id` |

Re-run from L1 after every fix. **Cap the loop at about three passes per
defect.** If it is not converging, stop and report precisely what fails, what
you tried, and what you think the cause is. A truthful "7 of 8 commands pass,
this one doesn't and here's why" is worth far more than a loop that quietly
gives up.

## Phase 8 — APPROACH.md, then report

`APPROACH.md` next to PLAN.md, covering: the contract table; the architecture
(messenger → channel receiver → Main → Router → command process → `api_rpc` →
domain process, and back through `Send Message`); the command table
(`/command` → alias → processes → dialog steps → outcomes); where copy and
keyboards live and how to change them; the alias rule and what breaks when it is
violated; the namespacing rule; known limitations. Full derived contracts as an
appendix. No tokens, no chat ids of real people, no session-specific paths.

Then report, in one compact block:

- **Orchestrator** — `folder_url` and folder id.
- **Channels** — one line each. For every entry in `webhooks_url`, the URL the
  user must register with that platform by hand. Report exactly the channels the
  wizard listed — it says nothing about the ones it omitted, so do not claim
  those are already wired.
- **Commands** — table `/command | alias | processes | wiring | shape`.
- **Coverage** — handed-in processes used / total, and any left out with why.
- **Verification** — one row per command with the observed L2/L3 result, and the
  node any failing task parked at. Never report a command as working on the
  strength of a clean lint.
- **Dashboard** — `dashboard_url` if the wizard returned one.
- **What the user must do by hand.**
- **Next step** — `/edit-bot` for changes; a second `corezoid-gen-bot execute`
  would build a whole second orchestrator.

## Rules

- **`create-communications-orchestrator` runs at most once per PLAN.md.** ~150
  processes, no undo, and a second build steals the webhook from the first.
  Check `orchestrator.folder_id`; write the response into PLAN.md before
  anything else; never retry a timeout.
- **Take inventory before pulling.** The workspace is usually an already-pulled
  stage; pull only the ids with no `<id>_*.conv.json` under the stage root.
  `pull-process` overwrites the local file, so a blind re-pull destroys unpushed
  edits — and a dirty local file is the one case that needs asking first.
- **Record where every file came from** (`local` + mtime, or `pulled`). A reused
  file is a snapshot of unknown age and Phase 2 derives the entire contract from
  it. There is no cheap staleness check — verifying is pulling — so state the
  age instead of implying freshness.
- **Never rename a pulled `<ID>_<Title>.conv.json`.** `push-process` and
  `lint-process` recover the process id from the filename.
- **`pull-folder` takes the STAGE id, writes to the stage root, and pulls the
  whole stage.** `folder_id` is required over MCP — the zero-argument form works
  only in the server's CLI mode. Never pass the orchestrator's `folder_id` to
  it: a subfolder id
  fetches only that folder and still unzips it at the stage root, so the
  `<folder_id>_Communications_Orchestrator/` directory never appears and every
  id read afterwards is read out of the wrong tree. After the pull, find the
  orchestrator by its `<folder_id>_` name prefix — the trailing number of the
  wizard's `folder_url`. If the directory is absent, stop.
- **Never hardcode a template process id.** Read every id from the pulled mirror
  into PLAN.md `template_ids`. Ids in `references/template_map.md` are examples
  from one build.
- **Answer the callability question before designing.** An `api_rpc` into a
  paused or reply-less process parks the task until the semaphore fires — a user
  waiting the full 30 s for an error. `params` does not reveal this; only the
  reachable `api_rpc_reply` nodes do.
- **Always put a `time` semaphore of ≥30 s on an `api_rpc`** into a domain
  process, routed to a node that tells the user and then copies `/end` into the
  Router. The template's own `api_rpc` nodes have none because their callees are
  in the same folder; a third-party process is not that. Lint rejects anything
  below 30 s, so 30 s is also the floor on how fast a command can fail — write
  the timeout copy to read sensibly after a half-minute.
- **Call a domain process with `group: ""` and an explicit `extra`.**
  `group: "all"` forwards the whole task — `channel`, `chat_id`, `message`, the
  Router's bookkeeping — into somebody else's process.
- **Namespace after every `api_rpc` when a command makes two or more calls**,
  with an `api_code`, not a `set_param`. Skip it for a single call; applying it
  unconditionally costs a node plus its own error cluster per branch.
- **Alternate outcomes are outcomes, not errors.** Map `recovery`,
  `registration` and friends to their own `text_id`, and to their own follow-up
  question where the flow continues.
- **Never generate a question for a token, password or API key,** and never for
  a value the bot can already read (`chat_id`, `channel`, User Profile).
- **A command that reaches a `likely`/`unknown` side effect needs a confirm
  step.** Never fire a side effect on the first message.
- **Never probe a process automatically.** Present the side-effect
  classification and let the user authorise each probe.
- **Every handed-in process appears in the coverage table.** If one does not
  fit, say so and ask. Never silently omit.
- **A command must be a legal alias once the `/` is stripped**
  (`^/[a-z0-9][a-z0-9-]{2,}$`) and must have an alias created. That pair is the
  entire dispatch mechanism: the Router computes
  `commandAlias = command.replace("/","")` and calls `@{{commandAlias}}`. A
  camelCase command deploys fine and is unreachable forever.
- **Never generate `/start`, `/end` or `/exit`,** and never collide with an
  alias already in the stage.
- **Every command ends by copying into the Router with `{"command":"/end"}` and
  `group:"all"` — including its error and timeout paths.** Otherwise the chat's
  System Diagram state stays `active` and the user's next message is delivered
  to a finished bot.
- **`group:"all"` carries the whole task; `group:""` sends only `data`.** Send
  Message takes `group:""` with explicit `channel`/`chat_id`; the Router takes
  `group:"all"`.
- **Copy each dialog answer into its own key** in a Code node right after the
  wait. Reading `{{message.text}}` later in a multi-step dialog reads the latest
  message, not the answer that step asked for.
- **Corezoid Code nodes are ES5, and `{{...}}` is not interpolated in `src`.**
  No `let`/`const`, arrow functions or template literals; read task data as
  `data.x`. Any `{{UPPER_CASE}}` in a skeleton's `src` is a generator-time
  substitution.
- **Localization and Attachments are runtime task data.** Seed with `run-task`,
  extend with `modify-task` **and `deep_merge: true`** — the API merges only
  top-level keys, so a shallow write to a nested language or channel map
  silently drops what you did not send.
- **A success text may only use keys in that process's `success.keys`.** A
  placeholder for a key the process never sends renders as literal `{{key}}` in
  the chat.
- **Add every command to `mainKeyboard` and `mainMenu`.** A command nobody can
  discover is not shipped.
- **A generated command must forward everything its message needs.** The
  `api_copy` into Send Message sends only the fields in its `data`, and
  `{{var}}` resolves against Send Message's own task — so forward each value the
  text interpolates, plus `items`/`currentPage` for a dynamic attachment, and use
  `items: ""` (not `[]`) when there are no rows.
- **End every path with `text_id: ""` and a non-empty `attachment_id`.**
  `group:"all"` leaks the command's own `text_id` into `/end`, and the Router then
  sends the same message a second time without any interpolated value.
- **`push-process` cannot create a process.** `create-process` then
  `pull-process` (for the baseline), then write the scheme, then push.
- **Read `_ALIASES_.json` before creating or calling an alias.** It is the
  authoritative list; and an alias whose name matches a handed-in process may
  point at a deprecated copy — match on `obj_to_id`.
- **A clean `run-task` at `Done` is not proof the user saw the right message.**
  The send-side defects are invisible there; render the text through Send Message
  (Phase 7 L2) for each distinct `text_id`.
- **`serviceError` is not shipped by the wizard** — seed it, or every error path
  delivers an empty message.
- **`{{t'key}}` has no dot.** `{{t'.key}}` matches the replacer's regex and then
  fails the lookup, leaving the placeholder visible.
- **A state-diagram `run-task` reporting "parked at a non-final node" succeeded.**
  Do not retry it or treat it as an error.
- **Tokens and credentials never touch a file.** Channel tokens are wizard
  arguments only.
- **A blocked snapshot on the *first* push of a generated command is expected,
  not an outage.** `CreateSnapshot` fails deterministically for a never-deployed
  process — nothing exists to snapshot — so `allow_no_snapshot=true` is the
  normal path there, once you have confirmed the baseline has zero nodes. On any
  later push of the same process it snapshots fine, and a block means wait.
- **Never `push-process --force`** past a structural lint finding, and never
  `overwrite_server_change` / `allow_no_snapshot` on a template process. If the
  platform's snapshot API is failing while you push a process **you created empty
  moments ago**, `allow_no_snapshot=true` is defensible on a mutable non-prod
  stage — confirm from the recorded baseline that the version being overwritten
  has zero nodes, and say so in the report.
- **Never modify the template's own processes in this skill.** Main, Router,
  Send Message, System Diagram and the Messengers folder are the wizard's
  output, and alias dispatch means a new command needs none of them touched.
  That is `edit-bot`'s job, under its own confirmation.
- **Never modify a handed-in domain process.** They are someone else's system
  and the bot is a caller, not an owner. If one genuinely has to change, say so
  and hand it to `corezoid:corezoid-edit` with the user's agreement.
- **A clean lint is not a passing test.** Every command is reported only with an
  observed L2/L3 result behind it, and an L4 check where the user can do one.
- **State the evidence for any claim about the backend.** Name the field you
  read before calling a handed-in process broken.
- **Never call `EnterPlanMode`.** The plan phase needs Bash, Write and MCP tool
  calls.
- **PLAN.md and `bot-contract.json` are written by the AI only,** and they are
  the sole source of truth for Phases 4–8. If a fact needed there is missing,
  that is a bug in Phase 3 — fix the plan first.
