Full rule list for `corezoid-gen-bot`. Each rule restates something already argued in its owning phase file — read this as the checklist to re-check against before Phase 4, not as new material.

## Rules

- **`create-communications-orchestrator` builds at most once per PLAN.md.** ~150
  processes, no undo, and a second build steals the webhook from the first.
  Check `orchestrator.folder_id`; preview with `apply=false` and get approval;
  build with `apply=true` plus the token the preview printed; write the response
  into PLAN.md before anything else; never retry a timeout or a transport error
  without first checking the stage for a folder that already exists.
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
