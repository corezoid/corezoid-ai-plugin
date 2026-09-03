# Anatomy of a Communications Orchestrator

Everything below was read off a real orchestrator produced by
`create-communications-orchestrator`. **Every numeric id in this document is an
example from one build — never copy one into a generated process.** The wizard
mints fresh ids per workspace; the skill reads the real ones out of the pulled
mirror (Step 4) and substitutes them.

## Folder layout

`pull-folder` (no arguments) mirrors the whole stage into the **stage root** —
the `RootPath` registered for this workspace, not the caller's cwd. The
orchestrator lands as one directory whose name prefix is the `folder_id` the
wizard returned (the trailing number of `folder_url`). Passing that folder's id
to `pull-folder` instead fetches only the folder and unzips it *at the stage
root*, which flattens the orchestrator over the stage and loses the directory
below.

```
<stage root>/
├── <stage_id>_<name>.stage.json     the marker every tool resolves stage from
├── CLAUDE.md                        process index (convenience; may be absent)
└── <folder_id>_Communications_Orchestrator/
    ├── Main.conv.json                  entry point for every inbound event
    ├── Router.conv.json                command dispatch + chat state machine
    ├── Send Message.conv.json          the only outbound message API
    ├── System Diagram.conv.json        state: active / inactive per chat
    ├── Subscription Management.conv.json
    ├── Sample_Bots/                    ← generated commands go here
    │   ├── /startChat, /closeChat, /changeLanguage, /NPS, …
    │   ├── ABC_Debug/                  one sample per Apple rich attachment
    │   ├── Sample_Survey/              survey + Results dashboard
    │   └── chatGTP/
    ├── Configs/
    │   ├── Localization.conv.json      state, one task, ref = "localization"
    │   ├── Attachments.conv.json       state, one task per attachment_id
    │   ├── User_Profile.conv.json      state, ref = "<channel>_<chat_id>"
    │   ├── Tokens.conv.json            state, channel credentials
    │   ├── Chats - by userId / by eventId, Simulator Cache
    └── Messengers/
        ├── Telegram/  Viber/  Facebook/  Apple_Business_Chat/  Simulator/
        │   └── <Channel> Receiver + API_Methods + Utils
```

## The one dispatch rule that matters

`Router` never contains a per-command branch. It derives the target process
from the command text and calls it **through an alias**:

```
Set commandAlias   api_code   data.commandAlias = data.command.replace("/","");
Init bot           api_copy   conv_id: "@{{commandAlias}}"
                              mode: "create", group: "all",
                              ref: "{{channel}}_{{chat_id}}"
```

So a command is wired up by **existing under the right alias** — there is no
registration table to edit. Two consequences the generator must respect:

1. **The command must be a legal Corezoid alias once the leading `/` is
   stripped**: `^[a-z0-9][a-z0-9-]{2,}$` — lowercase letters, digits, hyphens,
   at least 3 characters. `/orderStatus` produces `orderStatus`, which is not a
   legal `short_name`, so `@orderStatus` resolves to nothing and the user gets
   `commandNotFound`. Use `/order-status`.
2. **Reserved commands**: `/start`, `/end`, `/exit` are consumed by Main,
   Router and System Diagram. `/end` is how a bot hands control back; `/exit`
   is how the Router tells a running bot to abandon its dialog. Never generate
   a command with one of those names, and never generate one that collides with
   a sample bot's alias already in the stage.
3. **`_ALIASES_.json` at the stage root is the authoritative list** — read it
   instead of guessing, both to avoid a collision and before *calling* anything
   by `@alias`. An alias whose name matches a handed-in process may resolve
   somewhere else entirely: in the reference workspace `promolist`,
   `recovery-password` and `registration-verify-success` all pointed at
   deprecated `..._old/API:` copies. Match on `obj_to_id`, never on the name.

Follow-up messages reach a running bot the same way:
`api_copy conv_id:"@{{commandAlias}}" mode:"modify" is_sync:true ref:"{{channel}}_{{chat_id}}"`.
That is why a dialog bot must be created with `ref = {{channel}}_{{chat_id}}`
(the Router already does this) and must park on an `api_callback` node.

## Contracts a generated bot depends on

### Send Message  (`conv_type: process`)

The only way to talk to a user. Call it with `api_copy`, `mode: "create"`,
`group: ""` (send exactly these fields):

| Field | Required | Meaning |
|---|---|---|
| `channel` | yes | `telegram` / `viber` / `facebook` / `abc` |
| `chat_id` | yes | per-channel chat identifier |
| `text_id` | no | key into Localization; empty ⇒ no text |
| `attachment_id` | no | key into Attachments; empty ⇒ no attachment |
| `settings` | no | extra per-send options |

Anything else you put on the task is available to `{{var}}` interpolation
inside the resolved text and attachment (see "Text interpolation" below), which
is how a bot injects live values into a canned string.

⚠️ **"On the task" means on *Send Message's* task.** The call is `group: ""`, so
only the fields listed in `data` cross over. A value the calling bot holds but
does not name there is simply absent when the text is resolved, and the user sees
a literal `{{var}}`. The same applies to `items`/`currentPage` for a dynamic
attachment. This is the single most common way a generated bot looks correct in
`run-task` output and wrong in the chat.

Internally it resolves, in order:

```
language     {{conv[<user_profile_id>].ref[{{channel}}_{{chat_id}}].language}}   (default "en")
localization {{conv[<localization_id>].ref[localization]}}
text         {{localization.{{text_id}}.{{language}}}}
attachment   {{conv[<attachments_id>].ref[{{attachment_id}}].{{channel}}}}
```

Then `Replace variables in text/attachment`, then the per-channel native-syntax
Code node, then the channel's own send process.

### Router  (`conv_type: process`)

A bot ends its turn by copying a task **into the Router** with
`group: "all"` (carry the whole task so `channel`/`chat_id` travel with it).

Skipping this leaves `System Diagram` in `active` for that chat, so the next
thing the user types is delivered to the finished bot instead of the Router.
The state does eventually self-heal via the System Diagram timeout, but the
user sees a dead chat until it does — always route error paths to the `END`
node too, not straight to an error final.

**The `/end` handler decides whether the *Router* also sends a message**, by
branching on `text_id` and `attachment_id`:

| `text_id` | `attachment_id` | Router does |
|---|---|---|
| `""` | `""` | sends `mainMenu` (+ `mainKeyboard`) |
| set | `""` | sends `text_id`, adds `mainKeyboard` |
| set | set | sends `text_id` |
| `""` | set | **closes the state, sends nothing** |

So there are two valid conventions, and mixing them sends the reply twice:

```jsonc
// A. The bot sends nothing; the Router renders the closing text.
//    This is what the wizard's own sample bots do (/NPS, /sampleSurvey,
//    /changeLanguage, ...). Simple, but the Router's send passes only
//    channel/chat_id/text_id/attachment_id, so it CANNOT carry items,
//    currentPage or any value the text interpolates.
{ "group": "all", "data": { "command": "/end", "text_id": "surveyComplete" } }

// B. The bot sent its own message and the Router must stay silent.
//    Required for any command with a dynamic attachment or an interpolated
//    value. text_id MUST be blanked and attachment_id MUST be non-empty --
//    group:"all" would otherwise forward the bot's own text_id and the Router
//    would re-send it, stripped of every forwarded value.
{ "group": "all",
  "data": { "command": "/end", "text_id": "", "attachment_id": "mainKeyboard" } }
```

`/exchangeRates`, the wizard's only dynamic-attachment sample, avoids the choice
by never copying `/end` at all — it stays `active` forever so its paging buttons
keep working. Do not imitate that for a command that should end.

### User Profile  (`conv_type: state`, ref `<channel>_<chat_id>`)

Created by Main on first contact with `channel`, `chat_id`, `country`,
`language`, `user_name`, `unsubscribed`. A bot writes to it with
`api_copy mode:"modify" ref:"{{channel}}_{{chat_id}}" is_sync:true`, and reads
it with `{{conv[<id>].ref[{{channel}}_{{chat_id}}].<field>}}`. This is the only
sanctioned place for per-user state that must outlive one command.

### System Diagram  (`conv_type: state`, ref `<channel>_<chat_id>`)

Holds `state` (`active`/`inactive`), `last_command`, `commandAlias`. Owned by
the Router — **a generated bot never writes to it.**

### Localization  (`conv_type: state`, one task, ref `localization`)

```json
{ "mainMenu":        { "en": "Main menu", "uk": "Головне меню" },
  "commandNotFound": { "en": "Unknown command", "uk": "Невідома команда" } }
```

Seed and extend it with `modify-task` on `ref: "localization"` — **always with
`deep_merge: true`**, because the Corezoid task API merges only top level keys:
a shallow write of one `text_id` replaces nothing, but a shallow write of a
nested language map silently drops the languages you did not send.

### Attachments  (`conv_type: state`, one task per `attachment_id`)

The task `ref` **is** the `attachment_id`, and its data is keyed by channel:

```json
{ "telegram": { "type": "keyboard", "buttons": [["Balance"],["Rates"]] },
  "viber":    { "type": "keyboard", "buttons": [ … ] },
  "facebook": { "type": "quick_replies", "buttons": [ … ] },
  "abc":      { "type": "text_list_picker", … } }
```

A channel absent from the task means "send no attachment on that channel" — not
an error. Create one with `run-task` (`ref: "<attachment_id>"`), update with
`modify-task` + `deep_merge: true`.

### Tokens  (`conv_type: state`)

Channel credentials, populated by the wizard from the tokens passed to
`create-communications-orchestrator`. Read only; never regenerate it, never
print its contents.

## Text interpolation

`Replace variables in text/attachment` runs two passes over both the resolved
text and the resolved attachment:

- `{{someField}}` — read from the task's own data. This is how a bot injects a
  live value: put `balance` on the task, write `"Your balance is {{balance}}"`
  in Localization. **Flat keys only:** the matching regex is `/{{\w+}}/ig` and
  `\w` excludes `.`, so `{{message.text}}` or `{{a.b}}` is never even matched —
  it reaches the chat verbatim. (`matchToReplace` contains a dotted-path branch,
  which is why the wrong form looks supported; the regex never feeds it.)
  Flatten into a single key in a Code node first.
- `{{t'someKey}}` — read from the Localization document itself, for phrases
  reused inside other phrases and inside attachment labels. **No dot.** The key
  is extracted as `d[i].replace(/{{t'/, "").replace(/}}/, "")`, so `{{t'.someKey}}`
  looks up `".someKey"`, misses, and leaves the placeholder in the message. The
  regex that matches the token (`/{{t'.\w+}}/ig`) contains an unescaped dot,
  which is exactly why the wrong form looks plausible.

Both passes are string-level, so a value that is itself an object will not
interpolate — flatten it in a Code node first.

## Button payloads become commands

`Main → PARSE command` turns a plain text message into a command plus params:

```
separator "__"   union "_"   value "-"
"/order-status__id-42_page-2"  ⇒  command "/order-status", params { id: "42", page: "2" }
```

So a keyboard button whose text is `/order-status__id-42` invokes the command
with `{{params.id}} == "42"`. Keep generated button payloads inside that grammar
— a literal `_` or `-` inside a value silently splits it.

## Calling a domain process from a command

The processes the user hands in are **not** part of the orchestrator — they live
elsewhere in the workspace and are called natively. Three wirings, chosen by the
callee's contract (`contract_extraction.md` §2):

| Callee | Node | Reply |
|---|---|---|
| `process` with a reachable `api_rpc_reply` | `api_rpc` | `res_data` merges into the caller's task at **top level** |
| `process` with no reachable reply node | `api_copy` `mode:"create"` | none — the command can only acknowledge |
| `state` | `set_param` reading `{{conv[<id>].ref[<key>].<field>}}` | inline |

```jsonc
{"type":"api_rpc","conv_id":"@lookup-order",
 "extra":{"order_id":"{{answer}}"},"extra_type":{"order_id":"string"},
 "group":"","err_node_id":"<own error target>","user_id":<template user_id>}
```

Three things this shape depends on:

- **`group:""` plus an explicit `extra`** sends only the declared inputs.
  `group:"all"` forwards the whole task — including `channel`, `chat_id`,
  `message` and the Router's bookkeeping — into somebody else's process. The
  template does that deliberately for its *own* processes (`Main` → the
  message-to-command converter); a third-party domain process is not the place
  for it.
- **A `time` semaphor on every `api_rpc`.** Lint only rejects a value *below*
  30 s, it does not require one — but the template's own `api_rpc` nodes have
  none because their callees are in the same folder and always answer. A domain
  process can be paused, hung, or reply-less, and without a semaphor the task
  parks forever: the user gets no message and the chat's `System Diagram` state
  stays `active`, so their next message goes into the dead command too. 30 s is
  therefore both the guard and the floor on how fast a command can fail.
- **`@alias` beats a numeric `conv_id`.** A numeric id is stage-specific and
  does not survive a `deploy-stage` promotion; an alias resolves per stage.

### The envelope collision

`res_data` merging at top level means two domain calls in one command overwrite
each other's `result` / `code`. With exactly one call, read `{{result}}` and
`{{code}}` directly. With two or more, follow **every** call with an `api_code`
that copies the reply into namespaced keys and clears the shared envelope —
`api_code`, not `set_param`, because `lint-process` cannot see a `data.result`
read inside JavaScript and fails the file with `UNUSED SET_PARAM`. Fragment in
`rpc_call.nodes.json`.

## Node facts the generator must get right

| Fact | Why |
|---|---|
| `obj_type`: `1` start, `0` logic, `2` final, `3` escalation | Anything else is rejected |
| Every logics array ends with a default `{"type":"go"}` | `lint-process` blocks the deploy otherwise |
| Every action logic carries `err_node_id` | Same |
| Each failing node needs **its own** error target | `lint-process` reports shared error clusters |
| An error target that *acts* (an `obj_type:3` node that sends, then `go`es on) must not converge with another one | Two escalations routed into the same terminal make that terminal a cluster fed by two failing nodes — lint blocks it. Give each escalation its own final, even when both do the same thing |
| `err_node_id` may point at an `obj_type:2` final, never at an `obj_type:0` | Old-format node, forces a UI conversion |
| Time semaphores: `{"type":"time","dimension":"sec","value":>=30}` | Under 30s the server rejects the deploy |
| `api_copy` `group:"all"` carries the whole task; `group:""` sends only `data` | Getting this wrong loses `channel`/`chat_id` |
| `user_id` on every `api_copy`/`api_rpc` | Copy the value the wizard used in the template's own nodes |
| Code nodes are ES5 | No `let`/`const`, arrow functions, or template literals |
| `{{...}}` is **not** interpolated inside `api_code.src` | 85 of the template's 87 Code nodes never use it; the two that do carry it inside a regex literal. Read task data as `data.x`; any `{{UPPER_CASE}}` in a skeleton's `src` is substituted by the generator, not at runtime |
| `res_data` from an `api_rpc` callee merges at top level | Two calls in one task overwrite each other's `result`/`code` |
| `push-process` regenerates node ids and rewrites the file | Reference nodes by title; re-read the file after pushing |
| `push-process` cannot CREATE a process | `create-process`, then `pull-process` for a baseline, then write the scheme and push |
| Send Message's `api_copy` is `group:""` | Every interpolated value, plus `items`/`currentPage`, must be named in `data` |
| `/end` with a non-empty `text_id` makes the Router send | Blank it and set a non-empty `attachment_id` when the bot sent its own message |
| A `run-task` on a state diagram parks at its state node | That is success — the task *is* the document |
