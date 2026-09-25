Full rule list for `corezoid-edit-bot`. Each rule restates something already argued in its owning step file — read this as the checklist to re-check against before Step 4, not as new material.

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
- **Every `api_rpc` into a domain process carries a `time` semaphore**, routed
  to a node that tells the user and then copies `/end` into the Router. Lint
  enforces no floor on it (it's a timeout, not a hold) — 30 s is a reasonable
  default, not a required minimum.
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
