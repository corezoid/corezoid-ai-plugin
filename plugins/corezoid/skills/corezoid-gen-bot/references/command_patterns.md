# Process contract → bot command

The manifest from `contract_extraction.md` decides what gets generated. One
process does not have to mean one command, and one command may chain several
processes.

## Clustering heuristics

| Contract shape | Becomes | Skeleton |
|---|---|---|
| No real inputs (§7a covers the rest), scalar or small object output | **single-reply command** | `bot_reply` |
| No real inputs, array output | **paged command** — carousel/list attachment, `currentPage` + `+1 page` / `-1 page` nodes, `.slice(0, 100)` in a Code node | `bot_reply` + `carouselPattern` |
| One `ask` input | **one-question dialog** | `bot_dialog` |
| One `ask` input with a small closed value set | **one-question dialog + keyboard** — buttons are the allowed values, so the answer cannot be invalid where keyboards exist. Keep the regex anyway: Viber and ABC let the user type. Use a **reply** keyboard (telegram `type:"keyboard"`), not `inline_keyboard`: the wait node reads `message.text`, and a reply keyboard sends the button's label as exactly that, whereas `callback_data` is parsed as a new command | `bot_dialog` |
| 2–5 `ask` inputs | **multi-step dialog** — repeat ask/wait/keep/validate per input, each answer into its own key | `bot_dialog` |
| Process B's input = process A's output (`token`, `cardCode`) | **one command, chained calls** — `rpc_call.nodes.json`, and namespace after every call | `bot_dialog` |
| Emits a token / session key from credentials | **not a user-facing command.** Call it as the first step *inside* the command that needs the token, and store the token on User Profile. Never ask a messenger user for a password | — |
| Rich `alternate` outcomes (`402/registration`, `401/recovery`) | **branches inside one command**, each with its own `text_id` — and its own follow-up question when the flow continues. In a chat these are not separate screens; the conversation is continuous and the Router state belongs to the running command | `bot_dialog` |
| No reachable `api_rpc_reply` | **submit-and-acknowledge command** — `api_copy`, then a text that claims only that the request was submitted | `bot_dialog` + `_fire_and_forget` |
| `conv_type: state` | **not a command.** Read inline with a `set_param` where a command needs the value | `_state_read` |
| Side effects `likely` / `unknown` | **command with a mandatory confirm step** — a yes/no keyboard as the last question. Never fire a side effect on the first message | `bot_dialog` |
| Paused, or 6+ required inputs, or output nothing can render | **nothing** — record it in `Skipped processes` with the reason and raise it in the plan | — |

## The coverage rule

**Every process the user handed in must appear in the command map.** Emit an
explicit coverage table:

| Process | Command | Called by | Wiring |
|---|---|---|---|
| 1760349 Authorization + Bonus amount | `/balance` | step 1 of the command | `api_rpc` |
| 1760347 Transactions history | `/history` | after auth, same command | `api_rpc` |
| 1760351 Send complain | `/complaint` | after confirm | `api_copy` |
| 1760400 Cities | — | read inline by `/nearest` | `set_param` |

If a process genuinely does not fit the bot the user described, **say so and
ask** — offer to drop it, give it a plain command, or reshape the bot. Never
silently omit one: silent omission is the main way this skill can produce a
wrong result that still looks finished.

## Six inputs is the practical ceiling for a chat

A Smart Form can put ten fields on one screen. A chat asks them one at a time,
and every question is a place the user abandons the flow. Past five or six
`ask` inputs, propose one of these instead of generating the dialog:

- split the command in two, persisting the first half to User Profile;
- replace inputs the bot can already read (§7a of `contract_extraction.md`);
- use a channel form where the channel has one — ABC has native `form` and
  `text_list_picker` attachments, and the template ships `/formsExample`,
  `/formsQuestionnaire` and `/formsOrderGoods` as working references.

Say which you chose and why.

## Naming

- **Command**: `/` + the process's job in lowercase-hyphen, short enough to
  type. `Authorization + Bonus amount` → `/balance`,
  `Transactions history` → `/history`. Must satisfy `^/[a-z0-9][a-z0-9-]{2,}$` —
  this is the dispatch contract, not style (see `template_map.md`).
- **Alias**: the command without the `/`. Identical string by construction.
- **Process title**: the command with the slash (`/balance`), matching the
  sample bots.
- **`text_id`**: `<commandCamel>Ask<N>`, `<commandCamel>Done`,
  `<commandCamel>Invalid`, plus one per alternate outcome
  (`balanceRegistration`, `balanceRecovery`). Reuse the template's existing
  `serviceError`, `commandNotFound`, `timeout`, `selectError`, `mainMenu`
  rather than minting near-duplicates.
- **`attachment_id`**: `<commandCamel>Keyboard`, `<commandCamel>Carousel`.
  `mainKeyboard` and `carouselPattern` already exist — reuse them.

### Check every name is free *before* you write PLAN.md

An alias cannot be created twice and **`create-alias` cannot repoint an existing
one** — there is no delete-alias or modify-alias tool in the plugin. So a name
that is already taken is taken for good, and discovering that in Phase 5 means
renaming a command after its `text_id`s, button labels, menu entries and process
title are all written. Do it here instead, while a name is still just a string:

```bash
# the orchestrator does not exist yet in plan mode; the stage's alias table does
python3 - <<'EOF'
import json
taken = {a['short_name']: a.get('obj_to_id') for a in json.load(open('_ALIASES_.json'))}
for c in ['balance','history','faq']:          # your candidate commands, minus the slash
    print(c, '->', 'FREE' if c not in taken else 'TAKEN by %s' % taken[c])
EOF
```

**A taken name may point at nothing.** Watch for `obj_to_id: null` (the reference
stage had nine such rows, titled `"xz"`, ids 149910–149918 — leftovers of an
earlier attempt at the same bot whose processes were deleted). These are the worst
case: the name is unusable *and* `@name` currently resolves to nothing, so
anything already dispatching to it silently answers `commandNotFound`. You cannot
reclaim them from the plugin — either pick different names, or ask the owner to
delete the dangling rows in the Corezoid UI and say which you did.

If `_ALIASES_.json` is not on disk yet (nothing pulled), say so in the plan and
treat the names as unverified rather than assuming they are free.

## Menu entry

A generated command is reachable by typing it, but nobody will guess it. Add
every command to the `mainKeyboard` attachment (one button shape per channel)
and to the `mainMenu` text. This is part of the pipeline, not an optional extra:
a command with no button is not shipped.
