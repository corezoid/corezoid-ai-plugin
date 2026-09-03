# Invariants every change must preserve

Nine facts about how the orchestrator delivers a message. Every one of them is
**silent when broken**: `lint-process` passes, `push-process` succeeds, the
`run-task` on the command reaches `Done` with the right data on the task — and
the user gets an empty message, a duplicate message, a literal `{{var}}`, or
nothing at all.

They are grouped by what an edit typically breaks. Read §1 and §2 for any
change that touches copy, a keyboard or a send node; §3–§5 for anything a user
answers; §6–§7 for anything that calls a domain process; §8–§9 for dispatch.

---

## 1. Send Message receives only what the calling node names in `data`

The command talks to the user by copying a task into **Send Message** with
`api_copy`, `mode: "create"`, `group: ""`. `group: ""` means *send exactly the
fields in `data`* — nothing else on the calling task crosses over. Send Message
then resolves the localized text and the attachment against **its own** task
data.

So a value the command computed but did not forward is simply absent when the
text is resolved, and the placeholder reaches the chat verbatim:

```jsonc
// Localization: "balanceDone": {"en": "Your card has {{bonusAmount}} points."}
{"type":"api_copy","conv_id":<send_message_id>,"mode":"create","group":"",
 "data":{"channel":"{{channel}}","chat_id":"{{chat_id}}",
         "text_id":"balanceDone","attachment_id":"mainKeyboard",
         "bonusAmount":"{{bonusAmount}}"}}      // <-- without this line: "{{bonusAmount}} points."
```

Two families of this, and both are invisible to every test that reads the
*command's* task, because the value is present there and missing only on the far
side of the copy:

- **Values a text interpolates.** For every `text_id` a node sends, look the
  string up in Localization and forward one `data` key per `{{var}}` in it.
- **`items` + `currentPage` for any dynamic attachment.** A dynamic attachment
  is a *pattern*; its rows come from the calling task. Without them
  `createDynamicAttachment` has nothing to render and the carousel arrives
  empty.

**The empty-rows case is a trap of its own.** The gate into
`createDynamicAttachment` is `items == "" && buttons == ""` → static path. So a
command with no rows must send `items` as the **empty string**, not `[]`: an
empty array takes the dynamic path and re-renders a *static* keyboard's
`buttons` as a per-item pattern zero times, delivering a keyboard with no
buttons at all.

### What this means for a "just change the wording" request

`copy` is a Localization-only change **only while the new wording introduces no
placeholder the sending node does not already forward.** The moment it does, it
is also a process change. Find the senders before promising otherwise:

```bash
grep -rln '"text_id": *"balanceDone"' "$orch"
```

A node that sends the literal `{{text_id}}` can carry any `text_id` a Code node
assigns — union them all, and take a ternary's strings from after the `?`.

### And a placeholder must name a key the process really produces

A `{{var}}` that is forwarded but never populated renders as an empty string
rather than as a literal — quieter and just as wrong. Only keys in that
process's `success.keys` in `bot-contract.json` are safe.

---

## 2. `/end` decides whether the *Router* also sends — get it wrong and the reply arrives twice

A command ends its turn by copying into the Router with `group: "all"` (the
whole task, so `channel`/`chat_id` travel with it). The Router's `/end` handler
then branches on what it received:

| `text_id` | `attachment_id` | Router does |
|---|---|---|
| `""` | `""` | sends `mainMenu` (+ `mainKeyboard`) |
| set | `""` | sends `text_id`, adds `mainKeyboard` |
| set | set | sends `text_id` |
| `""` | set | **closes the state and sends nothing** |

There are therefore two valid conventions, and mixing them sends the reply
twice:

```jsonc
// A. The bot sends nothing; the Router renders the closing text.
//    What the wizard's own samples do (/NPS, /sampleSurvey, /changeLanguage).
//    Unavailable to any command that needs items/currentPage or an interpolated
//    value: the Router's send passes only channel/chat_id/text_id/attachment_id.
{"group":"all","data":{"command":"/end","text_id":"surveyComplete"}}

// B. The bot sent its own message and the Router must stay silent.
//    Required for every generated command.
{"group":"all","data":{"command":"/end","text_id":"","attachment_id":"mainKeyboard"}}
```

Because `group:"all"` forwards the whole task, a command that set its own
`data.text_id` leaks it into `/end` and the Router re-sends that text — and the
Router's send carries no forwarded values, so the **duplicate arrives with
literal `{{var}}` in it**. That pair of symptoms (every reply twice, the first
copy uninterpolated) is this invariant and nothing else.

`/exchangeRates`, the wizard's one dynamic-attachment sample, dodges the choice
by never copying `/end` at all and staying `active` forever. Do not imitate it:
it leaves the chat wedged.

**Every path ends this way, including error and timeout paths.** A command that
exits any other way leaves the chat's System Diagram state `active`, so the
user's next message is delivered to a finished bot. It self-heals on the System
Diagram timeout — the user sees a dead chat until it does.

---

## 3. Interpolation has exactly two forms, and both are easy to write wrongly

`Replace variables in text/attachment` runs two string-level passes over the
resolved text *and* the resolved attachment:

- **`{{someField}}`** — read from the task's own data. **Flat keys only.** The
  regex is `/{{\w+}}/ig` and `\w` excludes `.`, so `{{message.text}}` or
  `{{a.b}}` is never matched and reaches the chat verbatim. (`matchToReplace`
  does contain a dotted-path branch, which is exactly why the wrong form looks
  supported — the regex never feeds it.) Flatten into a single key in a Code
  node first.
- **`{{t'someKey}}`** — read from the Localization document itself, for phrases
  reused inside other phrases and inside attachment labels. **No dot.** The key
  is extracted as `d[i].replace(/{{t'/, "").replace(/}}/, "")`, so `{{t'.someKey}}`
  looks up `".someKey"`, misses, and leaves the placeholder in the message. The
  regex that matches the token (`/{{t'.\w+}}/ig`) has an *unescaped* dot, which
  is why the wrong form looks plausible. The wizard's own documents use
  `{{t'/exchangeRates}}` and `{{t'formsQuestionnaire}}`.

A value that is itself an object will not interpolate at all — both passes are
string-level.

---

## 4. A reply keyboard is an answer; an inline keyboard is a command

A dialog's wait node reads `data.message.text`.

- Telegram `type: "keyboard"` (a **reply** keyboard) sends the button's
  **label** as a text message — so the label arrives as the answer to the
  pending question. This is what an ask or confirm step needs.
- Telegram `type: "inline_keyboard"` sends `callback_data`, which
  `Main → PARSE command` interprets as a **command**. The pending question never
  receives an answer, and the user's tap looks like an unknown command.

Use `keyboard` for answer and confirm steps, `inline_keyboard` for display-only
rows and menus. Keep the validation regex even where a keyboard makes an invalid
answer impossible — Viber and ABC let the user type.

---

## 5. A button payload is parsed, not free text

`Main → PARSE command` turns a plain text message into a command plus params:

```
separator "__"   pair union "_"   key/value "-"
"/order-status__id-42_page-2"  ⇒  command "/order-status", params {id:"42", page:"2"}
```

So a literal `-` or `_` inside a value silently splits it. Never put free text
in a payload — which matters most on a `rename-command`, where every button
payload naming the old command has to be rewritten inside this grammar.

---

## 6. Every `api_rpc` into a domain process needs a `time` semaphore of ≥ 30 s

Lint rejects a value *below* 30 s but does not require one at all, and the
template's own `api_rpc` nodes have none — their callees are in the same folder
and always answer. A domain process is not that: it can be paused, hung or
reply-less, and without a semaphore the task parks forever. No message, and the
chat's System Diagram state stays `active`, so the user's next message goes into
the dead command too.

Route the semaphore to a node that tells the user and **then copies `/end` into
the Router** (§2). 30 s is therefore also the floor on how fast a command can
fail — write the timeout copy to read sensibly after a half-minute.

Call with `group: ""` and an explicit `extra`: `group: "all"` would forward the
whole task — `channel`, `chat_id`, `message`, the Router's bookkeeping — into
somebody else's process. Prefer `@alias` to a numeric `conv_id`: a numeric id is
stage-specific and does not survive a `deploy-stage` promotion.

---

## 7. Two domain calls in one command collide on `{result, code}`

An `api_rpc` callee's `res_data` merges into the caller's task at **top level**,
and every process in this family replies with the same `{result, code}`
envelope. So the second callee overwrites the first's verdict and the command
branches on the wrong outcome.

With exactly one call there is nothing to overwrite — read `{{result}}` and
`{{code}}` directly. With two or more, follow **every** call with an `api_code`
that copies the reply into namespaced keys and clears the shared envelope.

`api_code`, **not `set_param`**: `lint-process` cannot see a `data.result` read
inside JavaScript and fails the file with `UNUSED SET_PARAM`.

Which makes adding a second call a change to **both** calls, not an addition at
the end of the graph.

---

## 8. The alias is the entire dispatch mechanism, and it is write-once

The Router contains no per-command branch. It computes
`commandAlias = command.replace("/","")` and calls `@{{commandAlias}}`. A
command is wired up by *existing under the right alias* — there is nothing to
register.

- The name must satisfy `^[a-z0-9][a-z0-9-]{2,}$` after the slash. `/orderStatus`
  yields `orderStatus`, not a legal `short_name`, so `@orderStatus` resolves to
  nothing and the user gets `commandNotFound`. Deploys fine; unreachable forever.
- `/start`, `/end` and `/exit` are consumed by Main, Router and System Diagram.
- `_ALIASES_.json` at the stage root is the authoritative list. An
  `obj_to_id: null` row holds the name *and* resolves to nothing — the worst
  case, because anything already dispatching there answers `commandNotFound`.
- Before *calling* something by `@alias`, match on `obj_to_id`, never on the
  name. In the reference workspace `promolist`, `recovery-password` and
  `registration-verify-success` all pointed at deprecated `..._old/API:` copies
  rather than at the processes the user handed in.
- **`create-alias` is the only alias tool in the plugin.** No delete, no modify,
  no unlink, no list. A name once taken is taken for good; a rename creates a
  second alias and leaves the first pointing at the renamed process; deleting a
  command does not free its name. Real alias surgery is raw Corezoid API —
  `corezoid:corezoid-alias-manager`.

Follow-up messages reach a running dialog the same way:
`api_copy conv_id:"@{{commandAlias}}" mode:"modify" is_sync:true
ref:"{{channel}}_{{chat_id}}"`. Which is why a dialog command must be created
with `ref = {{channel}}_{{chat_id}}` and must park on an `api_callback` node.

---

## 9. Localization and Attachments are runtime task data, and the merge is shallow

They are `conv_type: state` diagrams whose *content is a task*, not a scheme —
so they are written with `run-task` / `modify-task`, never `push-process`.

- **`modify-task` needs `deep_merge: true`, always.** The Corezoid API merges
  only top-level keys: a shallow write of one language for a `text_id` replaces
  the whole language map, and a shallow write of one channel for an
  `attachment_id` drops the other three. Silent, and visible only as an empty
  message on the channels you did not send.
- **`show-task` before writing** — it tells you which keys and languages already
  exist, and whether the key you are "changing" is a shared one.
- **The wizard seeds `en`, `ru` and `uk` regardless of its `lang` argument.**
  Send Message picks the language from `User Profile.language`, which the
  Telegram receiver derives from `message.from.language_code`. There is no
  fallback: a `text_id` missing the user's language delivers an empty message.
- **`serviceError` is not shipped by the wizard**, although both skeletons
  reference it on every error and timeout path. Check and seed it.
- **A `run-task` on a state diagram reports "parked at a non-final node".**
  That is success — on a state diagram the task *is* the document, and it sits in
  the state node by design. Check the echoed data; do not retry with a larger
  `wait_sec`.
- **Channel-name asymmetry.** The wizard's messenger key is `fbmessenger`, but
  Send Message dispatches on `channel == "facebook"`. Attachment keys and
  `go_if_const` conditions use `facebook`. A per-channel check written against
  `fbmessenger` passes while that channel renders nothing.
- A channel absent from an attachment task means "send no attachment there" —
  legal, not an error.

---

## Shared keys have a template-sized blast radius

`mainMenu`, `mainKeyboard`, `serviceError`, `timeout`, `commandNotFound`,
`selectError`, `yes`/`no` and `carouselPattern` are referenced by every command
and by the Router itself. Editing one touches no process and still needs the
regression scope of a `template-edit`: smoke-test every command.
