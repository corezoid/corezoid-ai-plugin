Loaded only when executing Phase 6 of `corezoid-gen-bot`. Assumes Phase 5 already generated the command processes.

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

