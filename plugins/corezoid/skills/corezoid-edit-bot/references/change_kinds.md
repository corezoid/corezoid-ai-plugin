# Change kinds

One row per shape `corezoid-edit-bot` supports. `Touches` is what the change is allowed
to modify; anything outside that list is a different change kind. `Proves it` is
an **observed** result, never a clean lint.

Section references are to `invariants.md`.

| Kind | Touches | Verification that proves it |
|---|---|---|
| `add-command` | a new process in the bots folder (`create-process` → `pull-process` → write → push), a new alias, new Localization keys, a new Attachments entry, `mainKeyboard` + `mainMenu` | `run-task` on the Router with the new command reaches the bot (not `Command not found`); Send Message renders each new `text_id` with every `{{var}}` resolved; a dialog advances on the follow-up `modify` |
| `remove-command` | `pause-process` or `delete-process`, drop the `mainKeyboard` button and the `mainMenu` mention; **leave** the Localization keys | whatever the removed command now *actually* does, observed — the alias survives, so `Command not found` is a hypothesis, not a guarantee (below); every other command still reaches its bot |
| `rename-command` | process title, **a new** alias, button payloads, `mainMenu` text, PLAN.md | the new command reaches the bot; the old command's behaviour reported as observed, since its alias also survives (below) |
| `copy` | the Localization task — **plus every process that sends the affected `text_id`, if the new wording adds a `{{placeholder}}`** (§1) | `show-task ref: localization` shows the new text in every language the wizard seeded; `run-task` on Send Message renders it with no literal `{{var}}` left |
| `keyboard` | one Attachments task; plus the sending node if the attachment becomes dynamic (it then needs `items`/`currentPage` forwarded, §1) | `show-task ref: <attachment_id>` shows one key per channel in `channels` (`facebook`, not `fbmessenger`); `run-task` on Send Message returns the expected `reply_markup`; a live client renders it |
| `add-locale` | the Localization task — **every** key gains the new language — and PLAN.md `locales` | no key is missing the new language; a User Profile with `language: <new>` gets the new copy |
| `dialog-step` | one command's process (ask → wait → keep → validate block), its Localization keys, maybe its Attachments entry | the dialog advances through every step and the domain call receives each answer in its own argument — not the latest message |
| `wire-process` | one command's process (a new `api_rpc`/`api_copy`/state-read block, **plus namespacing on both calls** if it is now the 2nd, §7), its Localization keys, the send node's `data`, the PLAN.md coverage row | `run-task` on the command returns the new process's payload keys, the existing call's verdict is still read correctly, and Send Message renders the new values |
| `process-remap` | one command's `api_rpc` `extra`/`extra_type`, its outcome-mapping Code node, and the send node's `data` if the payload keys changed | `run-task` with a known input returns the expected rendered text, and every alternate outcome still maps to its own `text_id` |
| `bugfix` | the smallest set of nodes that reproduces the symptom | the failing repro now passes at the layer it failed at (`repair_loop.md`), plus one command that was already working |
| `template-edit` | a wizard-owned process (Main, Router, Send Message, System Diagram, a Messengers process) | **guarded** — see below |
| `deploy` | nothing locally; promotes one stage onto another | `deploy-stage` dry-run diff reviewed, then applied with the confirm token, then the full smoke test re-run **against the target** |

## The alias is write-once, which reshapes three of those rows

`create-alias` is the only alias tool in the plugin — there is no delete,
modify, unlink or list counterpart (§8). Consequences an edit must state rather
than paper over:

- **`remove-command`.** `delete-process` moves the process to the recycle bin
  (restorable from the UI) and `pause-process` leaves it in place refusing new
  tasks — but **neither frees the alias**, so the Router keeps resolving
  `@<command>` to it. What the user actually sees is therefore an open question:
  observe it. If the name must stop resolving, that is raw Corezoid API work —
  hand it to `corezoid:corezoid-alias-manager` with the user's agreement, and
  until then describe the command as still dispatching.
- **`rename-command`.** The new name needs a **new** alias. The old alias stays
  and keeps pointing at the same (now retitled) process, so the old command
  keeps working. Report that; do not claim the old command is gone.
- **`add-command`.** Check the candidate against `_ALIASES_.json` before writing
  CHANGE.md, while the name is still just a string. Discovering a collision
  after the `text_id`s, button labels, menu entries and process title are
  written means a rename that cascades through all of them — and an
  `obj_to_id: null` row holds the name while resolving to nothing.

`pause-process` is the reversible option and it is a dry-run by default: to
apply, pass `apply: true` with `confirm: "process#<id>:<live_status>->paused"`.
It is admission control, not proof that tasks already parked in nodes stopped —
a user mid-dialog keeps their session.

## `copy` and `keyboard` are not always task-only

They are the two kinds that look cheapest and are the easiest to get silently
wrong, because Send Message is called with `group: ""` and receives only the
fields the calling node names in `data` (§1):

- New wording that introduces `{{someValue}}` requires that key in **every**
  sending node's `data`. Find the senders with
  `grep -rln '"text_id": *"<id>"'` over the orchestrator; a node sending the
  literal `{{text_id}}` may carry any value a Code node assigns, so union them.
- An attachment that becomes dynamic requires `items` **and** `currentPage`
  forwarded, and `items: ""` (not `[]`) on the no-rows path.
- A placeholder for a key the process never produces renders as an empty string
  — only keys in that process's `success.keys` are safe.

And a **shared** key (`mainMenu`, `mainKeyboard`, `serviceError`, `timeout`,
`commandNotFound`, `selectError`, `carouselPattern`) has a `template-edit`-sized
blast radius even though it touches no process: set `regression_scope: all` and
smoke-test every command.

## Not supported — say so rather than improvising

- **Adding a channel to an existing orchestrator.** `create-communications-orchestrator`
  is not incremental: it builds a whole folder, including the per-channel
  receiver and API-method processes under `Messengers/`. There is no way to
  graft a channel the wizard did not build. Putting a token into the `Tokens`
  config produces a channel with no receiver and no send path — a bot that
  looks configured and answers nothing. The honest answer is a **new**
  orchestrator built with every channel, then re-pointing the webhooks and
  retiring the old folder; that is a `corezoid-gen-bot` run, not an edit.
- **Removing a channel.** Same reason. What you *can* do is stop registering
  its webhook, which leaves the processes idle.
- **Deleting, repointing or renaming an alias.** No MCP tool does it (§8).
  `corezoid:corezoid-alias-manager` documents the raw API calls.
- **Contract drift in a domain process.** If a handed-in process changed — lost
  a reply node, got paused, gained an alternate outcome, renamed an input — run
  `/corezoid-gen-bot refresh` first so PLAN.md and `bot-contract.json` reflect
  reality, then come back with a `process-remap` or `dialog-step` change.
  Editing the call against a stale contract is how a command starts sending an
  empty `extra` key or mapping a normal outcome to `serviceError`.
- **Editing a handed-in domain process.** They are someone else's system and the
  bot is a caller, not an owner. If one genuinely has to change, say so and hand
  it to `corezoid:corezoid-edit` with the user's agreement — then
  `/corezoid-gen-bot refresh` to pick up the new contract.
- **A second orchestrator "just to try something".** It steals the webhook from
  the first one, and there is no undo.

## Three invariants every domain-call change must preserve

All silent when broken — nothing fails at push time, the bot just misbehaves in
a chat. Full versions in `invariants.md`.

1. **A `time` semaphore of ≥ 30 s on every `api_rpc` into a domain process**
   (§6), routed to a node that tells the user and then copies `/end` into the
   Router. Without it a paused, hung or reply-less callee parks the task
   forever: no message, and the chat's `System Diagram` state stays `active`, so
   the user's next message goes into the dead command too. Lint rejects a value
   below 30 s, so 30 s is also the floor on how fast a command can fail.
2. **Namespacing once a command makes two or more calls** (§7). `res_data`
   merges into the caller's task at top level and every process in this family
   replies with the same `{result, code}` envelope, so the second callee
   overwrites the first's verdict. Follow every call with an `api_code` — not a
   `set_param`, which lint fails as `UNUSED SET_PARAM` because it cannot see a
   `data.result` read inside JavaScript. Adding a second call is therefore a
   change to **both** calls, not an addition to the end of the graph.
3. **Forwarding whatever the new payload feeds into the message** (§1). A remap
   that changes which keys the callee returns changes the send node's `data`
   too; otherwise the old key renders as an empty string and the new one as a
   literal `{{var}}`.

Also: **never probe or smoke-test a `likely`/`unknown` side effect without the
user's agreement.** A `run-task` against a real domain process can send a real
SMS or charge a real card. Agree a safe input, or exercise the dialog only up to
the confirm step.

## `template-edit` is guarded

The wizard's own processes are shared by every command and by all four
channels, and the alias dispatch means a new command never needs them touched.
So a `template-edit` is only legitimate when the change is genuinely global —
a new field on every outbound message, a channel-wide send fix — and then:

1. Name the exact process and the exact nodes in CHANGE.md, with why no
   command-level change achieves it.
2. Get the user's explicit confirmation of that named process. A bare "go" is
   not consent to edit Router.
3. `create-snapshot` before the push. `push-process` auto-snapshots an existing
   process anyway, but a titled manual checkpoint is the one thing here whose
   loss cannot be regenerated from PLAN.md.
4. Push without `force`, without `overwrite_server_change`, without
   `allow_no_snapshot`.
5. Smoke-test **every** command afterwards, not just the one that motivated the
   change — that is what "shared by every command" means.

A `push-process` on a template process reporting a concurrent server change
means someone or something else is editing this orchestrator. Stop and ask; do
not overwrite. `push-process merge=true` writes a reviewable 3-way merge plus a
`.pre-merge` backup and deploys nothing — that is the tool for reading what
changed under you.
